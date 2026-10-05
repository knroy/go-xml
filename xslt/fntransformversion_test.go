package xslt_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// globalItemSheet reports, from a version="3.0" stylesheet, what its global
// context item is, what the initial match is, and which XSLT version the
// processor says it implements: QT3's transform/variable-with-context.xsl.
const globalItemSheet = `<xsl:stylesheet version='3.0' xmlns:xsl='http://www.w3.org/1999/XSL/Transform'>
  <xsl:variable name='v' select='.'/>
  <xsl:template match='.'>
    <out root-is-doc='{$v instance of document-node()}' this-is-doc='{. instance of document-node()}' xslt-version='{system-property("xsl:version")}'><xsl:value-of select='name($v)'/></out>
  </xsl:template>
  <xsl:template name='xsl:initial-template'>
    <init global='{name($v)}'/>
  </xsl:template>
</xsl:stylesheet>`

// runGlobalItem calls fn:transform on globalItemSheet with the given extra
// options; $in is a parsed <dummy/>.
func runGlobalItem(t *testing.T, options string) (string, error) {
	t.Helper()
	outer := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:template match="/">
	    <xsl:variable name="in" select="parse-xml('&lt;dummy/&gt;')"/>
	    <xsl:sequence select="transform(map{'stylesheet-node': /, ` + options + `})?output"/>
	  </xsl:template>
	</xsl:stylesheet>`
	return runPP(t, outer, globalItemSheet)
}

// xslt-version is declared xs:decimal; the option conventions convert by the
// function conversion rules, under which a string is not a decimal.
func TestFnTransformXSLTVersionType(t *testing.T) {
	_, err := runGlobalItem(t, `'source-node': $in, 'xslt-version': '2.0'`)
	if xdm.ErrorCode(err) != "XPTY0004" {
		t.Errorf("xslt-version '2.0': got %v, want XPTY0004", err)
	}
	// An untyped value is cast, as the conversion rules do.
	if _, err := runGlobalItem(t,
		`'source-node': $in, 'xslt-version': xs:untypedAtomic('3.0')`); err != nil {
		t.Errorf("xslt-version untypedAtomic 3.0: %v", err)
	}
}

// No processor implements a version later than 3.0: FOXT0001.
func TestFnTransformXSLTVersionUnavailable(t *testing.T) {
	_, err := runGlobalItem(t, `'source-node': $in, 'xslt-version': 4.0`)
	if xdm.ErrorCode(err) != "FOXT0001" {
		t.Errorf("xslt-version 4.0: got %v, want FOXT0001", err)
	}
}

// xslt-version 2.0 selects the XSLT 2.0 processor, which says so in
// system-property and ignores global-context-item, a 3.0 option
// (fn-transform-82e). 1.0 selects it too, as the nearest later version.
func TestFnTransformXSLTVersion20(t *testing.T) {
	for _, v := range []string{"2.0", "1", "1.0"} {
		got, err := runGlobalItem(t,
			`'source-node': $in/*, 'global-context-item': $in/*, 'xslt-version': `+v)
		if err != nil {
			t.Fatalf("xslt-version %s: %v", v, err)
		}
		want := `<out root-is-doc="true" this-is-doc="false" xslt-version="2.0"/>`
		if !strings.Contains(got, want) {
			t.Errorf("xslt-version %s: got %s, want %s", v, got, want)
		}
	}
	got, err := runGlobalItem(t, `'source-node': $in, 'xslt-version': 3.0`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `xslt-version="3.0"`) {
		t.Errorf("xslt-version 3.0: got %s, want xsl:version 3.0", got)
	}
}

// global-context-item replaces the global context item source-node supplies,
// and source-node is still what templates are applied to (fn-transform-82c).
// Given alone, it is the context item for xsl:initial-template's globals.
func TestFnTransformGlobalContextItem(t *testing.T) {
	got, err := runGlobalItem(t,
		`'source-node': $in, 'global-context-item': $in/*, 'xslt-version': 3.0`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<out root-is-doc="false" this-is-doc="true" xslt-version="3.0">dummy</out>`; !strings.Contains(got, want) {
		t.Errorf("got %s, want %s", got, want)
	}
	got, err = runGlobalItem(t, `'global-context-item': $in/*`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<init global="dummy"/>`; !strings.Contains(got, want) {
		t.Errorf("alone: got %s, want %s", got, want)
	}
	_, err = runGlobalItem(t, `'source-node': $in, 'global-context-item': 1`)
	if xdm.ErrorCode(err) != "FOXT0001" {
		t.Errorf("atomic global-context-item: got %v, want FOXT0001", err)
	}
}

// An attribute XSLT 3.0 added is accepted on a version="2.0" module by a 3.0
// processor, for which section 3.9.2 defines 2.0 behavior as 3.0's, and
// refused by a 2.0 processor (fn-transform-61).
func TestSince30AttributeOn20Module(t *testing.T) {
	doc, err := xdm.ParseString(`<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:my="urn:my">
	  <xsl:function name="my:f" visibility="public"><xsl:sequence select="1"/></xsl:function>
	</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xslt.Compile(doc.Root, xslt.CompileOptions{}); err != nil {
		t.Errorf("3.0 processor: %v", err)
	}
	if _, err := xslt.Compile(doc.Root, xslt.CompileOptions{MaxVersion: 2.0}); err == nil ||
		!strings.Contains(err.Error(), "XTSE0090") {
		t.Errorf("2.0 processor: got %v, want XTSE0090", err)
	}
}
