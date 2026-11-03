package xslt

// Populations (lane G2): what an xsl:apply-templates, xsl:for-each or
// grouping hands its body, beyond a bare path of the stream.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// leafEmptySelect reports whether el, an xsl:apply-templates in the body of
// a rule run on a text, comment, processing-instruction or attribute node
// (focus vLeaf: a complete node, off the cursor), selects nothing there: its
// select is absent (child::node()) or one child or attribute step with no
// predicate. Such a node has neither children nor attributes, so the
// instruction runs as the tree runs it, over the empty sequence -- the
// identity template's <xsl:apply-templates select="@*"/> and
// <xsl:apply-templates/> on a text node.
func leafEmptySelect(el *xdm.Node) bool {
	src := el.AttrValue("select")
	if el.Attr("", "select") == nil {
		return true
	}
	st, ok := oneStep(parseAt(el, src)).(*xpath.Step)
	return ok && len(st.Predicates) == 0 && (st.Axis == xpath.AxisChild || st.Axis == xpath.AxisAttribute)
}

// oneStep is e, or the step a relative path of one step is ("node()" parses
// as such a path).
func oneStep(e xpath.Expr) xpath.Expr {
	if pe, ok := e.(*xpath.PathExpr); ok && !pe.Root && len(pe.Steps) == 1 {
		return pe.Steps[0]
	}
	return e
}
