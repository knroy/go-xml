package xslt

import (
	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Patterns combining steps with intersect or except (lane MM3, finished by
// MM8:
// evaluate-046's match="xsl:*[@use-when] | (* except xsl:*)[@xsl:use-when]").
// The trust walk classifies them (§19.8.10, streamaccumulators.go's hooks
// below); the rule plan reads them as the one step they are equivalent to
// (streamSetOpAlts), while the streamed dispatch matches them with the
// tree's own matcher on the view (findTemplateFrom), as every rule.

// patternSetOp reports whether op is intersect or except, which §5.5.3
// lets a pattern combine patterns with as it does union: N matches
// P1 except P2 when it matches P1 and not P2, so the result is motionless
// when both are (§19.8.10 classifies every alternative).
func patternSetOp(op string) bool { return op == "intersect" || op == "except" }

// parenPatternFreeRanging classifies (P)[q]: a parenthesized pattern whose
// predicates apply to the node P matched. P is classified as a pattern; the
// predicates as top-level ones of a node that may have children, which
// counts an atomizing predicate absorbing (evaluate-046's
// (* except xsl:*)[@xsl:use-when]).
func parenPatternFreeRanging(x *xpath.FilterExpr, at *xdm.Node, matchedAllowsChildren bool) (bool, bool) {
	switch x.Base.(type) {
	case *xpath.BinaryOp, *xpath.PathExpr, *xpath.Step:
	default:
		return false, false
	}
	free, known := patternExprFreeRangingIn(x.Base, at, matchedAllowsChildren)
	if !known || free {
		return free, known
	}
	return patternPredicatesFreeRanging(x.Predicates, true, matchedAllowsChildren)
}

// streamSetOpAlts is p with each XSLT 3.0 alternative of the form
// (L except R)[q] or (L intersect R)[q] -- L and R single child steps by
// name, R with no predicate -- read as the step L[not(self::R)][q] (or
// L[self::R][q]): a node N matches it when it matches L, does (or does not)
// match R, and q holds of it; both steps test N and require it a child, so
// the one step is exactly the pair. nil when an alternative has another
// form. Only patternClassesAt reads it.
func streamSetOpAlts(p *Pattern) *Pattern {
	q := &Pattern{src: p.src, alts: append([]*patternAlt(nil), p.alts...)}
	for _, g := range p.general {
		if g.selfOnly || g.rooted || g.expr == nil {
			return nil
		}
		a := setOpAlt(g.expr.Expr())
		if a == nil {
			return nil
		}
		q.alts = append(q.alts, a)
	}
	return q
}

func setOpAlt(e xpath.Expr) *patternAlt {
	var preds []xpath.Expr
	if f, ok := e.(*xpath.FilterExpr); ok {
		e, preds = f.Base, f.Predicates
	}
	b, ok := e.(*xpath.BinaryOp)
	if !ok || !patternSetOp(b.Op) {
		return nil
	}
	l, r := childNameStep(b.Left), childNameStep(b.Right)
	if l == nil || r == nil || len(r.Predicates) > 0 {
		return nil
	}
	var self xpath.Expr = &xpath.PathExpr{Steps: []xpath.Expr{&xpath.Step{Axis: xpath.AxisSelf, Test: r.Test}}}
	if b.Op == "except" {
		self = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "not"}, Args: []xpath.Expr{self}}
	}
	all := append(append(append([]xpath.Expr{}, l.Predicates...), self), preds...)
	return &patternAlt{steps: []patternStep{{nodeTest: l.Test, preds: all, explicitAxis: l.Explicit}}}
}

// childNameStep is e when it is one child step by name test.
func childNameStep(e xpath.Expr) *xpath.Step {
	if p, ok := e.(*xpath.PathExpr); ok && !p.Root && len(p.Steps) == 1 {
		e = p.Steps[0]
	}
	s, ok := e.(*xpath.Step)
	if !ok || s.Axis != xpath.AxisChild {
		return nil
	}
	if _, ok := s.Test.(*xpath.NameTest); !ok {
		return nil
	}
	return s
}

// generalStep is the one step g is equivalent to, or nil.
func generalStep(g *generalPattern) *patternAlt {
	if g.selfOnly || g.rooted || g.expr == nil {
		return nil
	}
	return setOpAlt(g.expr.Expr())
}

// matchNode matches node against g: a node of a stream (a view, which has
// no content to evaluate the equivalent expression from its ancestors over)
// by its one step, any other as g.matches does. The two agree (§5.5.3).
func (g *generalPattern) matchNode(node *xdm.Node, ctx *xpath.Context) (bool, error) {
	if g.step != nil && xdmburst.FromStream(node) {
		return g.step.matches(node, ctx)
	}
	return g.matches(node, ctx)
}
