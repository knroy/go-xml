package xdm

import (
	"io"
	"testing"
)

// TestLineEndReader checks the section 2.11 fold at every read boundary: a
// CR-LF split across two reads is still one line end, and so is a CR NEL
// pair, which is passed through for the tokeniser to fold by version.
func TestLineEndReader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\r\r\nb\r", "a\n\nb\n"},
		{"\r\n\r\n", "\n\n"},
		{"a\r\u0085b", "a\r\u0085b"},
		{"a\ré", "a\né"},
		{"no line ends", "no line ends"},
	}
	for _, tc := range cases {
		for chunk := 1; chunk <= len(tc.in); chunk++ {
			r := newLineEndReader(&chunkReader{s: tc.in, n: chunk})
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("%q in chunks of %d: got %q, want %q", tc.in, chunk, got, tc.want)
			}
		}
	}
}

// TestLineEndsNormalizedBeforeParsing is section 2.11 end to end. Comments and
// processing instructions kept their CRs when the tokeniser did the folding,
// and a CR-LF in an attribute value became two spaces instead of one.
func TestLineEndsNormalizedBeforeParsing(t *testing.T) {
	tr, err := ParseString("<a b=\"x\ty\nz\r\nw\rv&#13;u\">t\r\nu<!--c\r\nd\re--><?p x\r\ny?></a>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := kids(tr.Root)[0]
	if got, want := attrsOf(a)[0].Value(), "x y z w v\ru"; got != want {
		t.Errorf("attribute %q, want %q", got, want)
	}
	want := []string{"t\nu", "c\nd\ne", "x\ny"}
	for i, c := range kids(a) {
		if c.Value() != want[i] {
			t.Errorf("%v %q, want %q", c.Kind(), c.Value(), want[i])
		}
	}
}
