package xdm

import "testing"

func TestParseRejectsDuplicateAndNamespaceIllFormedNames(t *testing.T) {
	for _, src := range []string{
		`<r b="safe" b="evil"/>`,
		`<r xmlns:p="u" xmlns:q="u" p:a="1" q:a="2"/>`,
		`<p:r/>`,
		`<r xmlns:xml="wrong"/>`,
		`<r xmlns:xmlns="u"/>`,
		`<r xmlns:p=""/>`,
		`<r xmlns:p="http://www.w3.org/2000/xmlns/"/>`,
		`<r xmlns:p="http://www.w3.org/XML/1998/namespace"/>`,
	} {
		if _, err := ParseString(src, ParseOptions{}); err == nil {
			t.Errorf("ParseString(%q) accepted a namespace-ill-formed document", src)
		}
	}
}

func TestParseRejectsMalformedPrologAndDirectives(t *testing.T) {
	for _, src := range []string{
		`<?xml?><r/>`,
		`<?xml encoding="UTF-8"?><r/>`,
		`<?xml version="1.0" standalone="maybe"?><r/>`,
		`<!--c--><?xml version="1.0"?><r/>`,
		`<r/><?XML foo?>`,
		`<!BLAH><r/>`,
		`<r/><!BLAH>`,
		`<r><!ENTITY x "y"></r>`,
		"\u00a0<r/>",
		"<r/>\u00a0",
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("ParseString(%q) accepted malformed prolog/content", src)
		}
	}
}

func TestParseAcceptsXML11PrefixUndeclaration(t *testing.T) {
	if _, err := ParseString(`<?xml version="1.1"?><r xmlns:p=""/>`, ParseOptions{}); err != nil {
		t.Fatalf("XML 1.1 prefix undeclaration rejected: %v", err)
	}
}

// TestParseAcceptsSpacedXMLDeclaration pins [25] Eq ::= S? '=' S?. The first
// form of this validator required a bare "=", which rejected the declaration
// qt3tests/docs/atomic.xml carries and cost 241 QT3 cases. A rejection-only
// test cannot catch that: the whole defect was over-strictness.
func TestParseAcceptsSpacedXMLDeclaration(t *testing.T) {
	for _, src := range []string{
		`<?xml version="1.0"?><r/>`,
		`<?xml version = "1.0"?><r/>`,
		`<?xml version="1.0" encoding = "UTF-8"?><r/>`,
		`<?xml version="1.0" standalone = "yes"?><r/>`,
		`<?xml version = '1.0' encoding = 'UTF-8' standalone = 'no'?><r/>`,
		"<?xml version=\"1.0\"\n\tencoding=\"UTF-8\"?><r/>",
	} {
		if _, err := ParseString(src, ParseOptions{}); err != nil {
			t.Errorf("ParseString(%q) rejected a well-formed declaration: %v", src, err)
		}
	}
}
