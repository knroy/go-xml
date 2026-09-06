package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An optimiser bug is worse than an ordinary bug: it does not make a wrong
// program fail, it makes a right program quietly mean something else. Asserting
// "optimised output == the answer we wrote down" cannot catch that, because the
// answer we wrote down was itself read off the optimiser. The property that
// actually holds is a differential one:
//
//	optimize(parse(e))  ==  parse(e)
//
// observably, for every e and every focus. This file evaluates both trees
// against the same context and compares the whole observable surface: the error
// (by spec code, not prose), the sequence length, the item order, each item's
// value, and each atomic item's *type* -- xs:double 1 and xs:integer 1 are
// different values, which is the invariant evalToLiteral's comment calls out.
//
// The corpus is not a list of expressions that are known to fold. It is a list
// of expressions that must mean the same thing whether or not they do, which is
// the point: a rule that starts folding something it should not is caught by
// the entry it was never meant to touch.

// equivDoc is the focus document. It is deliberately shallow: the harness is
// testing the optimiser, and a path expression only needs to have a focus at
// all for the focus-dependent corpus entries to differ from the empty-focus run.
const equivDoc = `<catalog count="3" xml:base="urn:base/">` +
	`<book id="b1"><title>Go</title></book>` +
	`<book id="b2"><title>XML</title></book>` +
	`<book id="b3"><title>XSLT</title></book>` +
	`</catalog>`

// equivFocus names a dynamic context the corpus is evaluated against. Both
// trees see the same *Context value, so any difference is the optimiser's.
type equivFocus struct {
	name string
	make func(t *testing.T) *Context
}

func equivFocuses() []equivFocus {
	return []equivFocus{
		{
			// The absent focus. This is the context evalToLiteral itself folds
			// against, so it is the one where a wrongly-folded focus-dependent
			// call would be *hidden*: both sides raise XPDY0002 and agree. It is
			// here so the other focuses have something to be compared against.
			name: "empty-focus",
			make: func(t *testing.T) *Context { return NewContext(nil, Builtins()) },
		},
		{
			// A document node focus.
			name: "document",
			make: func(t *testing.T) *Context {
				return NewContext(equivRoot(t), Builtins())
			},
		},
		{
			// An element focus. This is where a wrongly folded string() or
			// name() shows up: the unoptimised tree reads the element, the
			// folded literal carries whatever the empty focus produced.
			name: "element",
			make: func(t *testing.T) *Context {
				return NewContext(equivElement(t, "catalog"), Builtins())
			},
		},
		{
			// A focus with position and size set away from their defaults, so
			// position() and last() have distinguishable answers. NewContext
			// gives 1/1; a folded position() would freeze one of those.
			name: "element-pos2of3",
			make: func(t *testing.T) *Context {
				el := equivElement(t, "catalog")
				return NewContext(el, Builtins()).WithFocus(el, 2, 3)
			},
		},
		{
			// An atomic focus. "." is not a node here, so string(.) and the
			// path-shaped entries take a different route through the evaluator.
			name: "atomic-item",
			make: func(t *testing.T) *Context {
				return NewContext(xdm.NewString("focus"), Builtins())
			},
		},
	}
}

func equivRoot(t *testing.T) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(equivDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse %s: %v", equivDoc, err)
	}
	return tree.Root
}

// equivElement returns the first element named local in the focus document.
func equivElement(t *testing.T, local string) *xdm.Node {
	t.Helper()
	var walk func(n *xdm.Node) *xdm.Node
	walk = func(n *xdm.Node) *xdm.Node {
		if n.Kind == xdm.KindElement && n.Name.Local == local {
			return n
		}
		for _, c := range n.Children {
			if got := walk(c); got != nil {
				return got
			}
		}
		return nil
	}
	got := walk(equivRoot(t))
	if got == nil {
		t.Fatalf("no <%s> in the focus document", local)
	}
	return got
}

// equivNS resolves the prefixes the corpus uses.
type equivNS struct{}

func (equivNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	case "xml":
		return xdm.NSXML, true
	}
	return "", false
}
func (equivNS) DefaultElementNamespace() string  { return "" }
func (equivNS) DefaultFunctionNamespace() string { return xdm.NSFN }

// --- the observable surface -------------------------------------------------

// equivResult is everything an evaluation can be observed to produce. Two
// evaluations are equivalent exactly when their equivResults render identically.
type equivResult struct {
	errCode string // spec code, or "" when there was no error
	errored bool
	items   []string
}

func (r equivResult) String() string {
	if r.errored {
		if r.errCode == "" {
			return "error(uncoded)"
		}
		return "error(" + r.errCode + ")"
	}
	return "(" + strings.Join(r.items, ", ") + ")"
}

func (r equivResult) equal(o equivResult) bool { return r.String() == o.String() }

// observe evaluates e in ctx and records the whole observable surface.
//
// An error is reduced to its spec code rather than its message: the prose is
// allowed to differ (a folded tree legitimately reports a different position in
// the source), the code is not. Every item is rendered with its dynamic type
// attached, because a fold that preserves the lexical value but changes
// xs:integer to xs:double has still changed the program.
func observe(e Expr, ctx *Context) equivResult {
	seq, err := e.Eval(ctx)
	if err != nil {
		return equivResult{errored: true, errCode: xdm.ErrorCode(err)}
	}
	items := make([]string, 0, len(seq))
	for _, it := range seq {
		items = append(items, equivRender(it))
	}
	return equivResult{items: items}
}

// equivRender renders one item as type-and-value. Order within the slice is
// preserved by the caller, so comparing the rendered slices compares ordering
// too.
func equivRender(it xdm.Item) string {
	switch v := it.(type) {
	case *xdm.Atomic:
		// TypeName is the point of this branch: two atomics that print the same
		// but carry different types must not compare equal.
		return fmt.Sprintf("%s(%q)", v.TypeName(), v.String())
	case *xdm.Node:
		return fmt.Sprintf("node:%v:%s(%q)", v.Kind, v.Name.Local, v.StringValue())
	default:
		return fmt.Sprintf("%T", it)
	}
}

// --- the harness ------------------------------------------------------------

// checkEquivalent parses src twice, optimises one copy with opt, evaluates both
// against ctx, and reports any observable difference.
//
// Parsing twice rather than deep-copying is deliberate: optimizeWith rewrites
// children in place, so an optimised tree and its "original" would be the same
// tree if only one parse happened, and the comparison would be vacuous.
func checkEquivalent(t *testing.T, src string, ctx *Context, focusName string, opt func(Expr) Expr) {
	t.Helper()

	plain, err := Parse(src, equivNS{})
	if err != nil {
		// A parse error is not the optimiser's business; the corpus should not
		// contain one, so this is a corpus bug rather than a finding.
		t.Fatalf("%s: parse: %v", src, err)
	}
	again, err := Parse(src, equivNS{})
	if err != nil {
		t.Fatalf("%s: reparse: %v", src, err)
	}
	optimized := opt(again)

	before := observe(plain, ctx)
	after := observe(optimized, ctx)

	if !before.equal(after) {
		t.Errorf("DIVERGENCE %q under %s:\n  unoptimised: %s\n  optimised:   %s\n  optimised tree: %s",
			src, focusName, before, after, optimized)
	}
}

// runEquivCorpus checks every expression against every focus.
func runEquivCorpus(t *testing.T, corpus []string, opt func(Expr) Expr) {
	t.Helper()
	for _, src := range corpus {
		for _, f := range equivFocuses() {
			t.Run(src+"/"+f.name, func(t *testing.T) {
				checkEquivalent(t, src, f.make(t), f.name, opt)
			})
		}
	}
}

// --- the corpus -------------------------------------------------------------

// equivCorpus is grouped by the property each group is probing. Every entry has
// to hold under every focus, so an entry that only makes sense with a node
// focus is still run against the empty one, where both sides must agree on
// raising XPDY0002.
var equivCorpus = []string{
	// Focus-dependent calls at zero arity. foldableFunction is keyed on the
	// QName *and the arity*, and refuses "string", "number" and
	// "string-length" at arity 0 because F&O declares those three forms
	// focus-dependent: they read the context item. Before the arity was part
	// of the key they reached foldConstant -- isClosed's loop over an empty
	// argument list succeeds vacuously -- and were saved only by evalToLiteral
	// raising XPDY0002 on its empty focus. This group is what settles that the
	// answer is the same either way, under every focus.
	`string()`,
	`number()`,
	`string-length()`,
	`normalize-space()`,
	`name()`,
	`local-name()`,
	`namespace-uri()`,
	`base-uri()`,
	`root()`,
	`position()`,
	`last()`,
	`.`,
	`boolean(.)`,
	`data(.)`,

	// The same functions with an explicit argument. These are genuinely closed
	// when the argument is a literal and genuinely open when it is ".".
	`string(.)`,
	`string('x')`,
	`string(1)`,
	`number(1)`,
	`number('1.5')`,
	`number('nonsense')`,
	`string-length(.)`,
	`string-length('hello')`,
	`normalize-space('  a  b  ')`,
	`name(.)`,
	`local-name(.)`,
	`root(.)`,
	`upper-case(string())`,
	`concat(string(), 'x')`,

	// The three names whose zero-arity form the arity check now refuses, in
	// both arities and wrapped so the refusal has to survive being an operand.
	// The one-arity forms must still fold; the zero-arity ones must not, and
	// both trees must agree under every focus either way.
	`string-length(string())`,
	`string-length(string(.))`,
	`number(string())`,
	`string(number())`,
	`string-length() + 1`,
	`number() + 1`,
	`concat(string-length(), 'x')`,
	`not(string() = '')`,
	`count((string(), number()))`,
	`string-length('hello') + string-length()`,
	`if (string() = '') then 1 else 2`,
	`upper-case(string(.))`,
	`lower-case(string())`,
	`substring(string(), 1, 2)`,
	`translate(string(), 'a', 'b')`,
	`boolean(string())`,
	`exists(string())`,
	`empty(string())`,
	`reverse((string(), 'x'))`,
	`avg((string-length(), 2))`,
	`sum((string-length(), 2))`,
	`abs(number())`,
	`ceiling(number())`,
	`floor(number())`,
	`round(number())`,
	`round-half-to-even(number())`,
	`xs:string(string())`,
	`xs:integer(string-length())`,
	`xs:double(number())`,

	// Collation-sensitive functions. optimize.go withholds these because the
	// default collation is set after compilation; collations-1006 makes
	// starts-with('abc','AB') true under a secondary-strength UCA collation and
	// false under codepoint. Under the default codepoint collation both trees
	// agree whatever happens, so what this group actually guards is that the
	// functions still evaluate identically -- the collation argument is the
	// part that cannot be tested from here.
	`starts-with('abc', 'AB')`,
	`starts-with('abc', 'ab')`,
	`ends-with('abc', 'BC')`,
	`contains('abc', 'B')`,
	`substring-before('a-b', '-')`,
	`substring-after('a-b', '-')`,
	`compare('a', 'A')`,
	`compare('a', 'a')`,
	`min(('a', 'B', 'c'))`,
	`max(('a', 'B', 'c'))`,
	`distinct-values(('a', 'A', 'a'))`,
	`distinct-values((1, 1.0, xs:double(1)))`,

	// Arithmetic. The typed results matter as much as the numeric ones:
	// 1 + 1 is xs:integer, 1.0 + 1 is xs:decimal, xs:double(1) + 1 is xs:double.
	`1 + 2`,
	`(1 + 2) * 3`,
	`7 idiv 2`,
	`7 mod 2`,
	`1.5 + 1.5`,
	`0.1 + 0.2`,
	`xs:double(1) + 1`,
	`xs:double(1) div 3`,
	`-5`,
	`- -5`,
	`+5`,
	`1 - 0.5`,
	`2 * 3.5`,

	// Errors that folding must not promote to compile time. If foldConstant
	// ever started folding these into an error, the two trees would still both
	// error here -- but a *compile* error would show up as a Parse failure,
	// which checkEquivalent reports as a fatal corpus bug. That is the intended
	// alarm.
	`1 div 0`,
	`1 idiv 0`,
	`1 mod 0`,
	`xs:double(1) div 0`,
	`xs:integer('nonsense')`,
	`xs:date('nonsense')`,

	// Comparisons. isComparisonOp withholds all of these from folding.
	`1 = 1`,
	`1 eq 1`,
	`'a' = 'a'`,
	`'Adele' eq 'ADELE'`,
	`1 < 2`,
	`1 lt 2`,
	`2 != 3`,
	`(1, 2, 3) = 2`,
	`xs:double(1) eq 1`,

	// The xs: constructors, which foldableFunction admits wholesale.
	`xs:integer(1)`,
	`xs:double(1)`,
	`xs:decimal(1)`,
	`xs:float(1)`,
	`xs:string(1)`,
	`xs:boolean('true')`,
	`xs:date('2020-01-01')`,
	`xs:dayTimeDuration('PT1H')`,
	`xs:QName('xs:string')`,
	`xs:anyURI('urn:x')`,
	`xs:untypedAtomic('1')`,
	`1 cast as xs:double`,
	`'1' cast as xs:integer`,
	`1 castable as xs:double`,
	`1 instance of xs:integer`,
	`1 treat as xs:integer`,

	// Ranges and aggregates over them.
	`1 to 3`,
	`count(1 to 3)`,
	`count(1 to 100)`,
	`sum(1 to 100)`,
	`avg(1 to 4)`,
	`reverse(1 to 3)`,
	`(3 to 1)`,
	`count(())`,
	`empty(())`,
	`exists((1, 2))`,

	// Sequences: length and ordering are part of the observable surface, and a
	// fold that collapsed a two-item sequence would be caught here.
	`(1, 2, 3)`,
	`(3, 2, 1)`,
	`(1, (2, 3), 4)`,
	`((), 1, ())`,
	`(1, 'a', xs:double(2))`,
	`reverse((1, 'a', true()))`,

	// Nested composition: a folded child feeding an unfolded parent, and the
	// reverse. optimizeChildren runs bottom-up, so these are where a rule that
	// looks correct in isolation goes wrong.
	`count(1 to (1 + 2))`,
	`concat('a', string(1 + 2))`,
	`string-length(concat('ab', 'cd'))`,
	`upper-case(substring('hello', 1 + 1, 3))`,
	`(1 + 2) = 3`,
	`starts-with(concat('ab', 'c'), 'ab')`,
	`abs(-(1 + 2))`,
	`concat(string(1 + 2), string())`,
	`count((1 to 3)[. > 1])`,
	`if (1 + 1 = 2) then 'y' else 'z'`,
	`if (position() = 1) then 'first' else 'other'`,
	`for $i in 1 to 3 return $i + 1`,
	`some $i in 1 to 3 satisfies $i = 2`,
	`every $i in 1 to 3 satisfies $i > 0`,
	`sum(for $i in 1 to 3 return $i * 2)`,

	// Paths and predicates. optimizeWith deliberately does not descend into a
	// path's steps; these entries are what would notice if it started to.
	`/catalog`,
	`/catalog/book`,
	`/catalog/book[2]`,
	`/catalog/book[position() = 2]`,
	`/catalog/book[@id = 'b2']`,
	`count(/catalog/book)`,
	`/catalog/@count`,
	`string(/catalog/@count)`,
	`//title`,
	`ancestor-or-self::*`,
	`self::*`,
	`(/catalog/book)[last()]`,
	`/catalog/book/string(@id)`,
	`count(//book) + 1`,

	// Booleans and the functions foldableFunction admits by name.
	`true()`,
	`false()`,
	`not(true())`,
	`not(())`,
	`boolean(0)`,
	`boolean('')`,
	`abs(-5)`,
	`ceiling(1.5)`,
	`floor(1.5)`,
	`round(1.5)`,
	`round-half-to-even(2.5)`,
	`round-half-to-even(1.2345, 2)`,
	`translate('abc', 'ab', 'xy')`,
	`substring('hello', 2)`,
	`substring('hello', 2, 2)`,
	`concat('a', 'b', 'c')`,
	`lower-case('ABC')`,
	`upper-case('abc')`,
	`string-join(('a', 'b'), '-')`,
	`tokenize('a b', ' ')`,

	// Values whose identity survives folding only if the type does.
	`xs:double('NaN')`,
	`xs:double('INF')`,
	`xs:double('-0')`,
	`-0`,
	`-0.0`,
	`xs:double(0) div -1`,
	`string(xs:double(1))`,
	`string(1.0)`,
	`string(xs:float(-0))`,
}

// TestOptimizeEquivalence is the whole point: for every expression and every
// focus, the optimised tree and the unoptimised tree must be observationally
// identical.
func TestOptimizeEquivalence(t *testing.T) {
	runEquivCorpus(t, equivCorpus, optimize)
}

// TestOptimizeCompatEquivalence runs the same corpus through the XPath 1.0
// compatibility pass. optimizeCompat withholds strictly more than optimize
// does, so if optimize is equivalence-preserving on an expression, compat must
// be too; a failure here means containsCompatSensitive let something through
// that foldConstant then folded wrongly.
//
// Note what this does *not* claim: it evaluates under a 2.0 context, because
// the pass is chosen at compile time by the host and the equivalence being
// tested is "same tree in, same answers out". The 1.0-vs-2.0 semantic
// difference is the host's business, not the optimiser's.
func TestOptimizeCompatEquivalence(t *testing.T) {
	runEquivCorpus(t, equivCorpus, optimizeCompat)
}

// TestOptimizeCompatWithholdsMore checks the documented relationship between
// the two passes rather than their answers: compat folds a subset of what
// optimize folds, and the subset it withholds is exactly the arithmetic and
// comparison subtrees containsCompatSensitive identifies.
func TestOptimizeCompatWithholdsMore(t *testing.T) {
	cases := []struct {
		src string
		// foldsPlain and foldsCompat say whether the whole expression collapses
		// to a single Literal under each pass.
		foldsPlain, foldsCompat bool
	}{
		// Arithmetic: folded by optimize, withheld by compat.
		{`1 + 2`, true, false},
		{`7 idiv 2`, true, false},
		{`-5`, true, false},
		{`abs(-5)`, true, false},         // the argument is a UnaryOp
		{`string(-0)`, true, false},      // the case the compat comment names
		{`count(1 to 3)`, true, false},   // "to" is compat-sensitive
		{`concat('a', 'b')`, true, true}, // no operator at all: both fold
		{`upper-case('abc')`, true, true},
		{`string-length('hello')`, true, true},
		{`xs:double(1)`, true, true},
		{`not(true())`, true, true},
		// Comparisons: withheld by both, for different reasons.
		{`1 = 1`, false, false},
		{`'a' eq 'a'`, false, false},
		// Collation-sensitive: withheld by both.
		{`starts-with('abc', 'ab')`, false, false},
		{`compare('a', 'b')`, false, false},
		{`min((1, 2))`, false, false},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			gotPlain := foldsToLiteral(t, c.src, optimize)
			gotCompat := foldsToLiteral(t, c.src, optimizeCompat)
			if gotPlain != c.foldsPlain {
				t.Errorf("optimize(%q) folds = %v, want %v", c.src, gotPlain, c.foldsPlain)
			}
			if gotCompat != c.foldsCompat {
				t.Errorf("optimizeCompat(%q) folds = %v, want %v", c.src, gotCompat, c.foldsCompat)
			}
			if gotCompat && !gotPlain {
				t.Errorf("%q: compat folded what optimize withheld, which inverts the documented relationship", c.src)
			}
		})
	}
}

func foldsToLiteral(t *testing.T, src string, opt func(Expr) Expr) bool {
	t.Helper()
	e, err := Parse(src, equivNS{})
	if err != nil {
		t.Fatalf("%s: parse: %v", src, err)
	}
	_, ok := opt(e).(*Literal)
	return ok
}

// TestFocusDependentZeroArityDoesNotFold pins the invariant the arity check
// exists to state: a focus-dependent call is never a candidate for folding.
//
// It is asserted at two levels, and the distinction is the whole point of the
// change. The *structural* assertion is that foldableFunction itself says no
// for fn:string#0, fn:number#0 and fn:string-length#0 -- all three are
// declared ·focus-dependent· by F&O 3.0, because they read the context item,
// while their one-argument forms are focus-independent. The *behavioural*
// assertion is that foldConstant does not fold them.
//
// Before the arity was part of foldableFunction's key, only the behavioural
// half held, and it held incidentally: the zero-arity spellings reached
// foldConstant (isClosed's loop over an empty argument list succeeds
// vacuously) and were rejected one layer further down, where evalToLiteral
// evaluates against NewContext(nil, ...) and the call raises XPDY0002. That
// made the optimiser's correctness rest on the accident that every
// focus-dependent function in the allowlist happens to raise on an absent
// focus, rather than on any rule forbidding the fold. Giving evalToLiteral a
// focus for some unrelated reason would have turned it into a miscompilation
// silently. Nothing was ever mis-folded; the safety was just in the wrong
// place.
//
// The behavioural assertion alone cannot tell those two worlds apart, which is
// why the structural one is here.
func TestFocusDependentZeroArityDoesNotFold(t *testing.T) {
	// The zero-arity forms of allowlisted names: refused by foldableFunction
	// itself, which is the new invariant.
	for _, src := range []string{
		`string()`, `number()`, `string-length()`,
	} {
		t.Run(src, func(t *testing.T) {
			e, err := Parse(src, equivNS{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			call, ok := e.(*FuncCall)
			if !ok {
				t.Fatalf("%s parsed as %T, not a FuncCall", src, e)
			}
			if len(call.Args) != 0 {
				t.Fatalf("%s parsed with %d args, want 0", src, len(call.Args))
			}
			if foldableFunction(call.Name, len(call.Args)) {
				t.Errorf("foldableFunction(%s, 0) = true; the zero-arity form "+
					"is focus-dependent and must never be a fold candidate. "+
					"It is currently unfolded only because evalToLiteral has "+
					"an empty focus, which is not a guarantee.", call.Name.Local)
			}
			// isClosed must agree: it is the other caller, and a focus-dependent
			// call appearing as a *sub*-expression must keep its parent unfoldable.
			if isClosed(e) {
				t.Errorf("isClosed(%s) = true", src)
			}
			if _, folded := foldConstant(e); folded {
				t.Fatalf("%s FOLDED to a constant. A focus-dependent call was "+
					"frozen at compile time; every stylesheet using it now sees "+
					"the empty-focus answer.", src)
			}
			// The empty-focus error is the *old* mechanism. It is still true,
			// and recording it keeps the two layers distinguishable: if this
			// stops holding, the structural check above is what carries the
			// invariant, and that is the intended state.
			if _, err := e.Eval(NewContext(nil, Builtins())); xdm.ErrorCode(err) != "XPDY0002" {
				t.Logf("%s in an empty focus: got %v, not XPDY0002 -- the "+
					"incidental protection is gone and only the arity check remains",
					src, err)
			}
		})
	}

	// The one-argument forms are focus-independent and must stay foldable:
	// the arity check has to be a refusal of arity 0, not of the name.
	for _, src := range []string{
		`string('x')`, `number('1.5')`, `string-length('hello')`,
	} {
		t.Run(src+"/still-folds", func(t *testing.T) {
			e, err := Parse(src, equivNS{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			call := e.(*FuncCall)
			if !foldableFunction(call.Name, len(call.Args)) {
				t.Fatalf("foldableFunction(%s, %d) = false; the arity check "+
					"has disabled the one-argument form too", call.Name.Local, len(call.Args))
			}
			if _, folded := foldConstant(e); !folded {
				t.Errorf("%s no longer folds", src)
			}
		})
	}

	// The functions that are focus-dependent but *not* in the allowlist are
	// refused by the name, and that should stay true.
	for _, src := range []string{
		`normalize-space()`, `name()`, `local-name()`, `position()`, `last()`,
		`base-uri()`, `root()`,
	} {
		t.Run(src+"/not-allowlisted", func(t *testing.T) {
			e, err := Parse(src, equivNS{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			call, ok := e.(*FuncCall)
			if !ok {
				t.Fatalf("%s parsed as %T", src, e)
			}
			if foldableFunction(call.Name, len(call.Args)) {
				t.Errorf("%s is focus-dependent but foldableFunction admits it; "+
					"it is then folded only if it happens to raise on an empty focus", src)
			}
			if _, folded := foldConstant(e); folded {
				t.Fatalf("%s FOLDED to a constant", src)
			}
		})
	}
}

// TestFoldableFunctionArity is the direct table for foldableFunction, covering
// every name in the allowlist at every arity the builtin library registers it
// at, plus the arities it does not. A name is admitted only at an arity whose
// F&O signature is ·focus-independent·.
func TestFoldableFunctionArity(t *testing.T) {
	for _, tc := range []struct {
		local string
		arity int
		want  bool
	}{
		// The three overloaded on focus-dependence. F&O 3.0 declares the
		// zero-argument form of each ·context-dependent· and ·focus-dependent·
		// and the one-argument form ·context-independent· and
		// ·focus-independent·.
		{"string", 0, false}, {"string", 1, true},
		{"number", 0, false}, {"number", 1, true},
		{"string-length", 0, false}, {"string-length", 1, true},

		// fn:true#0 and fn:false#0 are the only other zero-arity entries in the
		// allowlist, and both are focus-independent constants.
		{"true", 0, true},
		{"false", 0, true},

		// Everything else in the allowlist has no zero-arity form at all, so
		// arity is not load-bearing for it -- but the check must not have
		// broken the arities that do exist.
		{"abs", 1, true},
		{"ceiling", 1, true},
		{"floor", 1, true},
		{"round", 1, true}, {"round", 2, true},
		{"round-half-to-even", 1, true}, {"round-half-to-even", 2, true},
		{"count", 1, true},
		{"sum", 1, true}, {"sum", 2, true},
		{"avg", 1, true},
		{"concat", 2, true}, {"concat", 3, true},
		{"upper-case", 1, true},
		{"lower-case", 1, true},
		{"substring", 2, true}, {"substring", 3, true},
		{"translate", 3, true},
		{"not", 1, true},
		{"boolean", 1, true},
		{"empty", 1, true},
		{"exists", 1, true},
		{"reverse", 1, true},

		// Not in the allowlist at any arity: focus-dependent, collation-
		// dependent, or otherwise reading the dynamic context.
		{"normalize-space", 0, false}, {"normalize-space", 1, false},
		{"position", 0, false},
		{"last", 0, false},
		{"name", 0, false}, {"name", 1, false},
		{"current-dateTime", 0, false},
		{"starts-with", 2, false},
		{"compare", 2, false},
		{"doc", 1, false},
	} {
		name := xdm.QName{URI: xdm.NSFN, Local: tc.local}
		if got := foldableFunction(name, tc.arity); got != tc.want {
			t.Errorf("foldableFunction(fn:%s, %d) = %v, want %v",
				tc.local, tc.arity, got, tc.want)
		}
	}

	// The xs: constructor branch returns true for the whole namespace without
	// looking at arity. That is only sound while no constructor has a
	// zero-arity form -- a zero-arity constructor would be the same latent
	// problem in a place the arity check does not reach. Assert the premise
	// against the builtin library rather than trusting it.
	b := Builtins()
	for _, local := range []string{
		"string", "boolean", "decimal", "float", "double", "integer", "long",
		"int", "short", "byte", "anyURI", "QName", "date", "time", "dateTime",
		"duration", "dayTimeDuration", "yearMonthDuration", "untypedAtomic",
		"NCName", "Name", "token", "normalizedString", "hexBinary",
		"base64Binary", "gYear", "gMonth", "gDay",
	} {
		if _, ok := b.Lookup(xdm.QName{URI: xdm.NSXS, Local: local}, 0); ok {
			t.Errorf("xs:%s has a zero-arity form; the xs: branch of "+
				"foldableFunction admits the whole namespace without checking "+
				"arity, and now needs the same treatment fn:string got", local)
		}
	}
}
