package relaxng

import (
	"fmt"
	"strconv"
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

// ladderSchema is a grammar whose definitions d_i hold d_{i-1} twice in rung,
// linear in size with 2^n paths through d_n.
func ladderSchema(n int, start, d0, rung string) string {
	var sb strings.Builder
	sb.WriteString(`<grammar xmlns="http://relaxng.org/ns/structure/1.0" ` +
		`datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"><start>` +
		strings.ReplaceAll(start, "NN", strconv.Itoa(n)) + `</start><define name="d0">` + d0 + `</define>`)
	for i := 1; i <= n; i++ {
		r := strings.ReplaceAll(rung, "PP", strconv.Itoa(i-1))
		fmt.Fprintf(&sb, `<define name="d%d">%s</define>`, i, strings.ReplaceAll(r, "II", strconv.Itoa(i)))
	}
	return sb.String() + `</grammar>`
}

// SECURITY (run-5). A shared definition was walked once per path by
// nullable, staticFree and the derivatives: compiling or validating <r/>
// against a 1.5 KB ladder took 0.4 s at N=20 and four times that per two
// rungs. At N=26 each case below took seconds; now each is microseconds.
func TestSM10DefinitionLadderIsLinear(t *testing.T) {
	const n = 26
	opt := `<optional><element name="a"><empty/></element></optional>`
	twice := `<ref name="dPP"/><ref name="dPP"/>`
	cases := []struct{ name, start, d0, rung, inst string }{
		{"validate", `<element name="r"><ref name="dNN"/></element>`, opt, twice, `<r/>`},
		{"compile", `<element name="r"><group><ref name="dNN"/><ref name="dNN"/><ref name="dNN"/></group></element>`, opt, twice, `<r/>`},
		{"list", `<element name="r"><list><ref name="dNN"/></list></element>`, `<optional><data type="token"/></optional>`, twice, `<r>a b</r>`},
		{"attribute", `<element name="r"><ref name="dNN"/></element>`, `<attribute name="a"><value>v</value></attribute>`,
			`<choice><group><ref name="dPP"/><element name="xII"><empty/></element></group>` +
				`<group><element name="yII"><empty/></element><ref name="dPP"/></group></choice>`, `<r a="v"/>`},
	}
	for _, c := range cases {
		tree, err := xdm.ParseString(ladderSchema(n, c.start, c.d0, c.rung), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := xdm.ParseString(c.inst, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		s, err := Compile(tree.Root)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		err = s.Validate(doc.Root)
		if d := time.Since(start); d > 250*time.Millisecond {
			t.Errorf("%s: %v (err %v); a shared definition is walked per path again", c.name, d, err)
		}
		if c.name == "validate" || c.name == "compile" {
			if err != nil {
				t.Errorf("%s: valid <r/> rejected: %v", c.name, err)
			}
		}
	}
}
