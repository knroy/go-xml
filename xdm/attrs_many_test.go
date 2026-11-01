package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// A tag with several repeated attributes is refused for the first repeat in
// document order, not for the first in sort order (a, here), and an unbound
// prefix after a repeat does not displace it.
func TestManyAttrsReportFirstDuplicateInDocumentOrder(t *testing.T) {
	pad := ""
	for i := range 12 {
		pad += fmt.Sprintf(` x%d="1"`, i)
	}
	for _, tc := range []struct{ tag, want string }{
		{`b="1" a="1" b="2" a="2"`, "duplicate attribute {}b"},
		{`a="1" b="1" a="2" b="2" a="3"`, "duplicate attribute {}a"},
		// p and q bind alike, so p:k and q:k are the same attribute.
		{`q:z="1" p:k="1" a="1" q:k="2" a="2"`, "duplicate attribute {urn:p}k"},
		{`m="1" n="1" m="2" u:k="1"`, "duplicate attribute {}m"},
		{`m="1" u:k="1" n="1" m="2"`, "no namespace declaration is in scope for prefix \"u\""},
	} {
		doc := `<r xmlns:p="urn:p" xmlns:q="urn:p"` + pad + ` ` + tc.tag + `/>`
		_, err := ParseString(doc, ParseOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.tag, err, tc.want)
		}
	}
}

// The name index and the duplicate check's map made a tag of 50000
// distinct attributes allocate over 370 bytes an attribute. The checks are
// a sort of indexes and the index is left out for such a tag.
func TestManyAttrsAllocation(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation sizes are not stable under -race")
	}
	var b strings.Builder
	b.WriteString("<r")
	for i := range 50000 {
		fmt.Fprintf(&b, " a%d=\"v\"", i)
	}
	b.WriteString("/>")
	doc := b.String()
	per := allocated(func() {
		if _, err := ParseString(doc, ParseOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%d bytes allocated for %d bytes of input", per, len(doc))
	if per > 50000*260 {
		t.Errorf("parsing a tag of 50000 attributes allocated %d bytes, %d an attribute, want under 260", per, per/50000)
	}
}

// A tag that adds its names without the index must leave the table as one
// that interned them: a name met again later, in the same tag's wake or in a
// second big tag, is the one already there.
func TestManyAttrsLeaveNamesUnique(t *testing.T) {
	var b strings.Builder
	tag := func(from, to int) {
		for i := from; i < to; i++ {
			fmt.Fprintf(&b, ` a%d="v"`, i)
		}
	}
	b.WriteString("<r")
	tag(0, 300)
	b.WriteString("><c")
	tag(100, 400) // 200 old names, 100 new
	b.WriteString("/><c")
	tag(0, 1000)
	b.WriteString("/><d a5='v'/></r>")
	tree, err := ParseString(b.String(), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[QName]bool{}
	for _, q := range tree.names[1:] {
		if seen[q] {
			t.Errorf("name %+v is in the table twice", q)
		}
		seen[q] = true
	}
	if want := 1000 + 3; len(tree.names)-1 != want { // a0..a999, r, c, d
		t.Errorf("table holds %d names, want %d", len(tree.names)-1, want)
	}
}
