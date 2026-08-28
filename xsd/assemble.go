package xsd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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

	// XPathVersion selects the version of XPath the 1.1 assertions and
	// conditional type alternatives in this schema are written in.
	//
	// The zero value is XPath 2.0, which is what the specification requires:
	// XSD 1.1 defines assertions against a subset of XPath 2.0, so a schema
	// using a 3.0 construct is not portable and must not quietly work here.
	//
	// Raising it is for a host that controls its own schemas and wants the
	// later function library in an assertion — fn:parse-json or a map, say —
	// accepting that the schema is then this engine's rather than every
	// engine's. It does not affect anything but assertions and alternatives:
	// nothing else in a schema is XPath.
	XPathVersion xpath.Version

	// ParseOptions are passed to the XML parser for each schema document.
	// The zero value refuses a DOCTYPE, which is the right default: a
	// schema has no use for one and it is the entry point for entity
	// expansion attacks.
	ParseOptions xdm.ParseOptions

	// LaxUPA relaxes Unique Particle Attribution, which loading enforces,
	// to the reading Saxon and XSV take: two competing particles are
	// tolerated when both are references to the same element declaration.
	//
	// Off by default because the strict reading is the conforming one —
	// erratum E1-29 is explicit that particles at different points are
	// distinct "even if they originated from the same named model group".
	// It exists because schemas written against those processors do rely
	// on the permissive rule, and such a schema would otherwise be
	// unloadable rather than merely non-conforming.
	LaxUPA bool
}

// checkOptions carries the assembly's constraint settings into the content
// model checks.
func (o Options) checkOptions() CheckOptions {
	return CheckOptions{LaxUPA: o.LaxUPA, Version: o.Version}
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
	s.xpathVersion = opts.XPathVersion
	a := &assembler{
		schema: s,
		opts:   opts,
		seen:   map[docKey]bool{},
		p:      &parser{schema: s, attrsDone: map[*ComplexType]bool{}, assembled: true},
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
	a.checkCompositionCycles()
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
	// Unique Particle Attribution and Element Declarations Consistent are
	// checked after restriction for the same reason it runs last: both walk
	// compiled content models, which need every base resolved and every
	// group spliced.
	if err := checkContentModelConstraints(s, a.opts.checkOptions()); err != nil {
		return nil, err
	}
	if err := checkAllGroupLimited(s); err != nil {
		return nil, err
	}
	registerDerivedTypes(s)
	return s, nil
}

// registerDerivedTypes tells the data model which built-in each user-defined
// simple type erases to.
//
// A node annotated with a schema type has to atomise to a *typed* value, or
// every question about that type answers false: "instance of my:partNumberType"
// needs the value to remember what it was validated as, and a value that
// atomised to untypedAtomic has already forgotten. The data model cannot work
// this out for itself — it has no schema — and it cannot ask, because xsd
// imports xdm and not the other way round. So the schema tells it, once, here.
func registerDerivedTypes(s *Schema) {
	for name, t := range s.Types {
		if name.Local == "" || name.URI == NSSchema {
			continue
		}
		var base *SimpleType
		switch ct := t.(type) {
		case *SimpleType:
			if ct == nil {
				continue
			}
			// A LIST type's typed value is a sequence, one item per token, and
			// its item type is the only thing that says what those items are.
			// Registering it as merely deriving from its base loses that: a
			// list of xs:decimal derives from xs:anySimpleType, which the data
			// model cannot build a value from at all, so the node atomised to
			// a single untypedAtomic holding the whole literal.
			// listItemTypeOf sees through restrictions of a list, which carry
			// no item type of their own.
			if item := listItemTypeOf(ct); item != nil {
				key := xdm.AnnotationName(name.URI, name.Local)
				if in := annotationName(item); in != "" && in != key {
					xdm.RegisterListType(key, in)
				}
			}
			// A UNION type's base is always xs:anySimpleType — that is what
			// the specification says a union derives from — so the chain
			// registered below dead-ends at a name the data model can build
			// no value for, and the node atomises to xs:untypedAtomic. The
			// member list is the only thing that says what such a value can
			// be, so it is recorded separately, the way a list's item type is.
			if members := unionMemberTypesOf(ct); len(members) > 0 {
				key := xdm.AnnotationName(name.URI, name.Local)
				names := make([]string, 0, len(members))
				for _, m := range members {
					if mn := annotationName(m); mn != "" && mn != key {
						names = append(names, mn)
					}
				}
				xdm.RegisterUnionType(key, names)
			}
			base, _ = ct.Base.(*SimpleType)
		case *ComplexType:
			// A complex type with simple content atomises as its content
			// type, so it derives from whatever that content type does:
			// "complexSimpleContent extends xsd:decimal" makes an element
			// annotated with it an instance of element(E, xs:decimal).
			// Registering only simple types left every such name unknown to
			// the data model, so the derivation chain stopped one step short
			// and the match never happened.
			if ct == nil {
				continue
			}
			// PROBE ONLY (wave 8): complex content derivation.
			if ct.Content != ContentSimple {
				if b, ok := ct.Base.(*ComplexType); ok && b != nil {
					if bn := b.Name; bn.Local != "" && bn.URI != NSSchema &&
						!(bn.URI == name.URI && bn.Local == name.Local) {
						xdm.RegisterDerivedType(
							xdm.AnnotationName(name.URI, name.Local),
							xdm.AnnotationName(bn.URI, bn.Local))
					}
				}
				continue
			}
			// A complex type with simple content whose content type is a
			// union is a union for atomisation purposes: its typed value is
			// drawn from the same members. DateType in the XSLT suite is
			// exactly this — an extension of union(StandardDate, xs:string)
			// with an attribute — and without registering it here the union
			// only reachable through the simple-content path stayed invisible.
			if sc := ct.SimpleContent; sc != nil {
				if members := unionMemberTypesOf(sc); len(members) > 0 {
					key := xdm.AnnotationName(name.URI, name.Local)
					names := make([]string, 0, len(members))
					for _, m := range members {
						if mn := annotationName(m); mn != "" && mn != key {
							names = append(names, mn)
						}
					}
					xdm.RegisterUnionType(key, names)
				}
			}
			base = ct.SimpleContent
		default:
			continue
		}
		// The nearest named built-in ancestor is what the value atomises as,
		// which is exactly what annotationName computes for the base.
		if base != nil {
			key := xdm.AnnotationName(name.URI, name.Local)
			if prim := annotationName(base); prim != "" && prim != key {
				xdm.RegisterDerivedType(key, prim)
			}
		}
	}
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
	s.xpathVersion = opts.XPathVersion
	s.sourcePaths = append([]string(nil), paths...)
	a := &assembler{
		schema: s,
		opts:   opts,
		seen:   map[docKey]bool{},
		p:      &parser{schema: s, attrsDone: map[*ComplexType]bool{}, assembled: true},
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
	a.checkCompositionCycles()
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
	// Unique Particle Attribution and Element Declarations Consistent are
	// checked after restriction for the same reason it runs last: both walk
	// compiled content models, which need every base resolved and every
	// group spliced.
	if err := checkContentModelConstraints(s, a.opts.checkOptions()); err != nil {
		return nil, err
	}
	if err := checkAllGroupLimited(s); err != nil {
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

	// redefined maps an <xs:redefine> element to the root of the document
	// it names, so that the "is this name defined there" clauses can be
	// answered against that document rather than the whole schema.
	redefined map[*xdm.Node]*xdm.Node

	// compEdges is the schema-composition graph: one entry per
	// <xs:include>, <xs:redefine> or <xs:override> that resolved to a
	// document, in the order they were read. checkCompositionCycles walks
	// it looking for circular redefinition.
	compEdges []compEdge

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

// compEdge is one resolved schema-composition reference: the document that
// carried the <xs:include>, <xs:redefine> or <xs:override>, and the document it
// named.
type compEdge struct {
	el        *xdm.Node
	from      docKey
	to        docKey
	redefines bool
}

// addCompositionEdge records one resolved include/redefine/override.
//
// The "from" end is the document holding the element. Its key must be built the
// same way the "to" end of the edge that reached it was, or the two ends never
// meet and no cycle ever closes: same canonicalLocation, same adopted
// namespace.
func (a *assembler) addCompositionEdge(el *xdm.Node, doc *schemaDoc, to docKey, redefining bool) {
	from := docKey{location: canonicalLocation(doc.baseURI)}
	if doc.chameleon {
		from.adoptedNS = doc.targetNS
	}
	a.compEdges = append(a.compEdges, compEdge{
		el: el, from: from, to: to, redefines: redefining,
	})
}

// checkCompositionCycles refuses a schema whose documents redefine one
// component in a circle (§4.2.2).
//
// Composition may be circular in general — two documents may include or import
// each other, and schema families routinely do. So, more surprisingly, may
// redefinition: msData's schU1 has two documents redefining each other, and the
// suite expects it to load, because each redefines a component the other owns.
// Nothing there is circular except the file references.
//
// What cannot be circular is the *definition of one component*. A redefinition
// is written in terms of the component it replaces, so when the same name is
// redefined at two points around a cycle each of those definitions is written
// in terms of the other: in s4_2_4si01 two documents each redefine c1 by
// extending c1, and the only way to read that is as a type extending itself.
// Which of the two "wins" then depends on nothing but which document the
// processor happened to start from — the same schema, entered by its other
// door, is a different schema. That is the contradiction to refuse.
//
// The test is therefore not "is there a redefine cycle" but "does some
// component get redefined twice around one cycle".
func (a *assembler) checkCompositionCycles() {
	if len(a.compEdges) == 0 {
		return
	}
	out := map[docKey][]int{}
	for i, e := range a.compEdges {
		out[e.from] = append(out[e.from], i)
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[docKey]int{}
	reported := map[*xdm.Node]bool{}

	// Iterative depth-first search. The stack carries the edge index that
	// entered each node, so that when a back edge closes a cycle the
	// redefine edges on it can be read straight off the stack rather than
	// recovered by a second search.
	type frame struct {
		node docKey
		via  int // index into compEdges, or -1 for a root
		next int // next position in out[node] to try
	}

	var starts []docKey
	seenStart := map[docKey]bool{}
	for _, e := range a.compEdges {
		if !seenStart[e.from] {
			seenStart[e.from] = true
			starts = append(starts, e.from)
		}
	}

	for _, root := range starts {
		if color[root] != white {
			continue
		}
		stack := []frame{{node: root, via: -1}}
		color[root] = grey
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			edges := out[top.node]
			if top.next >= len(edges) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			ei := edges[top.next]
			top.next++
			e := a.compEdges[ei]
			switch color[e.to] {
			case grey:
				// Back edge: the cycle is the stack from e.to
				// to the top, plus this edge.
				at := -1
				for i := range stack {
					if stack[i].node == e.to {
						at = i
						break
					}
				}
				if at < 0 {
					continue
				}
				// Collect the components redefined by the
				// redefine edges around this cycle. A name that
				// turns up twice is one whose definition
				// depends on itself.
				around := []*xdm.Node{}
				if e.redefines {
					around = append(around, e.el)
				}
				for i := at + 1; i < len(stack); i++ {
					ce := a.compEdges[stack[i].via]
					if ce.redefines {
						around = append(around, ce.el)
					}
				}
				var culprit *xdm.Node
				seenComp := map[string]bool{}
				for _, el := range around {
					for _, k := range redefinedComponents(el) {
						if seenComp[k] {
							culprit = el
							break
						}
						seenComp[k] = true
					}
					if culprit != nil {
						break
					}
				}
				if culprit != nil && !reported[culprit] {
					reported[culprit] = true
					a.p.errs = append(a.p.errs, errorAt(culprit, "src-redefine.1",
						"circular redefinition: the redefine of %q "+
							"is reached again around a chain of "+
							"redefinitions, so the component would "+
							"be defined in terms of itself",
						culprit.AttrValue("schemaLocation")))
				}
			case white:
				color[e.to] = grey
				stack = append(stack, frame{node: e.to, via: ei})
			}
		}
	}
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
		// §3.15.2 (schema-namespace): targetNamespace names a
		// namespace, and "" names none. A document meaning "no target
		// namespace" leaves the attribute off; writing it empty is a
		// representation fault (schZ014_b).
		if attr.Value == "" {
			a.p.errs = append(a.p.errs, errorAt(root, "src-schema",
				"targetNamespace=\"\" is not a namespace name; "+
					"omit the attribute for the absent namespace"))
		}
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

	doc.elementFormQualified = a.p.formDefault(root, "elementFormDefault")
	doc.attributeFormQualified = a.p.formDefault(root, "attributeFormDefault")
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
			// §4.2.6.1: the namespace attribute names a namespace,
			// and no namespace may be named by the empty string.
			// Writing namespace="" is not the same as omitting it —
			// omitting it means "the absent namespace", while ""
			// names nothing at all (schZ014_a).
			if el.Attr("", "namespace") != nil && ns == "" {
				a.p.errs = append(a.p.errs, errorAt(el, "src-import.1.1",
					"namespace=\"\" is not a namespace name; "+
						"omit the attribute to import the absent namespace"))
				continue
			}
			if ns == doc.targetNS && doc.hasTargetNS {
				a.p.errs = append(a.p.errs, errorAt(el, "src-import.1.1",
					"a schema may not import its own namespace %q", ns))
				continue
			}
			// §4.2.6.2 src-import.1.2: "If the namespace [attribute]
			// is absent, then the *actual value* of the
			// targetNamespace [attribute] of the <schema> ancestor
			// must be present." An import with no namespace= imports
			// the absent namespace, and a schema that is itself in
			// the absent namespace would be importing its own — the
			// same self-import that clause 1.1 bans, just spelled
			// with two omitted attributes instead of two written
			// ones. addB008 and addB035 both pin this.
			if el.Attr("", "namespace") == nil && !doc.hasTargetNS {
				a.p.errs = append(a.p.errs, errorAt(el, "src-import.1.2",
					"an import with no namespace attribute requires the "+
						"enclosing schema to have a targetNamespace"))
				continue
			}
			if a.p.importedNamespaces == nil {
				a.p.importedNamespaces = map[string]bool{}
			}
			a.p.importedNamespaces[ns] = true
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

	// §4.2.1 src-include.1 / §4.2.2 src-redefine.1: the referenced document
	// must either have the same target namespace as the referring one, or
	// none at all (in which case it is a chameleon and adopts it).
	// §4.2.6.2 src-import.3.1/3.2: an import's namespace attribute, if
	// present, must equal the imported document's target namespace; if
	// absent, the imported document must have none.
	refNS, refHasNS := targetNSOf(tree.Root)
	if isInclude {
		if refHasNS && refNS != doc.targetNS {
			code := "src-include.1"
			if redefining {
				code = "src-redefine.1"
			}
			a.p.errs = append(a.p.errs, errorAt(el, code,
				"the document at %q has target namespace %q, "+
					"which differs from %q", resolved, refNS, doc.targetNS))
			return
		}
	} else if namespace != "" {
		if !refHasNS || refNS != namespace {
			a.p.errs = append(a.p.errs, errorAt(el, "src-import.3.1",
				"import names namespace %q but the document at %q "+
					"has target namespace %q", namespace, resolved, refNS))
			return
		}
	} else if refHasNS {
		a.p.errs = append(a.p.errs, errorAt(el, "src-import.3.2",
			"import has no namespace attribute but the document at %q "+
				"has target namespace %q", resolved, refNS))
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

	// Record the composition edge before the dedup check, not after: a
	// cycle is precisely the case where the far end has already been read,
	// so an edge recorded only for freshly-read documents would never close
	// one. See checkCompositionCycles.
	a.addCompositionEdge(el, doc, key, redefining)

	if a.seen[key] {
		return
	}
	a.seen[key] = true

	if isInclude {
		// Clauses 6.2.1 and 7.2.1 of §4.2.2 ask whether a name is
		// defined *in the document this redefine names* — not merely
		// somewhere in the assembled schema. The redefining document
		// may well declare a global of the same name itself (schS1),
		// so the question cannot be answered from Schema's maps.
		// The map is keyed on the <xs:include> and <xs:redefine>
		// elements alike, because a redefined document may itself have
		// got the component from an include of its own.
		if a.redefined == nil {
			a.redefined = map[*xdm.Node]*xdm.Node{}
		}
		a.redefined[el] = tree.Root
	}

	a.push(tree.Root, resolved, chameleon, redefining)
}

// targetNSOf returns a schema document's target namespace and whether it
// declares one at all.
func targetNSOf(root *xdm.Node) (string, bool) {
	if root.Kind == xdm.KindDocument {
		els := root.ChildElements()
		if len(els) == 0 {
			return "", false
		}
		root = els[0]
	}
	if attr := root.Attr("", "targetNamespace"); attr != nil {
		return attr.Value, true
	}
	return "", false
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
		// The source-form constraints are checked while the redefined
		// document's components are still under their own names, since
		// clauses 6.2.1 and 7.2.1 ask whether the name is defined there.
		prev := a.p.doc
		a.p.doc = r.doc
		a.checkRedefine(r.el, r.doc)
		a.p.doc = prev
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

// redefinedComponents names the components one <xs:redefine> or <xs:override>
// replaces, in the two symbol spaces that matter here: types share one, and
// groups and attribute groups have their own.
func redefinedComponents(el *xdm.Node) []string {
	var out []string
	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		name := c.AttrValue("name")
		if name == "" {
			continue
		}
		switch c.Name.Local {
		case "simpleType", "complexType":
			out = append(out, "type "+name)
		case "group", "attributeGroup", "element", "attribute", "notation":
			out = append(out, c.Name.Local+" "+name)
		}
	}
	return out
}
