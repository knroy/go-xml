package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// parseOptionalType reads an optional "as SequenceType" and leaves its source
// in p.lastType, empty when there was none.
//
// The type is captured as *text* rather than parsed. XQuery's SequenceType is
// XPath's, byte for byte, and this package has no business owning a second
// implementation of a grammar the expression parser already reads at 100% of
// the suite — including the schema types, the union types, the function tests
// and the map and array tests, which between them are far more of that
// grammar than a FLWOR needs to know about. So the text is scanned for its
// extent and handed back to xpath inside a "treat as", which is the one
// expression whose meaning is exactly "this value must match this type".
func (p *parser) parseOptionalType() error {
	p.lastType = ""
	save := p.pos
	if !p.consumeWord("as") {
		return nil
	}
	src, err := p.scanSequenceType()
	if err != nil {
		p.pos = save
		return err
	}
	p.lastType = src
	return nil
}

// scanSequenceType returns the source of the SequenceType at the cursor.
//
// The extent of a sequence type is decidable without parsing it: it is a name
// or a parenthesised test, optionally followed by an occurrence indicator.
// The only nesting is parenthetical — "function(xs:string) as xs:string",
// "map(xs:string, element(a))" — so a bracket count finds the end, and the
// indicator that may follow it is one of three characters.
//
// The one case a bracket count alone gets wrong is "empty-sequence()", whose
// parentheses are empty, and it is not a special case: the count handles it.
func (p *parser) scanSequenceType() (string, error) {
	p.skipSpaceAndComments()
	start := p.pos
	if p.consume("(") {
		// A parenthesised type, which the grammar admits only in 4.0 but
		// which costs nothing to accept the extent of; xpath decides whether
		// it is legal.
		depth := 1
		for !p.eof() && depth > 0 {
			switch p.src[p.pos] {
			case '(':
				depth++
			case ')':
				depth--
			}
			p.pos++
		}
	} else {
		if p.scanNCName() == "" {
			return "", p.errorf("XPST0003: expected a sequence type")
		}
		if p.lookingAt(":") && !p.lookingAt("::") {
			p.pos++
			if p.scanNCName() == "" {
				return "", p.errorf("XPST0003: expected a local name after %q",
					":")
			}
		}
		// A kind test, a function test or a map or array test carries its own
		// parentheses, which may hold a nested type.
		save := p.pos
		p.skipSpaceAndComments()
		if p.consume("(") {
			depth := 1
			for !p.eof() && depth > 0 {
				// A literal inside the test holds no parenthesis this depth
				// count should see, and a comment stands wherever whitespace
				// does -- "element(: c :)(a)" is a kind test with a comment
				// in it, and the "(" inside the comment is not the test's.
				// Going through skipNonSyntax is what gives this scan the
				// comment case it lacked.
				if end, ok, err := skipNonSyntax(p.src, p.pos); ok {
					if err != nil {
						return "", err
					}
					p.pos = end + 1
					continue
				}
				switch p.src[p.pos] {
				case '(':
					depth++
				case ')':
					depth--
				}
				p.pos++
			}
			// "function(...) as T" and "element(a, T)" continue past the
			// parentheses, and only "as" does so with a further type.
			if p.consumeWord("as") {
				if _, err := p.scanSequenceType(); err != nil {
					return "", err
				}
			}
		} else {
			p.pos = save
		}
	}
	// An occurrence indicator binds to the item type, and only one may
	// appear.
	save := p.pos
	p.skipSpaceAndComments()
	if !p.eof() {
		switch p.src[p.pos] {
		case '?', '*', '+':
			p.pos++
		default:
			p.pos = save
		}
	} else {
		p.pos = save
	}
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return "", p.errorf("XPST0003: expected a sequence type")
	}
	return src, nil
}

// compileTyped compiles a clause's binding expression with its declared type
// applied to the whole value, which is what "let" and a grouping variable
// declare.
//
// The check is written as "treat as" and handed to xpath, so the type grammar
// and the matching rules are the conformant ones rather than a second
// implementation. What has to be corrected afterwards is the error code:
// "treat as" raises XPDY0050, and a FLWOR type mismatch is XPTY0004, so the
// code is remapped where it surfaces (see retypeError). The alternative —
// exporting the matcher and calling it here — would put the same rule in two
// places for the sake of one error code.
func (p *parser) compileTyped(src, typ string) (*compiledExpr, error) {
	if typ == "" {
		return p.compileExpr(src)
	}
	c, err := p.compileExpr("(" + src + ") treat as " + typ)
	if err != nil {
		return nil, err
	}
	c.src = src
	c.typed = true
	return c, nil
}

// compileTypedFor is compileTyped for a "for" clause, whose declared type
// constrains each item bound rather than the sequence iterated.
//
// §3.10.2 is explicit: "for $x as xs:integer in (1, 2)" declares that each
// $x is an integer, not that the sequence is one. So the check cannot wrap
// the binding expression, and it is instead expressed as a per-item
// "for ... return ... treat as", which leaves the sequence the same length
// and raises on the first item that does not match. The rewrite is legal
// because "treat as" is the identity on a value that matches.
func (p *parser) compileTypedFor(src, typ string) (*compiledExpr, error) {
	if typ == "" {
		return p.compileExpr(src)
	}
	// The bound variable is in the reserved local-function namespace, which
	// no query may declare a variable in, so it cannot capture a name the
	// binding expression uses.
	c, err := p.compileExpr(
		"for $" + typeCheckVar + " in (" + src + ") return " +
			"($" + typeCheckVar + " treat as " + typ + ")")
	if err != nil {
		return nil, err
	}
	c.src = src
	c.typed = true
	return c, nil
}

// compileEmptyCheck compiles the test that the empty sequence matches the
// declared type typ, for a "for" clause that says "allowing empty".
//
// It covers the one binding a "for" clause's per-item check cannot reach.
// That check is a loop over the bound items, so a binding of no items runs it
// zero times — correct for an ordinary "for", which then produces no tuple at
// all, and wrong for "allowing empty", which produces a tuple whose variable
// is bound to the empty sequence. That binding is subject to the declaration
// like any other, and §3.10.2 makes a value that does not match err:XPTY0004.
//
// The test is compiled here and run in forClause.apply, on the branch that
// actually makes the empty binding, because whether it applies is a property
// of the *value* and not of the text. outer-012 and outer-013 differ by one
// character and settle it: both write "for $x as xs:integer allowing empty at
// $p in 1 to $n", and that clause is legal in both, because "1 to 5" is never
// empty and so the empty binding is never made. Refusing it at parse time on
// the ground that xs:integer excludes () would reject a conformant query. It
// is only the inner clause, whose "($x+1) to $n" *is* empty on the last
// iteration, that binds () — and there outer-013's "xs:integer" must raise
// where outer-012's "xs:integer?" must not.
//
// The test is written as "() treat as T" so that xpath's matcher decides,
// rather than a second reading of the occurrence indicators living here.
func (p *parser) compileEmptyCheck(typ string) (*compiledExpr, error) {
	if typ == "" {
		return nil, nil
	}
	c, err := p.compileExpr("() treat as " + typ)
	if err != nil {
		return nil, err
	}
	c.typed = true
	return c, nil
}

// runEmptyCheck applies the compiled empty-binding check, mapping treat's
// XPDY0050 to the XPTY0004 a declared-type mismatch carries.
//
// "() treat as T" reads neither the context item nor any function, so the
// tuple's context is not needed and the answer cannot vary with it. A fresh
// one is still routed through eval rather than handed to the compiled form
// directly, so that the typed rewrite the check depends on is applied by the
// same code path everything else uses; compileEmptyCheck sets typed, and a
// direct evaluation here would have to remember to call retypeError itself.
func runEmptyCheck(check *compiledExpr) error {
	if check == nil {
		return nil
	}
	ctx := &evalContext{xp: xpath.NewContext(nil, xpath.Builtins())}
	_, err := check.eval(ctx)
	return err
}

// typeCheckVar names the variable the per-item type check binds. It is
// prefixed with the reserved "local" prefix so that it cannot collide with a
// variable the query itself binds.
const typeCheckVar = "local:xq-typed-item"

// retypeError rewrites the error code a declared type produces.
//
// A value that does not match a FLWOR variable's declared type is XPTY0004
// (section 3.10.2), and the "treat as" the check is written with raises
// XPDY0050. Nothing else about the error changes, so only the code is
// replaced and the message it carries — which names the type that failed —
// is kept.
func retypeError(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if !strings.HasPrefix(s, "XPDY0050:") {
		return err
	}
	return fmt.Errorf("XPTY0004:%s", strings.TrimPrefix(s, "XPDY0050:"))
}

// compileTypeCheck compiles a declared type into a standalone check, for a
// value whose expression has no source text to wrap.
//
// A constructor and a nested FLWOR are both parsed rather than compiled, so
// there is nothing to put inside "(...) treat as T"; the check is instead
// compiled over a variable and applied to the value the expression produced.
// perItem selects the "for" reading of the declaration — each item must match
// — over the "let" reading, where the whole sequence must.
func (p *parser) compileTypeCheck(typ string, perItem bool) (*compiledExpr, error) {
	src := "$" + typeCheckVar + " treat as " + typ
	if perItem {
		src = "for $" + typeCheckVar + "-i in $" + typeCheckVar +
			" return ($" + typeCheckVar + "-i treat as " + typ + ")"
	}
	c, err := p.compileExpr(src)
	if err != nil {
		return nil, err
	}
	c.typed = true
	return c, nil
}

// applyCheck runs a standalone type check over a value.
func applyCheck(check *compiledExpr, v xdm.Sequence, ctx *evalContext) (xdm.Sequence, error) {
	if check == nil {
		return v, nil
	}
	// evalIn rather than a context built here and evaluated directly: the
	// value has to be bound on top of whatever bind produced, and the typed
	// rewrite compileTypeCheck asked for has to be applied to the error. Both
	// are evalIn's job, so neither can be forgotten by a later edit.
	return check.evalIn(ctx, func(xp *xpath.Context) *xpath.Context {
		return xp.WithVar(xdm.QName{URI: nsLocal, Local: "xq-typed-item"}, v)
	})
}
