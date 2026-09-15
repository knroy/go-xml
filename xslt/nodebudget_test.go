package xslt

import (
	"context"
	"errors"
	"fmt"
	// Aliased: this package declares its own "runtime" type.
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// crossProductSheet is the result-tree bomb: two nested xsl:for-each over the
// same axis, so the result holds the SQUARE of the input's node count. It is
// three lines, and nothing about it is exotic -- a report stylesheet that
// pairs every row with every other row has this shape by accident.
const crossProductSheet = `<xsl:stylesheet version="3.0" ` +
	`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
	`<xsl:template match="/"><out>` +
	`<xsl:for-each select="//i"><xsl:for-each select="//i">` +
	`<r><xsl:value-of select="."/></r>` +
	`</xsl:for-each></xsl:for-each>` +
	`</out></xsl:template></xsl:stylesheet>`

// crossProductDoc is n <i> elements, so crossProductSheet over it builds n*n
// elements plus their text.
func crossProductDoc(n int) string {
	var b strings.Builder
	b.WriteString("<d>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<i>%d</i>", i)
	}
	b.WriteString("</d>")
	return b.String()
}

// runCross transforms crossProductDoc(n) with crossProductSheet.
func runCross(t *testing.T, n int) error {
	t.Helper()
	sd, err := xdm.ParseString(crossProductSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(crossProductDoc(n), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	_, err = st.Transform(context.Background(), doc.Root, TransformOptions{})
	return err
}

func wantNodeRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected XPDY0130; the MaxNodes budget did not bind, and " +
			"the transform built the whole result tree")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from a bad stylesheet", err)
	}
	if !strings.Contains(err.Error(), "result-tree nodes") {
		t.Errorf("message %q is not the node-budget refusal", err)
	}
}

// A result tree larger than the bound is refused rather than built. 1,600 <i>
// elements is 16 kB of input and 2,560,000 result elements, over the two
// million MaxNodes allows.
func TestCrossProductResultTreeIsRefused(t *testing.T) {
	wantNodeRefusal(t, runCross(t, 1600))
}

// The other half, and the one that matters more: a result tree that is merely
// large must still be built. 800 <i> elements is 640,000 result elements --
// more than twice the largest tree the conformance suites or the DocBook and
// XSpec corpora construct -- and it is ordinary work.
func TestALargeLegitimateResultTreeStillBuilds(t *testing.T) {
	if err := runCross(t, 800); err != nil {
		t.Fatalf("a 640,000-node result tree -- larger than anything the "+
			"suites or the real-world corpora build -- was refused: %v", err)
	}
}

// The bound has to cost bounded MEMORY, not merely arrive eventually. Before
// the charge, this input allocated 44 GB over 49 seconds and returned a
// result; the first default tried (fifty million nodes) did refuse it, but
// only after 128 GB and seven minutes, which is not a memory bound at all.
//
// What the assertion pins is the shape rather than a machine's speed: the
// refusal's cost must not grow with the input. The 24 kB input is nine times
// the result tree of the 16 kB one, so an unbounded construction allocates
// several times as much for it, while a bound that binds allocates the same
// for both -- it stops at two million nodes either way.
func TestResultTreeRefusalCostsBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("timing and allocation test")
	}
	alloc := func(n int) (uint64, time.Duration) {
		var m0, m1 goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&m0)
		start := time.Now()
		err := runCross(t, n)
		el := time.Since(start)
		goruntime.ReadMemStats(&m1)
		wantNodeRefusal(t, err)
		return m1.TotalAlloc - m0.TotalAlloc, el
	}
	small, smallT := alloc(1600)
	big, bigT := alloc(3000)
	t.Logf("1600 -> %d MB in %v; 3000 -> %d MB in %v",
		small/(1<<20), smallT.Round(time.Millisecond),
		big/(1<<20), bigT.Round(time.Millisecond))
	// Nine times the result tree for at most a quarter more allocation. The
	// margin is loose because the charge is checked between instructions
	// rather than at the exact node, so the two runs stop at slightly
	// different points; it is nowhere near the several-fold growth an
	// unbounded construction shows.
	if big > small+small/4 {
		t.Errorf("refusing a 9,000,000-node tree allocated %d MB against "+
			"%d MB for a 2,560,000-node one.\nThe cost of the refusal is "+
			"growing with the input, so the budget is being reached after "+
			"the construction rather than during it.",
			big/(1<<20), small/(1<<20))
	}
	// A wall-clock floor too, since a bound that binds promptly is the point.
	// Generous by design: the pre-fix figure for the larger input was 49 s.
	budget := 30 * time.Second
	if raceEnabled {
		budget *= 4
	}
	if bigT > budget {
		t.Errorf("refusing took %v, over the %v budget; the transform is "+
			"building most of the tree before the bound is noticed",
			bigT.Round(time.Millisecond), budget)
	}
}

// The budget bounds ONE transform, not the lifetime of a compiled stylesheet.
// A counter armed but never reset would let a long-lived Stylesheet start
// refusing valid documents after enough use -- a denial of service arriving on
// legitimate traffic, which is worse than the runaway this guards.
func TestSequentialTransformsDoNotAccumulateNodes(t *testing.T) {
	sd, err := xdm.ParseString(crossProductSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(crossProductDoc(300), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	// 90,000 nodes a run, so thirty runs is 2,700,000 -- past the bound if
	// the counter leaked, and comfortably under it per transform.
	for i := 0; i < 30; i++ {
		if _, err := st.Transform(context.Background(), doc.Root,
			TransformOptions{}); err != nil {
			t.Fatalf("transform %d of 30 on one stylesheet was refused: %v; "+
				"the node budget is leaking across transforms", i+1, err)
		}
	}
}

// The reverse hazard: a budget reset so often that it never binds. One
// stylesheet run repeatedly must be refused every time -- if the refusal came
// only from residue an earlier run left, the first would pass.
func TestNodeRefusalIsReproducible(t *testing.T) {
	for i := 0; i < 3; i++ {
		wantNodeRefusal(t, runCross(t, 1600))
	}
}

// A refused transform must leave no residue: a small transform on the same
// compiled stylesheet afterwards succeeds.
func TestASmallTransformAfterARefusedNodeRunSucceeds(t *testing.T) {
	sd, err := xdm.ParseString(crossProductSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	big, err := xdm.ParseString(crossProductDoc(1600), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if _, err := st.Transform(context.Background(), big.Root,
		TransformOptions{}); err == nil {
		t.Fatal("expected the cross product to be refused")
	}
	small, err := xdm.ParseString(crossProductDoc(10), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if _, err := st.Transform(context.Background(), small.Root,
		TransformOptions{}); err != nil {
		t.Fatalf("a 100-node transform after a refused one was refused too: "+
			"%v; the failed transform left its charges behind", err)
	}
}

// A temporary tree is allocated for real while it is being built, so a
// runaway written into a variable has to be charged too. A bound that saw only
// the principal result tree would miss this entirely: the variable is never
// referenced, and its content never reaches the output.
func TestARunawayInsideAVariableIsRefused(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template match="/">` +
		`<xsl:variable name="v">` +
		`<xsl:for-each select="//i"><xsl:for-each select="//i">` +
		`<r><xsl:value-of select="."/></r>` +
		`</xsl:for-each></xsl:for-each>` +
		`</xsl:variable>` +
		`<out><xsl:value-of select="count($v//r)"/></out>` +
		`</xsl:template></xsl:stylesheet>`
	sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(crossProductDoc(1600), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	_, err = st.Transform(context.Background(), doc.Root, TransformOptions{})
	wantNodeRefusal(t, err)
}

// xsl:copy-of is the other route to the same runaway: copying a large subtree
// once per node of a large document builds the cross product without a nested
// loop constructing anything. Charging a copy as one node would leave it open.
func TestRepeatedCopyOfIsRefused(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template match="/"><out>` +
		`<xsl:for-each select="//i"><xsl:copy-of select="/d"/></xsl:for-each>` +
		`</out></xsl:template></xsl:stylesheet>`
	sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(crossProductDoc(1600), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	_, err = st.Transform(context.Background(), doc.Root, TransformOptions{})
	wantNodeRefusal(t, err)
}
