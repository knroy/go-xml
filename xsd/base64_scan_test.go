package xsd

import (
	"strings"
	"testing"
)

// isBase64Binary scans bytes in place. It converted the value to []rune
// first, four bytes per input byte: 98.6% of validation allocation on a UBL
// invoice with an embedded attachment. Every legal character is ASCII, so the
// byte scan must reject exactly what the rune scan did, non-ASCII included.
func TestBase64BinaryByteScan(t *testing.T) {
	for v, want := range map[string]bool{
		"":           true,
		"QUJD":       true,
		"QU JD":      true,
		"QUJD\nQUI=": true,
		"QQ==":       true,
		"QUI=":       true,
		"QUJ":        false,
		"QR==":       false,
		"QUJ=":       false,
		"Q===":       false,
		"QU=D":       false,
		"QQ==QUJD":   false,
		"QUJé":       false,
		"éQU":        false,
		"QUJéQUJD":   false,
	} {
		if got := isBase64Binary(v); got != want {
			t.Errorf("isBase64Binary(%q) = %v, want %v", v, got, want)
		}
	}
	long := strings.Repeat(strings.Repeat("QUJD", 19)+"\n", 5000) + "QUI="
	if n := testing.AllocsPerRun(5, func() { isBase64Binary(long) }); n != 0 {
		t.Errorf("isBase64Binary allocated %v times on a 380 KB value, want 0", n)
	}
}
