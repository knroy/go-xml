package xpath

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// parseXMLBomb is a document whose entity expansion is large but stays UNDER
// the 1 MB per-document ceiling: "c" expands to 16 KB, referenced 48 times for
// 786,432 bytes out of about 400 bytes of source. Every copy of it is a
// document xdm is willing to parse on its own, which is the point: the defect
// was never one oversized document, it was many legal ones.
func parseXMLBomb() string {
	return `<!DOCTYPE d [` +
		`<!ENTITY a "` + strings.Repeat("A", 64) + `">` +
		`<!ENTITY b "` + strings.Repeat("&a;", 16) + `">` +
		`<!ENTITY c "` + strings.Repeat("&b;", 16) + `">` +
		`]><d>` + strings.Repeat("&c;", 48) + `</d>`
}

// parseXMLBombBytes is what one parseXMLBomb expands to.
const parseXMLBombBytes = 64 * 16 * 16 * 48

// xpathLiteral quotes s as an XPath string literal.
func xpathLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "&apos;") + "'"
}

// evalAt31 compiles and evaluates expr under XPath 3.1 against a fresh
// Context, which is one evaluation and therefore one entity allowance.
func evalAt31(t *testing.T, expr string) (xdm.Sequence, error) {
	t.Helper()
	c, err := Compile(expr, CompileOptions{Namespaces: nil, Version: XPath31})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	return c.Eval(ctx)
}

// The entity-expansion ceiling must bound one EVALUATION, not one call to
// fn:parse-xml.
//
// fn:parse-xml built its ParseOptions fresh on every call, carrying no budget,
// so every call minted a new allowance. It is an ordinary function in the
// default library, so an expression calls it once per node with no fetch
// counter, no memo and no cache to bound the repetition — which makes it
// strictly worse than the XInclude case that fix 989e88d closed, where the
// pass at least caps at 200 fetches.
//
// Measured before the fix: 1,328 bytes of XPath — one parse-xml of an inlined
// bomb, called 60 times in a "for" — expanded 47,185,920 bytes and allocated
// 179.9 MB, and was ACCEPTED. At 300 calls it allocated 897.9 MB, growing
// linearly with the loop because nothing accumulated. The same expression is
// now refused with ErrResourceLimit after allocating 3.6 MB, and 300 calls
// allocate the same 3.6 MB rather than five times more.
//
// This is the crux of the fix. A bound that a loop re-mints is not a bound,
// so a test that only proves ONE oversized parse is refused would pass against
// the vulnerable code and prove nothing.
func TestParseXMLBudgetIsSharedAcrossOneEvaluation(t *testing.T) {
	// Two bombs already exceed the ceiling together (786,432 x 2 > 1 MB)
	// while each is separately legal, so a shared allowance must refuse the
	// loop and a per-call allowance must accept it.
	const calls = 60
	expr := fmt.Sprintf("count(for $i in 1 to %d return parse-xml(%s))",
		calls, xpathLiteral(parseXMLBomb()))

	if want := 2 * parseXMLBombBytes; want <= 1<<20 {
		t.Fatalf("test is not testing anything: two bombs expand to %d bytes, "+
			"which is under the %d ceiling", want, 1<<20)
	}

	_, err := evalAt31(t, expr)
	if err == nil {
		t.Fatalf("%d calls to parse-xml expanding %d bytes were ACCEPTED; "+
			"the budget is being re-minted per call",
			calls, calls*parseXMLBombBytes)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("refused, but not as a resource limit: %v", err)
	}
}

// The refusal must not depend on how many times the loop runs: a budget that
// binds at 60 calls but not at 600 would be an artefact of the bomb's size
// rather than a shared counter. Both must refuse, and for the same reason.
func TestParseXMLBudgetDoesNotScaleWithCallCount(t *testing.T) {
	for _, calls := range []int{2, 60, 600} {
		expr := fmt.Sprintf("count(for $i in 1 to %d return parse-xml(%s))",
			calls, xpathLiteral(parseXMLBomb()))
		_, err := evalAt31(t, expr)
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%d calls: want ErrResourceLimit, got %v", calls, err)
		}
	}
}

// Each evaluation is a fresh allowance. The budget is scoped to one
// evaluation, not to the process or to the shared builtin library, so a second
// evaluation must not inherit the first one's spend — otherwise a long-lived
// host would poison every later expression, which is the opposite failure and
// just as wrong.
func TestParseXMLBudgetIsFreshPerEvaluation(t *testing.T) {
	// Comfortably under the ceiling on its own: 16 KB expanded.
	one := `<!DOCTYPE d [<!ENTITY a "` + strings.Repeat("A", 64) + `">` +
		`<!ENTITY b "` + strings.Repeat("&a;", 16) + `">]><d>` +
		strings.Repeat("&b;", 16) + `</d>`
	expr := "count(parse-xml(" + xpathLiteral(one) + "))"

	for i := 0; i < 200; i++ {
		if _, err := evalAt31(t, expr); err != nil {
			t.Fatalf("evaluation %d of a legal document failed: %v", i, err)
		}
	}
}

// CONTROL: a legitimate large-but-bounded parse-xml must still work.
//
// This is what stops the fix from being "refuse everything". A single document
// expanding to roughly 786 KB — three quarters of the ceiling — is legal and
// must parse, and its content must be correct rather than truncated.
func TestParseXMLLegitimateLargeDocumentStillWorks(t *testing.T) {
	src := parseXMLBomb()
	seq, err := evalAt31(t,
		"string-length(string(parse-xml("+xpathLiteral(src)+")))")
	if err != nil {
		t.Fatalf("a single legal %d-byte expansion was refused: %v",
			parseXMLBombBytes, err)
	}
	if len(seq) != 1 {
		t.Fatalf("want one result, got %d", len(seq))
	}
	if got := fmt.Sprint(seq[0]); got != fmt.Sprint(parseXMLBombBytes) {
		t.Errorf("document expanded to %s bytes, want %d — the parse was "+
			"truncated rather than merely bounded", got, parseXMLBombBytes)
	}
}

// CONTROL: ordinary parse-xml, used the way a stylesheet actually uses it,
// must be untouched — many small documents in a loop, well within the
// allowance, parsed for their content.
func TestParseXMLOrdinaryLoopIsUntouched(t *testing.T) {
	seq, err := evalAt31(t,
		`count(for $i in 1 to 500 return parse-xml('<a><b>x</b></a>')//b)`)
	if err != nil {
		t.Fatalf("500 ordinary parses were refused: %v", err)
	}
	if len(seq) != 1 {
		t.Fatalf("want one result, got %d", len(seq))
	}
	if got := fmt.Sprint(seq[0]); got != "500" {
		t.Errorf("got %s matched elements, want 500", got)
	}
}

// A resource refusal must keep its sentinel rather than being reported as
// FODC0006. FODC0006 means "not a well-formed document", which is false here
// and which a try/catch on that code could swallow — laundering the refusal
// the way xi:fallback was measured doing before 989e88d made it fatal.
func TestParseXMLBudgetRefusalIsNotAWellFormednessError(t *testing.T) {
	expr := fmt.Sprintf("count(for $i in 1 to 60 return parse-xml(%s))",
		xpathLiteral(parseXMLBomb()))
	_, err := evalAt31(t, expr)
	if err == nil {
		t.Fatal("expansion was accepted")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("want ErrResourceLimit, got %v", err)
	}
	if strings.Contains(err.Error(), "FODC0006") {
		t.Errorf("a resource refusal was reported as a well-formedness "+
			"error, which a catch on FODC0006 could launder: %v", err)
	}
}
