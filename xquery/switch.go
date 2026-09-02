package xquery

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// switchExpr is "switch (Expr) case ... default return ...", XQuery 3.1
// §3.15, productions [71]-[73].
type switchExpr struct {
	operand *enclosed
	cases   []switchCase
	deflt   node
}

// switchCase is one clause. A clause may carry several case operands —
// "case 'COW' case 'OX' return ..." — which share one return expression, and
// which are separate operands rather than one sequence.
type switchCase struct {
	operands []*enclosed
	ret      node
}

// parseSwitch parses a switch expression.
//
// "switch" commits only on a following "(", because "switch" is a legal
// element name and "switch(1)" would otherwise be read as this rather than as
// a function call. The grammar puts a parenthesised Expr there, so the two are
// genuinely ambiguous until the "case" keyword; committing on the parenthesis
// and letting the case list fail is what BaseX and Saxon both do.
func (p *parser) parseSwitch() (node, bool, error) {
	save := p.pos
	p.pos += len("switch")
	p.skipSpaceAndComments()
	if !p.lookingAt("(") {
		p.pos = save
		return nil, false, nil
	}
	operand, err := p.parseParenExpr()
	if err != nil {
		p.pos = save
		return nil, false, nil
	}
	if !p.consumeKeyword("case") {
		// Not a switch after all: a function call named "switch".
		p.pos = save
		return nil, false, nil
	}
	sw := &switchExpr{operand: operand}
	for {
		var c switchCase
		for {
			op, err := p.scanExprSingle("case", "default", "return")
			if err != nil {
				return nil, true, err
			}
			c.operands = append(c.operands, asEnclosed(op))
			if !p.consumeKeyword("case") {
				break
			}
		}
		if !p.consumeKeyword("return") {
			return nil, true, p.errorf("XPST0003: expected %q in a %q clause",
				"return", "case")
		}
		ret, err := p.scanExprSingle("case", "default")
		if err != nil {
			return nil, true, err
		}
		c.ret = ret
		sw.cases = append(sw.cases, c)
		if !p.consumeKeyword("case") {
			break
		}
	}
	if !p.consumeKeyword("default") {
		return nil, true, p.errorf(
			"XPST0003: a %q expression needs a %q clause", "switch", "default")
	}
	if !p.consumeKeyword("return") {
		return nil, true, p.errorf("XPST0003: expected %q after %q",
			"return", "default")
	}
	d, err := p.scanExprSingle()
	if err != nil {
		return nil, true, err
	}
	sw.deflt = d
	return sw, true, nil
}

// parseParenExpr parses "( Expr )" and returns its body as an enclosed
// expression, leaving p.pos past the closing parenthesis.
//
// The body is parsed as a query body rather than handed straight to xpath so
// that it may hold a constructor, which "switch (<a>b</a>) ..." does.
func (p *parser) parseParenExpr() (*enclosed, error) {
	if !p.lookingAt("(") {
		return nil, p.errorf("XPST0003: expected %q", "(")
	}
	end, err := findParen(p.src, p.pos)
	if err != nil {
		return nil, err
	}
	body := p.src[p.pos+1 : end]
	p.pos = end + 1
	inner := &parser{src: body, sc: p.sc, version: p.version}
	items, err := inner.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return &enclosed{items: items}, nil
}

// asEnclosed wraps a parsed item so that its value can be taken rather than
// its contribution to a tree. A node that is already an enclosed is used as
// it stands; anything else — a constructor — becomes the single item of one.
func asEnclosed(n node) *enclosed {
	if e, ok := n.(*enclosed); ok {
		return e
	}
	return &enclosed{items: []node{n}}
}

func (n *switchExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.run(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

func (n *switchExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	return n.run(ctx)
}

// run picks the first case whose operand equals the switch operand.
//
// §3.15 defines the comparison precisely and it is not "eq": both operands are
// atomised and each must be a single item or the empty sequence — anything
// longer is XPTY0004 — and then they are compared with fn:deep-equal, which
// makes two empty sequences equal to each other where "eq" would yield the
// empty sequence, and which does not raise on comparing an xs:string with an
// xs:integer but simply answers false. Using "eq" here would make
// "switch (1) case 'a' return ..." an error rather than a non-match.
func (n *switchExpr) run(ctx *evalContext) (xdm.Sequence, error) {
	key, err := switchOperand(n.operand, ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range n.cases {
		for _, op := range c.operands {
			v, err := switchOperand(op, ctx)
			if err != nil {
				return nil, err
			}
			eq, err := switchEqual(key, v, ctx)
			if err != nil {
				return nil, err
			}
			if eq {
				return itemSequence(c.ret, ctx)
			}
		}
	}
	return itemSequence(n.deflt, ctx)
}

// switchOperand atomises a switch operand and checks its cardinality.
func switchOperand(e *enclosed, ctx *evalContext) (xdm.Item, error) {
	seq, err := e.sequence(ctx)
	if err != nil {
		return nil, err
	}
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return nil, err
	}
	switch len(atoms) {
	case 0:
		return nil, nil
	case 1:
		return atoms[0], nil
	}
	return nil, xdm.Errorf("XPTY0004",
		"a switch operand must be a single atomic value or the empty sequence")
}

// switchEqual compares two atomised switch operands with fn:deep-equal's rule
// for single atomic values.
//
// Two absent operands are equal, and an absent one is unequal to a present
// one; otherwise the comparison is the one fn:deep-equal makes, which treats
// values of incomparable types as unequal rather than as an error.
func switchEqual(a, b xdm.Item, ctx *evalContext) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	return xpath.DeepEqualSequences(ctx.xp, xdm.One(a), xdm.One(b))
}

// itemSequence evaluates a parsed return clause to its value.
func itemSequence(n node, ctx *evalContext) (xdm.Sequence, error) {
	return asEnclosed(n).sequence(ctx)
}
