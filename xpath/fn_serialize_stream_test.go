package xpath

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

func streamTestDoc(t *testing.T, n int) *xdm.Node {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<r xmlns:p="urn:p">`)
	for range n {
		b.WriteString(`<p:a p:b="1 &amp; 2">x &lt; y "z"</p:a>`)
	}
	b.WriteString(`</r>`)
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return tree.Root
}

// discardSink is a sinkWriter that keeps nothing, so that what a test counts
// is the serializer's own allocation and not the growth of its output.
type discardSink struct{}

func (discardSink) Write(p []byte) (int, error)       { return len(p), nil }
func (discardSink) WriteString(s string) (int, error) { return len(s), nil }

// Each text node and attribute value was escaped into a string of its own,
// and each prefixed name concatenated, before being written: allocations per
// node. They are now written to the sink in pieces, so the count must not
// grow with the number of nodes, for the node serializer and for a node
// inside a JSON value, which is escaped as it is written.
func TestSerializeNodeAllocsFlat(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*xdm.Node)
	}{
		{"xml", func(n *xdm.Node) {
			serializeNode(&serializeSink{w: discardSink{}}, n, serializeOptions{method: "xml"}, 0)
		}},
		{"json", func(n *xdm.Node) {
			if err := SerializeJSONTo(discardSink{}, xdm.One(n), SerializeParams{}); err != nil {
				t.Fatal(err)
			}
		}},
		{"adaptive", func(n *xdm.Node) {
			if err := SerializeAdaptiveTo(discardSink{}, xdm.One(n), SerializeParams{CharMap: map[rune]string{'z': "Z"}}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		few, many := streamTestDoc(t, 5), streamTestDoc(t, 500)
		a := testing.AllocsPerRun(10, func() { tc.run(few) })
		b := testing.AllocsPerRun(10, func() { tc.run(many) })
		if b > a+5 {
			t.Errorf("%s: 500 nodes %.0f allocations, 5 nodes %.0f; want the same", tc.name, b, a)
		}
	}
}

// The streaming entry points write what the string ones return, and a writer
// without WriteString is buffered rather than refused.
func TestSerializeToMatchesString(t *testing.T) {
	doc := streamTestDoc(t, 3)
	arr := xdm.One(xdm.NewArray(xdm.One(xdm.NewString("a/\"\u0085\xff")), xdm.One(doc)))
	p := SerializeParams{CharMap: map[rune]string{'z': "Zç", 'ÿ': "y"}, Encoding: "US-ASCII"}
	want, err := SerializeJSON(arr, p)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := SerializeJSONTo(struct{ io.Writer }{&got}, arr, p); err != nil {
		t.Fatal(err)
	}
	if got.String() != want {
		t.Errorf("json:\ngot  %q\nwant %q", got.String(), want)
	}
	want, err = SerializeAdaptive(arr, p)
	if err != nil {
		t.Fatal(err)
	}
	got.Reset()
	if err := SerializeAdaptiveTo(struct{ io.Writer }{&got}, arr, p); err != nil {
		t.Fatal(err)
	}
	if got.String() != want {
		t.Errorf("adaptive:\ngot  %q\nwant %q", got.String(), want)
	}
}
