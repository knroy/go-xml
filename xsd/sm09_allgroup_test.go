package xsd

import (
	"fmt"
	"strings"
	"testing"
)

// TestSM09SharedAllGroupDAGFlattenedLinearly: g_i = all(g_{i-1}, g_{i-1})
// has 2^n paths to g0. flattenAll inlines each shared group at most twice,
// so the duplicate still reaches the UPA check and the walk stays linear.
func TestSM09SharedAllGroupDAGFlattenedLinearly(t *testing.T) {
	const n = 40
	var sb strings.Builder
	sb.WriteString(`<xs:group name="g0"><xs:all><xs:element name="a"/></xs:all></xs:group>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, `<xs:group name="g%d"><xs:all><xs:group ref="g%d"/><xs:group ref="g%d"/></xs:all></xs:group>`, i, i-1, i-1)
	}
	fmt.Fprintf(&sb, `<xs:complexType name="t"><xs:group ref="g%d"/></xs:complexType>`, n)
	sm09Within(t, "all-group DAG", func() {
		_, err := Load(sm09Schema(t, sb.String()), "", Options{Version: Version11})
		if err == nil || !strings.Contains(err.Error(), "cos-nonambig") {
			t.Errorf("err=%v, want cos-nonambig for the duplicated particle", err)
		}
	})

	// A group referenced once is inlined unchanged.
	s, err := Load(sm09Schema(t, `<xs:group name="g"><xs:all><xs:element name="a"/><xs:element name="b"/></xs:all></xs:group>`+
		`<xs:element name="e"><xs:complexType><xs:all><xs:group ref="g"/><xs:element name="c"/></xs:all></xs:complexType></xs:element>`), "", Options{Version: Version11})
	if err != nil {
		t.Fatal(err)
	}
	for doc, ok := range map[string]bool{"<e><c/><b/><a/></e>": true, "<e><a/><c/></e>": false} {
		if got, msg := validateDoc(t, s, doc); got != ok {
			t.Errorf("%s: valid=%v (%s), want %v", doc, got, msg, ok)
		}
	}
}
