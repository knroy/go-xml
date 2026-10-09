package xdm

import (
	"fmt"
	"testing"

	xml "github.com/knroy/go-xml/v2/internal/xmltok"
)

func TestLexicalIs(t *testing.T) {
	for _, c := range []struct {
		prefix, local, s string
		want             bool
	}{
		{"", "a", "a", true},
		{"", "a", "b", false},
		{"", "a", ":a", false},
		{"p", "a", "p:a", true},
		{"p", "a", "a", false},
		{"p", "a", "q:a", false},
		{"p", "a", "p:b", false},
		{"p", "a", "pa:", false},
		{"p", "a", "p:a:", false},
		{"pp", "a", "p:pa", false},
	} {
		if got := lexicalIs(c.prefix, c.local, c.s); got != c.want {
			t.Errorf("lexicalIs(%q, %q, %q) = %v, want %v", c.prefix, c.local, c.s, got, c.want)
		}
	}
}

// DTD attribute typing compares every declared (element, attribute) pair
// with every element of the document, and built each prefixed name to do
// it: 30% of the CPU of loading the XSD schema for schemas. A short name is
// built in a stack buffer, so the cost shows as an allocation only past 32
// bytes, which is what this test uses; the comparison now builds nothing.
func TestDTDAttributeTypingDoesNotBuildNames(t *testing.T) {
	var types []attDeclaredType
	for i := range 40 {
		types = append(types, attDeclaredType{element: fmt.Sprintf("schema:element-with-a-long-name-%d", i), attr: "identifier-with-a-long-name", typ: "ID"})
	}
	tok := xml.StartElement{
		Name: xml.Name{Space: "schema", Local: "element-with-a-long-name-x"},
		Attr: []xml.Attr{{Name: xml.Name{Space: "prefix", Local: "identifier-with-a-long-name"}, Value: "a"}},
	}
	el := &Node{Kind: KindElement, Name: QName{Prefix: "schema", Local: "element-with-a-long-name-x"}}
	el.AddAttr(&Node{Kind: KindAttribute, Name: QName{Prefix: "prefix", Local: "identifier-with-a-long-name"}, Value: "a"})
	if n := testing.AllocsPerRun(100, func() {
		normalizeAttTokens(tok, types)
		applyAttTypes(el, types)
	}); n != 0 {
		t.Errorf("typing an element against 40 declarations allocated %.0f times, want 0", n)
	}
}
