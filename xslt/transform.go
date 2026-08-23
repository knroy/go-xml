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
	if source == nil && opts.InitialTemplate == "" {
		return nil, fmt.Errorf(
			"Transform: source document is nil and no initial template was named")
	}

	// Whitespace stripping is applied to a copy so that the caller's tree is
	// left as they parsed it. Stripping in place would surprise a caller that
	// reuses one parsed document across several stylesheets with different
	// strip-space declarations.
	if source != nil && len(s.strip) > 0 {
		source = s.stripWhitespace(source)
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
	rt.ctx.Funcs = lib

	out := newOutputBuilder()

	if opts.InitialTemplate != "" {
		t, ok := s.named[xdm.QName{Local: opts.InitialTemplate}.Clark()]
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
		if err := applyToNode(rt, source, opts.InitialMode, nil, nil, out); err != nil {
			return nil, err
		}
	}

	return &Result{
		Nodes:     out.sequence(),
		Messages:  *rt.messages,
		Secondary: *rt.secondary,
		output:    s.output,
		charMap:   s.activeCharMap,
	}, nil
}

// stripWhitespace returns a copy of the tree with whitespace-only text nodes
// removed from the elements named by xsl:strip-space.
//
// xsl:preserve-space overrides xsl:strip-space, and a specific name beats a
// wildcard, so the decision is made per element by scanning both lists rather
// than precomputing a set.
func (s *Stylesheet) stripWhitespace(root *xdm.Node) *xdm.Node {
	tree := xdm.NewTree()
	tree.Root.BaseURI = root.BaseURI
	for _, ch := range root.Children {
		if c := s.stripCopy(ch, false); c != nil {
			tree.Root.AppendChild(c)
		}
	}
	tree.Finalize()
	return tree.Root
}

// stripCopy copies n, dropping whitespace-only text where stripping applies.
// preserving carries xml:space="preserve" down the subtree.
func (s *Stylesheet) stripCopy(n *xdm.Node, preserving bool) *xdm.Node {
	switch n.Kind {
	case xdm.KindText:
		if !preserving && xdm.IsXMLWhitespace(n.Value) {
			return nil
		}
		return &xdm.Node{Kind: xdm.KindText, Value: n.Value}

	case xdm.KindElement:
		c := &xdm.Node{Kind: xdm.KindElement, Name: n.Name, BaseURI: n.BaseURI}
		for _, ns := range n.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, a := range n.Attrs {
			c.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value})
		}

		childPreserving := preserving
		if a := n.Attr(xdm.NSXML, "space"); a != nil {
			childPreserving = a.Value == "preserve"
		} else if !s.stripsElement(n.Name) {
			childPreserving = true
		}

		for _, ch := range n.Children {
			if cc := s.stripCopy(ch, childPreserving); cc != nil {
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
