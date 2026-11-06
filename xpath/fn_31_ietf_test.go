package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestParseIETFDateTinyFractionDoesNotPanic: a fraction of a second below the
// nine places the lexical form keeps trims away to a single digit, and
// formatIETFSeconds then read s[1] of a one-byte string. The runtime panic
// left Compiled.Eval, since only a refusal is recovered there.
func TestParseIETFDateTinyFractionDoesNotPanic(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Wed, 06 Jun 1994 07:29:05.0000000001 GMT", "1994-06-06T07:29:05Z"},
		{"Wed, 06 Jun 1994 07:29:00.0000000001 GMT", "1994-06-06T07:29:00Z"},
		{"Wed, 06 Jun 1994 07:29:05.5 GMT", "1994-06-06T07:29:05.5Z"},
	} {
		ctx := NewContext(nil, Builtins()).WithVersion(XPath31)
		seq, err := Eval("string(parse-ietf-date('"+tc.in+"'))", ctx, nil)
		if err != nil {
			t.Fatalf("parse-ietf-date(%q): %v", tc.in, err)
		}
		if got := seq[0].(*xdm.Atomic).String(); got != tc.want {
			t.Errorf("parse-ietf-date(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
