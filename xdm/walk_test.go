package xdm

import (
	"fmt"
	"strings"
	"testing"
)

const walkSrc = `<a xmlns:p="urn:p">t1<b>t2<c/>t3</b><!--x--><?pi?><p:d k="v"><e/></p:d>t4<f/></a>`

// label names a visited node: "#doc" for the document, "{uri}local" for a
// namespaced element, the local name otherwise.
func label(n *Node) string {
	switch {
	case n.Kind() == KindDocument:
		return "#doc"
	case n.Name().URI != "":
		return "{" + n.Name().URI + "}" + n.Name().Local
	}
	return n.Name().Local
}

// Walk visits n whatever its kind, then descendant elements only, in
// document order; false stops the whole walk, not just the subtree.
func TestWalk(t *testing.T) {
	tree, err := ParseString(walkSrc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc := tree.Root
	a := kids(doc)[0]
	for _, tc := range []struct {
		name   string
		start  *Node
		stopAt string // label whose visit returns false; "" never stops
		want   string
	}{
		{"document node", doc, "", "#doc a b c {urn:p}d e f"},
		{"element", a, "", "a b c {urn:p}d e f"},
		{"subtree", a.ChildElements()[1], "", "{urn:p}d e"},
		{"leaf", a.ChildElements()[2], "", "f"},
		{"stop at start", doc, "#doc", "#doc"},
		{"stop is not a subtree skip", doc, "b", "#doc a b"},
		{"stop deep", doc, "e", "#doc a b c {urn:p}d e"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			tc.start.Walk(func(n *Node) bool {
				got = append(got, label(n))
				return label(n) != tc.stopAt
			})
			if s := strings.Join(got, " "); s != tc.want {
				t.Errorf("got %q, want %q", s, tc.want)
			}
		})
	}
}

func TestFirstElement(t *testing.T) {
	tree, err := ParseString(`<r xmlns:p="urn:p"><x id="1"><p:y id="2"/></x><y id="3"/><p:y id="4"/></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc := tree.Root
	r := kids(doc)[0]
	for _, tc := range []struct {
		name       string
		start      *Node
		uri, local string
		want       string // id of the match, "self" for n itself, "" for nil
	}{
		{"nested before later sibling", doc, "urn:p", "y", "2"},
		{"no namespace", doc, "", "y", "3"},
		{"namespace mismatch", doc, "urn:q", "y", ""},
		{"no match", doc, "", "z", ""},
		{"n itself", r, "", "r", "self"},
		{"document never matches", doc, "", "", ""},
		{"scoped to subtree", r.ChildElements()[0], "", "y", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.start.FirstElement(tc.uri, tc.local)
			var s string
			switch {
			case got == tc.start:
				s = "self"
			case got != nil:
				s = got.AttrValue("id")
			}
			if s != tc.want {
				t.Errorf("got %q, want %q", s, tc.want)
			}
		})
	}
}

func ExampleNode_FirstElement() {
	tree, _ := ParseString(`<doc xmlns:p="urn:p"><head/><body><p:item>one</p:item></body></doc>`, ParseOptions{})
	item := tree.Root.FirstElement("urn:p", "item")
	fmt.Println(item.Name().Local, item.StringValue())
	// Output: item one
}
