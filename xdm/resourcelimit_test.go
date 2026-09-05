package xdm

import (
	"errors"
	"strings"
	"testing"
)

// xdm defines ErrResourceLimit and documents what it is for: a caller can ask
// errors.Is and back off rather than reporting a syntax error to its user.
// xpath, xquery and xsd all applied it; the package that defines it did not
// apply it to any of its own four limits, so every refusal xdm itself raised
// was indistinguishable from malformed input.
func TestParseLimitsCarryResourceLimit(t *testing.T) {
	deep := strings.Repeat("<a>", 1100) + strings.Repeat("</a>", 1100)

	var ents strings.Builder
	ents.WriteString(`<!DOCTYPE r [<!ENTITY e "` + strings.Repeat("A", 65000) + `">]><r>`)
	for i := 0; i < 100000; i++ {
		ents.WriteString("&e;")
	}
	ents.WriteString("</r>")

	for _, tc := range []struct {
		name string
		doc  string
		opts ParseOptions
		want string
	}{
		{"depth", deep, ParseOptions{}, "nesting exceeds 1000 levels"},
		{"nodes", `<a><b/><c/><d/><e/><f/><g/><h/><i/><j/><k/><l/></a>`,
			ParseOptions{MaxNodes: 10}, "document exceeds 10 nodes"},
		{"bytes", "<a>" + strings.Repeat("x", 5000) + "</a>",
			ParseOptions{MaxBytes: 100}, "document exceeds 100 bytes"},
		{"entities", ents.String(), ParseOptions{AllowDOCTYPE: true},
			"entity expansion exceeds 1048576 bytes in total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString(tc.doc, tc.opts)
			if err == nil {
				t.Fatalf("no refusal")
			}
			if !errors.Is(err, ErrResourceLimit) {
				t.Fatalf("errors.Is(err, ErrResourceLimit) is false: %v", err)
			}
			// The sentinel is APPENDED, never woven into the message: the
			// existing text must still lead, because tests and conformance
			// suites match on the prefix. A Contains check would not catch a
			// wrap that renders the sentinel first and the real reason after.
			want := "parse XML: " + tc.want + ": " + ErrResourceLimit.Error()
			if err.Error() != want {
				t.Fatalf("message changed:\n got %q\nwant %q", err.Error(), want)
			}
		})
	}
}

// A malformed document is not a resource refusal, and must not report as one:
// the whole value of the sentinel is that the two can be told apart.
func TestSyntaxErrorIsNotResourceLimit(t *testing.T) {
	_, err := ParseString(`<a><b></a></b>`, ParseOptions{})
	if err == nil {
		t.Fatal("malformed document accepted")
	}
	if errors.Is(err, ErrResourceLimit) {
		t.Fatalf("a syntax error reports as a resource limit: %v", err)
	}
}
