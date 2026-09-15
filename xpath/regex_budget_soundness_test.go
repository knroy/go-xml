package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The backtracking budget must be DISTINGUISHABLE from an ordinary non-match.
//
// backtrackBudget bounds the steps a single match may take, and the bound is a
// legitimate resource control: without one, "(a*)*\1b" against sixty "a"s does
// not finish. What matters is what running out is reported as. A budget that
// silently returns false makes a security guard change the language: a pattern
// that should have matched reports "did not match", the caller cannot tell the
// two apart, and a document is accepted or rejected on the strength of an
// answer this engine never computed.
//
// So the required shape is
//
//	no match         -> a normal false result
//	budget exhausted -> a dynamic error, FORX0002
//
// The mechanism is btMachine.overrun, which findFrom propagates and every
// exported method turns into Err(). These tests pin that end to end, through
// the public function surface, so a caller that forgets to consult Err() is
// caught here rather than in a user's document.
//
// Two things make that harder than it looks, and both were got wrong here
// before:
//
//   - The backtracking engine is OFF by default. Without
//     SetBacktrackingRegex(true) the pattern below never reaches the
//     backtracker at all: compileBacktrackFallback declines, the RE2/backref
//     path rejects it at COMPILE time, and the test exercises nothing.
//   - That compile-time rejection is *also* reported as FORX0002. So asserting
//     on the code alone cannot tell "the budget ran out" from "this pattern was
//     refused before it ever ran", which is precisely the distinction these
//     tests exist to pin. An earlier version asserted only
//     strings.Contains(err, "FORX0002") and stayed green with the guard deleted.
//
// What separates the two is xdm.ErrResourceLimit, which errBacktrackBudget
// wraps and the compile-time refusals do not. Every assertion below therefore
// checks that sentinel, not just the code.

// pathological is the shape the budget exists for: nested unbounded
// quantification with a backreference, which explores exponentially many
// splits and cannot finish within any workable bound.
const pathological = `(a*)*\1b`

// enableBacktracking turns the backtracking engine on for one test and
// restores the previous setting afterwards. Without it the patterns here are
// refused at compile time and nothing reaches the budget.
func enableBacktracking(t *testing.T) {
	t.Helper()
	prev := BacktrackingRegexEnabled()
	SetBacktrackingRegex(true)
	t.Cleanup(func() { SetBacktrackingRegex(prev) })
}

// wantBudgetError asserts that err is the budget's own error rather than any
// other FORX0002. The sentinel is what makes this test non-vacuous: a
// compile-time rejection carries the same code but not xdm.ErrResourceLimit.
func wantBudgetError(t *testing.T, expr string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s returned a verdict; exhausting the budget must be an error, "+
			"because a false here is indistinguishable from a genuine non-match", expr)
		return
	}
	if got := xdm.ErrorCode(err); got != "FORX0002" {
		t.Errorf("%s: ErrorCode = %q, want FORX0002 (err: %v)", expr, got, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("%s = %v, want the budget error wrapping xdm.ErrResourceLimit; "+
			"a bare FORX0002 here means the pattern was refused before it ran, "+
			"so the budget was never exercised", expr, err)
	}
}

// TestBacktrackBudgetIsAnErrorNotFalse drives fn:matches with a pattern that
// exhausts the budget and asserts the result is FORX0002 rather than the
// "false" a silent guard would produce.
func TestBacktrackBudgetIsAnErrorNotFalse(t *testing.T) {
	enableBacktracking(t)
	input := strings.Repeat("a", 60)
	expr := `matches("` + input + `", "` + pathological + `")`

	wantBudgetError(t, expr, evalErr(t, testDoc, expr))
}

// TestBudgetErrorIsDistinctFromCompileRefusal is the guard against this file's
// own historical failure: the same pattern, with the backtracker OFF, is
// refused at compile time with FORX0002 and no resource-limit sentinel. If the
// two ever became indistinguishable, the assertions above would stop meaning
// anything, so the difference is pinned explicitly.
func TestBudgetErrorIsDistinctFromCompileRefusal(t *testing.T) {
	input := strings.Repeat("a", 60)
	expr := `matches("` + input + `", "` + pathological + `")`

	prev := BacktrackingRegexEnabled()
	SetBacktrackingRegex(false)
	t.Cleanup(func() { SetBacktrackingRegex(prev) })

	err := evalErr(t, testDoc, expr)
	if err == nil {
		t.Fatalf("%s: want a compile-time refusal with the engine off", expr)
	}
	if got := xdm.ErrorCode(err); got != "FORX0002" {
		t.Errorf("compile refusal: ErrorCode = %q, want FORX0002", got)
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("compile refusal %v must NOT wrap xdm.ErrResourceLimit; "+
			"if it does, that sentinel can no longer tell a budget overrun "+
			"from a pattern that never ran", err)
	}
}

// TestBacktrackBudgetErrorReachesEveryFunction checks the other three regex
// functions, since each has its own path out of the engine and each must
// consult Err(). fn:replace and fn:tokenize call MatchString("") first, so the
// budget can be hit on that call rather than on the input.
func TestBacktrackBudgetErrorReachesEveryFunction(t *testing.T) {
	enableBacktracking(t)
	input := strings.Repeat("a", 60)
	for _, expr := range []string{
		`matches("` + input + `", "` + pathological + `")`,
		`replace("` + input + `", "` + pathological + `", "x")`,
		`tokenize("` + input + `", "` + pathological + `")`,
	} {
		wantBudgetError(t, expr, evalErr(t, testDoc, expr))
	}
}

// TestOrdinaryNonMatchIsStillFalse is the other half, and the reason the test
// above is not vacuous: a pattern that simply does not match must return false
// rather than an error. Without this, "everything is FORX0002" would pass.
func TestOrdinaryNonMatchIsStillFalse(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches("abc", "^z")`, "false"},
		{`matches("abc", "^a")`, "true"},
		// A backreference pattern the backtracker handles cheaply: it
		// must give a real verdict, not be swept up by the budget.
		{`matches("abcabc", "^(abc)\1$")`, "true"},
		{`matches("abcabd", "^(abc)\1$")`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestBacktrackOverrunSetsErrAtEveryEntryPoint pins the engine's own contract
// below the function layer: each exported method must set Err() when the
// machine overran, so RegexpErr can be trusted by the XSLT layer too.
func TestBacktrackOverrunSetsErrAtEveryEntryPoint(t *testing.T) {
	bt, err := compileBacktrack(pathological, "", XPath20)
	if err != nil {
		// Not a skip: compileBacktrack is called directly here, so a refusal
		// means the probe pattern no longer reaches the engine and every
		// assertion below has silently stopped testing anything.
		t.Fatalf("compileBacktrack(%q) = %v; the budget probe must compile, "+
			"otherwise this test passes without exercising the guard",
			pathological, err)
	}
	input := strings.Repeat("a", 60)

	t.Run("MatchString", func(t *testing.T) {
		got := bt.MatchString(input)
		if bt.Err() == nil {
			t.Fatalf("MatchString returned %v with a nil Err(); the caller cannot "+
				"tell an exhausted budget from a non-match", got)
		}
		if got {
			t.Errorf("an exhausted match must not report true")
		}
	})

	t.Run("FindAllStringSubmatchIndex", func(t *testing.T) {
		got := bt.FindAllStringSubmatchIndex(input, -1)
		if bt.Err() == nil {
			t.Fatalf("FindAllStringSubmatchIndex returned %v with a nil Err()", got)
		}
	})

	t.Run("RegexpErr", func(t *testing.T) {
		bt.MatchString(input)
		if RegexpErr(bt) == nil {
			t.Errorf("RegexpErr must surface the budget error, since the XSLT " +
				"layer consults only that")
		}
	})
}
