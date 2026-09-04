package xpath

import (
	"strings"
	"testing"
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

// pathological is the shape the budget exists for: nested unbounded
// quantification with a backreference, which explores exponentially many
// splits and cannot finish within any workable bound.
const pathological = `(a*)*\1b`

// TestBacktrackBudgetIsAnErrorNotFalse drives fn:matches with a pattern that
// exhausts the budget and asserts the result is FORX0002 rather than the
// "false" a silent guard would produce.
func TestBacktrackBudgetIsAnErrorNotFalse(t *testing.T) {
	input := strings.Repeat("a", 60)
	expr := `matches("` + input + `", "` + pathological + `")`

	err := evalErr(t, testDoc, expr)
	if err == nil {
		t.Fatalf("%s returned a verdict; exhausting the budget must be an error, "+
			"because a false here is indistinguishable from a genuine non-match", expr)
	}
	if !strings.Contains(err.Error(), "FORX0002") {
		t.Errorf("%s = %v, want FORX0002", expr, err)
	}
}

// TestBacktrackBudgetErrorReachesEveryFunction checks the other three regex
// functions, since each has its own path out of the engine and each must
// consult Err(). fn:replace and fn:tokenize call MatchString("") first, so the
// budget can be hit on that call rather than on the input.
func TestBacktrackBudgetErrorReachesEveryFunction(t *testing.T) {
	input := strings.Repeat("a", 60)
	for _, expr := range []string{
		`matches("` + input + `", "` + pathological + `")`,
		`replace("` + input + `", "` + pathological + `", "x")`,
		`tokenize("` + input + `", "` + pathological + `")`,
	} {
		err := evalErr(t, testDoc, expr)
		if err == nil {
			t.Errorf("%s returned a verdict rather than failing; "+
				"a budget-exhausted match must not be reported as a result", expr)
			continue
		}
		if !strings.Contains(err.Error(), "FORX0002") {
			t.Errorf("%s = %v, want FORX0002", expr, err)
		}
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
		t.Skipf("this pattern is refused at compile time here: %v", err)
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
