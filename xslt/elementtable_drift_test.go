package xslt

import (
	"fmt"
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

// TestExposeSummary pins xsl:expose to its syntax summary: visibility is
// public|private|final|abstract with no "hidden" (that is xsl:accept's), and
// section 3.5 allows the element "only as a child of xsl:package".
func TestExposeSummary(t *testing.T) {
	pkg := func(vis string) string {
		return `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://x/" name="http://x/p" version="3.0">
		  <xsl:expose component="function" names="*" visibility="` + vis + `"/>
		  <xsl:function name="x:f"><xsl:sequence select="1"/></xsl:function>
		</xsl:package>`
	}
	for _, c := range []struct{ src, code string }{
		{pkg("public"), ""},
		{pkg("final"), ""},
		{pkg("hidden"), "XTSE0020"},
		{`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:expose component="function" names="*" visibility="public"/>
		</xsl:stylesheet>`, "XTSE0010"},
	} {
		err := compileDrift(t, c.src)
		switch {
		case c.code == "" && err != nil:
			t.Errorf("refused: %v\n%s", err, c.src)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("want %s, got %v\n%s", c.code, err, c.src)
		}
	}
}

// TestEnumeratedAVTs pins the summaries of four attribute value templates
// the table left open: xsl:result-document's @method and
// @json-node-output-method, and @data-type on xsl:sort and xsl:merge-key.
// Each is an enumeration united with eqname, so a literal outside it is the
// static XTSE0020 and a computed one the dynamic XTDE0030; a prefixed name
// is an implementation-defined value and passes.
func TestEnumeratedAVTs(t *testing.T) {
	sheet := func(body string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://x/" version="3.0">
		  <xsl:variable name="v" select="'bogus'"/>
		  <xsl:template name="xsl:initial-template">` + body + `</xsl:template>
		</xsl:stylesheet>`
	}
	rd := func(attr string) string {
		return `<xsl:result-document href="r.xml" ` + attr + `><r/></xsl:result-document><out/>`
	}
	sort := func(dt string) string {
		return `<out><xsl:for-each select="3, 1, 2"><xsl:sort select="." data-type="` +
			dt + `"/><xsl:value-of select="."/></xsl:for-each></out>`
	}
	for _, c := range []struct{ body, code string }{
		{rd(`method="text"`), ""},
		{rd(`method="json"`), ""},
		{rd(`method="bogus"`), "XTSE0020"},
		{rd(`method="{$v}"`), "XTDE0030"},
		{rd(`method="{'xml'}"`), ""},
		{rd(`json-node-output-method="html"`), ""},
		{rd(`json-node-output-method="json"`), "XTSE0020"},
		{rd(`json-node-output-method="{$v}"`), "XTDE0030"},
		{sort("number"), ""},
		{sort("bogus"), "XTSE0020"},
		{sort("{$v}"), "XTDE0030"},
		{sort("{'text'}"), ""},
		{`<xsl:merge><xsl:merge-source select="1 to 3">
		    <xsl:merge-key select="." data-type="bogus"/></xsl:merge-source>
		  <xsl:merge-action><out/></xsl:merge-action></xsl:merge>`, "XTSE0020"},
	} {
		err := compileAttrSetSheet(t, sheet(c.body))
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s: refused: %v", c.body, err)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("%s: want %s, got %v", c.body, c.code, err)
		}
	}
	// A prefixed name compiles; what it means is implementation-defined.
	for _, body := range []string{rd(`method="x:mine"`), sort("x:mine")} {
		if err := compileDrift(t, sheet(body)); err != nil {
			t.Errorf("%s: refused: %v", body, err)
		}
	}
}

// TestStandardAttrValuesEverywhere pins section 3.5's standard attributes to
// their types on any XSLT element, not only on the module element whose
// table entry already checked them.
func TestStandardAttrValuesEverywhere(t *testing.T) {
	sheet := func(rootAttr, tmplAttr string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://x/" version="3.0" ` + rootAttr + `>
		  <xsl:mode name="x:m"/>
		  <xsl:template name="t" ` + tmplAttr + `/>
		</xsl:stylesheet>`
	}
	for _, c := range []struct{ root, tmpl, code string }{
		{``, `version="2.0"`, ""},
		{``, `default-validation="strip"`, ""},
		{``, `default-mode="x:m"`, ""},
		{``, `default-mode="#unnamed"`, ""},
		{`default-mode="x:m"`, ``, ""},
		{``, `version="abc"`, "XTSE0110"},
		{``, `default-validation="lax"`, "XTSE0020"},
		{``, `default-mode="#bogus"`, "XTSE0020"},
		{`default-mode="#bogus"`, ``, "XTSE0020"},
	} {
		err := compileDrift(t, sheet(c.root, c.tmpl))
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s %s: refused: %v", c.root, c.tmpl, err)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("%s %s: want %s, got %v", c.root, c.tmpl, c.code, err)
		}
	}
	// xsl:output/@version is the output method's, any NMTOKEN, not this one.
	out := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    version="3.0"><xsl:output method="html" version="5"/></xsl:stylesheet>`
	if err := compileDrift(t, out); err != nil {
		t.Errorf("xsl:output version=\"5\" refused: %v", err)
	}
}

// TestNormalizationFormIsNMTOKEN pins the summary's nmtoken: a literal that
// is not one is XTSE0020 when compiling, where it used to reach the
// serialiser as SESU0011. A well-formed form this engine does not support is
// still the serialiser's question.
func TestNormalizationFormIsNMTOKEN(t *testing.T) {
	for _, c := range []struct{ body, code string }{
		{`<xsl:output normalization-form="NFC"/>`, ""},
		{`<xsl:output normalization-form="weird"/>`, ""},
		{`<xsl:output normalization-form="not an nmtoken!"/>`, "XTSE0020"},
		{`<xsl:template name="t"><xsl:result-document href="r.xml"
		    normalization-form="bad form"/></xsl:template>`, "XTSE0020"},
		{`<xsl:template name="t"><xsl:result-document href="r.xml"
		    normalization-form="{'NFC'}"/></xsl:template>`, ""},
	} {
		err := compileDrift(t, `<xsl:stylesheet version="3.0"
		    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+c.body+`</xsl:stylesheet>`)
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s: refused: %v", c.body, err)
		case c.code != "" && (err == nil || !strings.Contains(err.Error(), c.code)):
			t.Errorf("%s: want %s, got %v", c.body, c.code, err)
		}
	}
}

// TestTwoPointZeroAttributes pins the table's version conventions for the
// attributes the drift audit found unmarked. xsl:function/@override is a 2.0
// attribute, so a 2.0 module gets the 2.0 pair only; and three 3.0
// attributes are not attributes at all to a 2.0 processor (MaxVersion 2.0).
// xsl:output's html-version, item-separator and suppress-indentation stay
// accepted there: output-0724..0726 and validation-0214/0215 are XSLT20+
// cases that write them in version="2.0" modules.
func TestTwoPointZeroAttributes(t *testing.T) {
	compile := func(src string, max float64) error {
		_, err := Compile(mustParse(t, src), CompileOptions{MaxVersion: max})
		return err
	}
	fn := func(ver, override string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://x/" version="` + ver + `">
		  <xsl:function name="x:f" override="` + override + `"><xsl:sequence select="1"/></xsl:function>
		</xsl:stylesheet>`
	}
	if err := compile(fn("2.0", "true"), 0); err == nil || !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf(`override="true" at 2.0: want XTSE0020, got %v`, err)
	}
	for _, c := range []struct{ ver, v string }{{"2.0", "yes"}, {"3.0", "true"}} {
		if err := compile(fn(c.ver, c.v), 0); err != nil {
			t.Errorf(`override="%s" at %s refused: %v`, c.v, c.ver, err)
		}
	}
	for _, body := range []string{
		`<xsl:template name="t" visibility="public"/>`,
		`<xsl:template name="t"><xsl:copy-of select="." copy-accumulators="yes"/></xsl:template>`,
		`<xsl:template name="t"><xsl:message error-code="x:e"/></xsl:template>`,
	} {
		src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://x/" version="2.0">` + body + `</xsl:stylesheet>`
		if err := compile(src, 2.0); err == nil || !strings.Contains(err.Error(), "XTSE0090") {
			t.Errorf("%s under a 2.0 processor: want XTSE0090, got %v", body, err)
		}
		if err := compile(src, 0); err != nil {
			t.Errorf("%s under a 3.0 processor refused: %v", body, err)
		}
	}
}

// TestPackageVersionRequired pins section 3.5's summary, version = decimal
// with no "?": a package without one is refused, as a stylesheet is.
func TestPackageVersionRequired(t *testing.T) {
	const pkg = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    name="http://x/p"%s><xsl:template name="t"/></xsl:package>`
	if err := compileDrift(t, fmt.Sprintf(pkg, ` version="3.0"`)); err != nil {
		t.Errorf("package with version refused: %v", err)
	}
	err := compileDrift(t, fmt.Sprintf(pkg, ``))
	if err == nil || !strings.Contains(err.Error(), "XTSE0010") {
		t.Errorf("package without version: want XTSE0010, got %v", err)
	}
}
