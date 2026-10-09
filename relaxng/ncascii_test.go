package relaxng

import (
	"testing"
	"unicode"
)

// The ASCII fast paths must agree with the Unicode-table definitions.
func TestNCName4ASCIIFastPath(t *testing.T) {
	for r := rune(0); r < 0x80; r++ {
		start := r == '_' || unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lo, unicode.Lt, unicode.Lm)
		char := start || r == '-' || r == '.' || unicode.In(r, unicode.Nd, unicode.Mn, unicode.Mc)
		if isNameStart4(r) != start || isNameChar4(r) != char {
			t.Errorf("%q: start %v/%v char %v/%v", r, isNameStart4(r), start, isNameChar4(r), char)
		}
	}
}
