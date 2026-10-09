package xdm

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xmltok"
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
	if got := tree.Root.ChildElements()[0].namespaces; len(got) != 2 || got[1].value != " urn:x " {
		t.Errorf("CDATA xmlns:b namespaces = %+v, want the value unnormalized", got)
	}
}

// TestDuplicateAttributeCheck covers both halves of the duplicate check: the
// scan used for a tag's first few attributes and the map it hands over to,
// with the duplicate reported for the attribute that repeats an earlier one.
// The common case, a tag of a few attributes, must not allocate.
func TestDuplicateAttributeCheck(t *testing.T) {
	attrs := func(n int, extra string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, ` a%d="1"`, i)
		}
		return b.String() + extra
	}
	for _, n := range []int{0, 3, 7, 8, 9, 30} {
		ns := `<r xmlns:p="urn:x" xmlns:q="urn:x"`
		if _, err := ParseString(ns+attrs(n, ` p:z="1"`)+`/>`, ParseOptions{}); err != nil {
			t.Fatalf("n=%d distinct: %v", n, err)
		}
		_, err := ParseString(ns+attrs(n, ` p:z="1" b="2" q:z="3" a0="4"`)+`/>`, ParseOptions{})
		if want := "parse XML: duplicate attribute {urn:x}z"; err == nil || err.Error() != want {
			t.Errorf("n=%d: err = %v, want %s", n, err, want)
		}
		if n > 0 {
			_, err = ParseString(`<r`+attrs(n, ` a0="4"`)+`/>`, ParseOptions{})
			if want := "parse XML: duplicate attribute {}a0"; err == nil || err.Error() != want {
				t.Errorf("n=%d: err = %v, want %s", n, err, want)
			}
		}
	}

	tok := xmltok.StartElement{Name: xmltok.Name{Local: "e"}}
	for i := range 6 {
		tok.Attr = append(tok.Attr, xmltok.Attr{Name: xmltok.Name{Space: "xml", Local: fmt.Sprint("a", i)}, Value: "v"})
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := validateStartElement(tok, nil, false); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("validateStartElement on six attributes allocated %.0f times, want 0", n)
	}
}
