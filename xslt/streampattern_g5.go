package xslt

// Lane g5: patterns the stream's matcher need not test (bucket PT).

import "github.com/knroy/go-xml/v2/xpath"

// matchesNoNode reports whether every alternative of p is ".[. instance of
// T]" (perhaps with more predicates after) for an item type T no node is an
// instance of: a map, array, function or atomic type. Such a rule matches no
// node, so it decides nothing for any class of node (patternClasses); items
// it does match are not the stream's.
func matchesNoNode(p *Pattern) bool {
	if len(p.alts) > 0 || len(p.general) == 0 {
		return false
	}
	for _, g := range p.general {
		f, ok := g.expr.Expr().(*xpath.FilterExpr)
		if !g.selfOnly || !ok || !isContextItem(f.Base) || len(f.Predicates) == 0 {
			return false
		}
		io, ok := f.Predicates[0].(*xpath.InstanceOfExpr)
		if !ok || !isContextItem(io.Operand) {
			return false
		}
		t := io.Type
		if t.Empty || !(t.IsMapTest || t.IsArrayTest || t.IsFunctionTest || t.HasAtomicType || t.IsNumericType) {
			return false
		}
	}
	return true
}
