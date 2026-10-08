package xdm

import (
	"io"
	"strings"
	"testing"
)

// chunkReader hands out at most n bytes per Read, so that the normalizer is
// exercised across every place a read boundary can fall. A delimiter split
// across two reads is the failure mode this filter is most exposed to.
type chunkReader struct {
	s    string
	i, n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.i >= len(c.s) {
		return 0, io.EOF
	}
	k := c.n
	if k > len(p) {
		k = len(p)
	}
	if c.i+k > len(c.s) {
		k = len(c.s) - c.i
	}
	copy(p, c.s[c.i:c.i+k])
	c.i += k
	return k, nil
}

// attValues parses src, read n bytes at a time, and returns the attribute
// values of every element in document order, joined by "|".
func attValues(t *testing.T, src string, n int) string {
	t.Helper()
	tree, err := Parse(&chunkReader{s: src, n: n}, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	var vals []string
	var walk func(*Node)
	walk = func(n *Node) {
		for _, a := range n.Attrs {
			vals = append(vals, a.Value)
		}
		for _, c := range n.ChildElements() {
			walk(c)
		}
	}
	walk(tree.Root)
	return strings.Join(vals, "|")
}

// TestAttributeValueNormalization is XML 1.0 section 3.3.3 as the tokeniser
// applies it: a literal TAB, LF or CR in an attribute value becomes one
// space, a character reference to one does not, and nothing outside a value
// is touched. Every read size from 1 to 20 bytes is tried, so that a value,
// a CR-LF or a delimiter split across reads is covered.
func TestAttributeValueNormalization(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"newline", "<a s=\"x\ny\"/>", "x y"},
		{"tab", "<a s=\"x\ty\"/>", "x y"},
		{"cr lf is one space", "<a s=\"x\r\ny\"/>", "x y"},
		{"lone cr", "<a s=\"x\ry\"/>", "x y"},
		{"single quotes", "<a s='x\ny'/>", "x y"},
		{"char ref survives", "<a s=\"x&#10;y\"/>", "x\ny"},
		{"hex char ref survives", "<a s=\"x&#xA;y&#9;&#xD;\"/>", "x\ny\t\r"},
		{"between attributes", "<a\ns=\"1\"\nt=\"2\"/>", "1|2"},
		{"comment", "<!-- a\nb --><c s=\"m\nn\"/>", "m n"},
		{"comment with quote", "<!-- \" --><c s=\"m\nn\"/>", "m n"},
		{"cdata", "<r><a><![CDATA[q\"\nz]]></a><b s=\"m\nn\"/></r>", "m n"},
		{"pi", "<?pi x=\"a\nb\"?><c s=\"m\nn\"/>", "m n"},
		{"internal subset", "<!DOCTYPE r [<!ENTITY e \"a\nb\">]><r s=\"m\nn\"/>", "m n"},
		{"system id holding gt", "<!DOCTYPE r SYSTEM \"a>b\"><r s=\"m\nn\"/>", "m n"},
		// The byte pre-pass this replaced lost its place on these: a quote
		// or "[" in a comment in the internal subset left every later
		// attribute value unnormalized.
		{"subset comment with apostrophe", "<!DOCTYPE r [<!-- don't -->]><r s=\"m\nn\"/>", "m n"},
		{"subset comment with bracket", "<!DOCTYPE r [<!-- [ -->]><r s=\"m\nn\"/>", "m n"},
		{"utf-8 neighbour", "<a s=\"é\ny\"/>", "é y"},
		// XML 1.1 §2.11 makes NEL, U+2028 and CR NEL line ends, folded
		// before §3.3.3 applies, so each is one space. Under 1.0 NEL and
		// U+2028 are ordinary characters; the CR before a NEL is a line end.
		{"1.1 nel", "<?xml version=\"1.1\"?><a s=\"1\r\u00852\" t=\"1\u00852\" u=\"1\u20282\" v=\"1\r\n\u00852\"/>", "1 2|1 2|1 2|1  2"},
		{"1.1 nel by reference", "<?xml version=\"1.1\"?><a s=\"1&#x85;2&#x2028;3\"/>", "1\u00852\u20283"},
		{"1.0 nel", "<a s=\"1\r\u00852\" t=\"1\u00852\u20283\"/>", "1 \u00852|1\u00852\u20283"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n := 1; n <= 20; n++ {
				if got := attValues(t, c.src, n); got != c.want {
					t.Fatalf("read size %d: got %q, want %q", n, got, c.want)
				}
			}
			if got := attValues(t, c.src, len(c.src)+1); got != c.want {
				t.Fatalf("whole: got %q, want %q", got, c.want)
			}
		})
	}
}

// TestPositionsCountAttributeNewlines: the newlines in a multi-line attribute
// value are lines of the source, so a node after one is reported on its real
// line. The byte pre-pass that used to normalize values rewrote them in the
// retained source too, and reported <b/> below on line 2.
func TestPositionsCountAttributeNewlines(t *testing.T) {
	tree, err := ParseString("<a x=\"1\n2\n3\">\n<b/></a>", ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	line, col, ok := tree.Root.ChildElements()[0].ChildElements()[0].Position()
	if !ok || line != 4 || col != 1 {
		t.Fatalf("got line %d col %d ok %v, want line 4 col 1", line, col, ok)
	}
}

// TestParseNormalizesAttributeValues checks the rewrite through the parser,
// which is where it has to hold: this is what boolean-082 in the XSLT suite
// asserts, a style attribute wrapped across two lines.
func TestParseNormalizesAttributeValues(t *testing.T) {
	tree, err := ParseString("<td style=\"color: #336699; font-weight:\nbold\"/>",
		ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	el := tree.Root.ChildElements()[0]
	if got, want := el.Attrs[0].Value, "color: #336699; font-weight: bold"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// The reference is not normalized, which is the half a naive
	// strings.Replace on the decoded value would get wrong.
	tree, err = ParseString("<a s=\"x&#10;y\"/>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].Attrs[0].Value; got != "x\ny" {
		t.Fatalf("character reference not preserved: got %q", got)
	}
}

// TestEntityReplacementInAttributeValue is XML 1.0 §3.3.3's recursion: an
// entity reference in an attribute value is replaced by its replacement text
// normalized the same way, so the literal white space in that text becomes a
// space, while a character the replacement text holds as a reference is kept.
// The rows are the section's own example, whose declarations put a CR and an
// LF into replacement text through references in the entity value, and
// Appendix D's "&#38;#..." form, which leaves a reference in replacement text.
// Each runs on both entity paths: dec.Entity, and the re-parse a markup
// entity forces.
func TestEntityReplacementInAttributeValue(t *testing.T) {
	const decls = `<!ENTITY d "&#xD;"><!ENTITY a "&#xA;"><!ENTITY da "&#xD;&#xA;">` +
		`<!ENTITY ref "&#38;#xA;"><!ENTITY amp2 "&#38;#38;"><!ENTITY n "x&a;y">` +
		"<!ENTITY lit \"p\tq\nr\">"
	cases := []struct{ name, attr, want string }{
		{"literal line ends", "\n\nxyz", "  xyz"},
		{"section 3.3.3 entities", "&d;&d;A&a;&#x20;&a;B&da;", "  A   B  "},
		{"section 3.3.3 references", "&#xd;&#xd;A&#xa;&#xa;B&#xd;&#xa;", "\r\rA\n\nB\r\n"},
		{"reference in replacement text", "&ref;", "\n"},
		{"appendix D ampersand", "&amp2;", "&"},
		{"nested", "&n;", "x y"},
		{"literal white space in entity value", "&lit;", "p q r"},
	}
	for _, markup := range []bool{false, true} {
		extra := ""
		if markup {
			extra = `<!ENTITY m "<b/>">`
		}
		for _, c := range cases {
			src := "<!DOCTYPE r [" + decls + extra + "]><r v=\"" + c.attr + "\">&a;</r>"
			tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
			if err != nil {
				t.Fatalf("%s (markup path %v): %v", c.name, markup, err)
			}
			r := tree.Root.ChildElements()[0]
			if got := r.Attrs[0].Value; got != c.want {
				t.Errorf("%s (markup path %v): got %q, want %q", c.name, markup, got, c.want)
			}
			// In content the replacement text is not normalized.
			if got := r.StringValue(); got != "\n" {
				t.Errorf("%s (markup path %v): content %q, want %q", c.name, markup, got, "\n")
			}
		}
	}
}
