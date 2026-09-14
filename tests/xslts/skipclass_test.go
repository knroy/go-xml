package xslts

import "testing"

// Every reason string the scope filter can produce must carry a class, and
// the class must be the right one: a "not implemented" skip counted as out of
// scope is the conflation this split exists to end.
func TestSkipReasonsAreClassified(t *testing.T) {
	cases := map[string]string{
		"spec XSLT30+":                            skipOutOfScope,
		"streamability (XSLT 3.0)":                skipOutOfScope,
		"xsl:package (XSLT 3.0)":                  skipOutOfScope,
		"initial function (XSLT 3.0)":             skipOutOfScope,
		"adaptive/json output method (XSLT 3.0)":  skipOutOfScope,
		"fn:current-output-uri (XSLT 3.0)":        skipOutOfScope,
		"needs XSD_1.1 absent":                    skipOutOfScope,
		"depends on unicode-version":              skipOutOfScope,
		"depends on available_documents":          skipOutOfScope,
		"unknown feature streaming-fallback":      skipUnimplemented,
		"unmodelled dependency enable_assertions": skipUnimplemented,
		"document is not UTF-8 or UTF-16":         skipUnimplemented,
	}
	// The unsupported features are classified through the map itself, so a
	// new entry is covered without being retyped here.
	for f, why := range unsupportedFeatures {
		cases[f+": "+why] = skipUnimplemented
	}
	for _, c := range xslt30Constructs {
		cases[c.why] = skipOutOfScope
	}
	for why, want := range cases {
		if got := skipClass(why); got != want {
			t.Errorf("skipClass(%q) = %q, want %q", why, got, want)
		}
	}
	if got := skipClass("some reason nobody recorded"); got != "" {
		t.Errorf("an unrecorded reason classified as %q, want \"\"", got)
	}
}
