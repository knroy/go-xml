package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// mg-props-correct.2 names exactly the groups that lie on a cycle, in sorted
// name order, on every load. Group "a" only refers into the b <-> c cycle, so
// it is not circular itself; the ten d/e pairs make map order show.
func TestGroupCyclesReportedOnCycleInNameOrder(t *testing.T) {
	ref := func(name, to string) string {
		return fmt.Sprintf(`<xs:group name="%s"><xs:sequence><xs:group ref="t:%s"/></xs:sequence></xs:group>`, name, to)
	}
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">`)
	b.WriteString(ref("a", "b") + ref("b", "c") + ref("c", "b"))
	var want []string
	want = append(want, "b", "c")
	for i := 0; i < 10; i++ {
		b.WriteString(ref(fmt.Sprintf("d%d", i), fmt.Sprintf("e%d", i)))
		b.WriteString(ref(fmt.Sprintf("e%d", i), fmt.Sprintf("d%d", i)))
	}
	for i := 0; i < 10; i++ {
		want = append(want, fmt.Sprintf("d%d", i))
	}
	for i := 0; i < 10; i++ {
		want = append(want, fmt.Sprintf("e%d", i))
	}
	b.WriteString(`</xs:schema>`)
	st, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 20; run++ {
		_, err := Load(st.Root, "", Options{})
		if err == nil {
			t.Fatal("circular groups accepted")
		}
		var got []string
		for _, line := range strings.Split(err.Error(), "\n") {
			if i := strings.Index(line, `mg-props-correct.2: group "`); i >= 0 {
				rest := line[i+len(`mg-props-correct.2: group "`):]
				got = append(got, rest[:strings.Index(rest, `"`)])
			}
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("run %d: circular groups reported %v, want %v\n%v", run, got, want, err)
		}
	}
}
