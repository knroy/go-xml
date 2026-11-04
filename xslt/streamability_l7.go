package xslt

import "github.com/knroy/go-xml/v2/xpath"

// l7Builtin assesses the cases of fn:root and fn:fold-right that §19.8.9.18
// and §19.8.9.9 settle without the parts of their rules this analysis does
// not model; ok false leaves the call unmodelled, as before.
//
// fn:root: "root() is equivalent to root(.)", and root(X) is rewritten as
// head((X)/ancestor-or-self::node()) unless X is a striding document node.
// Only a grounded X is taken: every node reached from a grounded node is
// grounded (§19.8.8.9), and head() keeps the posture, so the call is X's
// posture and sweep. A grounded X standing for a node of the stream (the
// streaming parameter, streamedGrounded) is not taken: its ancestors are
// the stream's. sf-snapshot-0102's $s/root() is the case.
//
// fn:fold-right follows the general rules with its first argument's usage
// navigation. Only a first argument that is grounded and consuming is taken
// (sf-fold-right-003: P/data()): navigation of a grounded operand is the
// operand, so the general rules give the call grounded and consuming. Every
// other first argument -- the empty sequence of the analysis tests that
// stand for "a function the analysis does not model" among them -- stays
// unmodelled, so no verdict and no XTSE3430 changes there.
func (a *analyzer) l7Builtin(x *xpath.FuncCall) (props, bool) {
	switch {
	case x.Name.Local == "root" && len(x.Args) <= 1:
		var arg xpath.Expr = &xpath.ContextItem{}
		if len(x.Args) == 1 {
			arg = x.Args[0]
		}
		o := a.operandOf(arg, usageNavigation)
		if o.props.posture != postureGrounded || o.streamedGrounded || !o.props.streamable() {
			return props{}, false
		}
		return o.props, true
	case x.Name.Local == "fold-right" && len(x.Args) == 3:
		ops := []operand{
			a.operandOf(x.Args[0], usageNavigation),
			a.operandOf(x.Args[1], usageNavigation),
			a.operandOf(x.Args[2], usageInspection),
		}
		first := ops[0]
		if first.props.posture != postureGrounded || first.streamedGrounded || first.props.sweep != sweepConsuming {
			return props{}, false
		}
		p := a.combine(ops, false)
		if !p.streamable() || p.posture != postureGrounded {
			return props{}, false
		}
		return p, true
	}
	return props{}, false
}
