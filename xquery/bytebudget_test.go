package xquery

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// bomb builds the doubling chain: n nested "let"s, each concatenating the
// previous string with itself, so the result is seed * 2^n bytes from an
// expression that grows by one line per doubling.
func bomb(n int, seed, tail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "let $s0 := '%s' return ", seed)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "let $s%d := concat($s%d, $s%d) return ", i, i-1, i-1)
	}
	fmt.Fprintf(&b, tail, n)
	return b.String()
}

// wantByteRefusal asserts the byte-budget refusal, with the code and the
// sentinel a caller distinguishes it by.
func wantByteRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected XPDY0130; the MaxBytes budget did not bind, and " +
			"the evaluation built the whole string")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from a bad query", err)
	}
	// The wording separates this refusal from the item budget's, which
	// carries the same code.
	if !strings.Contains(err.Error(), "bytes of string content") {
		t.Errorf("message %q is not the byte-budget refusal", err)
	}
}

// The reported defect: a 1,009-byte expression returned 671,088,640 bytes in
// about 600ms with no error, because MaxItems counts items and a string is
// one item however long it is.
func TestStringBombIsRefused(t *testing.T) {
	q := bomb(26, "AAAAAAAAAA", "string-length($s%d)")
	if len(q) > 1100 {
		t.Fatalf("the bomb is %d bytes; it is meant to be about a kilobyte",
			len(q))
	}
	_, err := evalQ(t, q)
	wantByteRefusal(t, err)
}

// The refusal must arrive from the budget rather than from exhaustion: four
// more doublings is ten gigabytes, and it must be refused just as quickly
// rather than being attempted.
func TestALargerBombIsRefusedToo(t *testing.T) {
	_, err := evalQ(t, bomb(30, "AAAAAAAAAA", "string-length($s%d)"))
	wantByteRefusal(t, err)
}

// The "||" operator is fn:concat by definition and evaluates its operands
// itself, so a chain of it is the same bomb written another way.
func TestStringConcatOperatorChainIsRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString("let $s0 := 'AAAAAAAAAA' return ")
	for i := 1; i <= 26; i++ {
		fmt.Fprintf(&b, "let $s%d := $s%d || $s%d return ", i, i-1, i-1)
	}
	b.WriteString("string-length($s26)")
	_, err := evalQ(t, b.String())
	wantByteRefusal(t, err)
}

// The hazard on the other side of the fix, and the worse one: a bound set too
// low refuses valid work, and this library's docs record a false rejection as
// a conformance bug.
//
// The sizes here are drawn from measurement rather than taste. Instrumenting
// the charge points and running the suites and the real-world corpora, the
// largest string legitimately built was 14,516,346 bytes (the XSLT 3.0
// suite); the DocBook xslTNG and XSpec corpora peaked at 1,031,269 across 877
// documents. Sixteen megabytes is above both and must still build.
func TestALargeLegitimateStringStillBuilds(t *testing.T) {
	// 10 bytes doubled twenty-one times is 20,971,520 -- larger than
	// anything either suite or either corpus was measured to build.
	seq, err := evalQ(t, bomb(21, "AAAAAAAAAA", "string-length($s%d)"))
	if err != nil {
		t.Fatalf("a 20 MB string -- larger than anything the suites or the "+
			"real-world corpora build -- was refused: %v", err)
	}
	if got := seq[0].(*xdm.Atomic).Int64(); got != 20971520 {
		t.Errorf("string-length = %d, want 20971520", got)
	}
}

// fn:string-join over a large sequence is the shape a report that
// concatenates thousands of rows takes, and it is ordinary work.
func TestALargeStringJoinStillBuilds(t *testing.T) {
	seq, err := evalQ(t,
		"string-length(string-join(for $i in 1 to 200000 return 'abcdefghij', '-'))")
	if err != nil {
		t.Fatalf("joining 200,000 rows was refused: %v", err)
	}
	if got := seq[0].(*xdm.Atomic).Int64(); got != 2199999 {
		t.Errorf("string-length = %d, want 2199999", got)
	}
}

// The budget bounds ONE evaluation, not the lifetime of a context. A counter
// armed but never reset would let a long-lived context accumulate across
// independent queries and start refusing valid ones after enough use -- a
// denial of service that arrives on legitimate traffic, which is worse than
// the bug being fixed.
//
// Each iteration here builds about 80 MB, so thirty of them is well past a
// gigabyte and a context that leaked would fail before the end.
func TestSequentialQueriesOnOneContextDoNotAccumulateBytes(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	q := bomb(23, "AAAAAAAAAA", "string-length($s%d)")
	for i := 0; i < 30; i++ {
		seq, err := Eval(q, ctx, Options{})
		if err != nil {
			t.Fatalf("query %d of 30 on a reused context was refused: %v; "+
				"the byte budget is leaking across evaluations", i+1, err)
		}
		if got := seq[0].(*xdm.Atomic).Int64(); got != 83886080 {
			t.Fatalf("query %d: string-length = %d, want 83886080", i+1, got)
		}
	}
}

// The reverse hazard: a budget reset so often that it never binds. One query
// evaluated many times must be refused every time -- if the refusal came only
// from residue an earlier run left, the first would pass.
func TestByteRefusalIsReproducibleOnAReusedContext(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	q := bomb(26, "AAAAAAAAAA", "string-length($s%d)")
	for i := 0; i < 3; i++ {
		_, err := Eval(q, ctx, Options{})
		wantByteRefusal(t, err)
	}
}

// A refused query must not poison the context it ran on: the budget is reset
// when the next evaluation arms it, so a small query after a refused one
// succeeds.
func TestASmallQueryAfterARefusedByteBombSucceeds(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	if _, err := Eval(bomb(26, "AAAAAAAAAA", "string-length($s%d)"),
		ctx, Options{}); err == nil {
		t.Fatal("expected the bomb to be refused")
	}
	seq, err := Eval("concat('a', 'b')", ctx, Options{})
	if err != nil {
		t.Fatalf("a two-byte concat after a refused bomb was refused too: "+
			"%v; the failed evaluation left its charges on the context", err)
	}
	if got := seq[0].(*xdm.Atomic).String(); got != "ab" {
		t.Errorf("= %q, want \"ab\"", got)
	}
}

// A Context assembled by hand rather than through NewContext has no budget
// and must stay unbounded and usable, which is what keeps the type a plain
// value. The nil counter must not panic.
func TestAHandBuiltContextIsUnbounded(t *testing.T) {
	c := &xpath.Context{Funcs: xpath.Builtins()}
	if err := c.ChargeBytes(1 << 40); err != nil {
		t.Errorf("a hand-built context charged %v; it has no budget and "+
			"must be unbounded", err)
	}
	var nilCtx *xpath.Context
	if err := nilCtx.ChargeBytes(1 << 40); err != nil {
		t.Errorf("a nil context charged %v; the guard must be nil-safe", err)
	}
}
