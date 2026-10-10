package xdm

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestNodePositions(t *testing.T) {
	src := "<r>\n  <a/>\n  <b>\n    <c/>\n  </b>\n</r>\n"
	tree, err := ParseString(src, ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int{
		"r": {1, 1}, "a": {2, 3}, "b": {3, 3}, "c": {4, 5},
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Kind() == KindElement {
			line, col, ok := n.Position()
			w, known := want[n.Name().Local]
			if !known {
				t.Errorf("unexpected element %q", n.Name().Local)
			} else if !ok || line != w[0] || col != w[1] {
				t.Errorf("%s at line %d col %d (ok=%v), want line %d col %d",
					n.Name().Local, line, col, ok, w[0], w[1])
			}
			delete(want, n.Name().Local)
		}
		for _, ch := range kids(n) {
			walk(ch)
		}
	}
	walk(tree.Root)
	for name := range want {
		t.Errorf("element %q was never visited", name)
	}
}

// Without TrackPositions the position must be reported as unknown. Returning
// line 1 would be indistinguishable from a real answer, and a report naming
// line 1 for every failure is worse than one naming none.
func TestPositionsUnknownWithoutTracking(t *testing.T) {
	tree, err := ParseString("<r>\n  <a/>\n</r>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := tree.Root.ChildElements()[0].Position(); ok {
		t.Error("a position was reported for a document parsed without TrackPositions")
	}
}

// A node built by a transform rather than parsed has no source position, and
// the zero value of the offset field must read as unknown rather than as the
// start of the document.
func TestConstructedNodeHasNoPosition(t *testing.T) {
	n := NewNode(KindElement, QName{Local: "made-up"}, "")
	if _, _, ok := n.Position(); ok {
		t.Error("a constructed node reported a source position")
	}
	// A nil node is asked about whenever an accessor is given an empty
	// sequence, so it must answer rather than panic.
	var nilNode *Node
	if _, _, ok := nilNode.Position(); ok {
		t.Error("a nil node reported a source position")
	}
}

// CRLF line endings and a leading blank line must not shift the count.
func TestPositionsWithCRLF(t *testing.T) {
	src := "<r>\r\n\r\n  <a/>\r\n</r>"
	tree, err := ParseString(src, ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	// ChildElements skips the whitespace text nodes between the elements.
	line, col, ok := tree.Root.ChildElements()[0].ChildElements()[0].Position()
	if !ok || line != 3 || col != 3 {
		t.Errorf("<a/> at line %d col %d (ok=%v), want line 3 col 3", line, col, ok)
	}
}

// TestParseStringPositionsMatchParse pins that ParseString, which keeps the
// caller's string as the position source when decoding would not change it,
// reports what Parse over a plain reader does, which keeps a decoded copy.
func TestParseStringPositionsMatchParse(t *testing.T) {
	utf16 := func(s string) string {
		b := []byte{0xFF, 0xFE}
		for _, r := range s {
			b = append(b, byte(r), 0)
		}
		return string(b)
	}
	for _, src := range []string{
		"<r>\n  <a/>\n  <b>\n    <c/>\n  </b>\n</r>\n",
		"\n <?xml-stylesheet x?>\n<r>\r\n  <a/>\r\n</r>",
		"\xEF\xBB\xBF<r>\n  <a/></r>",
		utf16("<r>\n  <a/>\n</r>"),
		"<!DOCTYPE r [<!ENTITY e \"<x/>\n<y/>\">]>\n<r>\n  &e;\n  <a/>\n</r>",
	} {
		opts := ParseOptions{TrackPositions: true, AllowDOCTYPE: true}
		want, err := Parse(io.MultiReader(strings.NewReader(src)), opts)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		got, err := ParseString(src, opts)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if p, q := positions(want.Root), positions(got.Root); p != q || !strings.Contains(p, ":") {
			t.Errorf("%q:\n  Parse       %s\n  ParseString %s", src, p, q)
		}
	}
}

func positions(n *Node) string {
	var b strings.Builder
	if n.Kind() == KindElement {
		l, c, ok := n.Position()
		fmt.Fprintf(&b, "%s@%d:%d:%v ", n.Name().Local, l, c, ok)
	}
	for _, ch := range kids(n) {
		b.WriteString(positions(ch))
	}
	return b.String()
}
