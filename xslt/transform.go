package xslt

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// TransformOptions configures one transform.
type TransformOptions struct {
	// Params supplies values for top-level xsl:param, keyed by Clark name
	// ("{uri}local", or just "local" for a no-namespace parameter).
	Params map[string]xdm.Sequence

	// Documents resolves fn:doc and fn:document. Nil disables them, which is
	// the default: a stylesheet that can open arbitrary URIs is an SSRF and
	// file-disclosure vector, and validation rule sets need at most the code
	// lists shipped beside them.
	Documents xpath.DocumentResolver

	// Collections resolves fn:collection. Nil disables it, which is the
	// default, and setting Documents does not set this: enabling fn:doc for
	// a known code list should not also let a stylesheet enumerate whatever
	// a collection URI happens to name.
	Collections xpath.CollectionResolver

	// Texts resolves fn:unparsed-text. Nil disables it, which is the
	// default, and setting Documents does not set this: fn:doc hands back a
	// parsed XML document, while fn:unparsed-text hands back the raw bytes
	// of whatever the resolver will open. See xpath.TextResolver.
	Texts xpath.TextResolver

	// MaxDepth bounds template recursion. Zero means DefaultMaxDepth; a
	// negative value means no limit.
	//
	// The bound catches a stylesheet that recurses without a base case,
	// which is the common authoring mistake. But it also counts the ordinary
	// descent of an identity transform, so a limit below the parser's left
	// this refusing documents it had just accepted: at the old fixed 300, a
	// legal 500-deep document could be parsed and not transformed.
	MaxDepth int

	// InitialMode names the mode for the initial apply-templates.
	InitialMode string

	// InitialTemplate names a template to invoke instead of matching the
	// document root, which is how a stylesheet with only named templates is
	// entered.
	InitialTemplate string
	// InitialTemplateURI is the namespace URI of InitialTemplate, for a
	// caller that has already resolved the prefix in its own namespace
	// context. Empty means resolve any prefix in InitialTemplate against the
	// stylesheet's own declarations instead.
	//
	// The two are not interchangeable. A caller naming the template from
	// outside the stylesheet — a test catalog, a command line with its own
	// bindings — binds the prefix itself, and resolving it a second time
	// against the stylesheet can silently select a DIFFERENT template that
	// happens to spell another namespace with the same prefix.
	InitialTemplateURI string

	// Now fixes the value fn:current-dateTime returns. Leave it zero to use
	// the wall clock; set it to make a transform reproducible, which is what
	// a golden-file test needs.
	Now time.Time

	// ImplicitTimezone is the offset in minutes for date values with no
	// timezone. Defaults to UTC so that results are reproducible across
	// machines.
	ImplicitTimezone int
}

// Result is the outcome of a transform.
type Result struct {
	// charMap is the flattened xsl:character-map table for serialisation.
	charMap map[rune]string
	// Nodes is the result sequence, which for a typical stylesheet is a
	// single element.
	Nodes xdm.Sequence
	// Messages holds xsl:message output, in the order produced.
	Messages []string
	// Secondary holds the documents produced by xsl:result-document, in the
	// order produced. It is empty for the great majority of stylesheets,
	// which produce a single result.
	Secondary []SecondaryResult
	// BaseURI is the URI this result tree is identified by, which Tree()
	// puts on the document node it manufactures. It is empty for the
	// principal result, whose document node has no URI of its own; a caller
	// assembling a Result from a SecondaryResult sets it from that
	// document's BaseURI so that base-uri(/) answers inside it.
	BaseURI string
	// output carries the stylesheet's serialisation settings.
	output OutputSettings
}

// Transform applies the stylesheet to a source document.
//
// The Stylesheet is not mutated, so one compiled stylesheet may be used from
// many goroutines concurrently.
func (s *Stylesheet) Transform(ctx context.Context, source *xdm.Node, opts TransformOptions) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// A nil source is legal when the transform starts from a named template.
	// XSLT 2.0 section 2.3 makes the source document optional in exactly that
	// case, and it is how a stylesheet that generates its own content is
	// invoked — so this is checked where a source is actually needed rather
	// than on the way in.
	// xsl:initial-template is the conventional default entry point: a
	// stylesheet that declares a template with that name is asking to be
	// started there when the caller names neither a source document nor a
	// template. The name is already whitelisted as the one legal XSLT-
	// namespace template name, and honouring it here is what lets a
	// source-free stylesheet run at all.
	defaultEntry := xdm.QName{URI: xdm.NSXSL, Local: "initial-template"}.Clark()
	useDefaultEntry := false
	if source == nil && opts.InitialTemplate == "" {
		if _, ok := s.named[defaultEntry]; !ok {
			return nil, fmt.Errorf(
				"Transform: source document is nil and no initial template was named")
		}
		useDefaultEntry = true
	}

	// Whitespace stripping is applied to a copy so that the caller's tree is
	// left as they parsed it. Stripping in place would surprise a caller that
	// reuses one parsed document across several stylesheets with different
	// strip-space declarations.
	if source != nil && len(s.strip) > 0 {
		// Stripping applies to the whole tree the initial context node belongs
		// to, not to the node itself. When the caller starts the transform at
		// an inner node -- the suite does this with <source select="..."/> --
		// stripping only that subtree left the node's ancestors unstripped,
		// and passing a text node made stripWhitespace walk a node with no
		// children and produce an empty document.
		//
		// The node is then looked up again in the stripped copy. If whitespace
		// stripping deleted it, there is no initial context item at all, and
		// every expression that needs one raises XPDY0002 -- which is exactly
		// what section 4.4 means by stripping happening before the transform
		// begins rather than as it runs.
		root := source.Root()
		stripped, found := s.stripWhitespaceFrom(root, source)
		if source == root {
			source = stripped
		} else {
			source = found
		}
	}

	// Type annotations are stripped from the same source trees whitespace
	// stripping applies to — section 3.5 says so — and after it, so that the
	// two passes compose rather than one undoing the other.
	//
	// Restricted to a document node for the same reason whitespace stripping
	// now works from the root: stripTypeAnnotationsFrom copies the children of
	// what it is handed, so handing it an inner node discarded everything
	// above and beside it.
	if source != nil && source.Kind == xdm.KindDocument && s.stripTypeAnnotations {
		source = s.stripTypeAnnotationsFrom(source)
	}

	// Documents loaded by fn:doc and fn:document are source documents too, so
	// the same whitespace declarations apply to them. The wrapper is per
	// transform because its cache holds the stripped copies, which must not
	// outlive the declarations that produced them.
	if len(s.strip) > 0 && opts.Documents != nil {
		opts.Documents = &stripSpaceResolver{sheet: s, inner: opts.Documents}
	}

	rt, err := newRuntime(s, ctx, source, opts)
	if err != nil {
		return nil, err
	}
	// Bind the runtime so key(), current() and xsl:function can reach it.
	rt.ctx = rt.ctx.WithVar(runtimeVar,
		xdm.One(&xdm.Opaque{Label: "runtime", Value: rt}))

	// Grouping and regex accessors are added on top of the per-transform
	// library so that they see the same bindings.
	lib := xpath.NewLibrary(rt.ctx.Funcs)
	registerGroupingFuncs(lib)
	registerFormatNumber(lib, s)
	registerPositionFuncs(lib)
	registerCurrentOutputURI(lib)
	rt.ctx.Funcs = lib

	out := newOutputBuilder()

	if useDefaultEntry {
		t := s.named[defaultEntry]
		for _, p := range t.Params {
			if p.Required && !p.Tunnel {
				return nil, fmt.Errorf(
					"XTDE0060: the initial template xsl:initial-template "+
						"declares required parameter $%s", p.Name.Lexical())
			}
		}
		if err := runTemplate(rt, t, nil, nil, out); err != nil {
			return nil, err
		}
	} else if opts.InitialTemplate != "" {
		// XTDE0047: "it is a non-recoverable dynamic error if the invocation
		// of the stylesheet specifies both an initial mode and an initial
		// template". The two are alternative ways of saying where processing
		// starts, and honouring only one of them silently discards half of
		// what the caller asked for.
		if m := opts.InitialMode; m != "" && m != "#default" && m != "#unnamed" {
			return nil, fmt.Errorf(
				"XTDE0047: the invocation specifies both an initial mode %q "+
					"and an initial template %q", m, opts.InitialTemplate)
		}
		// The name the caller supplies is lexical, so a prefix in it is
		// resolved against the stylesheet's own namespace declarations before
		// the lookup. Treating "foo:temp" as a local name found nothing, and
		// a template declared with a prefixed name could not be invoked.
		prefix, local := xdm.SplitQName(opts.InitialTemplate)
		initName := xdm.QName{Local: opts.InitialTemplate}
		switch {
		case opts.InitialTemplateURI != "":
			initName = xdm.QName{URI: opts.InitialTemplateURI, Local: local}
		case prefix != "":
			if uri, found := s.prefixes[prefix]; found {
				initName = xdm.QName{URI: uri, Local: local}
			}
		}
		t, ok := s.named[initName.Clark()]
		if !ok {
			return nil, fmt.Errorf(
				"XTDE0040: no template named %q", opts.InitialTemplate)
		}
		// XTDE0060: the initial template may not declare a required
		// parameter, because a transform started at it supplies none.
		for _, p := range t.Params {
			if p.Required && !p.Tunnel {
				return nil, fmt.Errorf(
					"XTDE0060: the initial template %q declares required "+
						"parameter $%s", opts.InitialTemplate, p.Name.Lexical())
			}
		}
		if err := runTemplate(rt, t, nil, nil, out); err != nil {
			return nil, err
		}
	} else {
		// "#default" and "#unnamed" both name the unnamed mode; a caller
		// passing either means "start where a stylesheet with no @mode
		// starts", which is the empty name.
		initialMode := opts.InitialMode
		if initialMode == "#default" || initialMode == "#unnamed" {
			initialMode = ""
		}
		// XTDE0045: "it is a non-recoverable dynamic error if the invocation
		// of the stylesheet specifies an initial mode (other than the default
		// mode) that does not match the expanded-QName in the mode attribute
		// of any template". mode="#all" makes a template apply in every mode
		// but does not NAME one, so it cannot satisfy the invocation: per
		// W3C bugzilla 3690 an initial mode matched only by "#all" is still
		// this error. Only tokens that are real names count as declaring.
		if initialMode != "" {
			// The caller's name is lexical, so a prefix in it is resolved
			// against the stylesheet's declarations before comparison, the
			// same way InitialTemplate is above.
			want := initialMode
			if prefix, local, found := strings.Cut(initialMode, ":"); found {
				if uri, ok := s.prefixes[prefix]; ok {
					want = xdm.QName{URI: uri, Local: local}.Clark()
				}
			}
			if !s.declaresMode(want) {
				return nil, fmt.Errorf(
					"XTDE0045: the invocation specifies initial mode %q, "+
						"which no template declares", opts.InitialMode)
			}
			initialMode = want
		}
		if err := applyToNode(rt, source, initialMode, nil, nil, out); err != nil {
			return nil, err
		}
	}

	// The base output URI belongs to the principal result tree. An
	// xsl:result-document with an absent or empty @href names that same URI,
	// so a stylesheet that both writes there and leaves content in the
	// principal tree has produced two documents at one URI. The check is made
	// here rather than in the instruction because the ordering is not fixed:
	// the implicit content may be written either side of the instruction, and
	// only at the end is it known that both happened.
	if *rt.baseURIUsed && len(out.sequence()) > 0 {
		return nil, fmt.Errorf(
			"XTDE1490: two result documents were written to the base output " +
				"URI: the principal result tree and an xsl:result-document " +
				"with no href")
	}

	return &Result{
		Nodes:     out.sequence(),
		Messages:  *rt.messages,
		Secondary: *rt.secondary,
		output:    s.output,
		charMap:   s.activeCharMap,
	}, nil
}

// registerCurrentOutputURI adds fn:current-output-uri.
//
// The function is XSLT 3.0, but result-document-1006 is declared XSLT20+ and
// its expected result is XTRE1495/XTDE1490 rather than a rejection, so a 2.0
// processor that raises XPST0017 for the name never reaches the condition the
// test is about. Every test in the fn/current-output-uri set is XSLT30+ and so
// out of scope; this name is reachable in scope from that one test alone.
//
// The value is the empty sequence whenever the transform is writing the
// principal result tree and no base output URI was supplied to it — which is
// always the case here, because TransformOptions has no base output URI and
// this engine never writes files itself. Interpolated into @href by an
// attribute value template the empty sequence gives "", which is the same
// spelling xsl:result-document already uses for "the base output URI", so the
// duplicate-destination check in Transform sees the collision it should.
func registerCurrentOutputURI(l *xpath.Library) {
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-output-uri"}, Arity: 0,
		Call: func(*xpath.Context, []xdm.Sequence) (xdm.Sequence, error) {
			return xdm.Empty, nil
		},
	})
}

// stripWhitespace returns a copy of the tree with whitespace-only text nodes
// removed from the elements named by xsl:strip-space.
//
// xsl:preserve-space overrides xsl:strip-space, and a specific name beats a
// wildcard, so the decision is made per element by scanning both lists rather
// than precomputing a set.
func (s *Stylesheet) stripWhitespace(root *xdm.Node) *xdm.Node {
	stripped, _ := s.stripWhitespaceFrom(root, nil)
	return stripped
}

// stripWhitespaceFrom strips root and reports where want ended up in the copy.
//
// The second return is nil when want was itself a whitespace-only text node
// that stripping removed, which is the one case the caller must distinguish:
// an initial context node that no longer exists is not an error to raise here
// but an absent focus, and absence is what XPDY0002 reports at the point of
// use.
func (s *Stylesheet) stripWhitespaceFrom(root, want *xdm.Node) (*xdm.Node, *xdm.Node) {
	var found *xdm.Node
	tree := xdm.NewTree()
	tree.Root.BaseURI = root.BaseURI
	// The DOCTYPE text is a property of the tree, not of the root node, and
	// it is where the unparsed-entity declarations live. Building a fresh
	// tree without it made fn:unparsed-entity-uri answer "" for every
	// document a stylesheet with xsl:strip-space was applied to — a
	// whitespace declaration silently deleting an unrelated part of the data
	// model.
	if src := root.Tree(); src != nil {
		tree.DocType = src.DocType
	}
	if want == root {
		found = tree.Root
	}
	for _, ch := range root.Children {
		if c := s.stripCopy(ch, false, want, &found); c != nil {
			tree.Root.AppendChild(c)
		}
	}
	tree.Finalize()
	return tree.Root, found
}

// stripSpaceResolver applies the stylesheet's xsl:strip-space and
// xsl:preserve-space declarations to documents loaded by fn:doc and
// fn:document.
//
// Section 4.4 scopes whitespace stripping to "all source documents", not to
// the principal one: a document retrieved by fn:doc is a source document and
// is stripped by the same declarations. Transform stripped only the principal
// source, so a stylesheet declaring xsl:strip-space saw stripped whitespace in
// its input and unstripped whitespace in everything doc() returned.
//
// The stripped trees are cached by URI because fn:doc must be stable: two
// calls with the same argument return the same node, and stripping afresh each
// time would return a different tree with different node identities.
type stripSpaceResolver struct {
	sheet *Stylesheet
	inner xpath.DocumentResolver
	// done caches by the tree the inner resolver returned rather than by the
	// URI string, so that two URIs the inner resolver maps to one document
	// still map to one stripped document here.
	done map[*xdm.Tree]*xdm.Tree
}

func (r *stripSpaceResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	t, err := r.inner.ResolveDocument(uri, base)
	if err != nil || t == nil || t.Root == nil {
		return t, err
	}
	if c, ok := r.done[t]; ok {
		return c, nil
	}
	root := r.sheet.stripWhitespace(t.Root)
	out := root.Tree()
	if out == nil {
		return t, nil
	}
	if r.done == nil {
		r.done = map[*xdm.Tree]*xdm.Tree{}
	}
	r.done[t] = out
	return out, nil
}

// stripCopy copies n, dropping whitespace-only text where stripping applies.
//
// preserving carries xml:space="preserve" down the subtree, and *only* that:
// whether an element's own whitespace is stripped is decided from the
// strip-space and preserve-space declarations matching its name.
func (s *Stylesheet) stripCopy(n *xdm.Node, preserving bool, want *xdm.Node, found **xdm.Node) *xdm.Node {
	c := s.stripCopyNode(n, preserving, want, found)
	if c != nil && n == want {
		*found = c
	}
	return c
}

func (s *Stylesheet) stripCopyNode(n *xdm.Node, preserving bool, want *xdm.Node, found **xdm.Node) *xdm.Node {
	switch n.Kind {
	case xdm.KindText:
		// Whitespace-only text is dropped unless xml:space preserves it or
		// its parent element is outside the strip-space list. The parent is
		// what the declarations are matched against, which is why the test
		// happens here rather than being decided by the parent and passed in.
		if !preserving && xdm.IsXMLWhitespace(n.Value) &&
			n.Parent != nil && s.stripsElement(n.Parent.Name) {
			return nil
		}
		return &xdm.Node{Kind: xdm.KindText, Value: n.Value}

	case xdm.KindElement:
		// The type annotation travels with the copy. Whitespace stripping is
		// defined over which text nodes survive, not over what the surviving
		// nodes are: section 4.4 says nothing about types, and dropping them
		// here would mean declaring xsl:strip-space silently untyped a
		// document the caller had validated. This pass is gated on there
		// being a strip-space declaration at all, which is the only reason
		// the loss was not visible — removing that gate cost 115 tests.
		c := &xdm.Node{Kind: xdm.KindElement, Name: n.Name, BaseURI: n.BaseURI,
			TypeAnnotation: n.TypeAnnotation,
			IsID:           n.IsID, IsIDREFS: n.IsIDREFS}
		for _, ns := range n.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, a := range n.Attrs {
			ac := &xdm.Node{Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value,
				TypeAnnotation: a.TypeAnnotation,
				IsID:           a.IsID, IsIDREFS: a.IsIDREFS}
			c.AddAttr(ac)
			if a == want {
				*found = ac
			}
		}

		// Section 4.4 decides stripping per element, from the strip-space and
		// preserve-space declarations matching *that* element's name. What
		// inherits down the tree is xml:space, not the outcome: an ancestor
		// outside the strip-space list says nothing about its descendants.
		//
		// Threading the outcome down made it latch. The document element is
		// outside the list in the ordinary case, so it set "preserve", and
		// every element beneath it then preserved whitespace whatever the
		// declarations said — which is to say xsl:strip-space did nothing at
		// all unless it also named the document element.
		childPreserving := preserving
		if a := n.Attr(xdm.NSXML, "space"); a != nil {
			childPreserving = a.Value == "preserve"
		}

		for _, ch := range n.Children {
			if cc := s.stripCopy(ch, childPreserving, want, found); cc != nil {
				c.AppendChild(cc)
			}
		}
		return c

	default:
		return &xdm.Node{Kind: n.Kind, Name: n.Name, Value: n.Value}
	}
}

// stripsElement reports whether whitespace inside the named element is
// stripped. A specific preserve-space entry wins over a wildcard strip-space
// entry, matching the spec's import-precedence rule for the common case.
func (s *Stylesheet) stripsElement(name xdm.QName) bool {
	best, strip := -1, false
	rank := func(q xdm.QName) int {
		switch {
		case q.Local == "*" && q.URI == "":
			return 0 // "*"
		case q.Local == "*", q.URI == "*":
			return 1 // "prefix:*" or "*:local"
		default:
			return 2 // a specific name
		}
	}
	consider := func(q xdm.QName, isStrip bool) {
		// "*:local" matches that local name in any namespace, which is
		// recorded with URI "*" because a namespace URI cannot be one.
		if q.URI == "*" {
			if q.Local != name.Local {
				return
			}
			if r := rank(q); r >= best {
				best, strip = r, isStrip
			}
			return
		}
		if q.Local != "*" && (q.Local != name.Local || q.URI != name.URI) {
			return
		}
		if q.Local == "*" && q.URI != "" && q.URI != name.URI {
			return
		}
		if r := rank(q); r >= best {
			best, strip = r, isStrip
		}
	}
	for _, q := range s.strip {
		consider(q, true)
	}
	for _, q := range s.preserve {
		consider(q, false)
	}
	return strip
}

// String renders the result using the stylesheet's output settings.
func (r *Result) String() string {
	var sb strings.Builder
	_ = r.Serialize(&sb)
	return sb.String()
}

// Serialize writes the result using the stylesheet's xsl:output settings.
//
// Deliberately not named WriteTo: that name implies io.WriterTo, whose
// contract returns a byte count this would have to fabricate.
func (r *Result) Serialize(w io.Writer) error {
	return serialize(w, r.Nodes, r.output, r.charMap)
}

// Tree returns the result as a document node, for callers that want to keep
// navigating it rather than serialise it — which is what a Schematron driver
// does with an SVRL report.
func (r *Result) Tree() *xdm.Node {
	tree := xdm.NewTree()
	// The document node is manufactured here, so it is the only place the
	// result's own URI can be put on it. Without this base-uri(/) answered
	// "" even when every element below it had a base URI.
	tree.Root.BaseURI = r.BaseURI
	for _, it := range r.Nodes {
		switch v := it.(type) {
		case *xdm.Node:
			tree.Root.AppendChild(v)
		case *xdm.Atomic:
			tree.Root.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: v.String()})
		}
	}
	tree.Finalize()
	return tree.Root
}

// stripTypeAnnotations returns a copy of the tree with every type annotation
// removed, as input-type-annotations="strip" requires.
//
// Section 3.5 states the effect exactly: the annotation of every element
// becomes xs:untyped and of every attribute xs:untypedAtomic, the typed value
// of both becomes the string value as xs:untypedAtomic, and the is-nilled
// property of every element becomes false. The is-id and is-idrefs properties
// are explicitly *not* changed, which is why xsi:nil is the only attribute
// dropped here and the ID-bearing ones are copied through untouched.
//
// The work is done on a copy for the same reason whitespace stripping is: a
// caller may reuse one parsed document across several stylesheets, and only
// some of them ask for the annotations to go.
func (s *Stylesheet) stripTypeAnnotationsFrom(root *xdm.Node) *xdm.Node {
	tree := xdm.NewTree()
	tree.Root.BaseURI = root.BaseURI
	for _, ch := range root.Children {
		if c := stripAnnotationCopy(ch); c != nil {
			tree.Root.AppendChild(c)
		}
	}
	tree.Finalize()
	return tree.Root
}

// stripAnnotationCopy copies n with its type annotation cleared.
//
// An empty TypeAnnotation is how this data model spells xs:untyped for an
// element and xs:untypedAtomic for an attribute: Atomize returns an
// untypedAtomic for a node carrying none, which is precisely the typed value
// the specification asks for here.
func stripAnnotationCopy(n *xdm.Node) *xdm.Node {
	switch n.Kind {
	case xdm.KindElement:
		c := &xdm.Node{Kind: xdm.KindElement, Name: n.Name, BaseURI: n.BaseURI}
		for _, ns := range n.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, a := range n.Attrs {
			// xsi:nil is dropped rather than copied: the is-nilled
			// property of every element becomes false, and this data
			// model computes is-nilled from the attribute rather than
			// storing it, so removing the attribute is what sets the
			// property. Every other attribute is kept, which is what
			// leaves is-id and is-idrefs unchanged.
			if a.Name.URI == xdm.NSXSI && a.Name.Local == "nil" {
				continue
			}
			c.AddAttr(&xdm.Node{
				Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value,
				IsID: a.IsID, IsIDREFS: a.IsIDREFS,
			})
		}
		for _, ch := range n.Children {
			if cc := stripAnnotationCopy(ch); cc != nil {
				c.AppendChild(cc)
			}
		}
		return c
	case xdm.KindText:
		return &xdm.Node{Kind: xdm.KindText, Value: n.Value}
	case xdm.KindComment:
		return &xdm.Node{Kind: xdm.KindComment, Value: n.Value}
	case xdm.KindPI:
		return &xdm.Node{Kind: xdm.KindPI, Name: n.Name, Value: n.Value}
	}
	return nil
}

// SerializeAsXML renders a result with the xml output method, ignoring the
// stylesheet's own xsl:output.
//
// It exists for a caller making a *tree* assertion about a result — the W3C
// conformance harness is the one in this repository. The stylesheet's method
// is part of what serialisation means, not part of what the tree is: the html
// method injects a content-type meta into <head> and writes void elements
// unclosed, so a result asserted as a tree would be compared against markup
// the stylesheet never produced, and would not parse back as XML at all.
//
// Indentation and the other settings are deliberately left at their defaults
// rather than inherited, for the same reason.
func SerializeAsXML(r *Result) string {
	var sb strings.Builder
	_ = serialize(&sb, r.Nodes, OutputSettings{Method: "xml"}, r.charMap)
	return sb.String()
}
