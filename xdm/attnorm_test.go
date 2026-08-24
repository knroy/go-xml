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

func normalizeAll(t *testing.T, src string, chunk, buf int) string {
	t.Helper()
	r := newAttNormReader(&chunkReader{s: src, n: chunk})
	var out []byte
	b := make([]byte, buf)
	for {
		n, err := r.Read(b)
		out = append(out, b[:n]...)
		if err != nil {
			if err != io.EOF {
				t.Fatalf("read: %v", err)
			}
			break
		}
	}
	return string(out)
}

func TestAttNormReader(t *testing.T) {
	cases := []struct{ name, src, want string }{
		// XML 1.0 section 3.3.3: a literal TAB, LF or CR in an attribute
		// value becomes one space.
		{"newline", "<a s=\"x\ny\"/>", "<a s=\"x y\"/>"},
		{"tab", "<a s=\"x\ty\"/>", "<a s=\"x y\"/>"},
		{"cr lf is two spaces", "<a s=\"x\r\ny\"/>", "<a s=\"x  y\"/>"},
		{"single quotes", "<a s='x\ny'/>", "<a s='x y'/>"},

		// ...but a CHARACTER REFERENCE to the same character survives. This
		// is the whole reason the rewrite is here and not on a.Value, where
		// the decoder has already made the two indistinguishable.
		{"char ref survives", "<a s=\"x&#10;y\"/>", "<a s=\"x&#10;y\"/>"},
		{"hex char ref survives", "<a s=\"x&#xA;y\"/>", "<a s=\"x&#xA;y\"/>"},

		// Whitespace outside a value is structure or content, never
		// normalized.
		{"between attributes", "<a\ns=\"1\"\nt=\"2\"/>", "<a\ns=\"1\"\nt=\"2\"/>"},
		{"character data", "<a>text\nhere</a>", "<a>text\nhere</a>"},

		// Regions where markup is not recognised.
		{"comment", "<!-- a\nb --><c s=\"m\nn\"/>", "<!-- a\nb --><c s=\"m n\"/>"},
		{"empty comment", "<!----><c s=\"m\nn\"/>", "<!----><c s=\"m n\"/>"},
		{"comment with quote", "<!-- \" --><c s=\"m\nn\"/>", "<!-- \" --><c s=\"m n\"/>"},
		{"cdata", "<a><![CDATA[q\"\nz]]></a><b s=\"m\nn\"/>",
			"<a><![CDATA[q\"\nz]]></a><b s=\"m n\"/>"},
		{"cdata trailing brackets", "<a><![CDATA[]]]]></a><b s=\"m\nn\"/>",
			"<a><![CDATA[]]]]></a><b s=\"m n\"/>"},
		{"pi", "<?pi x=\"a\nb\"?><c s=\"m\nn\"/>", "<?pi x=\"a\nb\"?><c s=\"m n\"/>"},
		{"empty pi", "<?pi?><c s=\"m\nn\"/>", "<?pi?><c s=\"m n\"/>"},

		// A markup declaration's literals belong to the DTD, not to an
		// attribute value, and may contain ">" and "[".
		{"internal subset", "<!DOCTYPE r [<!ENTITY e \"a\nb\">]><r s=\"m\nn\"/>",
			"<!DOCTYPE r [<!ENTITY e \"a\nb\">]><r s=\"m n\"/>"},
		{"system id holding gt", "<!DOCTYPE r SYSTEM \"a>b\"><r s=\"m\nn\"/>",
			"<!DOCTYPE r SYSTEM \"a>b\"><r s=\"m n\"/>"},

		// A multi-byte character next to the rewrite must not be split: the
		// scanner is byte-oriented and every byte it acts on is ASCII.
		{"utf-8 neighbour", "<a s=\"é\ny\"/>", "<a s=\"é y\"/>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for chunk := 1; chunk <= 20; chunk++ {
				for buf := 1; buf <= 20; buf++ {
					if got := normalizeAll(t, c.src, chunk, buf); got != c.want {
						t.Fatalf("chunk=%d buf=%d\n got %q\nwant %q",
							chunk, buf, got, c.want)
					}
				}
			}
			// And in one piece, which is how every ordinary caller reads.
			if got := normalizeAll(t, c.src, len(c.src)+1, 1<<16); got != c.want {
				t.Fatalf("whole\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// TestAttNormPreservesLength is the invariant the rest of the parser depends
// on: TrackPositions and the entity base spans both index into this stream, so
// a filter that changed a length would move every recorded position.
func TestAttNormPreservesLength(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := 0; i < 500; i++ {
		b.WriteString("<a s=\"x\ny\"><!-- pad --></a><?p q?><![CDATA[z]]>")
	}
	b.WriteString("</r>")
	src := b.String()
	got := normalizeAll(t, src, 7, 4096)
	if len(got) != len(src) {
		t.Fatalf("length changed: got %d, want %d", len(got), len(src))
	}
	if want := strings.ReplaceAll(src, "x\ny", "x y"); got != want {
		t.Fatal("normalization differs from the expected rewrite")
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
