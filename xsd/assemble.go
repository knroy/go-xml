package xsd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		p:      &parser{schema: s, attrsDone: map[*ComplexType]bool{}},
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
	// Particle Valid (Restriction) is checked last: it needs every base
	// resolved, every content model spliced, and — for clause 2.1's
	// substitution-group expansion — the substitution closure already
	// linked.
	if err := a.p.checkParticleRestriction(); err != nil {
		return nil, err
	}
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
	s, err := Load(tree.Root, resolved, opts)
	if s != nil {
		s.sourcePaths = []string{path}
	}
	return s, err
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
	s.sourcePaths = append([]string(nil), paths...)
	a := &assembler{
		schema: s,
		opts:   opts,
		seen:   map[docKey]bool{},
		p:      &parser{schema: s, attrsDone: map[*ComplexType]bool{}},
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
		// A document named directly by the caller adopts no namespace, so
		// its key carries an empty adoptedNS; a later chameleon include of
		// the same file keys separately and is still read.
		if resolved != "" {
			a.seen[docKey{location: canonicalLocation(resolved)}] = true
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
	// Particle Valid (Restriction) is checked last: it needs every base
	// resolved, every content model spliced, and — for clause 2.1's
	// substitution-group expansion — the substitution closure already
	// linked.
	if err := a.p.checkParticleRestriction(); err != nil {
		return nil, err
	}
	return s, nil
}

// docKey identifies a schema document for deduplication.
//
// The key is the *resolved* location, not the schemaLocation as written. A
// modular schema set reaches the same file by different spellings — UBL's
// Invoice imports "../common/UBL-CommonBasicComponents-2.1.xsd" while its
// CommonAggregateComponents, sitting in that directory, imports
// "UBL-CommonBasicComponents-2.1.xsd" — and keying on the raw string makes
// those two documents. The file is then read twice and every global in it is
// reported as a duplicate of itself. That is a diamond in the import graph,
// which is the normal shape of a large schema set rather than an unusual one.
//
// The key also carries the namespace a chameleon include will make the
// document adopt, because that genuinely changes which components the file
// produces. boeingData's ipo3 hands the harness ipo.xsd, address.xsd and
// itematt.xsd together; itematt.xsd declares no target namespace, so read as a
// top-level document it defines {}ItemDelivery, while ipo.xsd's include of it
// makes it define {http://www.example.com/IPO}ItemDelivery. Both readings are
// required — ipo.xsd's attributeGroup ref names the second — and a key on
// location alone lets whichever reading came first suppress the other, leaving
// the ref dangling. The adopted namespace is empty for every non-chameleon
// document, so the ordinary diamond-import case still collapses to one entry.
type docKey struct {
	location  string
	adoptedNS string
}

// redefinesAnything reports whether a redefine or override has any child that
// actually replaces a component.
//
// Only the four definition kinds do. An annotation is documentation, so a
// redefine carrying nothing else asks nothing of the document it names.
func redefinesAnything(el *xdm.Node) bool {
	for _, kid := range el.ChildElements() {
		switch kid.Name.Local {
		case "simpleType", "complexType", "group", "attributeGroup":
			return true
		}
	}
	return false
}

// canonicalLocation reduces a resolved location to a form two spellings of the
// same document share.
//
// A resolved path is still a string, and one file can be named by more than
// one of them: msData's schZ012 imports "Schz012_b.xsd" from a document that
// was itself read as "schZ012_b.xsd", which on a case-insensitive filesystem
// is one file reached twice. Read twice, every global in it collides with
// itself. Asking the filesystem which file a path names settles it without
// guessing at case rules — case-folding the string would be wrong on a
// case-sensitive filesystem, where those really are two files.
//
// Anything that is not a local path, or that does not exist, is left as it
// was: a remote URL has no filesystem identity to appeal to, and a location
// that cannot be statted will fail when it is read.
func canonicalLocation(resolved string) string {
	if resolved == "" || strings.Contains(resolved, "://") {
		return resolved
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return resolved
	}
	dir := filepath.Dir(resolved)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return resolved
	}
	for _, e := range entries {
		ei, err := e.Info()
		if err != nil {
			continue
		}
		if os.SameFile(info, ei) {
			return filepath.Join(dir, e.Name())
		}
	}
	return resolved
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
		doc.chameleon = true
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

	// Every document reaching the assembler gets its ids checked. The
	// single-document path in readDocument does the same; a schema built
	// from includes never passes through it, which is where attgA006 and
	// attgA009 live.
	a.p.checkIDs(root)

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

	rc, resolved, err := a.opts.Resolver.Resolve(namespace, location, doc.baseURI)
	if err != nil || rc == nil {
		// A redefine that redefines nothing costs nothing when its
		// location cannot be resolved — anyURI_a001 says of exactly
		// this case that the document "should give only 3 warning for
		// the unresolved schemaLocations". An annotation is not a
		// redefinition, so a redefine carrying only one is as empty as
		// a redefine carrying nothing (schH9).
		if redefining && !redefinesAnything(el) {
			return
		}
		if !redefining {
			// schemaLocation is a hint, not a requirement. §4.2.1
			// clause 1 says of include that where the location
			// cannot be resolved "no corresponding inclusion is
			// performed", and §4.2.6.2 gives import the same
			// latitude — the location merely offers a document,
			// and the components may be available some other way
			// or not be needed at all.
			//
			// A redefine is different: its children are defined in
			// terms of what it redefines, so there is nothing to
			// carry on with.
			//
			// Failing here meant a schema naming a document this
			// processor could not fetch — a remote URL with the
			// network off, most commonly — failed to load
			// entirely, rather than losing only what that document
			// would have contributed. Any reference that really
			// needed those components still fails, at the
			// reference, naming what is missing — unless the whole
			// namespace went missing with the document, which §5.3
			// Missing Sub-components makes an absent value rather
			// than a fault. absentNamespace explains that case.
			if !isInclude && namespace != "" {
				if a.p.unresolvedImports == nil {
					a.p.unresolvedImports = map[string]bool{}
				}
				a.p.unresolvedImports[namespace] = true
			}
			return
		}
		if err == nil {
			err = fmt.Errorf("resolved to nothing")
		}
		a.p.errs = append(a.p.errs, errorAt(el, "src-resolve",
			"cannot resolve schemaLocation %q: %v", location, err))
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
	if isInclude && doc.hasTargetNS && !declaresTargetNS(tree.Root) {
		// Only an include can be a chameleon: an import brings in a
		// different namespace by definition, and a document that
		// declares its own target namespace keeps it.
		chameleon = doc.targetNS
	}

	// Deduplication happens here rather than at the resolver, because the
	// key needs the namespace this reading of the document will produce
	// components in, and that is only known once the document has been
	// parsed and its own targetNamespace inspected. Keying on the raw
	// schemaLocation instead of the resolved path would make two documents
	// of one file whenever a schema set reaches it by different spellings;
	// see docKey.
	key := docKey{location: canonicalLocation(resolved), adoptedNS: chameleon}
	if a.seen[key] {
		return
	}
	a.seen[key] = true

	a.push(tree.Root, resolved, chameleon, redefining)
}

// declaresTargetNS reports whether a schema document element carries a
// targetNamespace of its own, and so cannot be made to adopt an includer's.
func declaresTargetNS(root *xdm.Node) bool {
	if root.Kind == xdm.KindDocument {
		els := root.ChildElements()
		if len(els) == 0 {
			return false
		}
		root = els[0]
	}
	return root.Attr("", "targetNamespace") != nil
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
	// Schema.Elements is a map, so ranging it directly would seed `direct`
	// in a different order on every run and leave {substitution group}
	// membership in a different order with it. That is not cosmetic:
	// Particle Valid (Restriction) maps a derived choice onto a base choice
	// with an *order-preserving* mapping (RecurseLax clause 2), so when
	// clause 2.1 expands a substitution group head into a choice, the order
	// of that choice decides whether a valid schema is accepted. elemZ027a
	// — a choice over m1 and m2 restricting a ref to their head — passed or
	// failed from run to run before this walk was made deterministic.
	//
	// Sorting by qualified name is enough to pin it down; the spec does not
	// order {substitution group}, so any stable order is conformant, and
	// document order is not available here because members may be declared
	// across several documents read in any order.
	names := make([]xdm.QName, 0, len(s.Elements))
	for name := range s.Elements {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].URI != names[j].URI {
			return names[i].URI < names[j].URI
		}
		return names[i].Local < names[j].Local
	})

	// direct maps a head to the declarations naming it directly.
	direct := map[*ElementDecl][]*ElementDecl{}
	for _, name := range names {
		d := s.Elements[name]
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
	// The queue is walked front to back rather than as a stack so that
	// members come out in the order `direct` holds them, which the sort
	// above made deterministic; popping from the back would reverse each
	// level and put a head's own members in descending name order.
	for _, name := range names {
		head := s.Elements[name]
		var out []*ElementDecl
		seen := map[*ElementDecl]bool{head: true}
		queue := append([]*ElementDecl(nil), direct[head]...)
		for len(queue) > 0 {
			d := queue[0]
			queue = queue[1:]
			if seen[d] {
				continue
			}
			seen[d] = true
			// A member is substitutable only if the derivation
			// taking its type to the head's is not in the head's
			// {disallowed substitutions} (§3.3.6). block= on the
			// head element and blockDefault= on the schema are
			// what fill that set, and ignoring it let a blocked
			// member substitute anyway.
			//
			// The member is still pushed onto the stack: blocking
			// it does not block what substitutes for *it*, since
			// each step is judged against the head it names.
			if !substitutionBlockedBy(head, d) {
				out = append(out, d)
			}
			queue = append(queue, direct[d]...)
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
		prevOverride := a.p.inOverride
		a.p.doc, a.p.inOverride = o.doc, true
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
		a.p.doc, a.p.inOverride = prev, prevOverride
	}
}

// substitutionBlockedBy reports whether a head's {disallowed substitutions}
// keep a member out of its substitution group.
//
// The derivations examined are those from the member's type up to the head's:
// a member two steps away goes through the intermediate type, and it is the set
// of methods used along the way that the block applies to.
func substitutionBlockedBy(head, member *ElementDecl) bool {
	blocked := head.DisallowedSubstitutions
	if ct, ok := head.Type.(*ComplexType); ok {
		blocked = DerivationSet(uint8(blocked) | uint8(ct.Prohibits))
	}
	if blocked == 0 {
		return false
	}
	// block="substitution" blocks substitution itself, whatever the types
	// do. It is not a derivation method, so it never appears on the chain
	// walked below — particlesDc004 has an abstract head with
	// block="substitution" and members of the same (absent) type, where
	// there is no chain to walk at all.
	if blocked.Has(DerivationSubstitution) {
		return true
	}
	if member.Type == nil || head.Type == nil {
		return false
	}
	if member.Type == head.Type {
		return false
	}

	seen := 0
	for cur := member.Type; cur != nil && cur != head.Type; {
		ct, ok := cur.(*ComplexType)
		if !ok {
			return blocked.Has(DerivationRestriction)
		}
		if blocked.Has(ct.DerivationMethod) {
			return true
		}
		if ct.Base == cur {
			return false
		}
		cur = ct.Base
		if seen++; seen > 64 {
			return false
		}
	}
	return false
}
