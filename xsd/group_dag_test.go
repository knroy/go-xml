package xsd

import (
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

// A schema whose group graph is a wide acyclic DAG must load in time
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
func TestGroupDAGLoadsInGraphTime(t *testing.T) {
	for _, n := range []int{8, 16, 24, 32, 40} {
		src := dagSchema(n)
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("n=%d: parse: %v", n, err)
		}
		start := time.Now()
		if _, err := Load(st.Root, "", Options{}); err != nil {
			t.Fatalf("n=%d: the schema is valid and acyclic, but load failed: %v", n, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("n=%d (%d bytes): load took %v, want graph-proportional time", n, len(src), d)
		}
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
