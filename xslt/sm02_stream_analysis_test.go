package xslt

// Round SM lane 02: the streamability analysis costs the size of the
// stylesheet, not its square (or worse), however its constructs repeat.

import (
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// sm02Growth times run(n) and run(4n), each the best of three, after
// setup, and fails when the larger took more than ratio times as long: a
// cost linear in n grows about fourfold, a quadratic one sixteenfold.
// Each timing repeats run until 2ms have passed and takes the mean, so a
// scheduler or timer tick (about 1ms on Windows) cannot decide the ratio.
func sm02Growth(t *testing.T, n int, ratio float64, setup func(n int) func()) {
	t.Helper()
	best := func(n int) time.Duration {
		run := setup(n)
		d := time.Duration(1 << 62)
		for range 3 {
			goruntime.GC()
			t0 := time.Now()
			for k := 1; ; k++ {
				run()
				if el := time.Since(t0); el >= 2*time.Millisecond {
					d = min(d, el/time.Duration(k))
					break
				}
			}
		}
		return d
	}
	small, large := best(n), best(4*n)
	if r := float64(large) / float64(small); r > ratio {
		t.Fatalf("n=%d took %v, 4n took %v: %.1fx, want at most %.0fx", n, small, large, r, ratio)
	}
}

func sm02Doc(t *testing.T, src string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return tree.Root
}

const sm02Head = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f">`

// sm02Find is the first element below root named local in the XSLT namespace.
func sm02Find(root *xdm.Node, local string) *xdm.Node {
	var out *xdm.Node
	walkElements(root, func(el *xdm.Node) bool {
		if out == nil && isXSL(el, local) {
			out = el
		}
		return out == nil
	})
	return out
}

// streamexprs.go inlineFunction: k streamed bindings in scope and an inline
// function body of size s cost one walk of the body, not k.
func TestSM02InlineFunctionOneBodyWalk(t *testing.T) {
	src := func(k int) string {
		var b strings.Builder
		b.WriteString("some ")
		for i := range k {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$t%d in a", i)
		}
		b.WriteString(" satisfies (function(){(" + strings.Repeat("1,", 10*k) + "1)}, true())[2]")
		return b.String()
	}
	analyze := func(s string) *analyzer {
		e, err := xpath.ParseVersion(s, nil, xpath.XPath31)
		if err != nil {
			t.Fatal(err)
		}
		a := &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: true, known: true}
		a.expr(e)
		return a
	}
	sm02Growth(t, 400, 8, func(k int) func() {
		s := src(k)
		return func() { analyze(s) }
	})
	// The answer is the per-binding walk's: a reference to a streamed
	// binding withholds the verdict, by expanded name; none grounds it.
	if a := analyze("some $t in a satisfies (function(){ $t })() = 1"); a.known {
		t.Error("an inline function closing over a streamed binding: known, want unknown")
	}
	if a := analyze("some $t in a satisfies (function(){ 1 })() = 1"); !a.known {
		t.Error("an inline function closing over nothing streamed: unknown, want known")
	}
}

// streaminstructions.go calledTemplate: N xsl:call-template instructions
// resolve against one index of the module, not a walk of it each.
func TestSM02CalledTemplateIndexed(t *testing.T) {
	sheet := func(n int) *xdm.Node {
		var b strings.Builder
		b.WriteString(sm02Head + `<xsl:template name="main"><xsl:source-document streamable="yes" href="in.xml">`)
		for range n {
			b.WriteString(`<xsl:call-template name="t"/>`)
		}
		b.WriteString(`</xsl:source-document></xsl:template>`)
		for i := range n {
			fmt.Fprintf(&b, `<xsl:template name="u%d"/>`, i)
		}
		b.WriteString(`<xsl:template name="t"/></xsl:stylesheet>`)
		return sm02Find(sm02Doc(t, b.String()), "source-document")
	}
	sm02Growth(t, 500, 8, func(n int) func() {
		sd := sheet(n)
		return func() { sequenceConstructorVerdict(sd, postureStriding, nil, nil, nil) }
	})
	a := &instrAnalyzer{ctxPosture: postureStriding, ctxAllowsChildren: true, known: true}
	a.body(sheet(3))
	if len(a.tmpls) != 1 {
		t.Errorf("template index has %d modules, want 1", len(a.tmpls))
	}
}

// streamtrust.go accumulatorVerified: N calls on accumulator-before resolve
// the accumulator against one index of the module.
func TestSM02AccumulatorIndexed(t *testing.T) {
	sheet := func(n int) *xdm.Node {
		var b strings.Builder
		b.WriteString(sm02Head + `<xsl:template name="main"><xsl:source-document streamable="yes" href="in.xml"><out>`)
		for range n {
			b.WriteString(`<xsl:value-of select="accumulator-before('a')"/>`)
		}
		b.WriteString(`</out></xsl:source-document></xsl:template>`)
		for i := range n {
			fmt.Fprintf(&b, `<xsl:accumulator name="b%d" initial-value="0" streamable="yes"><xsl:accumulator-rule match="x" select="1"/></xsl:accumulator>`, i)
		}
		b.WriteString(`<xsl:accumulator name="a" initial-value="0" streamable="yes"><xsl:accumulator-rule match="*" select="$value"/></xsl:accumulator></xsl:stylesheet>`)
		return sm02Find(sm02Doc(t, b.String()), "source-document")
	}
	sm02Growth(t, 300, 8, func(n int) func() {
		sd := sheet(n)
		return func() {
			tr := newDeclTrust(documentElementOf(sd), false)
			sequenceConstructorVerdict(sd, postureStriding, tr.sets, tr.funcs, tr)
		}
	})
	sd := sheet(2)
	tr := newDeclTrust(documentElementOf(sd), false)
	at := sm02Find(sd, "value-of")
	if !tr.accumulatorVerified(at, &xpath.Literal{Val: xdm.NewString("a")}) {
		t.Error("accumulator a: unverified, want verified")
	}
	if tr.accumulatorVerified(at, &xpath.Literal{Val: xdm.NewString("zz")}) {
		t.Error("an undeclared accumulator: verified, want unverified")
	}
}

// streamtrust.go declTrust.verified: a chain of declarations each resting on
// the next recurses no deeper than DefaultMaxDepth, and leaves the deeper
// ones unverified.
func TestSM02DeclTrustDepthBounded(t *testing.T) {
	tr := &declTrust{state: map[any]trustState{}}
	n, deepest := 3*DefaultMaxDepth, 0
	var check func(i int) bool
	check = func(i int) bool {
		return tr.verified(i, func() bool {
			deepest = max(deepest, tr.depth)
			return i == n || check(i+1)
		})
	}
	if check(0) {
		t.Error("a chain past the bound: verified, want unverified")
	}
	if deepest > DefaultMaxDepth {
		t.Errorf("recursed %d deep, want at most %d", deepest, DefaultMaxDepth)
	}
	// A chain within the bound still verifies.
	tr = &declTrust{state: map[any]trustState{}}
	n = 10
	if !check(0) {
		t.Error("a short chain: unverified, want verified")
	}

	// End to end: a chain of declared-streamable functions longer than the
	// bound compiles under a streamable mode.
	var b strings.Builder
	b.WriteString(sm02Head + `<xsl:mode streamable="yes"/><xsl:template match="doc"><xsl:sequence select="f:a0(.)"/></xsl:template>`)
	k := DefaultMaxDepth + 50
	for i := range k {
		fmt.Fprintf(&b, `<xsl:function name="f:a%d" streamability="absorbing"><xsl:param name="p"/><xsl:sequence select="f:a%d($p)"/></xsl:function>`, i, i+1)
	}
	fmt.Fprintf(&b, `<xsl:function name="f:a%d" streamability="absorbing"><xsl:param name="p"/><xsl:sequence select="count($p/*)"/></xsl:function></xsl:stylesheet>`, k)
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(tree.Root, CompileOptions{}); err != nil {
		t.Fatal(err)
	}
}

// streamtry.go besideFails: which operands lead to the split variable is
// found in one walk, not by re-optimising each operand along the spine.
func TestSM02BesideFailsOneWalk(t *testing.T) {
	base, err := xpath.CompileVersion("1", nil, xpath.XPath31)
	if err != nil {
		t.Fatal(err)
	}
	ref := func() xpath.Expr { return &xpath.VarRef{Name: splitItems} }
	one := func() xpath.Expr { return &xpath.Literal{Val: xdm.NewInteger(1)} }
	plus := func(l, r xpath.Expr) xpath.Expr { return &xpath.BinaryOp{Op: "+", Left: l, Right: r} }
	sm02Growth(t, 500, 8, func(h int) func() {
		e := ref()
		for range h {
			e = plus(e, one())
		}
		return func() {
			if besideFails(base, e, splitItems) {
				t.Fatal("literal operands: may fail, want cannot")
			}
		}
	})
	div := &xpath.BinaryOp{Op: "div", Left: one(), Right: &xpath.Literal{Val: xdm.NewInteger(0)}}
	if !besideFails(base, plus(plus(ref(), one()), div), splitItems) {
		t.Error("a division beside the path: cannot fail, want may")
	}
	// A binding of the split variable's name: refersTo decides, as before.
	let := &xpath.LetExpr{Bindings: []xpath.Binding{{Var: splitItems, Seq: one()}}, Return: ref()}
	if readersOf(plus(ref(), let), splitItems) != nil {
		t.Error("a let binding the split variable: marked, want left to refersTo")
	}
}

// streampop_l6.go choiceOfStreams (now ifPop): nested ifs around a stream
// path build each branch once, so the compile grows with the depth, not two
// to its power.
func TestSM02NestedIfPopLinear(t *testing.T) {
	sheet := func(d int) *xdm.Node {
		sel := "doc/rec"
		for range d {
			sel = "if ($p) then (" + sel + ") else ()"
		}
		return sm02Doc(t, sm02Head+`<xsl:param name="p" select="true()"/><xsl:template name="main">`+
			`<xsl:source-document streamable="yes" href="doc.xml"><xsl:for-each select="`+sel+`">`+
			`<xsl:value-of select="name()"/></xsl:for-each></xsl:source-document></xsl:template></xsl:stylesheet>`)
	}
	sm02Growth(t, 6, 10, func(d int) func() {
		root := sheet(d)
		return func() {
			if _, err := Compile(root, CompileOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// streamview_leafcall.go leafInlinedSafe: M template rules on text nodes,
// each calling a stylesheet function on the leaf, share the plan's one
// table of the module's functions.
func TestSM02LeafCallFuncsOncePerPlan(t *testing.T) {
	sheet := func(m int) *xdm.Node {
		var b strings.Builder
		b.WriteString(sm02Head + `<xsl:mode streamable="yes"/>` +
			`<xsl:function name="f:len" as="xs:integer" streamability="absorbing" xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			`<xsl:param name="input" as="text()"/><xsl:sequence select="string-length($input)"/></xsl:function>`)
		for i := range m {
			fmt.Fprintf(&b, `<xsl:template match="a%d/text()"><xsl:value-of select="f:len(.)"/></xsl:template>`, i)
		}
		return sm02Doc(t, b.String()+`</xsl:stylesheet>`)
	}
	sm02Growth(t, 100, 8, func(m int) func() {
		root := sheet(m)
		return func() {
			if _, err := Compile(root, CompileOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// streamcall.go useSets: N literal result elements using an attribute set
// share the plan's one table of the module's attribute sets.
func TestSM02UseSetsOncePerPlan(t *testing.T) {
	sheet := func(n int) *xdm.Node {
		var b strings.Builder
		b.WriteString(sm02Head + `<xsl:template name="main"><xsl:source-document streamable="yes" href="in.xml"><out>`)
		for range n {
			b.WriteString(`<e xsl:use-attribute-sets="s"/>`)
		}
		b.WriteString(`</out></xsl:source-document></xsl:template>`)
		for i := range n {
			fmt.Fprintf(&b, `<xsl:template name="u%d"/>`, i)
		}
		return sm02Doc(t, b.String()+`<xsl:attribute-set name="s" streamable="yes"><xsl:attribute name="a" select="'1'"/></xsl:attribute-set></xsl:stylesheet>`)
	}
	sm02Growth(t, 1500, 8, func(n int) func() {
		root := sheet(n)
		return func() {
			if _, err := Compile(root, CompileOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// streamsplit_calls.go judgeSheetCall: N calls on a function reaching a
// chain of K others judge the function once per compile, not once per call.
func TestSM02SheetCallJudgedOnce(t *testing.T) {
	sheet := func(n int) *xdm.Node {
		const k = 50
		var b strings.Builder
		b.WriteString(sm02Head)
		for i := 1; i < k; i++ {
			fmt.Fprintf(&b, `<xsl:function name="f:f%d"><xsl:param name="p"/><xsl:sequence select="f:f%d($p)"/></xsl:function>`, i, i+1)
		}
		fmt.Fprintf(&b, `<xsl:function name="f:f%d"><xsl:param name="p"/><xsl:sequence select="$p"/></xsl:function>`, k)
		b.WriteString(`<xsl:template name="main"><xsl:source-document streamable="yes" href="in.xml"><out>`)
		for range n {
			b.WriteString(`<xsl:value-of select="f:f1(1)"/>`)
		}
		return sm02Doc(t, b.String()+`<xsl:value-of select="count(*/*)"/></out></xsl:source-document></xsl:template></xsl:stylesheet>`)
	}
	sm02Growth(t, 300, 8, func(n int) func() {
		root := sheet(n)
		return func() {
			if _, err := Compile(root, CompileOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	})
	fns := collectStreamFuncs(sheet(1))
	j := newSheetJudge(fns)
	x := &xpath.FuncCall{Name: xdm.QName{URI: "urn:f", Local: "f1"}, Args: []xpath.Expr{&xpath.Literal{Val: xdm.NewInteger(1)}}}
	got, ok := j.judge(x)
	want, wantOK := judgeSheetCall(x, fns)
	if ok != wantOK || got.values != want.values || got.burst != want.burst {
		t.Errorf("memoised judgement %+v %v, want %+v %v", got, ok, want, wantOK)
	}
	if _, ok := j.judge(x); !ok || len(j.memo) != 1 {
		t.Errorf("second judgement: memo holds %d, want 1", len(j.memo))
	}
}
