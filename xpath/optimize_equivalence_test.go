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
	// Focus-dependent calls at zero arity. foldableFunction is keyed on QName
	// alone, with no arity, so "string" and "number" reach foldConstant even
	// when written with no argument -- at which point isClosed's loop over
	// zero arguments trivially succeeds. Whether that actually folds is
	// decided downstream, in evalToLiteral, and this group is what settles it.
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

// TestFocusDependentZeroArityDoesNotFold is the specific claim worth pinning
// down on its own, because the reasoning is subtle and the code does not state
// it anywhere.
//
// foldableFunction is keyed on the QName with no arity parameter, so "string",
// "number" and "string-length" are admitted whether they are written with an
// argument or without. Nothing between foldConstant and evalToLiteral rejects
// the zero-argument spelling: isClosed's loop over an empty argument list
// succeeds vacuously. The reason string() is not folded to a constant is one
// layer further down -- evalToLiteral evaluates against NewContext(nil, ...),
// the call raises XPDY0002 because there is no context item, and the "err !=
// nil" arm returns the unfolded tree.
//
// That makes the safety here *incidental*: it rests on the accident that every
// focus-dependent function in the allowlist happens to raise on an absent
// focus, not on any rule that says a focus-dependent call must not be folded.
// This test records the property so that a future change which makes any of
// these return a value instead of raising -- a default focus, a more forgiving
// fn:string -- fails here rather than silently freezing a constant.
func TestFocusDependentZeroArityDoesNotFold(t *testing.T) {
	// Every one of these is in foldableFunction's allowlist (or reaches it via
	// the fn: namespace) and takes an implicit focus argument.
	for _, src := range []string{
		`string()`, `number()`, `string-length()`,
	} {
		t.Run(src, func(t *testing.T) {
			e, err := Parse(src, equivNS{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// It must reach foldConstant at all -- otherwise this test is
			// asserting nothing and would keep passing if the allowlist gained
			// an arity check for a different reason.
			call, ok := e.(*FuncCall)
			if !ok {
				t.Fatalf("%s parsed as %T, not a FuncCall", src, e)
			}
			if !foldableFunction(call.Name) {
				t.Fatalf("%s: foldableFunction says no, so this test no longer "+
					"exercises the path it was written for", src)
			}
			if _, folded := foldConstant(e); folded {
				t.Fatalf("%s FOLDED to a constant. A focus-dependent call was "+
					"frozen at compile time; every stylesheet using it now sees "+
					"the empty-focus answer.", src)
			}
			// And confirm the reason, so the test says why rather than just that.
			if _, err := Parse(src, equivNS{}); err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if _, err := e.Eval(NewContext(nil, Builtins())); xdm.ErrorCode(err) != "XPDY0002" {
				t.Errorf("%s in an empty focus: got %v, want XPDY0002 -- the "+
					"absence of folding depends on this error", src, err)
			}
		})
	}

	// The functions that are focus-dependent but *not* in the allowlist are
	// safe for the stated reason instead, and that should stay true.
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
			if foldableFunction(call.Name) {
				t.Errorf("%s is focus-dependent but foldableFunction admits it; "+
					"it is then folded only if it happens to raise on an empty focus", src)
			}
			if _, folded := foldConstant(e); folded {
				t.Fatalf("%s FOLDED to a constant", src)
			}
		})
	}
}
