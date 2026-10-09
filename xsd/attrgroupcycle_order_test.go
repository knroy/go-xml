package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// src-attribute_group.3 names exactly the attribute groups that reach
// themselves, in sorted name order, on every load. "a" only refers into the
// b <-> c cycle, so it is not circular; the ten d/e pairs make map order show.
func TestAttributeGroupCyclesReportedOnCycleInNameOrder(t *testing.T) {
	ref := func(name, to string) string {
		return fmt.Sprintf(`<xs:attributeGroup name="%s"><xs:attributeGroup ref="t:%s"/></xs:attributeGroup>`, name, to)
	}
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">`)
	b.WriteString(ref("a", "b") + ref("b", "c") + ref("c", "b"))
	want := []string{"b", "c"}
	for i := 0; i < 10; i++ {
		b.WriteString(ref(fmt.Sprintf("d%d", i), fmt.Sprintf("e%d", i)))
		b.WriteString(ref(fmt.Sprintf("e%d", i), fmt.Sprintf("d%d", i)))
	}
	for _, p := range "de" {
		for i := 0; i < 10; i++ {
			want = append(want, fmt.Sprintf("%c%d", p, i))
		}
	}
	b.WriteString(`</xs:schema>`)
	st, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const marker = `src-attribute_group.3: attribute group "`
	for run := 0; run < 20; run++ {
		_, err := Load(st.Root, "", Options{})
		if err == nil {
			t.Fatal("circular attribute groups accepted")
		}
		var got []string
		for _, line := range strings.Split(err.Error(), "\n") {
			if i := strings.Index(line, marker); i >= 0 {
				rest := line[i+len(marker):]
				got = append(got, rest[:strings.Index(rest, `"`)])
			}
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("run %d: circular groups reported %v, want %v\n%v", run, got, want, err)
		}
	}
}
