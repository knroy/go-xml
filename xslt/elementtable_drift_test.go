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
		// 9.2's summary for xsl:param stops at "static?", so @export is one
		// of them. It was tolerated until the xsl:on-completion placement
		// rule moved ahead of the attribute sweep; see
		// TestIteratePlacementOutranksUnknownAttribute below.
		{"param export removed", `<xsl:param name="p" export="yes"/>`, "XTSE0090"},
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

// TestIteratePlacementOutranksUnknownAttribute pins the ordering the export
// entry depends on.
//
// A stylesheet that misplaces an xsl:on-completion AND writes an attribute
// the summaries do not allow is a static error either way; the question is
// only which error it is told about. Section 8.4's content model for
// xsl:iterate is the structural fact, so it is read first and the module is
// refused for the shape of its tree rather than for a stray attribute inside
// it. suite case iterate-024 is exactly this stylesheet, and expects
// XTSE0010.
//
// The controls matter as much as the case: a module with only one of the two
// faults must still report that one, so the reordering cannot be moving
// errors around for stylesheets that are broken in a single way.
func TestIteratePlacementOutranksUnknownAttribute(t *testing.T) {
	sheet := func(param, onCompletion string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    version="3.0">
		  <xsl:template name="main">
		    <out>
		      <xsl:iterate select="1 to 3">
		        <xsl:param name="count" select="0" ` + param + `/>
		        <xsl:sequence select="$count"/>
		      </xsl:iterate>
		      ` + onCompletion + `
		    </out>
		  </xsl:template>
		</xsl:stylesheet>`
	}
	const misplaced = `<xsl:on-completion><done/></xsl:on-completion>`
	cases := []struct {
		name, param, onCompletion, code string
	}{
		{"both faults report the structural one", `export="yes"`, misplaced, "XTSE0010"},
		{"misplaced element alone", "", misplaced, "XTSE0010"},
		{"unknown attribute alone", `export="yes"`, "", "XTSE0090"},
		{"neither fault compiles", "", "", ""},
	}
	for _, c := range cases {
		err := compileDrift(t, sheet(c.param, c.onCompletion))
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("%s: want %s, got %v", c.name, c.code, err)
		}
	}
	// An xsl:on-completion correctly placed as a child of xsl:iterate is not
	// touched by the pre-pass.
	const wellPlaced = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    version="3.0">
	  <xsl:template name="main">
	    <xsl:iterate select="1 to 3">
	      <xsl:on-completion><done/></xsl:on-completion>
	      <xsl:sequence select="."/>
	    </xsl:iterate>
	  </xsl:template>
	</xsl:stylesheet>`
	if err := compileDrift(t, wellPlaced); err != nil {
		t.Errorf("well-placed xsl:on-completion was refused: %v", err)
	}
}

// TestOutputTypedAttrs covers the two xsl:output attributes that reached no
// validator at all. Both were in the table, both were flagged as attribute
// value templates, and neither had an enumeration or a qnameAttrs entry -- so
// checkAttrValue returned before consulting anything and accepted every
// value, an invented method and a value that is no URI alike.
//
// json-node-output-method is deliberately narrower than @method: section 26.1
// writes it "xml" | "html" | "xhtml" | "text" | eqname, withholding the "json"
// and "adaptive" that @method admits.
func TestOutputTypedAttrs(t *testing.T) {
	const head = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    version="3.0">
	  <xsl:variable name="x" select="'text'"/>`
	const tail = `</xsl:stylesheet>`
	cases := []struct {
		name, body, code string // code "" means the stylesheet must compile
	}{
		// Neither attribute is an attribute value template.
		{"json-node-output-method avt",
			`<xsl:output json-node-output-method="{$x}"/>`, "XTSE0020"},
		{"parameter-document avt",
			`<xsl:output parameter-document="{$x}"/>`, "XTSE0020"},
		// Every value the summary lists for json-node-output-method.
		{"json-node xml", `<xsl:output json-node-output-method="xml"/>`, ""},
		{"json-node html", `<xsl:output json-node-output-method="html"/>`, ""},
		{"json-node xhtml", `<xsl:output json-node-output-method="xhtml"/>`, ""},
		{"json-node text", `<xsl:output json-node-output-method="text"/>`, ""},
		// The union's second half, as xsl:function/@streamability takes it.
		{"json-node eqname",
			`<xsl:output json-node-output-method="Q{http://xxx.com/}mine"/>`, ""},
		// Outside the list. "json" and "adaptive" are @method's, not this
		// attribute's, and are refused here exactly as an invented name is.
		{"json-node invented",
			`<xsl:output json-node-output-method="nonsense"/>`, "XTSE0020"},
		{"json-node json", `<xsl:output json-node-output-method="json"/>`, "XTSE0020"},
		{"json-node adaptive",
			`<xsl:output json-node-output-method="adaptive"/>`, "XTSE0020"},
		// parameter-document is a URI: a relative reference and an absolute
		// one are both legal, and only a value that cannot be a URI is not.
		{"parameter-document relative",
			`<xsl:output parameter-document="params.xml"/>`, ""},
		{"parameter-document absolute",
			`<xsl:output parameter-document="http://xxx.com/p.xml"/>`, ""},
		{"parameter-document bad scheme",
			`<xsl:output parameter-document=":::no scheme"/>`, "XTSE0020"},
		{"parameter-document bad escape",
			`<xsl:output parameter-document="p%zzq.xml"/>`, "XTSE0020"},
		{"parameter-document control character",
			"<xsl:output parameter-document=\"p\x7fq.xml\"/>", "XTSE0020"},
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
