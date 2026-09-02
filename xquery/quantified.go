package xquery

import (
	"github.com/knroy/go-xml/xdm"
)

// quantified is "some ... satisfies" or "every ... satisfies": §3.11.2,
// production [42].
//
// XPath 3.1 has this expression too, and xpath implements it, so a quantified
// expression is normally handed straight through. What is not shared is the
// type declaration: XQuery's QuantifiedExpr admits "some $x as xs:integer in
// E satisfies P" and XPath's does not, and that one difference is why this
// exists.
//
// It is not a FLWOR — the specification puts it in a section of its own — but
// it is the same machinery: a stream of tuples, one per combination of
// bindings, with a predicate over each. So it is compiled to the same clauses
// and evaluated over the same pipeline, and the only thing that differs is
// what is done with the surviving tuples.
type quantified struct {
	// every reports the quantifier: "every" is true where all tuples must
	// satisfy the test, "some" false where one is enough.
	every    bool
	bindings []clause
	test     *compiledExpr
}

func (q *quantified) eval(ctx *evalContext) (xdm.Sequence, error) {
	stream := []tuple{{}}
	for _, c := range q.bindings {
		var err error
		stream, err = c.apply(stream, ctx)
		if err != nil {
			return nil, err
		}
	}
	for _, t := range stream {
		ok, err := q.test.evalBool(t.sub(ctx))
		if err != nil {
			return nil, err
		}
		// "some" is satisfied by the first true and "every" refuted by the
		// first false, and both stop there. Short-circuiting is not only an
		// optimisation: §3.11.2 permits the test to be evaluated on any
		// subset sufficient to decide the answer, so an error raised by a
		// later tuple need not surface once the answer is known.
		if ok != q.every {
			return xdm.One(xdm.NewBoolean(!q.every)), nil
		}
	}
	// An empty stream makes "every" vacuously true and "some" false, which
	// falls out of the quantifier rather than needing a case of its own.
	return xdm.One(xdm.NewBoolean(q.every)), nil
}

// evalNode implements node.
func (q *quantified) evalNode(out *builderRef, ctx *evalContext) error {
	seq, err := q.eval(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

// parseQuantified parses a quantified expression.
//
// Every binding is compiled to a forClause, which is exactly what the
// quantifier means: the tuples it tests are the cartesian product of the
// bindings, and that is what a run of "for" clauses produces.
func (p *parser) parseQuantified() (*quantified, error) {
	q := &quantified{}
	switch {
	case p.consumeWord("every"):
		q.every = true
	case p.consumeWord("some"):
	default:
		return nil, p.errorf("XPST0003: expected %q or %q", "some", "every")
	}
	for {
		name, err := p.parseVarName()
		if err != nil {
			return nil, err
		}
		if err := p.parseOptionalType(); err != nil {
			return nil, err
		}
		typ := p.lastType
		if !p.consumeWord("in") {
			return nil, p.errorf("XPST0003: expected %q after $%s", "in",
				name.Lexical())
		}
		// The declared type constrains each item bound, as it does in a
		// "for" clause and for the same reason.
		seq, err := p.parseTypedClauseExpr(typ, true)
		if err != nil {
			return nil, err
		}
		q.bindings = append(q.bindings, &forClause{name: name, seq: seq})
		if !p.consumeAtDepthZero(",") {
			break
		}
	}
	if !p.consumeWord("satisfies") {
		return nil, p.errorf("XPST0003: expected %q", "satisfies")
	}
	test, err := p.parseClauseExpr()
	if err != nil {
		return nil, err
	}
	q.test = test
	return q, nil
}

// looksLikeQuantified reports whether a quantified expression starts here.
//
// As with "for", the keyword alone does not decide it: "some" and "every" are
// not reserved, so "some(1)" is a function call and "some" on its own an
// element name. A quantified expression binds a variable, so the keyword must
// be followed by "$".
func (p *parser) looksLikeQuantified() bool {
	save := p.pos
	defer func() { p.pos = save }()
	switch p.peekWord() {
	case "some", "every":
	default:
		return false
	}
	p.scanNCName()
	p.skipSpaceAndComments()
	return p.lookingAt("$")
}
