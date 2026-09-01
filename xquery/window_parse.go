package xquery

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// forIsWindow reports whether the "for" at the cursor begins a window clause
// rather than an ordinary one, which the word after it decides.
func (p *parser) forIsWindow() bool {
	save := p.pos
	defer func() { p.pos = save }()
	if !p.consumeWord("for") {
		return false
	}
	w := p.peekWordAt()
	return w == "sliding" || w == "tumbling"
}

// parseWindowClause parses a tumbling or sliding window: [51]-[58].
func (p *parser) parseWindowClause() ([]clause, error) {
	if !p.consumeWord("for") {
		return nil, p.errorf("XPST0003: expected %q", "for")
	}
	c := &windowClause{}
	switch {
	case p.consumeWord("sliding"):
		c.sliding = true
	case p.consumeWord("tumbling"):
	default:
		return nil, p.errorf("XPST0003: expected %q or %q after %q",
			"tumbling", "sliding", "for")
	}
	if !p.consumeWord("window") {
		return nil, p.errorf("XPST0003: expected %q", "window")
	}
	name, err := p.parseVarName()
	if err != nil {
		return nil, err
	}
	c.name = name
	if err := p.parseOptionalType(); err != nil {
		return nil, err
	}
	typ := p.lastType
	if !p.consumeWord("in") {
		return nil, p.errorf("XPST0003: expected %q after $%s",
			"in", name.Lexical())
	}
	// A window variable is bound to a sequence, so its declared type
	// constrains that sequence — but it constrains each *window*, not the
	// sequence the windows are cut from, and the windows are not known until
	// the clause runs. The check therefore cannot be compiled into the
	// binding expression the way a "let"'s is; it is applied per window at
	// evaluation instead.
	c.seq, err = p.parseClauseExpr()
	if err != nil {
		return nil, err
	}
	if typ != "" {
		c.typeCheck, err = p.compileTypeCheck(typ, false)
		if err != nil {
			return nil, err
		}
	}

	if !p.consumeWord("start") {
		return nil, p.errorf("XPST0003: a window clause needs a %q condition",
			"start")
	}
	if err := p.parseWindowVars(&c.start); err != nil {
		return nil, err
	}
	// "only end" and "end" are both end conditions; "only" merely discards
	// the window that never closed.
	if p.consumeWord("only") {
		c.only = true
		if !p.consumeWord("end") {
			return nil, p.errorf("XPST0003: expected %q after %q", "end",
				"only")
		}
		c.hasEnd = true
	} else if p.consumeWord("end") {
		c.hasEnd = true
	}
	if c.hasEnd {
		if err := p.parseWindowVars(&c.end); err != nil {
			return nil, err
		}
	} else if c.sliding {
		// §3.10.4 makes the end condition optional only for a tumbling
		// window, whose windows partition the sequence and so end where the
		// next begins. A sliding window has no such rule and would have no
		// way to end at all.
		return nil, p.errorf(
			"XPST0003: a sliding window requires an %q condition", "end")
	}
	if err := c.checkDistinct(); err != nil {
		return nil, p.errorf("%v", err)
	}
	return []clause{c}, nil
}

// checkDistinct enforces XQST0103: the variables a window clause binds must
// all differ.
//
// The rule exists because a window clause's variables are bound together
// rather than in sequence, so a repeated name has no reading — unlike two
// "for" clauses, where the second shadows the first and the query is
// well-defined. Saxon and BaseX both raise this statically, and so does this,
// because nothing about it depends on the data.
func (c *windowClause) checkDistinct() error {
	seen := map[xdm.QName]bool{c.name: true}
	for _, v := range []*windowVars{&c.start, &c.end} {
		for _, n := range v.names() {
			if seen[n] {
				return fmt.Errorf(
					"XQST0103: the window clause binds $%s more than once",
					n.Lexical())
			}
			seen[n] = true
		}
	}
	return nil
}

// parseWindowVars parses one boundary condition's variables and its "when".
//
// The four are all optional and all positional — "$x at $i previous $p next
// $n" — so each is tried in the order the grammar writes them and skipped
// when its keyword is absent.
func (p *parser) parseWindowVars(v *windowVars) error {
	p.skipSpaceAndComments()
	if p.lookingAt("$") {
		name, err := p.parseVarName()
		if err != nil {
			return err
		}
		v.item, v.hasItem = name, true
	}
	if p.consumeWord("at") {
		name, err := p.parseVarName()
		if err != nil {
			return err
		}
		v.pos, v.hasPos = name, true
	}
	if p.consumeWord("previous") {
		name, err := p.parseVarName()
		if err != nil {
			return err
		}
		v.prev, v.hasPrev = name, true
	}
	if p.consumeWord("next") {
		name, err := p.parseVarName()
		if err != nil {
			return err
		}
		v.next, v.hasNext = name, true
	}
	if !p.consumeWord("when") {
		return p.errorf("XPST0003: expected %q in a window condition", "when")
	}
	when, err := p.parseClauseExpr()
	if err != nil {
		return err
	}
	v.when = when
	return nil
}
