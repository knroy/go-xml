package xquery

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// typeswitchExpr is "typeswitch (Expr) case ... default ... return ...",
// XQuery 3.1 §3.14, productions [74]-[77].
type typeswitchExpr struct {
	operand  *enclosed
	cases    []typeswitchCase
	defltVar *xdm.QName
	deflt    node
}

// typeswitchCase is one clause: an optional variable, one or more sequence
// types that select it, and the expression it returns.
//
// [75] CaseClause admits several types separated by "|" — a SequenceTypeUnion
// — which is one clause matching any of them, not several clauses.
type typeswitchCase struct {
	variable *xdm.QName
	types    []xpath.SequenceType
	ret      node
}

// parseTypeswitch parses a typeswitch expression.
//
// Unlike switch, "typeswitch" is a reserved function name in XQuery, so a
// following "(" can only be this. It is still checked, because a query that
// wrote "typeswitch" alone should get a message about the parenthesis rather
// than about whatever came next.
func (p *parser) parseTypeswitch() (node, bool, error) {
	start := p.pos
	p.pos += len("typeswitch")
	p.skipSpaceAndComments()
	if !p.lookingAt("(") {
		return nil, true, p.errorAt(start,
			"XPST0003: expected %q after %q", "(", "typeswitch")
	}
	operand, err := p.parseParenExpr()
	if err != nil {
		return nil, true, err
	}
	ts := &typeswitchExpr{operand: operand}
	for p.consumeKeyword("case") {
		c, err := p.parseCaseClause()
		if err != nil {
			return nil, true, err
		}
		ts.cases = append(ts.cases, c)
	}
	if len(ts.cases) == 0 {
		return nil, true, p.errorf(
			"XPST0003: a %q expression needs at least one %q clause",
			"typeswitch", "case")
	}
	if !p.consumeKeyword("default") {
		return nil, true, p.errorf(
			"XPST0003: a %q expression needs a %q clause",
			"typeswitch", "default")
	}
	p.skipSpaceAndComments()
	if p.lookingAt("$") {
		v, err := p.parseVarName()
		if err != nil {
			return nil, true, err
		}
		ts.defltVar = &v
	}
	if !p.consumeKeyword("return") {
		return nil, true, p.errorf("XPST0003: expected %q after %q",
			"return", "default")
	}
	// The default branch is the last thing in the typeswitch, so no keyword
	// of this construct follows it -- but a clause of an enclosing FLWOR may,
	// and its keyword ends this ExprSingle just as "case" ended the ones
	// before it. See enclosingClauseStops.
	d, err := p.scanExprSingle(enclosingClauseStops...)
	if err != nil {
		return nil, true, err
	}
	ts.deflt = d
	return ts, true, nil
}

// parseCaseClause parses [75] "case ($VarName as)? SequenceTypeUnion return
// ExprSingle", with "case" already consumed.
//
// The "$v as" is optional and its absence is not merely a missing binding: a
// clause with no variable is written "case xs:integer return ...", and telling
// the two apart needs a lookahead for "$" alone, because a SequenceType never
// begins with one.
func (p *parser) parseCaseClause() (typeswitchCase, error) {
	var c typeswitchCase
	p.skipSpaceAndComments()
	if p.lookingAt("$") {
		v, err := p.parseVarName()
		if err != nil {
			return c, err
		}
		c.variable = &v
		if !p.consumeKeyword("as") {
			return c, p.errorf("XPST0003: expected %q after a %q variable",
				"as", "case")
		}
	}
	for {
		st, err := p.parseTypeUntil("return", "|")
		if err != nil {
			return c, err
		}
		c.types = append(c.types, st)
		p.skipSpaceAndComments()
		if !p.consume("|") {
			break
		}
	}
	if !p.consumeKeyword("return") {
		return c, p.errorf("XPST0003: expected %q in a %q clause",
			"return", "case")
	}
	ret, err := p.scanExprSingle("case", "default")
	if err != nil {
		return c, err
	}
	c.ret = ret
	return c, nil
}

// parseTypeUntil takes the run of source up to the next "return" or "|" at
// nesting depth zero and asks xpath to parse it as a SequenceType.
//
// The end has to be found here because a SequenceType is not self-delimiting
// against what follows it: "element(a) return" and "element(a)|element(b)"
// both continue past a point the type parser would happily stop at, and
// handing it the rest of the clause would have it report an error about
// "return" rather than parse the type. The stop keywords cannot appear inside
// a type at depth zero — a kind test's arguments are parenthesised and an
// occurrence indicator is one character — so scanning for them is exact.
func (p *parser) parseTypeUntil(stops ...string) (xpath.SequenceType, error) {
	p.skipSpaceAndComments()
	start := p.pos
	i := p.pos
	depth := 0
	for i < len(p.src) {
		c := p.src[i]
		switch c {
		case '\'', '"':
			end, err := skipString(p.src, i)
			if err != nil {
				return xpath.SequenceType{}, err
			}
			i = end + 1
			continue
		case '(':
			depth++
		case '{':
			depth++
		case ')', '}':
			depth--
			if depth < 0 {
				goto done
			}
		case '|':
			if depth == 0 {
				goto done
			}
		default:
			if depth == 0 && isNameByte(c) && (i == start || !isNameByte(p.src[i-1])) {
				for _, kw := range stops {
					if kw == "|" {
						continue
					}
					if strings.HasPrefix(p.src[i:], kw) &&
						(i+len(kw) == len(p.src) || !isNameByte(p.src[i+len(kw)])) {
						goto done
					}
				}
			}
		}
		i++
	}
done:
	src := strings.TrimSpace(p.src[start:i])
	p.pos = i
	if src == "" {
		return xpath.SequenceType{}, p.errorAt(start,
			"XPST0003: expected a sequence type")
	}
	st, err := xpath.ParseSequenceType(src, p.sc)
	if err != nil {
		return xpath.SequenceType{}, p.errorAt(start, "%v", err)
	}
	return st, nil
}

func (n *typeswitchExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.run(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

func (n *typeswitchExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	return n.run(ctx)
}

// run evaluates the operand once and returns the first clause whose type
// matches it.
//
// The operand is evaluated once and only once, which matters: §3.14 says so,
// and an operand with a side effect — fn:error, or a document load — must not
// be run again for each clause. The match is a whole-sequence one rather than
// an item one, because the clause writes a SequenceType and its occurrence
// indicator is part of what it tests: "case xs:integer" does not match a
// two-item sequence.
//
// The variable, where the clause has one, is bound to the operand's value
// untouched. It is not treated-as or cast to the matched type — the value
// already conforms, so there is nothing to convert.
func (n *typeswitchExpr) run(ctx *evalContext) (xdm.Sequence, error) {
	seq, err := n.operand.sequence(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range n.cases {
		for _, t := range c.types {
			if !t.Matches(seq) {
				continue
			}
			return itemSequence(c.ret, bindCaseVar(ctx, c.variable, seq))
		}
	}
	return itemSequence(n.deflt, bindCaseVar(ctx, n.defltVar, seq))
}

// bindCaseVar returns ctx with the clause's variable bound, or ctx itself
// when the clause has none.
func bindCaseVar(ctx *evalContext, name *xdm.QName, seq xdm.Sequence) *evalContext {
	if name == nil {
		return ctx
	}
	return &evalContext{xp: ctx.xp.WithVar(*name, seq), sc: ctx.sc}
}
