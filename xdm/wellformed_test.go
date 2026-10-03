package xdm

import (
	"strings"
	"testing"
)

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

// TestParseQNameColons pins Namespaces in XML §3 [7] QName and §7: a name
// with an empty prefix or local part, and a PI target with a colon, are not
// namespace-well-formed (rmt-ns10-014, -015, -016, -042). libxml2 reports
// these as namespace errors but still exits 0.
func TestParseQNameColons(t *testing.T) {
	for _, c := range []struct {
		src string
		ok  bool
	}{
		{`<foo: />`, false},
		{`<:foo/>`, false},
		{`<r xmlns:="urn:x"/>`, false},
		{`<r a:="1"/>`, false},
		{`<r :a="1"/>`, false},
		{`<?a:b bogus?><r/>`, false},
		{`<r><?a:b?></r>`, false},
		{`<p:r xmlns:p="urn:x" p:a="1"/>`, true},
		{`<?a-b bogus?><r><?ab?></r>`, true},
	} {
		_, err := ParseString(c.src, ParseOptions{})
		if (err == nil) != c.ok {
			t.Errorf("ParseString(%q): err = %v, want ok=%v", c.src, err, c.ok)
		}
	}
}

// TestParseNamespaceBindsNormalizedValue pins Namespaces in XML §3: a prefix
// is bound to the declaration's normalized value. With xmlns:b typed NMTOKEN,
// " urn:x " binds urn:x, so a:attr and b:attr are one attribute twice
// (rmt-ns10-012). Typed CDATA, the spaces stay and the URIs differ.
func TestParseNamespaceBindsNormalizedValue(t *testing.T) {
	doc := func(typ string) string {
		return `<!DOCTYPE foo [<!ATTLIST foo xmlns:b ` + typ + ` #IMPLIED>]>` +
			`<foo xmlns:a="urn:x" xmlns:b=" urn:x "><bar a:attr="1" b:attr="2"/></foo>`
	}
	if _, err := ParseString(doc("NMTOKEN"), ParseOptions{AllowDOCTYPE: true}); err == nil ||
		!strings.Contains(err.Error(), "duplicate attribute") {
		t.Errorf("NMTOKEN xmlns:b: err = %v, want a duplicate attribute", err)
	}
	tree, err := ParseString(doc("CDATA"), ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("CDATA xmlns:b: %v", err)
	}
	if got := tree.Root.ChildElements()[0].Namespaces; len(got) != 2 || got[1].Value != " urn:x " {
		t.Errorf("CDATA xmlns:b namespaces = %+v, want the value unnormalized", got)
	}
}
