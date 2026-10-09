package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// The name cache in front of the intern table must not confuse names that
// hash alike: aXa and aYa share length and end characters, so they share a
// nameHot slot, and the parse alternates them past the small-table limit.
func TestInternHotCacheKeepsCollidingNamesApart(t *testing.T) {
	var sb strings.Builder
	var want []string
	sb.WriteString("<r>")
	for i := 0; i < 3*smallNames; i++ {
		n := fmt.Sprintf("a%ca", 'A'+i%20)
		want = append(want, n)
		fmt.Fprintf(&sb, "<%s %s='1' p:%s='2' xmlns:p='urn:%d'/>", n, n, n, i%2)
	}
	sb.WriteString("</r>")
	if nameHash(QName{Local: "aXa"}) != nameHash(QName{Local: "aYa"}) {
		t.Fatal("test premise: aXa and aYa should hash alike")
	}
	tree, err := ParseString(sb.String(), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	for el := range tree.Root.FirstChild().Children() {
		if el.Name().Local != want[i] {
			t.Fatalf("element %d is %s, want %s", i, el.Name().Local, want[i])
		}
		a, p := el.AttrAt(0).Name(), el.AttrAt(1).Name()
		if a.Local != want[i] || a.URI != "" || p.Local != want[i] || p.URI != fmt.Sprintf("urn:%d", i%2) {
			t.Fatalf("element %d attributes %v %v", i, a, p)
		}
		i++
	}
}

// A tree with no namespace declarations resolves every prefix-less name to
// no namespace without walking ancestors; one with declarations still walks.
func TestResolvePrefixWithAndWithoutDeclarations(t *testing.T) {
	plain, err := ParseString(`<a><b c="1"><d/></b></a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.frames != nil {
		t.Fatal("test premise: a tree without declarations has no frames")
	}
	for n := range plain.Root.Descendants() {
		if n.Name().URI != "" {
			t.Errorf("%s in namespace %q", n.Name().Local, n.Name().URI)
		}
	}
	ns, err := ParseString(`<a xmlns="urn:d" xmlns:p="urn:p"><b p:c="1"><p:d/><e xmlns=""/></b></a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for n := range ns.Root.Descendants() {
		got = append(got, n.Name().Clark())
		for a := range n.Attrs() {
			got = append(got, "@"+a.Name().Clark())
		}
	}
	want := "{urn:d}a {urn:d}b @{urn:p}c {urn:p}d e"
	if strings.Join(got, " ") != want {
		t.Errorf("names %q, want %q", strings.Join(got, " "), want)
	}
}
