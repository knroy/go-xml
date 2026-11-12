package xslt

// Lane MM8: a motionless xsl:evaluate in a streamed rule body
// (evaluate-046: <xsl:evaluate xpath="@*:use-when"
// with-params="accumulator-before('static-vars')"/>).
//
// §10.4.2: the target expression's focus is the context-item attribute's
// value (absent without one), its variables the parameters only, and the
// XSLT-specific context (current(), groups, accumulators of a node, the
// current rule) absent. So with no context-item and parameters that hold
// no node of the stream, it reaches nothing of the stream, whatever it
// says: the instruction is motionless when its own expressions are. An
// accumulator's value holds no node of the parse (accumStreamWhy's held
// checks), nor do atomic values, arrays and maps of them (nodeFree). That
// the target may load the streamed URI and compare what it loads with a
// node of the stream needs a node of the stream given to it, which the
// alias guard counts (streamalias_l7.go, aliasFlow's evaluate).

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// evaluateMotionless checks el, an xsl:evaluate the analysis found
// motionless, at focus f.
func (b *streamBuilder) evaluateMotionless(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		n := a.Name()
		if n.URI != "" {
			continue
		}
		switch n.Local {
		case "xpath":
			if !b.expr(el, a.Value(), f, false) { // atomized to the expression's text
				return false
			}
		case "with-params":
			if e := parseAt(el, a.Value()); e == nil || !evalParamsNodeFree(el, e) {
				return b.refuse("xsl:evaluate with-params that may hold a node: " + a.Value())
			}
			if !b.expr(el, a.Value(), f, false) {
				return false
			}
		case "as", "base-uri", "schema-aware":
			if !b.avt(el, a.Value(), f) {
				return false
			}
		default: // context-item, namespace-context: a node of the stream
			return b.refuse("attribute " + n.Local + " of xsl:evaluate")
		}
	}
	if hasContent(el) {
		return b.refuse("xsl:evaluate with content")
	}
	return true
}

// evalParamsNodeFree reports whether e, a with-params map written on el,
// holds no node: an accumulator's value by a literal name (of the focus, or
// of an attribute's parent: parentAccum), a local variable bound to one, or
// atomic values, arrays and maps of them.
func evalParamsNodeFree(el *xdm.Node, e xpath.Expr) bool {
	if accumCall(e) != nil || accumCall(parentStep(e)) != nil {
		return true
	}
	if v, ok := e.(*xpath.VarRef); ok && v.Name.URI == "" {
		if d := localVariable(el, v.Name.Local); d != nil && d.Attr("", "select") != nil && !hasContent(d) {
			if x := parseAt(d, d.AttrValue("select")); x != nil {
				return evalParamsNodeFree(d, x)
			}
		}
		return false
	}
	return nodeFree(e) // streamcopy_nodefree.go
}

// accumCall is e when it is accumulator-before or -after by a literal name.
func accumCall(e xpath.Expr) *xpath.FuncCall {
	x, ok := e.(*xpath.FuncCall)
	if !ok || x.Name.URI != fnNS || len(x.Args) != 1 ||
		x.Name.Local != "accumulator-before" && x.Name.Local != "accumulator-after" {
		return nil
	}
	if _, lit := x.Args[0].(*xpath.Literal); !lit {
		return nil
	}
	return x
}

// parentStep is S when e is ../S, else nil.
func parentStep(e xpath.Expr) xpath.Expr {
	p, ok := e.(*xpath.PathExpr)
	if !ok || p.Root || len(p.Steps) != 2 {
		return nil
	}
	st, ok := p.Steps[0].(*xpath.Step)
	if !ok || st.Axis != xpath.AxisParent || len(st.Predicates) > 0 {
		return nil
	}
	if kt, ok := st.Test.(*xpath.KindTest); !ok || !kt.Any {
		return nil
	}
	return p.Steps[1]
}

// parentAccum reports whether e is ../accumulator-before('A') at the focus
// of an attribute rule (evaluate-046): the attribute is one of a view's, at
// its element's start tag, where A's value before the element is known, as
// accumulator-before(.) of an element's view is.
func (b *streamBuilder) parentAccum(e xpath.Expr, f vfocus) bool {
	x := accumCall(parentStep(e))
	return x != nil && x.Name.Local == "accumulator-before" && f == vLeaf && b.x != nil && b.x.class == classAttr
}

// localVariable is the xsl:variable named name in scope at el, declared in
// the template or function el is in, or nil.
func localVariable(el *xdm.Node, name string) *xdm.Node {
	for n := el; n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if isXSL(n, "template") || isXSL(n, "function") {
			return nil
		}
		for s := n.PrevSibling(); s != nil; s = s.PrevSibling() {
			if s.Kind() == xdm.KindElement && (isXSL(s, "variable") || isXSL(s, "param")) && s.AttrValue("name") == name {
				if isXSL(s, "param") {
					return nil
				}
				return s
			}
		}
	}
	return nil
}

// analyzeMotionless checks el, an xsl:analyze-string the analysis found
// motionless, at focus f: its select and attribute value templates, and the
// content of its matching-substring and non-matching-substring, whose focus
// is a string (checked as f, which reads no less).
func (b *streamBuilder) analyzeMotionless(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		n := a.Name()
		switch {
		case n.URI != "":
		case n.Local == "select":
			if !b.expr(el, a.Value(), f, false) {
				return false
			}
		case n.Local == "regex", n.Local == "flags":
			if !b.avt(el, a.Value(), f) {
				return false
			}
		default:
			return b.refuse("attribute " + n.Local + " of xsl:analyze-string")
		}
	}
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "matching-substring") && !isXSL(ch, "non-matching-substring") || ch.NumAttrs() > 0 {
			return b.refuse("xsl:analyze-string content")
		}
		if !b.content(ch, f) {
			return false
		}
	}
	return true
}
