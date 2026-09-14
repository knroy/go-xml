package xslt

import (
	"strings"
	"testing"
)

// The element table had drifted from the Recommendation's element syntax
// summaries in three ways, each of which accepted what the summaries forbid:
// attributes flagged as attribute value templates where the summary writes
// no braces, which let a "{...}" escape the static XTSE0020 check (on
// xsl:output and xsl:function; xsl:copy-of's @validation looked like the
// same defect but is refused by validate.go, so its flag is untouched);
// visibility enumerations carrying "hidden", which a component acquires
// through xsl:accept or xsl:expose and never declares; and working-draft
// attributes the Recommendation dropped, tolerated where a 3.0 module should
// be told the name is not allowed. The last are listed with removed30, so
// the refusal survives forwards-compatible leniency.
func TestElementTableRecommendationDrift(t *testing.T) {
	const head = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:x="http://xxx.com/" xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    version="3.0" exclude-result-prefixes="xs x">
	  <xsl:variable name="x" select="'yes'"/>`
	const tail = `</xsl:stylesheet>`
	fn := func(attr string) string {
		return `<xsl:function name="x:f" ` + attr + ` as="xs:integer">
		    <xsl:param name="n" as="xs:integer"/><xsl:sequence select="$n"/>
		  </xsl:function>`
	}
	cases := []struct {
		name, body, code string // code "" means the stylesheet must compile
	}{
		// X1: not attribute value templates.
		{"output build-tree avt", `<xsl:output build-tree="{$x}"/>`, "XTSE0020"},
		{"output allow-duplicate-names avt", `<xsl:output allow-duplicate-names="{$x}"/>`, "XTSE0020"},
		{"function new-each-time avt", fn(`new-each-time="{$x}"`), "XTSE0020"},
		{"function streamability avt", fn(`streamability="{$x}"`), "XTSE0020"},
		// X3: enumerations no wider than the summary.
		{"template visibility hidden", `<xsl:template name="main" visibility="hidden"/>`, "XTSE0020"},
		{"mode visibility hidden", `<xsl:mode name="m" visibility="hidden"/>`, "XTSE0020"},
		{"variable visibility hidden", `<xsl:variable name="v" select="1" visibility="hidden"/>`, "XTSE0020"},
		{"function visibility hidden", fn(`visibility="hidden"`), "XTSE0020"},
		{"attribute-set visibility hidden", `<xsl:attribute-set name="a" visibility="hidden"/>`, "XTSE0020"},
		{"function cache full", fn(`cache="full"`), "XTSE0020"},
		// X5: working-draft attributes the Recommendation does not define.
		// param/@export is deliberately not among them: it is accepted and
		// ignored so that iterate-024 still reaches the XTSE0010 it exists
		// to pin. See the table's comment on the entry.
		{"param export tolerated", `<xsl:param name="p" export="yes"/>`, ""},
		{"function identity-sensitive", fn(`identity-sensitive="no"`), "XTSE0090"},
		{"accumulator applies-to",
			`<xsl:accumulator name="a" initial-value="0" applies-to="x"/>`, "XTSE0090"},
		// Refused, but by another rule, so these pin behaviour rather than
		// this change: xsl:copy-of/@validation is checked in validate.go,
		// and an xsl:global-context-item is checked against the
		// "context-item" table key, which defines no @use-accumulators.
		{"copy-of validation braces",
			`<xsl:template name="main"><xsl:copy-of select="." validation="{$x}"/></xsl:template>`,
			"XTSE0020"},
		{"global-context-item use-accumulators",
			`<xsl:global-context-item use-accumulators="#all"/>`, "XTSE0090"},
		// Controls: what the summaries do admit still compiles.
		{"result-document build-tree avt",
			`<xsl:template name="main"><xsl:result-document build-tree="{$x}"/></xsl:template>`, ""},
		{"function streamability eqname", fn(`streamability="Q{http://xxx.com/}mine"`), ""},
		{"function cache true", fn(`cache="true"`), ""},
		{"output indent true", `<xsl:output indent="true"/>`, ""},
		{"template visibility final", `<xsl:template name="main" visibility="final"/>`, ""},
	}
	for _, c := range cases {
		err := compileDrift(t, head+c.body+tail)
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("%s: want %s, got %v", c.name, c.code, err)
		}
	}
}

// TestPackageUsePackageRemoved covers xsl:package/@use-package, which needs
// a package rather than a stylesheet; xsl:accept/@visibility="hidden" is
// the control, since hidden is exactly what xsl:accept confers.
func TestPackageUsePackageRemoved(t *testing.T) {
	const pkg = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    version="3.0" %s>
	  <xsl:use-package name="http://example.com/lib">
	    <xsl:accept component="function" names="*" visibility="hidden"/>
	  </xsl:use-package>
	</xsl:package>`
	err := compileDrift(t, strings.Replace(pkg, "%s", `use-package="http://example.com/lib"`, 1))
	if err == nil || !strings.Contains(err.Error(), "XTSE0090") {
		t.Errorf("package/@use-package: want XTSE0090, got %v", err)
	}
	// Without the draft attribute the same package is refused, if at all,
	// for the unresolved library rather than for xsl:accept's "hidden".
	err = compileDrift(t, strings.Replace(pkg, "%s", "", 1))
	if err != nil && strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("accept/@visibility=\"hidden\" was refused: %v", err)
	}
}
