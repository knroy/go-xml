package xquery

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// ifExpr is "if (Expr) then ExprSingle else ExprSingle", XQuery 3.1 §3.8,
// production [77].
//
// XPath has the same expression and reads it perfectly, so this exists for one
// reason: §3.8 requires that "exactly one of the two ExprSingles is
// evaluated", and the route by which a branch holding a constructor or a
// FLWOR reached xpath could not honour it. Such a branch is lifted into a
// variable that is bound before the rewritten expression runs (see
// operand.go), so both branches were evaluated and an error in the one not
// taken was raised — "if ($n instance of element()) then element
// {node-name($n)} {...} else $n" failed on a text node, where the whole point
// of the test was to keep the constructor away from one.
//
// Only such an expression is read here. Where both branches are ordinary
// XPath the expression goes to xpath as before, which keeps one implementation
// of the common case rather than two that must agree.
type ifExpr struct {
	cond *enclosed
	then node
	els  node
}

// parseIf parses a conditional whose branches this parser must read.
//
// "if" commits on a following "(" the way "switch" does, since "if" is a
// legal element name and a function may be declared under it. Unlike switch
// there is no second keyword to confirm the reading before any source is
// consumed, so a "then" that does not follow is reported rather than backed
// out of: nothing else in the grammar spells "if (" at the start of an
// ExprSingle.
func (p *parser) parseIf() (node, bool, error) {
	save := p.pos
	if !needsXQueryParser(p.src[p.pos:]) {
		// Every branch is ordinary XPath, and xpath reads the whole
		// expression — including the laziness, which is its own.
		return nil, false, nil
	}
	p.pos += len("if")
	p.skipSpaceAndComments()
	if !p.lookingAt("(") {
		p.pos = save
		return nil, false, nil
	}
	cond, err := p.parseParenExpr()
	if err != nil {
		p.pos = save
		return nil, false, nil
	}
	if !p.consumeKeyword("then") {
		p.pos = save
		return nil, false, nil
	}
	// "else" ends the "then" branch. It cannot appear at depth zero inside an
	// ExprSingle for the reason every other stop keyword cannot: a bare
	// "else" there would have to be a name, and a name cannot follow a
	// complete expression.
	then, err := p.scanExprSingle("else")
	if err != nil {
		return nil, true, err
	}
	if !p.consumeKeyword("else") {
		return nil, true, p.errorf("XPST0003: expected %q after the %q branch",
			"else", "then")
	}
	els, err := p.scanExprSingle()
	if err != nil {
		return nil, true, err
	}
	return &ifExpr{cond: asEnclosed(cond), then: then, els: els}, true, nil
}

func (n *ifExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.run(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq)
}

func (n *ifExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	return n.run(ctx)
}

// run takes the effective boolean value of the condition and evaluates the
// branch it selects, and only that branch.
func (n *ifExpr) run(ctx *evalContext) (xdm.Sequence, error) {
	seq, err := n.cond.sequence(ctx)
	if err != nil {
		return nil, err
	}
	b, err := xpath.EffectiveBooleanValue(seq)
	if err != nil {
		return nil, err
	}
	if b {
		return itemSequence(n.then, ctx)
	}
	return itemSequence(n.els, ctx)
}
