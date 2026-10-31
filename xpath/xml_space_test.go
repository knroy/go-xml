package xpath

import (
	"math/rand"
	"strings"
	"testing"
	"unsafe"
)

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

// collapseXMLSpace replaced strings.Join(splitXMLSpace(s), " ") in
// normalize-space and tokenize#1, which built a list of every token: 43 GB over
// a rule set normalising one 15 MB value 180 times. Over random strings of XML
// S, no-break spaces, multi-byte runes and invalid UTF-8 it must give what that
// expression gave, byte for byte.
func TestCollapseXMLSpaceMatchesSplitJoin(t *testing.T) {
	alphabet := []string{" ", " ", "\t", "\n", "\r", "a", "b", nbsp, "é", "\xff", "\xc3"}
	rng := rand.New(rand.NewSource(1))
	for range 100000 {
		var b strings.Builder
		for range rng.Intn(12) {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		s := b.String()
		if got, want := collapseXMLSpace(s), strings.Join(splitXMLSpace(s), " "); got != want {
			t.Fatalf("collapseXMLSpace(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestCollapseXMLSpaceAllocation pins the cost the audit measured: a collapsed
// value comes back without a copy, and one that is not is built in a single
// allocation, however many tokens it has.
func TestCollapseXMLSpaceAllocation(t *testing.T) {
	collapsed := strings.Repeat("a ", 100000) + "a"
	spread := strings.Repeat(" a\t", 100000)
	for _, tc := range []struct {
		name string
		in   string
		want float64
	}{{"collapsed", collapsed, 0}, {"spread", spread, 1}} {
		if n := testing.AllocsPerRun(10, func() { collapseXMLSpace(tc.in) }); n != tc.want {
			t.Errorf("%s: %.0f allocations, want %.0f", tc.name, n, tc.want)
		}
	}
	if got := collapseXMLSpace(collapsed); unsafe.StringData(got) != unsafe.StringData(collapsed) {
		t.Error("a collapsed value was copied")
	}
}
