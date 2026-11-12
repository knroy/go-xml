package xslt

// xsl:copy of an attribute of the stream with validation or type (lane L7,
// the V bucket's si-copy-116/-117).

import (
	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// copiesAttribute reports whether xsl:copy el selects attributes only: its
// select is a path ending in an attribute step. With a type, the attribute
// branch of copyInstr.Execute assesses a detached copy of a node of the
// stream (streamAssessedAttr), which needs nothing beyond the attribute. Strict
// and lax are not taken: the declaration found may be namespace-sensitive
// (xs:QName), whose value the attribute's parent element resolves.
func copiesAttribute(el *xdm.Node) bool {
	a := el.Attr("", "select")
	if a == nil {
		return false
	}
	pe, ok := parseAt(el, a.Value()).(*xpath.PathExpr)
	if !ok || len(pe.Steps) == 0 {
		return false
	}
	st, ok := pe.Steps[len(pe.Steps)-1].(*xpath.Step)
	return ok && st.Axis == xpath.AxisAttribute
}

// streamAssessedAttr is the attribute xsl:copy's validation assesses for n: n
// itself, or, against a type (typed), for a node copied out of a stream (a
// view's attribute, or one of a grounded copy) a parentless attribute of the
// same name and value.
//
// The validator's typed copy of an attribute with a parent copies the whole
// tree it is in, which for a view is a document node with no content. What
// the assessment reads of the attribute is its name and value: a type is a
// simple type (XTTE1535 refuses a complex one first), never one sensitive to
// namespaces or to the document (XTTE1545 refuses xs:QName, xs:NOTATION,
// xs:ID, xs:IDREF(S) and xs:ENTITY/-IES first). The error
// carries no node path (the validator's path stack is empty for an
// attribute) and no position (a document read as a tree for the stream is
// parsed without positions, as the tree's is; a cached tree is not
// streamed). And copyInstr.Execute keeps only the result's name and value.
func streamAssessedAttr(n *xdm.Node, typed bool) *xdm.Node {
	if !typed || xdmburst.FromStream == nil || !xdmburst.FromStream(n) {
		return n
	}
	return xdm.NewNode(xdm.KindAttribute, n.Name(), n.Value())
}
