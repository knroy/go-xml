package xsd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// dagSchema builds n group definitions where each references the next TWICE.
// The graph is acyclic and the schema is valid, but it has 2^(n-1) distinct
// root-to-leaf paths. A walker that explores paths rather than the graph is
// exponential in n while the schema itself stays a few kilobytes.
func dagSchema(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">`)
	for i := 0; i < n; i++ {
		b.WriteString(fmt.Sprintf(`<xs:group name="g%d"><xs:sequence>`, i))
		if i == n-1 {
			b.WriteString(`<xs:element name="leaf" type="xs:string"/>`)
		} else {
			b.WriteString(fmt.Sprintf(`<xs:group ref="t:g%d"/><xs:group ref="t:g%d"/>`, i+1, i+1))
		}
		b.WriteString(`</xs:sequence></xs:group>`)
	}
	b.WriteString(`<xs:complexType name="C"><xs:sequence><xs:group ref="t:g0"/></xs:sequence></xs:complexType>`)
	b.WriteString(`<xs:element name="root" type="t:C"/></xs:schema>`)
	return b.String()
}

// A schema whose group graph is a wide acyclic DAG must be DECIDED in time
// proportional to the graph, not to the number of paths through it.
//
// checkGroupCycles and checkAllGroupLimited both walked paths: cycleFrom kept
// only the current descent, so a group reachable by k routes was explored k
// times, and badNestedAll had no memo at all. Together they made a 3.0 KB
// schema of 29 doubly-referencing groups take 35.8 seconds to load, 86% of it
// in the first and 8% in the second. Both memoise now.
//
// n=40 has 2^39 paths — over five hundred billion. Before the fix this did not
// finish; the whole point of the bound below is that it now returns promptly.
//
// PROMPTLY, not successfully. This test asserted a successful load at every n
// up to 40, and that promise was never satisfiable: the model has 2^(n-1)
// positions at ~400 bytes each, so n=40 is some 220 GB. It passed only because
// a compile failure was silently skipped, which is the false accept fixed in
// checkContentModelConstraints — the schema "loaded" precisely because none of
// its content-model constraints had been checked. Restoring honesty there made
// the unsatisfiable half of this assertion visible.
//
// So the two halves are now separated. The timing bound is what this test was
// written for and it applies at EVERY n: path-walking cycle detection would
// blow up on the graph long before the automaton is ever built, and it is the
// regression that matters. The verdict is asserted only where it is
// meaningful — a model within the position budget must genuinely load, and one
// beyond it must be refused as a resource limit rather than accepted unchecked.
func TestGroupDAGLoadsInGraphTime(t *testing.T) {
	for _, n := range []int{8, 16, 24, 32, 40} {
		src := dagSchema(n)
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("n=%d: parse: %v", n, err)
		}
		// The model this schema expands to, against the budget that
		// decides whether it can be built at all.
		positions := 1 << (n - 1)
		fits := positions <= DefaultMaxContentModelPositions

		start := time.Now()
		_, lerr := Load(st.Root, "", Options{})
		d := time.Since(start)

		// The guarantee this test exists for: graph-proportional, at
		// every n, whether the answer is acceptance or refusal.
		if d > 5*time.Second {
			t.Errorf("n=%d (%d bytes): load took %v, want graph-proportional time",
				n, len(src), d)
		}
		switch {
		case fits && lerr != nil:
			t.Errorf("n=%d: the schema is valid, acyclic and fits the position "+
				"budget (%d <= %d), but load failed: %v",
				n, positions, DefaultMaxContentModelPositions, lerr)
		case !fits && lerr == nil:
			t.Errorf("n=%d: the model needs %d positions, over the budget of %d, "+
				"so its content-model constraints could not be checked — yet the "+
				"schema loaded clean. A declined check must never read as a pass.",
				n, positions, DefaultMaxContentModelPositions)
		case !fits && !errors.Is(lerr, xdm.ErrResourceLimit):
			t.Errorf("n=%d: refused, but not as a resource limit: %v. A caller "+
				"cannot tell \"too large to check\" from \"your schema is invalid\"",
				n, lerr)
		}
	}
}

// TestGroupDAGLoadsWhenTheBudgetIsRaised is the other half of the same story,
// and it is what makes Options.MaxContentModelPositions more than decoration.
//
// The n=16 DAG is a valid, acyclic 1.7 KB schema needing 32,768 positions. At
// the default budget it is refused; raised past its size it loads and is fully
// checked. The refusal is therefore a policy the host sets, not a verdict on
// the schema — which is exactly the distinction the resource-limit sentinel
// exists to carry.
func TestGroupDAGLoadsWhenTheBudgetIsRaised(t *testing.T) {
	st, err := xdm.ParseString(dagSchema(16), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Load(st.Root, "", Options{}); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("at the default budget the n=16 DAG must be refused as a "+
			"resource limit, got %v", err)
	}
	if _, err := Load(st.Root, "", Options{MaxContentModelPositions: 1 << 17}); err != nil {
		t.Errorf("MaxContentModelPositions=%d admits the 32,768-position model, "+
			"so the schema must load: %v", 1<<17, err)
	}
}

// The memoisation must not cost cycle detection: a group that reaches itself
// is still circular, whether directly, through an intermediate, or when it
// also sits in a DAG that the memo has already marked explored.
func TestGroupCyclesStillDetected(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"self", `<xs:group name="a"><xs:sequence><xs:group ref="t:a"/></xs:sequence></xs:group>`},
		{"mutual", `<xs:group name="a"><xs:sequence><xs:group ref="t:b"/></xs:sequence></xs:group>` +
			`<xs:group name="b"><xs:sequence><xs:group ref="t:a"/></xs:sequence></xs:group>`},
		{"three-hop", `<xs:group name="a"><xs:sequence><xs:group ref="t:b"/></xs:sequence></xs:group>` +
			`<xs:group name="b"><xs:sequence><xs:group ref="t:c"/></xs:sequence></xs:group>` +
			`<xs:group name="c"><xs:sequence><xs:group ref="t:a"/></xs:sequence></xs:group>`},
	} {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">` +
			tc.body + `</xs:schema>`
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		if _, err := Load(st.Root, "", Options{}); err == nil {
			t.Errorf("%s: circular group accepted, want mg-props-correct.2", tc.name)
		}
	}
}
