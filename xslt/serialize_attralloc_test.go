package xslt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An html attribute value is escaped a character at a time, and each plain
// character used to cost a one-character string and a builder: a long value
// allocated twice per character. The count must not grow with the value's
// length, and the characters that are escaped still are.
func TestSerializeHTMLAttrAllocsFlat(t *testing.T) {
	page := func(v string) xdm.Sequence {
		tree, err := xdm.ParseString(`<html><body><p class="`+v+`">x</p></body></html>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return xdm.One(tree.Root)
	}
	opts := OutputSettings{Method: "html", Encoding: "UTF-8"}

	var out bytes.Buffer
	if err := Serialize(&out, page("a&quot;b&amp;c&lt;d&gt;e&#x85;&amp;{f}"), opts, nil); err != nil {
		t.Fatal(err)
	}
	if want := `class="a&#34;b&amp;c&lt;d&gt;e&#133;&{f}"`; !strings.Contains(out.String(), want) {
		t.Fatalf("got %s, want it to contain %s", out.String(), want)
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
	short, long := count(page(strings.Repeat("ab", 4))), count(page(strings.Repeat("ab", 400)))
	if long > short+2 {
		t.Errorf("800-character value: %.0f allocations, 8-character value: %.0f; want the same", long, short)
	}
}
