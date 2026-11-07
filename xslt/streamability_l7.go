package xslt

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// l7Builtin assesses the cases of fn:root that §19.8.9.18 settles without
// the parts of its rule this analysis does not model; ok false leaves the
// call unmodelled, as before.
//
// "The zero-argument function root() is equivalent to root(.). Given the
// expression root(X), if the static type of X is U{document-node()}, and if
// its posture is striding, then root(X) is rewritten as X. Otherwise, it is
// rewritten as head((X)/ancestor-or-self::node())."
//
// A grounded X is X's posture and sweep: every node reached from a grounded
// node is grounded (§19.8.8.9), and head() keeps the posture. A grounded X
// standing for a node of the stream (the streaming parameter,
// streamedGrounded) is not taken: its ancestors are the stream's.
// sf-snapshot-0102's $s/root() is the case.
//
// A crawling or climbing X fails the "striding" condition, and a striding X
// that can never be a document node (neverDocument) fails the type one, so
// both take the head() form: ancestor-or-self from any of the three is
// climbing and motionless (the §19.8.8.9 table), the path keeps X's sweep
// (§19.8.8.8) and head transmits. A striding X that may be a document node
// needs the static type this analysis does not infer -- rewriting it would
// refuse root()//x at a document node -- and stays unmodelled.
func (a *analyzer) l7Builtin(x *xpath.FuncCall) (props, bool) {
	if x.Name.Local != "root" || len(x.Args) > 1 {
		return props{}, false
	}
	var arg xpath.Expr = &xpath.ContextItem{}
	if len(x.Args) == 1 {
		arg = x.Args[0]
	}
	o := a.operandOf(arg, usageNavigation)
	switch {
	case !o.props.streamable():
		// A roaming X roams on any axis (§19.8.8.9), so the rewrite does.
		return roamingFreeRanging, true
	case o.streamedGrounded:
		return props{}, false
	case o.props.posture == postureGrounded:
		return o.props, true
	case o.props.posture == postureCrawling, o.props.posture == postureClimbing,
		o.props.posture == postureStriding && neverDocument(arg):
		return props{postureClimbing, o.props.sweep}, true
	}
	return props{}, false
}

// neverDocument reports whether e, syntactically, can only select nodes
// that are not document nodes: an axis step whose axis or node test excludes
// one, or a path or filter ending in such a step.
func neverDocument(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step:
		switch x.Axis {
		case xpath.AxisSelf, xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf,
			xpath.AxisDescendantOrSelf:
			// A name test selects elements, attributes or namespaces.
			if _, ok := x.Test.(*xpath.NameTest); ok {
				return true
			}
			kt, ok := x.Test.(*xpath.KindTest)
			return ok && !kt.Any && kt.Kind != xdm.KindDocument
		}
		return true
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && neverDocument(x.Steps[len(x.Steps)-1])
	case *xpath.FilterExpr:
		return neverDocument(x.Base)
	}
	return false
}
