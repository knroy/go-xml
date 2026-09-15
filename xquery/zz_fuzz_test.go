package xquery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// Compile must never panic, whatever the input, and must never take
// disproportionately long to reach its answer.
//
// The second half is the reason this target exists. Nine fuzz targets in this
// repository had run some 16 million executions between them and none of them
// covered xquery.Compile at all, so the direct-constructor scan was never
// fuzzed. More than that: every one of those targets asserts only "no panic",
// and the two denial-of-service findings in that scan panic at neither half.
//
//   - An unterminated "<a>{" repeated 30 times -- 120 bytes -- spent 53
//     seconds and returned the CORRECT error. A no-panic assertion passes on
//     that, and so does any test that merely checks a malformed query is
//     refused; the cost is paid before the right answer comes back.
//   - The same shape well-formed was ACCEPTED at 20,000 levels of nesting,
//     bypassing the parser's own depth bound, and returned a valid query.
//     Nothing about that looks like a fault to a target watching for crashes.
//
// So this target asserts a time budget as well. A fuzzer explores exactly the
// nested-delimiter shapes that make a scanner superlinear, and a budget is
// what turns "it finished" into a result worth having. See
// xsd/complexity_fuzz_test.go, which measures growth rates for the same
// reason.
//
// KNOWN OPEN FINDING, not caught by the budget below. The first 60-second run
// of this target (1.58M executions) found a SEPARATE non-terminating loop
// that is not in the constructor scan and is not fixed here:
//
//	Compile("for$A in M\x17<combining mark>0(00000\xdf")  -- never returns
//
// scanExprSingleSource's word loop tests the byte with isNameStartByte
// (flwor_parse.go), which accepts any byte >= 0x80, and then reads the name
// with scanNCName (parser.go), which decodes a RUNE and tests
// xdm.IsNameStartChar. A byte that begins a character which is a name char
// but not a name START char satisfies the first and fails the second, so
// scanNCName returns "" having advanced p.pos by nothing and the loop spins
// on the same offset forever. The two tests have to agree about what starts a
// name, or the loop has no guarantee of progress.
//
// A SECOND open finding, from the next run of this target -- a panic rather
// than a hang, and again outside the constructor scan:
//
//	Compile("declare variable$A:=<")
//	  panic: slice bounds out of range [:22] with length 21
//	  scanDeclExpr, prolog.go:976
//
// The declaration-body scan steps over a direct constructor and, where that
// constructor is unterminated, leaves p.pos one past len(p.src); the
// "p.src[start:p.pos]" at the end of the scan then slices out of range.
// "declare variable $a := <" is the same fault with the spaces written in.
//
// Both are left open deliberately rather than folded into this change, which
// is about the constructor scan in enclosed.go: the faults, the files and the
// fixes are all different. Both were verified to reproduce IDENTICALLY with
// this commit's fix reverted, so neither is a regression from it.
//
// Neither input is checked in as a corpus file, because a seed that hangs or
// panics would leave this package's tests failing for bugs this commit does
// not claim to fix. Fuzzing this target rediscovers them in about a minute
// each, which is the point: nine existing targets and ~16M executions never
// covered xquery.Compile at all.
func FuzzCompileNoPanic(f *testing.F) {
	for _, s := range []string{
		`1 + 2`, `<a>{1}</a>`, `<a b="{1}">t</a>`, `<a/>`,
		`<f>f's value</f>`, `<a>{ 'x}y' }</a>`, `<a>{ <b>{1}</b> }</a>`,
		`<a>{{literal}}</a>`, `<a><![CDATA[ {x} ]]></a>`, `<a><!-- {x} --></a>`,
		`for $x in 1 to 3 return $x`, `if (1) then 2 else 3`,
		`declare variable $v := 1; $v`, `(: c :)1`, `1 < 2`,
		`let $x := <a/> return $x`, `map {'a':1}`, `[1,2]`,
		`<a>{`, `<a>{<a>{<a>{`, `` + "``[x]``",
		`element a {1}`, `attribute a {1}`, `<?p i?>`,
		`some $x in (1,2) satisfies $x eq 1`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 400 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile(%q) panicked: %v", src, r)
			}
		}()
		start := time.Now()
		q, err := Compile(src, Options{})
		// 400 bytes is a trivial amount of text. Anything approaching a
		// second on it is a superlinear scan, which is the finding this
		// target exists to catch -- the unterminated constructor took 53
		// seconds on 120 bytes and returned the correct error. The bound is
		// loose enough not to be flaky on a loaded fuzzing machine.
		if el := time.Since(start); el > 2*time.Second {
			t.Fatalf("Compile(%q) took %v on %d bytes; superlinear scan",
				src, el, len(src))
		}
		if err != nil {
			// Every compile error must carry a spec code, not a bare message.
			if code := xdm.ErrorCode(err); code == "" {
				t.Fatalf("Compile(%q) error without a code: %v", src, err)
			}
			return
		}
		if q == nil {
			t.Fatalf("Compile(%q) returned no query and no error", src)
		}
		_ = strings.TrimSpace(src)
	})
}

// A query nested past the scan's bound must be REFUSED, never accepted and
// never run until the stack gives out; one inside the bound must still
// compile. Both directions matter: a bound that refuses real queries is a
// worse bug than the one it fixes.
//
// This is the half a no-panic target cannot see -- the over-deep query
// compiled successfully. Generating the shape directly rather than waiting
// for the fuzzer to find it keeps the property checkable in a normal run.
//
// The thresholds are in SOURCE levels, and one source level is not one count.
// "<a>{ ... }</a>" charges the counter twice per level, once for the
// constructor and once for the enclosed expression inside it, so the ceiling
// sits at 500 source levels rather than at maxConstructorDepth. Measured:
// 500 compiles, 501 is refused. The fuzzer found this by refusing depth 985,
// which was this test's assumption being wrong rather than the bound being
// wrong -- so the two are asserted with a gap between them, and nothing is
// claimed about the handful of levels either side of the boundary.
func FuzzConstructorDepthIsBounded(f *testing.F) {
	for _, n := range []int{1, 5, 100, 501, 1001, 5000} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n int) {
		if n < 0 || n > 50000 {
			return
		}
		src := strings.Repeat("<a>{", n) + "1" + strings.Repeat("}</a>", n)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile at depth %d panicked: %v", n, r)
			}
		}()
		start := time.Now()
		_, err := Compile(src, Options{})
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("depth %d took %v", n, el)
		}
		// Comfortably past the ceiling: must be refused, and as a resource
		// limit rather than as a syntax fault.
		if n > 600 {
			if err == nil {
				t.Fatalf("depth %d was accepted; the bound did not apply", n)
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Fatalf("depth %d refused without the sentinel: %v", n, err)
			}
		}
		// Comfortably inside it: must still compile.
		if n > 0 && n < 400 && err != nil {
			t.Fatalf("depth %d is well within the bound but was refused: %v",
				n, err)
		}
	})
}
