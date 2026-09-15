package xpath

import (
	"strings"
	"testing"
)

// Every XML Schema datatype whose whiteSpace facet is "collapse" strips only
// XML S (#x20 #x9 #xD #xA) from its lexical form. strings.TrimSpace and
// strings.Fields use Go's whole Unicode White_Space set, so a no-break space
// around a lexical value was silently stripped and the value accepted, where
// the spec makes the NBSP part of the lexical form and therefore invalid.
//
// This is the cross-type corpus: for each datatype, XML whitespace around a
// valid lexical form must still be accepted, and an NBSP in the same position
// must be rejected.
func TestCastsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"

	// Each case is a datatype and a lexical form that is valid bare.
	for _, tc := range []struct{ typ, lexical string }{
		{"xs:boolean", "true"},
		{"xs:integer", "42"},
		{"xs:decimal", "1.5"},
		{"xs:double", "1.0e3"},
		{"xs:float", "2.5"},
		{"xs:date", "2026-01-02"},
		{"xs:dateTime", "2026-01-02T03:04:05"},
		{"xs:time", "03:04:05"},
		{"xs:gYear", "2026"},
		{"xs:duration", "P1Y"},
		{"xs:dayTimeDuration", "P1D"},
		{"xs:yearMonthDuration", "P1Y"},
		{"xs:QName", "a"},
		{"xs:hexBinary", "AB"},
		{"xs:base64Binary", "AQID"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			// XML whitespace around the value: still valid (collapse).
			for _, pad := range []string{" %s ", "\t%s\n", "\r%s "} {
				expr := tc.typ + "('" + strings.Replace(pad, "%s", tc.lexical, 1) + "')"
				if _, err := evalRaw(t, expr); err != nil {
					t.Errorf("%s: XML whitespace must collapse, got %v", expr, err)
				}
			}
			// NBSP around the value: the NBSP is part of the lexical form,
			// which no longer matches the datatype's grammar.
			for _, pad := range []string{nbsp + "%s", "%s" + nbsp, nbsp + "%s" + nbsp} {
				expr := tc.typ + "('" + strings.Replace(pad, "%s", tc.lexical, 1) + "')"
				if _, err := evalRaw(t, expr); err == nil {
					t.Errorf("%s: NBSP is not XML whitespace, must not be stripped", expr)
				}
			}
		})
	}

	// xs:anyURI has whiteSpace="collapse" too, but every string is a valid
	// anyURI, so the observable is the value rather than an error: the NBSP
	// must survive into it where XML whitespace is trimmed away.
	t.Run("xs:anyURI keeps NBSP", func(t *testing.T) {
		if got := evalStr(t, testDoc, "string-length(xs:anyURI(' x '))"); got != "1" {
			t.Errorf("XML whitespace must collapse: got %q, want 1", got)
		}
		if got := evalStr(t, testDoc, `string-length(xs:anyURI('`+nbsp+`x`+nbsp+`'))`); got != "3" {
			t.Errorf("NBSP must survive in xs:anyURI: got %q, want 3", got)
		}
	})

	// base64Binary permits XML whitespace *between* the encoded characters,
	// which is a separate code path from the edge trim. An NBSP in the middle
	// is not whitespace and makes the lexical form invalid.
	t.Run("xs:base64Binary interior", func(t *testing.T) {
		if _, err := evalRaw(t, "xs:base64Binary('AQ ID')"); err != nil {
			t.Errorf("interior XML whitespace is permitted: %v", err)
		}
		if _, err := evalRaw(t, `xs:base64Binary('AQ`+nbsp+`ID')`); err == nil {
			t.Error("interior NBSP must not be removed")
		}
	})
}

// evalRaw evaluates expr against testDoc and returns the error unwrapped, so a
// cast failure can be asserted without failing the test.
func evalRaw(t *testing.T, expr string) (string, error) {
	t.Helper()
	root := mustParse(t, testDoc)
	ctx := NewContext(root, Builtins())
	seq, err := Eval(expr, ctx, testNS{})
	if err != nil {
		return "", err
	}
	return renderSeq(seq), nil
}
