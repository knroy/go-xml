package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// hoistLib is the builtins plus t:id#1, which returns its argument. It
// is not a built-in, so wrapping an operand in it keeps that operand from
// being hoisted: the wrapped form is the per-item reference.
func hoistLib() *Library {
	lib := NewLibrary(Builtins())
	lib.Add(Function{Name: xdm.QName{URI: "urn:t", Local: "id"}, Arity: 1,
		Call: func(_ *Context, a []xdm.Sequence) (xdm.Sequence, error) { return a[0], nil }})
	return lib
}

func hoistCtx(t *testing.T, lib FunctionLibrary) *Context {
	t.Helper()
	ctx := NewContext(mustParse(t, `<r><l>ab</l><l>cd</l><l n="2">ab</l><l>1</l><l>2</l></r>`), lib)
	ctx = ctx.WithVar(xdm.QName{Local: "term"}, xdm.One(xdm.NewString("abc")))
	ctx = ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(xdm.NewInteger(2)))
	return ctx.WithVar(xdm.QName{Local: "ss"}, xdm.Sequence{xdm.NewString("cd"), xdm.NewString("zz")})
}

// hoistNS adds the prefix t for t:id.
type hoistNS struct{ testNS }

func (n hoistNS) ResolvePrefix(p string) (string, bool) {
	if p == "t" {
		return "urn:t", true
	}
	return n.testNS.ResolvePrefix(p)
}

func hoistEval(ctx *Context, expr string) (string, error) {
	seq, err := Eval(expr, ctx, hoistNS{})
	if err != nil {
		return "", err
	}
	return renderSeq(seq), nil
}

// TestPredicateHoistMatchesPerItem compares each predicate with its hoistable
// operand against the same predicate with that operand wrapped in a
// non-built-in, which is evaluated per item.
func TestPredicateHoistMatchesPerItem(t *testing.T) {
	cases := []struct{ pred, operand string }{
		{"[. = %s]", "substring($term, 1, 2)"},
		{"[%s = .]", "substring($term, 1, 2)"},
		{"[. != %s]", "'ab'"},
		{"[. = %s]", "$ss"},
		{"[. eq %s]", "concat('a', 'b')"},
		{"[%s ne .]", "'cd'"},
		{"[. < %s]", "'c'"},
		{"[number(.) >= %s]", "$n"},
		{"[. = %s]", "$n"},
		{"[@n = %s]", "$n"},
		{"[@n eq %s]", "string($n)"},
		{"[. = %s]", "()"},
		{"[. eq %s]", "()"},
		{"[position() = %s]", "$n"},
		{"[position() = %s]", "($n, 4)"},
		{"[. = %s]", "lower-case(upper-case('AB'))"},
		{"[%s]", "$n"}, // numeric: positional, never a comparison
	}
	for _, base := range []string{"/r/l", "(/r/l)", "/r/l/string()"} {
		for _, c := range cases {
			hoisted := base + strings.Replace(c.pred, "%s", c.operand, 1)
			wrapped := base + strings.Replace(c.pred, "%s", "t:id("+c.operand+")", 1)
			got, gerr := hoistEval(hoistCtx(t, hoistLib()), hoisted)
			want, werr := hoistEval(hoistCtx(t, hoistLib()), wrapped)
			if got != want || (gerr == nil) != (werr == nil) ||
				(gerr != nil && xdm.ErrorCode(gerr) != xdm.ErrorCode(werr)) {
				t.Errorf("%s: got %q, %v; per item %q, %v", hoisted, got, gerr, want, werr)
			}
		}
	}
}

// TestPredicateHoistErrorOrder pins the errors a hoisted operand may raise:
// none for an empty sequence, and when both operands fail, the left one's.
func TestPredicateHoistErrorOrder(t *testing.T) {
	cases := []struct{ expr, code string }{
		{"()[. = xs:integer('x')]", ""},
		{"(1, 2)[. = 3][. = xs:integer('x')]", ""},
		{"(1, 2)[. = xs:integer('x')]", "FORG0001"},
		// The right operand is hoistable and fails; the left fails first.
		{"(1, 2)[(. idiv 0) = xs:integer('x')]", "FOAR0001"},
		{"(1, 2)[(. idiv 0) eq xs:integer('x')]", "FOAR0001"},
		// The left operand is hoistable and fails before the right.
		{"(1, 2)[xs:integer('x') = (. idiv 0)]", "FORG0001"},
		// The hoisted operand succeeds; the other side's error stands.
		{"(1, 'a')[. = 1]", "XPTY0004"},
		{"(1, 2)[. eq (1, 2)]", "XPTY0004"},
		{"(1, 2)[. = (1 to 3)]", ""},
	}
	for _, c := range cases {
		_, err := hoistEval(NewContext(nil, Builtins()), c.expr)
		if got := xdm.ErrorCode(err); (err == nil) != (c.code == "") || (err != nil && got != c.code) {
			t.Errorf("%s: got %v, want %q", c.expr, err, c.code)
		}
	}
}

// TestPredicateHoistHonoursRebinding: a host library that binds fn:substring
// to its own function gets a call per item, since its function need not be
// pure. With the stock built-in the call is made once per predicate.
func TestPredicateHoistHonoursRebinding(t *testing.T) {
	calls := 0
	lib := hoistLib()
	sub, _ := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: "substring"}, 3)
	lib.Add(Function{Name: sub.Name, Arity: 3, Call: func(c *Context, a []xdm.Sequence) (xdm.Sequence, error) {
		calls++
		return sub.Call(c, a)
	}})
	got, err := hoistEval(hoistCtx(t, lib), "/r/l[. = substring($term, 1, 2)]")
	if err != nil || got != "ab,ab" || calls != 5 {
		t.Errorf("got %q, %v after %d calls; want ab,ab after 5", got, err, calls)
	}
}

// TestPredicateHoistAllocations is the optimisation itself: the hoisted
// operand is evaluated once, not once per item.
func TestPredicateHoistAllocations(t *testing.T) {
	ctx := NewContext(nil, hoistLib())
	ctx = ctx.WithVar(xdm.QName{Local: "term"}, xdm.One(xdm.NewString("abc")))
	ctx = ctx.WithVar(xdm.QName{Local: "s"}, func() xdm.Sequence {
		s := make(xdm.Sequence, 500)
		for i := range s {
			s[i] = xdm.NewString("zz")
		}
		return s
	}())
	measure := func(src string) float64 {
		c := MustCompile(src, hoistNS{})
		return testing.AllocsPerRun(5, func() {
			if _, err := c.Eval(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
	hoisted := measure("$s[. = upper-case(substring($term, 1, 2))]")
	perItem := measure("$s[. = t:id(upper-case(substring($term, 1, 2)))]")
	if hoisted*2 > perItem {
		t.Errorf("hoisted %v allocations, per item %v: the operand is not hoisted", hoisted, perItem)
	}
}
