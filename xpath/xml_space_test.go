package xpath

import "testing"

// XML S, per F&O 3.1 §5.4.5 and XML 1.0 §2.3, is exactly these four.
const (
	nbsp = " " // no-break space: Unicode whitespace, not XML S
	ff   = ""// form feed: Unicode whitespace, not XML S
	ogh = " " // ogham space mark: Unicode whitespace, not XML S
)

// F&O 3.1 §5.4.5 defines the whitespace fn:normalize-space strips and collapses
// as exactly the four XML S characters: #x20, #x9, #xD and #xA. Go's
// strings.Fields splits on the whole Unicode White_Space set, which made a
// no-break space a separator: normalize-space("a b") came back three
// characters long with the NBSP replaced by an ordinary space, where the spec
// makes the NBSP data that has to survive untouched.
func TestNormalizeSpaceUsesXMLWhitespaceOnly(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"xml S collapses", "  a\t\nb\r ", "a b"},
		{"nbsp is data", "a" + nbsp + "b", "a" + nbsp + "b"},
		{"nbsp run is data", "a" + nbsp + nbsp + "b", "a" + nbsp + nbsp + "b"},
		{"nbsp not trimmed", nbsp + "a" + nbsp, nbsp + "a" + nbsp},
		{"form feed is data", ff + "a" + ff, ff + "a" + ff},
		{"ogham space is data", "a" + ogh + "b", "a" + ogh + "b"},
		{"nbsp kept, xml S collapsed", " a" + nbsp + " \t b ", "a" + nbsp + " b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := mustParse(t, testDoc)
			ctx := NewContext(root, Builtins())
			ctx.Vars["v"] = strSeq(tc.in)
			seq, err := Eval("normalize-space($v)", ctx, testNS{})
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got := renderSeq(seq); got != tc.want {
				t.Errorf("normalize-space(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
