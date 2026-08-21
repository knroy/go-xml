package xsd

import (
	"fmt"
	"io"

	"github.com/knroy/go-xml/xdm"
)

// Options configure schema assembly.
type Options struct {
	// Resolver locates the documents named by include, import and
	// redefine. When nil, a FileResolver with no root is used, so a schema
	// can include a file beside it but nothing is fetched over the network.
	//
	// To follow remote locations, set an HTTPResolver. That is off by
	// default because it hands control of what this process fetches to
	// whoever wrote the schema.
	Resolver Resolver

	// MaxDocuments bounds how many documents one assembly may read. A
	// schema that includes a generator of schemas would otherwise be a way
	// to spend the process. Zero means DefaultMaxDocuments.
	MaxDocuments int

	// Version selects XSD 1.0 or 1.1. The zero value is 1.0, because a
	// schema written for 1.0 must not acquire 1.1's relaxations by
	// accident — 1.1 changes which schemas are legal, not only which
	// documents are.
	Version Version

	// ParseOptions are passed to the XML parser for each schema document.
	// The zero value refuses a DOCTYPE, which is the right default: a
	// schema has no use for one and it is the entry point for entity
	// expansion attacks.
	ParseOptions xdm.ParseOptions
}

// DefaultMaxDocuments bounds an assembly that does not set MaxDocuments.
const DefaultMaxDocuments = 512

// Load assembles a schema from a document and everything it includes, imports
// or redefines.
//
// The base URI locates the first document, so that relative locations in it
// resolve. It may be empty when the document names only absolute locations.
func Load(root *xdm.Node, baseURI string, opts Options) (*Schema, error) {
	if opts.Resolver == nil {
		opts.Resolver = &FileResolver{}
	}
	if opts.MaxDocuments == 0 {
		opts.MaxDocuments = DefaultMaxDocuments
	}

	s := NewSchema()
	s.Version = opts.Version
	a := &assembler{
		schema: s,
		opts:   opts,
		seen:   map[docKey]bool{},
		p:      &parser{schema: s},
	}
	// The root document is marked seen before anything else runs. Without
	// it a schema that is imported back by one of its own imports — legal,
	// and common in schema families — is read a second time, and every
	// global in it is then reported as a duplicate of itself.
	if baseURI != "" {
		a.seen[docKey{location: baseURI}] = true
	}
	a.push(root, baseURI, "", false)
	if err := a.run(); err != nil {
		return nil, err
	}
	a.runRedefines()
	a.runOverrides()
	if err := a.p.finish(); err != nil {
		return nil, err
	}
	a.linkSubstitutionGroups()
	return s, nil
}

// LoadFile assembles a schema from a file on disk.
func LoadFile(path string, opts Options) (*Schema, error) {
	if opts.Resolver == nil {
		opts.Resolver = &FileResolver{}
	}
	rc, resolved, err := opts.Resolver.Resolve("", path, "")
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return nil, fmt.Errorf("schema %q not found", path)
	}
	defer rc.Close()

	tree, err := xdm.Parse(rc, opts.ParseOptions)
	if err != nil {
		return nil, fmt.Errorf("parsing schema %q: %w", path, err)
	}
	return Load(tree.Root, resolved, opts)
}

// LoadFiles assembles one schema from several documents.
//
// A schema is a set of components, and nothing says they must come from a
// single file: a namespace is often split across documents that no one of them
// includes, with the caller naming them all. Loading each separately and
// merging afterwards would resolve each document's references against only
// what that document could see, so they are assembled together instead.
func LoadFiles(paths []string, opts Options) (*Schema, error) {
	switch len(paths) {
	case 0:
		return nil, fmt.Errorf("no schema documents given")
	case 1:
		return LoadFile(paths[0], opts)
	}

	if opts.Resolver == nil {
		opts.Resolver = &FileResolver{}
	}
	if opts.MaxDocuments == 0 {
		opts.MaxDocuments = DefaultMaxDocuments
	}

	s := NewSchema()
	s.Version = opts.Version
	a := &assembler{
		schema: s,
		opts:   opts,
		seen:   map[docKey]bool{},
		p:      &parser{schema: s},
	}

	for _, path := range paths {
		rc, resolved, err := opts.Resolver.Resolve("", path, "")
		if err != nil {
			return nil, err
		}
		if rc == nil {
			return nil, fmt.Errorf("schema %q not found", path)
		}
		tree, err := xdm.Parse(rc, opts.ParseOptions)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("parsing schema %q: %w", path, err)
		}
		if resolved != "" {
			a.seen[docKey{location: resolved}] = true
		}
		a.push(tree.Root, resolved, "", false)
	}

	if err := a.run(); err != nil {
		return nil, err
	}
	a.runRedefines()
	a.runOverrides()
	if err := a.p.finish(); err != nil {
		return nil, err
	}
	a.linkSubstitutionGroups()
	return s, nil
}

// docKey identifies a schema document for deduplication.
//
// The key is the location alone. Keying on (namespace, location) looks more
// precise and is wrong: two schemas may import each other, and the second
// reference to a document arrives with a different declared namespace than the
// first — a.xsd entered as the root with no namespace, then again as
// urn:a from b.xsd's import. Under a pair key those are distinct, the document
// is read twice, and every global in it is reported as a duplicate.
//
// The chameleon case that a pair key seemed to serve is handled where it
// belongs, in readOne: the adopted namespace comes from the *including*
// document, so the same file included from two namespaces would need to be
// read twice — a case this deliberately does not support, since the components
// would collide under one schema anyway.
type docKey struct {
	location string
}

// pending is a document waiting to be read.
type pending struct {
	root *xdm.Node
	base string

	// chameleonNS is the namespace an included document with no target
	// namespace adopts. It is empty unless this is a chameleon include.
	chameleonNS string

	// redefining marks a document reached through xs:redefine.
	redefining bool
}

// assembler reads a schema document and everything it references.
//
// The work list is breadth-first rather than recursive so that the document
// count is bounded by one counter rather than by stack depth, and so that a
// cycle — which is legal, since two schemas may import each other — terminates
// on the seen set rather than on stack exhaustion.
type assembler struct {
	schema *Schema
	opts   Options
	seen   map[docKey]bool
	queue  []pending
	p      *parser
	count  int

	// pendingOverrides are the XSD 1.1 <xs:override> elements awaiting the
	// same treatment, minus the self-reference binding: an override's
	// replacement does not derive from what it replaces.
	pendingOverrides []pendingRedefine

	// pendingRedefines are the <xs:redefine> elements whose children have
	// still to be read. They are deferred to the end of assembly because a
	// redefinition is defined in terms of the document it redefines, which
	// must therefore have been read first.
	pendingRedefines []pendingRedefine
}

// pendingRedefine is one <xs:redefine> awaiting its children being read.
type pendingRedefine struct {
	el  *xdm.Node
	doc *schemaDoc
}

func (a *assembler) push(root *xdm.Node, base, chameleonNS string, redefining bool) {
	a.queue = append(a.queue, pending{
		root: root, base: base, chameleonNS: chameleonNS, redefining: redefining,
	})
}

// run drains the work list.
func (a *assembler) run() error {
	for len(a.queue) > 0 {
		item := a.queue[0]
		a.queue = a.queue[1:]

		a.count++
		if a.count > a.opts.MaxDocuments {
			return fmt.Errorf(
				"schema assembly read more than %d documents; raise "+
					"Options.MaxDocuments if this is legitimate",
				a.opts.MaxDocuments)
		}

		root := item.root
		if root.Kind == xdm.KindDocument {
			els := root.ChildElements()
			if len(els) == 0 {
				return fmt.Errorf("schema document at %q is empty", item.base)
			}
			root = els[0]
		}
		if !root.IsElement(NSSchema, "schema") {
			return fmt.Errorf(
				"schema document at %q has root {%s}%s, want {%s}schema",
				item.base, root.Name.URI, root.Name.Local, NSSchema)
		}

		if err := a.readOne(root, item); err != nil {
			return err
		}
	}
	return nil
}

// readOne reads one document's components and queues what it references.
func (a *assembler) readOne(root *xdm.Node, item pending) error {
	doc := &schemaDoc{root: root, baseURI: item.base}
	if attr := root.Attr("", "targetNamespace"); attr != nil {
		doc.targetNS = attr.Value
		doc.hasTargetNS = true
	} else if item.chameleonNS != "" {
		// A chameleon include: a document with no target namespace, read
		// from one that has one, adopts it. Every component built here
		// therefore lands in the including namespace — including,
		// per clause 3.2.2, the namespace constraints of wildcards,
		// which readWildcard picks up from doc.targetNS.
		doc.targetNS = item.chameleonNS
		doc.hasTargetNS = true
	}

	doc.elementFormQualified = root.AttrValue("elementFormDefault") == "qualified"
	doc.attributeFormQualified = root.AttrValue("attributeFormDefault") == "qualified"
	doc.defaultAttributes = root.AttrValue("defaultAttributes")

	var err error
	if doc.blockDefault, err = a.p.derivationSet(root, "blockDefault"); err != nil {
		a.p.errs = append(a.p.errs, err)
	}
	if doc.finalDefault, err = a.p.derivationSet(root, "finalDefault"); err != nil {
		a.p.errs = append(a.p.errs, err)
	}

	// The references are queued before the body is read, so that a
	// reference to an imported component resolves through the fixup list
	// once every document has been read.
	// A schema document the versioning attributes exclude contributes
	// nothing, and that includes the documents it would have pulled in:
	// vc:maxVersion on <xs:schema> is how a file says "this is for some
	// other version", and following its includes would read exactly the
	// components it was hiding.
	if !includeElement(root, a.schema.Version) {
		return nil
	}

	for _, el := range root.ChildElements() {
		if el.Name.URI != NSSchema || !includeElement(el, a.schema.Version) {
			continue
		}
		switch el.Name.Local {
		case "include":
			a.queueRef(el, doc, "", el.AttrValue("schemaLocation"), true, false)
		case "redefine":
			a.queueRef(el, doc, "", el.AttrValue("schemaLocation"), true, true)
		case "override":
			// XSD 1.1. Like redefine in that it reads another document
			// and replaces components of it, but the replacement does
			// *not* derive from the original — it stands on its own,
			// which is what makes override usable where redefine's
			// self-reference rule is too restrictive.
			a.queueRef(el, doc, "", el.AttrValue("schemaLocation"), true, false)
		case "import":
			ns := el.AttrValue("namespace")
			if ns == doc.targetNS && doc.hasTargetNS {
				a.p.errs = append(a.p.errs, errorAt(el, "src-import.1.1",
					"a schema may not import its own namespace %q", ns))
				continue
			}
			a.queueRef(el, doc, ns, el.AttrValue("schemaLocation"), false, false)
		}
	}

	prev := a.p.doc
	a.p.doc = doc
	for _, el := range root.ChildElements() {
		a.p.readTopLevel(el)
	}
	a.p.doc = prev

	// A redefine's own children are read after the redefined document, so
	// that a self-reference resolves to the definition being replaced
	// rather than to the replacement. That ordering is the whole of what
	// makes redefine work, and it is why this cannot be done while the
	// referenced document is merely queued.
	for _, el := range root.ChildElements() {
		switch {
		case el.IsElement(NSSchema, "redefine"):
			a.pendingRedefines = append(a.pendingRedefines,
				pendingRedefine{el: el, doc: doc})
		case el.IsElement(NSSchema, "override"):
			a.pendingOverrides = append(a.pendingOverrides,
				pendingRedefine{el: el, doc: doc})
		}
	}
	return nil
}

// queueRef resolves and queues a referenced document.
func (a *assembler) queueRef(el *xdm.Node, doc *schemaDoc, namespace, location string, isInclude, redefining bool) {
	if location == "" {
		if !isInclude {
			// An import with no schemaLocation declares a dependency
			// without saying where to find it. That is legal: the
			// components may already be present, or the resolver may
			// know the namespace.
			rc, resolved, err := a.opts.Resolver.Resolve(namespace, "", doc.baseURI)
			if err != nil || rc == nil {
				return
			}
			defer rc.Close()
			a.parseAndQueue(el, rc, resolved, namespace, doc, isInclude, redefining)
			return
		}
		a.p.errs = append(a.p.errs, errorAt(el, "src-include.1",
			"an include must have a schemaLocation"))
		return
	}

	key := docKey{location: location}
	if a.seen[key] {
		return
	}
	a.seen[key] = true

	rc, resolved, err := a.opts.Resolver.Resolve(namespace, location, doc.baseURI)
	if err != nil {
		if isInclude && !redefining {
			// An unresolvable include is explicitly not an error
			// (§4.2.1 clause 1): "no corresponding inclusion is
			// performed". A redefine is different, because its
			// children are defined in terms of what it redefines.
			return
		}
		a.p.errs = append(a.p.errs, errorAt(el, "src-resolve",
			"cannot resolve schemaLocation %q: %v", location, err))
		return
	}
	if rc == nil {
		if isInclude && !redefining {
			return
		}
		a.p.errs = append(a.p.errs, errorAt(el, "src-resolve",
			"schemaLocation %q resolved to nothing", location))
		return
	}
	defer rc.Close()
	a.parseAndQueue(el, rc, resolved, namespace, doc, isInclude, redefining)
}

func (a *assembler) parseAndQueue(el *xdm.Node, rc io.Reader, resolved, namespace string, doc *schemaDoc, isInclude, redefining bool) {
	tree, err := xdm.Parse(rc, a.opts.ParseOptions)
	if err != nil {
		a.p.errs = append(a.p.errs, errorAt(el, "src-resolve",
			"parsing %q: %v", resolved, err))
		return
	}

	chameleon := ""
	if isInclude && doc.hasTargetNS {
		// Only an include can be a chameleon: an import brings in a
		// different namespace by definition.
		chameleon = doc.targetNS
	}
	a.push(tree.Root, resolved, chameleon, redefining)
}

// linkSubstitutionGroups fills the transitive substitution group membership
// cached on each element declaration.
//
// This runs after every document is read because a member may be declared in a
// document that had not been read when its head was. The closure is transitive:
// if B substitutes for A and C for B, then C substitutes for A.
func (a *assembler) linkSubstitutionGroups() {
	linkSubstitutionGroups(a.schema)
}

func linkSubstitutionGroups(s *Schema) {
	// direct maps a head to the declarations naming it directly.
	direct := map[*ElementDecl][]*ElementDecl{}
	for _, d := range s.Elements {
		heads := d.SubstitutionGroups
		if len(heads) == 0 && d.SubstitutionGroup != nil {
			heads = []*ElementDecl{d.SubstitutionGroup}
		}
		for _, h := range heads {
			direct[h] = append(direct[h], d)
		}
	}

	// The closure is computed with an explicit stack and a seen set rather
	// than by recursion, because a malformed schema can name a circular
	// substitution group and the spec bans it rather than making it
	// impossible to write.
	for _, head := range s.Elements {
		var out []*ElementDecl
		seen := map[*ElementDecl]bool{head: true}
		stack := append([]*ElementDecl(nil), direct[head]...)
		for len(stack) > 0 {
			d := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
			stack = append(stack, direct[d]...)
		}
		head.substitutable = out
	}
}

// Substitutable returns the element declarations that may substitute for this
// one, transitively, not including the declaration itself.
//
// The list is empty until the schema is assembled, since a member may be
// declared in a document read after the head.
func (d *ElementDecl) Substitutable() []*ElementDecl { return d.substitutable }

// runRedefines applies the deferred redefinitions.
//
// They run after every document has been read, and innermost first: a redefine
// of a document that itself redefines another has to see the inner result. The
// queue is in the order the documents were read, which is outermost first, so
// it is walked backwards.
func (a *assembler) runRedefines() {
	for i := len(a.pendingRedefines) - 1; i >= 0; i-- {
		r := a.pendingRedefines[i]
		hold := a.prepareRedefine(r.el, r.doc)
		a.applyRedefine(r.el, r.doc, hold)
	}
}

// runOverrides applies the deferred XSD 1.1 overrides.
//
// An override replaces a component outright: unlike a redefine, the new
// definition does not derive from the old one, so there is no self-reference to
// bind and the originals are simply displaced and dropped.
func (a *assembler) runOverrides() {
	for i := len(a.pendingOverrides) - 1; i >= 0; i-- {
		o := a.pendingOverrides[i]
		a.prepareRedefine(o.el, o.doc)

		prev := a.p.doc
		a.p.doc = o.doc
		for _, c := range o.el.ChildElements() {
			if c.Name.URI != NSSchema {
				continue
			}
			switch c.Name.Local {
			case "simpleType", "complexType", "group", "attributeGroup",
				"element", "attribute", "notation":
				a.p.readTopLevel(c)
			}
		}
		a.p.doc = prev
	}
}
