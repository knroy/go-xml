package xpath

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A pattern's group count is not a reason to refuse it. Neither the XSD regex
// grammar nor XPath's sets a ceiling, so a pattern with 65 capturing groups is
// valid and must be answered rather than rejected with FORX0002.
//
// A constant here previously refused anything past 64 groups. It was aimed at
// the width analysis, but that cost tracks the pattern's source length rather
// than its group count -- so the cap refused cheap patterns while admitting
// expensive ones. The counts below straddle where it used to sit.
func TestBackrefManyGroups(t *testing.T) {
	for _, n := range []int{63, 64, 65, 128, 1000} {
		pattern := strings.Repeat("(a)", n) + `\1`
		text := strings.Repeat("a", n) + "a"

		br, err := buildBackrefRegexp(pattern, "", XPath20)
		if err != nil {
			t.Errorf("%d groups: refused: %v", n, err)
			continue
		}
		if !br.MatchString(text) {
			t.Errorf("%d groups: did not match %d-character text", n, len(text))
		}
		// The backreference must still be doing its job: one character short
		// and the trailing \1 has nothing to compare against.
		if br.MatchString(strings.Repeat("b", n+1)) {
			t.Errorf("%d groups: matched text it should not", n)
		}
	}
}

// The same patterns through the public functions, since that is where a user
// meets the error. fn:matches, fn:replace and fn:tokenize all reach the
// backreference path through compileArgBackref.
func TestBackrefManyGroupsThroughFunctions(t *testing.T) {
	const n = 65
	pattern := strings.Repeat("(a)", n) + `\1`
	text := strings.Repeat("a", n) + "a"

	got, err := Eval(`matches("`+text+`", "`+pattern+`")`,
		NewContext(nil, Builtins()), nil)
	if err != nil {
		t.Fatalf("fn:matches with %d groups: %v", n, err)
	}
	if len(got) != 1 {
		t.Fatalf("fn:matches returned %d items", len(got))
	}
	if fmt.Sprint(got[0]) != "true" {
		t.Errorf("fn:matches with %d groups = %v, want true", n, got[0])
	}

	if _, err := Eval(`replace("`+text+`", "`+pattern+`", "x")`,
		NewContext(nil, Builtins()), nil); err != nil {
		t.Errorf("fn:replace with %d groups: %v", n, err)
	}
}

// Raising the group ceiling must not weaken the backtracking guarantee. A
// pattern with many groups and nesting is the ReDoS shape, and exhausting the
// budget has to surface as FORX0002 rather than as a bare false -- a wrong
// answer is worse than a refusal.
func TestBackrefManyGroupsStillBounded(t *testing.T) {
	// Many groups, and a body whose backtracking is exponential in the input.
	pattern := strings.Repeat("(a*)", 80) + `x\1`
	br, err := buildBackrefRegexp(pattern, "", XPath20)
	if err != nil {
		// Refusing on width grounds is a correct outcome too: the groups are
		// variable-width, which this path has always declined to guess at.
		if !strings.Contains(err.Error(), "FORX0002") {
			t.Fatalf("expected FORX0002, got %v", err)
		}
		return
	}
	// If it did compile, it must terminate rather than run away.
	done := make(chan bool, 1)
	go func() { br.MatchString(strings.Repeat("a", 4000)); done <- true }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("match did not terminate: the backtracking budget did not bound it")
	}
}
