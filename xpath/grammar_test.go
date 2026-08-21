package xpath

import "testing"

// TestGrammarCoverage exercises every production of the XPath 2.0 grammar at
// least once.
//
// It is a coverage guard rather than a semantic test: it asserts only that
// each construct parses, which is what catches a production being dropped or
// broken by a change to the precedence ladder. The semantics are covered by
// the evaluation tests.
func TestGrammarCoverage(t *testing.T) {
	exprs := map[string]string{
		"ForExpr":            `for $x in (1,2) return $x`,
		"QuantifiedSome":     `some $x in (1) satisfies $x=1`,
		"QuantifiedEvery":    `every $x in (1) satisfies $x=1`,
		"IfExpr":             `if (1) then 2 else 3`,
		"OrExpr":             `1=1 or 2=2`,
		"AndExpr":            `1=1 and 2=2`,
		"ValueComp":          `1 eq 1`,
		"GeneralComp":        `1 = 1`,
		"NodeComp is":        `/. is /.`,
		"NodeComp <<":        `/. << /.`,
		"RangeExpr":          `1 to 3`,
		"AdditiveExpr":       `1 + 2 - 3`,
		"MultiplicativeExpr": `2 * 3 div 4 idiv 5 mod 6`,
		"UnionExpr |":        `/a | /b`,
		"UnionExpr word":     `/a union /b`,
		"IntersectExcept":    `/a intersect /b except /c`,
		"InstanceOf":         `1 instance of xs:integer`,
		"TreatAs":            `1 treat as xs:integer`,
		"CastableAs":         `1 castable as xs:string`,
		"CastAs":             `1 cast as xs:string`,
		"UnaryExpr":          `-1`,
		"PathExpr abs":       `/a/b`,
		"PathExpr desc":      `//a`,
		"PathExpr rel":       `a/b`,
		"AxisStep fwd":       `child::a`,
		"AxisStep rev":       `ancestor::a`,
		"AbbrevAttrib":       `@id`,
		"AbbrevParent":       `..`,
		"ContextItem":        `.`,
		"Predicate":          `a[1][@x]`,
		"FilterExpr":         `(1,2)[1]`,
		"FunctionCall":       `count(/a)`,
		"Literal num":        `1.5e3`,
		"Literal str":        `'x'`,
		"VarRef":             `$x`,
		"ParenExpr":          `(1)`,
		"EmptySeq":           `()`,
		"SequenceExpr":       `1,2,3`,
		"KindTest node":      `/node()`,
		"KindTest text":      `/text()`,
		"KindTest comment":   `/comment()`,
		"KindTest pi":        `/processing-instruction()`,
		"KindTest pi-named":  `/processing-instruction('x')`,
		"KindTest element":   `/element()`,
		"KindTest elem-nm":   `/element(a)`,
		"KindTest attr":      `/attribute()`,
		"KindTest docnode":   `/document-node()`,
		"KindTest nsnode":    `/namespace-node()`,
		// schema-element() and schema-attribute() are deliberately absent:
		// both name a global declaration in an imported schema, and this
		// engine imports none, so every such name is a static error
		// (XPST0008) rather than a parse. See TestStaticErrorCodes.
		"Wildcard *":       `/*`,
		"Wildcard ns:*":    `/xs:*`,
		"Wildcard *:local": `/*:a`,
		"SeqType empty":    `() instance of empty-sequence()`,
		"SeqType item":     `1 instance of item()`,
		"SeqType occ ?":    `1 instance of xs:integer?`,
		"SeqType occ *":    `1 instance of xs:integer*`,
		"SeqType occ +":    `1 instance of xs:integer+`,
		"Comment":          `1 (: c :) + 2`,
	}
	var fails []string
	for name, e := range exprs {
		if _, err := Parse(e, testNS{}); err != nil {
			fails = append(fails, name+": "+e+" -> "+err.Error())
		}
	}
	for _, f := range fails {
		t.Errorf("grammar production failed to parse: %s", f)
	}
}
