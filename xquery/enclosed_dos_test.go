package xquery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// The direct-constructor scan was both exponential and unbounded, and the two
// faults were independent. Each half is pinned separately below, because a
// fix for either one alone leaves a remotely reachable denial of service.
//
// The scan is reached by compiling a query, so every case here is an
// anonymous string an embedder would accept from a caller. Nothing about
// either shape requires privileges, a schema, or a document.

// Finding A: work multiplied per nesting level instead of adding.
//
// findEnclosed's "<" case called skipDirectConstructor and, on a false
// report, left the scan position where it was. The loop's i++ then re-entered
// the same recursion one byte later, so every level re-scanned the whole
// remaining input and recursed again from each position in it. The cost
// doubled per added character:
//
//	n=22   88 bytes    210ms
//	n=26  104 bytes   3.29s
//	n=30  120 bytes  52.8s
//
// 120 bytes pinning a core for most of a minute is the whole finding. The
// error returned was always CORRECT -- "unterminated enclosed expression" --
// which is why no test asserting that a malformed query is refused ever
// caught it: the cost is paid before the right answer is returned.
//
// The bound is deliberately loose. A correct scan finishes these in well
// under a millisecond, so a second is orders of magnitude of headroom on a
// loaded machine while still failing decisively against doubling: the
// unfixed scan needs ~53s for the last case alone.
func TestUnterminatedConstructorScanIsNotExponential(t *testing.T) {
	for _, n := range []int{22, 26, 30, 40, 60} {
		src := strings.Repeat("<a>{", n)
		done := make(chan error, 1)
		start := time.Now()
		go func() {
			_, err := Compile(src, Options{})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("n=%d: %q was accepted; it is unterminated", n, src)
				continue
			}
			if el := time.Since(start); el > time.Second {
				t.Errorf("n=%d: %d bytes took %v; the scan is superlinear again",
					n, len(src), el)
			}
		case <-time.After(10 * time.Second):
			// Reporting rather than hanging the package: an exponential scan
			// at n=60 would not finish in the lifetime of the run.
			t.Fatalf("n=%d: %d bytes did not compile within 10s; the scan is "+
				"exponential again", n, len(src))
		}
	}
}

// Finding B: the parser's depth limit was bypassed entirely.
//
// xpath's maxParseDepth refuses 1,000 levels of nesting and fires on every
// other route into the parser -- 5,000 parentheses are refused in
// milliseconds. It never saw a direct constructor, because this scan runs
// BEFORE any expression text reaches xpath. So the well-formed query below
// was ACCEPTED at depth 20,000, having spent 4.8 seconds and a matching
// amount of stack getting there.
//
// The refusal must carry the same code and the same sentinel as the
// parenthetical route, or an embedder cannot treat the two alike.
func TestDeeplyNestedConstructorsAreRefused(t *testing.T) {
	// The depths are written as literals rather than as maxConstructorDepth+1
	// so that this test still COMPILES against a tree without the fix, and so
	// fails on the behaviour it is pinning rather than on a missing constant.
	// A test that cannot build proves nothing about a revert.
	for _, n := range []int{1001, 5000, 20000} {
		src := strings.Repeat("<a>{", n) + "1" + strings.Repeat("}</a>", n)
		done := make(chan error, 1)
		start := time.Now()
		go func() {
			_, err := Compile(src, Options{})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("n=%d: accepted; the depth bound did not apply", n)
				continue
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("n=%d: refused without the sentinel: %v; a caller "+
					"cannot tell this from a malformed query", n, err)
			}
			if code := xdm.ErrorCode(err); code != "XPDY0130" {
				t.Errorf("n=%d: code %q, want XPDY0130; the wrap must ADD the "+
					"sentinel, never replace the code", n, code)
			}
			if el := time.Since(start); el > 5*time.Second {
				t.Errorf("n=%d: refused only after %v", n, el)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("n=%d: did not terminate", n)
		}
	}
}

// The refusal must match the route that already worked, so that a query
// refused for depth reads the same however it was nested. This is the control
// that proved the bypass: same Compile, same depth, one refused and one not.
func TestConstructorAndParenDepthRefusalsAgree(t *testing.T) {
	const n = 5000
	_, parenErr := Compile(strings.Repeat("(", n)+"1"+strings.Repeat(")", n),
		Options{})
	_, ctorErr := Compile(
		strings.Repeat("<a>{", n)+"1"+strings.Repeat("}</a>", n), Options{})
	if parenErr == nil || ctorErr == nil {
		t.Fatalf("both routes must refuse depth %d: parens=%v ctors=%v",
			n, parenErr, ctorErr)
	}
	for _, c := range []struct {
		name string
		err  error
	}{{"parens", parenErr}, {"constructors", ctorErr}} {
		if !errors.Is(c.err, xdm.ErrResourceLimit) {
			t.Errorf("%s: %v does not wrap ErrResourceLimit", c.name, c.err)
		}
		if !strings.Contains(c.err.Error(), "nesting exceeds") {
			t.Errorf("%s: %v is not a nesting refusal", c.name, c.err)
		}
	}
}

// The bound must not cost the queries this scanner exists to get right. Each
// case below is one the comment on findEnclosed names, and a fix that broke
// any of them would be worse than the vulnerability: K2-Axes-1 is the suite
// case for the apostrophe, and the brace-in-string case is why the scan may
// not simply skip a constructor wholesale.
func TestOrdinaryConstructorsStillCompile(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"apostrophe in content", `<f>f's value</f>`},
		{"brace in string inside enclosed", `<a>{ 'x}y' }</a>`},
		{"nested constructor", `<a>{ <b>{1}</b> }</a>`},
		{"attribute with enclosed", `<a b="{1}">t</a>`},
		{"self closing", `<a/>`},
		{"comparison not a constructor", `1 < 2`},
		{"cdata", `<a><![CDATA[ {not an expr} ]]></a>`},
		{"comment in content", `<a><!-- {x} --></a>`},
		{"doubled braces", `<a>{{literal}}</a>`},
		{"deep but legal", strings.Repeat("<a>{", 100) + "1" +
			strings.Repeat("}</a>", 100)},
	} {
		if _, err := Compile(c.src, Options{}); err != nil {
			t.Errorf("%s: %q: %v", c.name, c.src, err)
		}
	}
}
