package xslt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Each text node was escaped into a builder of its own before being written:
// two allocations per node. The runs that need no escaping are now copied to
// the writer whole, so the count must not grow with the number of text nodes,
// and the characters that are escaped still are.
func TestSerializeTextAllocsFlat(t *testing.T) {
	doc := func(n int) xdm.Sequence {
		var b strings.Builder
		b.WriteString(`<r>`)
		for range n {
			b.WriteString(`<a>x &amp; y &lt; z &gt; ü&#xD;</a>`)
		}
		b.WriteString(`</r>`)
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return xdm.One(tree.Root)
	}
	opts := OutputSettings{Method: "xml", Encoding: "US-ASCII", OmitXMLDecl: true}
	var out bytes.Buffer
	if err := Serialize(&out, doc(1), opts, nil); err != nil {
		t.Fatal(err)
	}
	if want := `<r><a>x &amp; y &lt; z &gt; &#252;&#13;</a></r>`; out.String() != want {
		t.Fatalf("got  %s\nwant %s", out.String(), want)
	}

	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	count := func(seq xdm.Sequence) float64 {
		var buf bytes.Buffer
		return testing.AllocsPerRun(20, func() {
			buf.Reset()
			if err := Serialize(&buf, seq, opts, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	few, many := count(doc(5)), count(doc(500))
	if many > few+5 {
		t.Errorf("500 text nodes: %.0f allocations, 5 text nodes: %.0f; want the same", many, few)
	}
}

// Text is now written in pieces, so a character the method cannot output is
// found before any of the node is written: the node fails whole, as it did
// when it was escaped into a builder first, and the first such character is
// the one the error names.
func TestSerializeTextErrorWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		opts OutputSettings
		text string
		want string
	}{
		{OutputSettings{Method: "xml", OmitXMLDecl: true}, "ok\x02\x01", "SERE0006: character #x2 "},
		{OutputSettings{Method: "html", HTMLVersion: "4.0"}, "ok\u0085\u0086", "SERE0014: character #x85 "},
	} {
		var out bytes.Buffer
		err := Serialize(&out, xdm.Sequence{xdm.NewString(tc.text)}, tc.opts, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.opts.Method, err, tc.want)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q before failing, want nothing", tc.opts.Method, out.String())
		}
	}
}
