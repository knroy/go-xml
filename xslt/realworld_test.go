package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// These cover defects that the W3C suites did not reach and that two real
// XSLT 3.0 codebases did: DocBook xslTNG and XSpec. Each is reduced to the
// smallest stylesheet that still shows the fault, so that the test says what
// broke rather than that a corpus stopped rendering.

// TestCopyOfNonNodeContextItem covers xsl:copy with no select over a context
// item that is present but is not a node.
//
// 11.9.1 raises XTTE0945 only "when the context item is ABSENT"; a context
// item that is an atomic value or a function item falls under the next
// sentence instead, which returns the value. Conflating absent with
// not-a-node made every xsl:copy inside an xsl:for-each over atomics an
// error, which is how DocBook xslTNG failed on all 613 of its test documents.
func TestCopyOfNonNodeContextItem(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:template match="/"><out><xsl:for-each select="1,2,3"><xsl:copy/></xsl:for-each></out></xsl:template>` +
		`</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); got != "<out>1 2 3</out>" {
		t.Errorf("got %q, want the atomic values copied through", got)
	}
}

// TestCopyWithAbsentContextItemStillFails guards the other half of the rule:
// narrowing XTTE0945 to the absent case must not remove it.
func TestCopyWithAbsentContextItemStillFails(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template name="main"><xsl:copy/></xsl:template></xsl:stylesheet>`
	compiled, err := Compile(mustParse(t, sheet), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = compiled.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err == nil || !strings.Contains(err.Error(), "XTTE0945") {
		t.Errorf("got %v, want XTTE0945 for an absent context item", err)
	}
}

// TestEvaluateCallsStylesheetFunction covers a stylesheet function reached
// from the target expression of xsl:evaluate.
//
// 10.4.1 admits "all user-defined functions ... present in the containing
// package provided their visibility is not hidden or private", and a
// declaration with no visibility attribute defaults to private. That default
// belongs to a package, though, and a plain xsl:stylesheet is not one -- so
// applying it here made a stylesheet's own functions unreachable from its own
// xsl:evaluate. Saxon does not apply it either: its XSLT 3.0 results report
// evaluate-045 as "wrongError". Every stylesheet that evaluates XPath taken
// from data and calls its own functions from it -- DocBook xslTNG throughout
// -- depends on this.
func TestEvaluateCallsStylesheetFunction(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" ` +
		`xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:function name="f:double" as="xs:integer"><xsl:param name="n" as="xs:integer"/>` +
		`<xsl:sequence select="$n*2"/></xsl:function>` +
		`<xsl:template match="/"><r><xsl:evaluate xpath="'Q{urn:f}double(21)'"/></r></xsl:template>` +
		`</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); !strings.Contains(got, ">42<") {
		t.Errorf("got %q, want the stylesheet function to be callable", got)
	}
}

// TestKeyPrefixBoundInSeveralModules covers fn:key where the key's prefix is
// bound to different URIs in different modules.
//
// The name in the first argument is a lexical QName resolved at run time,
// against bindings collected at compile time. Keeping one binding per prefix
// meant that whichever module was included LAST decided what every such name
// expanded to, so the same two modules failed or succeeded purely by include
// order. XSpec binds "local" to nineteen different URIs, one per module, and
// key('local:scenarios', ...) raised XTDE1260 because of it.
func TestKeyPrefixBoundInSeveralModules(t *testing.T) {
	// The module declaring the key is included second, so its binding of
	// "local" is not the one a single-binding map would have kept.
	other := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" ` +
		`xmlns:local="urn:other"><xsl:template name="unused"/></xsl:stylesheet>`
	keyed := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" ` +
		`xmlns:local="urn:keyed">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:key name="local:items" match="x" use="@id"/>` +
		`<xsl:template match="/"><out><xsl:value-of select="count(key('local:items','a'))"/></out></xsl:template>` +
		`</xsl:stylesheet>`
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("other.xsl", other)
	write("keyed.xsl", keyed)

	mainSrc := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:include href="other.xsl"/><xsl:include href="keyed.xsl"/></xsl:stylesheet>`
	mainPath := filepath.Join(dir, "main.xsl")
	write("main.xsl", mainSrc)

	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	stree, err := xdm.ParseString(mainSrc, xdm.ParseOptions{BaseURI: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: mainPath})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	dtree, err := xdm.ParseString(`<doc><x id="a"/><x id="b"/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := compiled.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	// The count is what matters; the module's own namespace declaration is
	// carried onto the literal result element and is not part of the claim.
	if got := res.String(); !strings.Contains(got, ">1</out>") {
		t.Errorf("got %q, want the key found whatever the include order", got)
	}
}

// TestCompatDropAttributesOnDocumentNode covers the opt-in relaxation of
// XTDE0420.
//
// The error is correct and stays on by default: 5.8.1 applies its rules in
// the order listed, and the one that unwraps a document node in the result
// sequence unwraps document nodes only, so an attribute reaches the check.
// The suite asserts exactly this in error-0420a. Saxon accepts it anyway, and
// DocBook xslTNG builds such a tree in head.xsl for every document carrying an
// xml:lang -- so a caller running stylesheets written against Saxon can ask
// for the attribute to be dropped instead.
func TestCompatDropAttributesOnDocumentNode(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:template match="/">` +
		`<xsl:variable name="v"><xsl:apply-templates select="/*"/></xsl:variable>` +
		`<out><xsl:value-of select="$v"/></out></xsl:template>` +
		`<xsl:template match="doc"><xsl:attribute name="lang">en</xsl:attribute>` +
		`<xsl:text>kept</xsl:text></xsl:template></xsl:stylesheet>`

	// Default: the specified behaviour, which is the error.
	if _, err := runErr(t, sheet, `<doc/>`); err == nil ||
		!strings.Contains(err.Error(), "XTDE0420") {
		t.Errorf("default: got %v, want XTDE0420", err)
	}

	// Opt in: the attribute is dropped and everything else is built as it
	// would have been, which is what makes the rest of the tree usable.
	compiled, err := Compile(mustParse(t, sheet), CompileOptions{
		Compat: Compatibility{DropAttributesOnDocumentNode: true},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := compiled.Transform(context.Background(), src.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("with the relaxation: %v", err)
	}
	if got := res.String(); got != "<out>kept</out>" {
		t.Errorf("got %q, want the attribute dropped and the text kept", got)
	}
}
