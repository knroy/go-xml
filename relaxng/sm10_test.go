package relaxng

import (
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// SECURITY (run-5). Stacked postfix operators and a datatype except chain
// nested without charging maxCompactDepth, so a long run was a fatal stack
// overflow rather than the depth error. Short runs still parse.
func TestSM10CompactPostfixAndExceptChargeDepth(t *testing.T) {
	const n = 5000
	for name, src := range map[string]string{
		"postfix": "start = empty" + strings.Repeat("?", n),
		"except":  "start = string" + strings.Repeat(" - string", n),
	} {
		if _, err := ParseCompact(src); err == nil || !strings.Contains(err.Error(), "deep") {
			t.Errorf("%s nested %d deep: got %v, want the depth error", name, n, err)
		}
	}
	for _, src := range []string{"start = empty?*+", "start = string - string - string"} {
		if _, err := ParseCompact(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

// choiceWidthCost is the time to compile, and validate one element against,
// a choice of k alternatives no two of which patEq can equate.
func choiceWidthCost(t *testing.T, k int) time.Duration {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"><zeroOrMore><choice>`)
	for range k {
		sb.WriteString(`<element><anyName/><value>x</value></element>`)
	}
	sb.WriteString(`</choice></zeroOrMore></element>`)
	tree, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString(`<r><n>x</n></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		s, err := Compile(tree.Root)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Validate(doc.Root)
		best = min(best, time.Since(start))
	}
	return best
}

// SECURITY (run-5). Each alternative added to a choice was tested against
// every one before it, so compiling a K-way choice and deriving it cost
// O(K²): 0.47 s and 2.7 s at K=8000. Indexed, 8x the width costs about 8x.
func TestSM10ChoiceWidthIsLinear(t *testing.T) {
	small, large := choiceWidthCost(t, 500), choiceWidthCost(t, 4000)
	if r := float64(large) / float64(small); r > 24 {
		t.Fatalf("8x the alternatives cost %.0fx the time (%v vs %v); choice is quadratic again", r, large, small)
	}
}
