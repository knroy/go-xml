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

// A non-ASCII attribute character under an encoding spelled "UTF-8" asked
// representable for the encoding lower-cased, which allocated a string per
// character, and then escaped it as a one-character string. It is written
// unchanged, so the count must not grow with the value; a character the
// encoding cannot hold is still a reference.
func TestSerializeHTMLAttrWideAllocsFlat(t *testing.T) {
	page := func(v string) xdm.Sequence {
		tree, err := xdm.ParseString(`<html><body><p title="`+v+`">x</p></body></html>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return xdm.One(tree.Root)
	}
	var out bytes.Buffer
	if err := Serialize(&out, page("Grüße €"), OutputSettings{Method: "html", Encoding: "ISO-8859-1"}, nil); err != nil {
		t.Fatal(err)
	}
	if want := `title="Grüße&#8232;&#8364;"`; !strings.Contains(out.String(), want) {
		t.Fatalf("got %q, want it to contain %q", out.String(), want)
	}

	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	opts := OutputSettings{Method: "html", Encoding: "UTF-8"}
	count := func(seq xdm.Sequence) float64 {
		var buf bytes.Buffer
		return testing.AllocsPerRun(20, func() {
			buf.Reset()
			if err := Serialize(&buf, seq, opts, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	short, long := count(page(strings.Repeat("ü€", 4))), count(page(strings.Repeat("ü€", 400)))
	if long > short+2 {
		t.Errorf("800-character value: %.0f allocations, 8-character value: %.0f; want the same", long, short)
	}
}

// Each element allocated its prefixed name, a map of the bindings it
// declared and, when it had children, a copy of every binding in scope; each
// namespace declaration and attribute was concatenated before being written.
// Elements and attributes that need no escaping now cost nothing per
// element, so the count must not grow with the number of them -- and the
// bindings must still be declared once, undeclared where needed, and
// redeclared after their scope has closed.
func TestSerializeElementAllocsFlat(t *testing.T) {
	doc := func(n int) xdm.Sequence {
		var b strings.Builder
		b.WriteString(`<p:r xmlns:p="urn:p" xmlns="urn:d">`)
		for range n {
			b.WriteString(`<p:a p:k="v"><q:b xmlns:q="urn:q" q:k="v"><c xmlns=""><d/></c></q:b></p:a>`)
		}
		b.WriteString(`</p:r>`)
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return xdm.One(tree.Root)
	}
	opts := OutputSettings{Method: "xml", Encoding: "UTF-8", OmitXMLDecl: true}
	var out bytes.Buffer
	if err := Serialize(&out, doc(2), opts, nil); err != nil {
		t.Fatal(err)
	}
	one := `<p:a p:k="v"><q:b xmlns:q="urn:q" q:k="v"><c xmlns=""><d/></c></q:b></p:a>`
	if want := `<p:r xmlns:p="urn:p" xmlns="urn:d">` + one + one + `</p:r>`; out.String() != want {
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
		t.Errorf("2000 elements: %.0f allocations, 20 elements: %.0f; want the same", many, few)
	}
}
