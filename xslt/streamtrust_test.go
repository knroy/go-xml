package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The m10 soundness lane's reproducers: stylesheets the §19.8 analysis
// accepted with a known, grounded verdict although a streamed evaluation
// would need data it had already passed. Each must now either be refused
// (a syntactic hole, closed by a spec rule), compile with its verdict marked
// unverified (a declaration hole: the verdict rests on a declaration whose own
// check gave up, or that another module can override), or compile with an
// unknown verdict -- and none may reach the burst path. The "verified" rows
// are controls: the same declarations written so that their checks pass,
// which must not be marked.
func TestStreamTrustReproducers(t *testing.T) {
	const hdr = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f" xmlns:ext="urn:ext" exclude-result-prefixes="f xs ext">`
	const end = `</xsl:stylesheet>`
	sd := func(body string) string {
		return `<xsl:template name="main"><out><xsl:source-document streamable="yes" href="d.xml">` + body + `</xsl:source-document></out></xsl:template>`
	}
	sdAcc := func(body string) string {
		return `<xsl:template name="main"><out><xsl:source-document streamable="yes" href="d.xml" use-accumulators="a">` + body + `</xsl:source-document></out></xsl:template>`
	}
	fe := func(b string) string { return `<xsl:for-each select="doc/rec">` + b + `</xsl:for-each>` }
	const mode = `<xsl:mode name="s" streamable="yes"/>`
	apply := sd(`<xsl:apply-templates select="doc/rec" mode="s"/>`)
	const sib = `following-sibling::rec[1]/@k`

	for _, tc := range []struct {
		name  string
		want  string            // unverified, verified, unknown, or XTSE3430
		files map[string]string // other modules, by name
		main  string
		in    string // the module holding the xsl:source-document, if not main
	}{
		// Declared-streamable functions whose bodies the check skipped.
		{"F1 absorbing fn, where-populated body", "unverified", nil,
			hdr + `<xsl:function name="f:g" streamability="absorbing"><xsl:param name="n" as="node()"/><xsl:where-populated><w><xsl:value-of select="$n/` + sib + `"/></w></xsl:where-populated></xsl:function>` +
				sd(fe(`<v><xsl:copy-of select="f:g(.)"/></v>`)) + end, ""},
		{"F1b absorbing fn, two-instruction body", "unverified", nil,
			hdr + `<xsl:function name="f:g" streamability="absorbing" as="xs:string"><xsl:param name="n" as="node()"/><xsl:variable name="v" select="1"/><xsl:sequence select="string($n/` + sib + `)"/></xsl:function>` +
				sd(fe(`<v><xsl:value-of select="f:g(.)"/></v>`)) + end, ""},
		{"F1c inspection fn, choose body", "unverified", nil,
			hdr + `<xsl:function name="f:g" streamability="inspection" as="xs:string"><xsl:param name="n" as="node()"/><xsl:choose><xsl:when test="true()"><xsl:sequence select="string($n/../rec[last()]/@k)"/></xsl:when></xsl:choose></xsl:function>` +
				sd(fe(`<v><xsl:value-of select="f:g(.)"/></v>`)) + end, ""},
		{"F1 control: absorbing fn, modelled body", "verified", nil,
			hdr + `<xsl:function name="f:g" streamability="absorbing" as="xs:string"><xsl:param name="n" as="node()"/><xsl:sequence select="string($n/@k)"/></xsl:function>` +
				sd(fe(`<v><xsl:value-of select="f:g(.)"/></v>`)) + end, ""},
		// A streamable attribute set and accumulators whose checks gave up.
		{"F1d streamable attribute set via concat/2", "unverified", nil,
			hdr + `<xsl:attribute-set name="as" streamable="yes"><xsl:attribute name="a" select="concat(string(` + sib + `), '')"/></xsl:attribute-set>` +
				sd(fe(`<v xsl:use-attribute-sets="as"/>`)) + end, ""},
		{"F1d control: motionless attribute set", "verified", nil,
			hdr + `<xsl:attribute-set name="as" streamable="yes"><xsl:attribute name="a" select="string(@k)"/></xsl:attribute-set>` +
				sd(fe(`<v xsl:use-attribute-sets="as"/>`)) + end, ""},
		{"F1e accumulator rule select via concat/2", "unverified", nil,
			hdr + `<xsl:accumulator name="a" initial-value="''" streamable="yes"><xsl:accumulator-rule match="rec" select="concat(string(` + sib + `), '')"/></xsl:accumulator>` +
				sdAcc(fe(`<v><xsl:value-of select="accumulator-before('a')"/></v>`)) + end, ""},
		{"A1 accumulator rule with a sequence constructor", "unverified", nil,
			hdr + `<xsl:accumulator name="a" initial-value="''" streamable="yes"><xsl:accumulator-rule match="rec"><xsl:sequence select="string(` + sib + `)"/></xsl:accumulator-rule></xsl:accumulator>` +
				sdAcc(fe(`<v><xsl:value-of select="accumulator-before('a')"/></v>`)) + end, ""},
		{"A1 control: motionless accumulator", "verified", nil,
			hdr + `<xsl:accumulator name="a" initial-value="''" streamable="yes"><xsl:accumulator-rule match="rec" select="string(@k)"/></xsl:accumulator>` +
				sdAcc(fe(`<v><xsl:value-of select="accumulator-before('a')"/></v>`)) + end, ""},
		// Template rules of a streamable mode that its check did not hold
		// to §6.6.4.
		{"M1 mode=#all rule", "unverified", nil,
			hdr + mode + `<xsl:template match="rec" mode="#all"><v><xsl:value-of select="` + sib + `"/></v></xsl:template>` + apply + end, ""},
		{"M2 rule parameter default", "unverified", nil,
			hdr + mode + `<xsl:template match="rec" mode="s"><xsl:param name="p" select="string(` + sib + `)"/><v p="{$p}"/></xsl:template>` + apply + end, ""},
		{"M3 pattern withheld: numeric literal in a comparison", "unverified", nil,
			hdr + mode + `<xsl:template match="rec[following-sibling::rec/@k = 3]" mode="s"><hit k="{@k}"/></xsl:template>` + apply + end, ""},
		{"M3b pattern withheld: rec[x = 1]", "unverified", nil,
			hdr + mode + `<xsl:template match="rec[x = 1]" mode="s"><xsl:copy-of select="."/></xsl:template>` + apply + end, ""},
		{"M3d group-starting-with withheld", "unverified", nil,
			hdr + sd(`<xsl:for-each-group select="doc/rec" group-starting-with="rec[following-sibling::rec/@k = 3]"><g n="{count(current-group())}"/></xsl:for-each-group>`) + end, ""},
		{"M4 rule pattern unknown", "unverified", nil,
			hdr + mode + `<xsl:template match="rec[concat(` + sib + `, '') = '3']" mode="s"><hit k="{@k}"/></xsl:template>` + apply + end, ""},
		{"M5 rule body unknown", "unverified", nil,
			hdr + mode + `<xsl:template match="rec" mode="s"><xsl:where-populated><v><xsl:value-of select="` + sib + `"/></v></xsl:where-populated></xsl:template>` + apply + end, ""},
		{"M5 control: grounded rule", "verified", nil,
			hdr + mode + `<xsl:template match="rec" mode="s"><v><xsl:value-of select="@k"/></v></xsl:template>` + apply + end, ""},
		// Another module can add rules or override declarations.
		{"M6 rule in an included module", "unverified",
			map[string]string{"b.xsl": hdr + `<xsl:template match="rec" mode="s"><v><xsl:value-of select="` + sib + `"/></v></xsl:template>` + end},
			hdr + `<xsl:include href="b.xsl"/>` + mode + apply + end, ""},
		{"M7 apply-imports into an imported rule", "unverified",
			map[string]string{"b.xsl": hdr + `<xsl:template match="rec" mode="s"><v><xsl:value-of select="` + sib + `"/></v></xsl:template>` + end},
			hdr + `<xsl:import href="b.xsl"/>` + mode + `<xsl:template match="rec" mode="s"><w><xsl:apply-imports/></w></xsl:template>` + apply + end, ""},
		{"P1 named template overridden by the importer", "unverified",
			map[string]string{"b.xsl": hdr + `<xsl:template name="t"><xsl:context-item use="absent"/><v/></xsl:template>` +
				sd(fe(`<xsl:call-template name="t"/>`)) + end},
			hdr + `<xsl:import href="b.xsl"/><xsl:template name="t"><v><xsl:value-of select="` + sib + `"/></v></xsl:template>` + end, "b.xsl"},
		{"P2 function overridden by the importer", "unverified",
			map[string]string{"b.xsl": hdr + `<xsl:function name="f:g" streamability="inspection" as="xs:string"><xsl:param name="n" as="node()"/><xsl:sequence select="name($n)"/></xsl:function>` +
				sd(fe(`<v><xsl:value-of select="f:g(.)"/></v>`)) + end},
			hdr + `<xsl:import href="b.xsl"/><xsl:function name="f:g" as="xs:string"><xsl:param name="n" as="node()"/><xsl:sequence select="string($n/` + sib + `)"/></xsl:function>` + end, "b.xsl"},
		// Operands the analysis skipped, now assessed by their own rules.
		{"S1 xsl:map-entry nested in xsl:for-each inside xsl:map", "XTSE3430", nil,
			hdr + sd(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:for-each select="doc/rec"><xsl:map-entry key="string(@k)" select="."/></xsl:for-each></xsl:map></xsl:variable><xsl:copy-of select="$m('1')/following-sibling::rec[1]"/>`) + end, ""},
		{"S1 control: xsl:map-entry with a grounded value", "verified", nil,
			hdr + sd(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:for-each select="doc/rec"><xsl:map-entry key="string(@k)" select="string(.)"/></xsl:for-each></xsl:map></xsl:variable><v><xsl:value-of select="$m('1')"/></v>`) + end, ""},
		{"S2 an extension instruction's xsl:fallback", "XTSE3430", nil,
			hdr + sd(fe(`<ext:op xsl:extension-element-prefixes="ext"><xsl:fallback><v><xsl:value-of select="`+sib+`"/></v></xsl:fallback></ext:op>`)) + end, ""},
		{"S2 control: a grounded xsl:fallback", "verified", nil,
			hdr + sd(fe(`<ext:op xsl:extension-element-prefixes="ext"><xsl:fallback><v><xsl:value-of select="@k"/></v></xsl:fallback></ext:op>`)) + end, ""},
		{"S3 xsl:result-document serialization AVT", "XTSE3430", nil,
			hdr + sd(`<xsl:result-document href="o.xml" method="xml" item-separator="{string(doc)}"><xsl:copy-of select="doc/rec"/></xsl:result-document>`) + end, ""},
		{"S3 control: literal serialization attributes", "verified", nil,
			hdr + sd(`<xsl:result-document href="o.xml" method="xml" item-separator="|"><xsl:copy-of select="doc/rec"/></xsl:result-document>`) + end, ""},
		// The burst path's own shape, which must stay trusted.
		{"control: record loop", "verified", nil,
			hdr + sd(fe(`<v><xsl:value-of select="@k"/></v>`)) + end, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, s string) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("d.xml", `<doc><rec k="1"><x>a</x></rec><rec k="2"><x>b</x></rec><rec k="3"><x>c</x></rec></doc>`)
			for n, s := range tc.files {
				write(n, s)
			}
			_, err := streamTrustRun(t, tc.main, dir)
			if tc.want == "XTSE3430" {
				if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
					t.Fatalf("want XTSE3430, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compile or run: %v", err)
			}
			if burstRuns != (tc.name == "control: record loop") {
				t.Fatalf("burst path taken: %v", burstRuns)
			}
			// The verdict, read as the burst gate reads it.
			src := tc.main
			if tc.in != "" {
				src = tc.files[tc.in]
			}
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var el *xdm.Node
			walkElements(firstElement(tree.Root), func(n *xdm.Node) bool {
				if isXSL(n, "source-document") {
					el = n
				}
				return el == nil
			})
			shared := len(tc.files) > 0
			tr := newDeclTrust(documentElementOf(el), shared)
			_, known, unverified := sequenceConstructorVerdict(el, postureStriding, tr.sets, tr.funcs, tr)
			got := "verified"
			switch {
			case !known:
				got = "unknown"
			case unverified:
				got = "unverified"
			}
			if got != tc.want {
				t.Fatalf("verdict %s, want %s", got, tc.want)
			}
			if burstVerdict(el, shared) != (got == "verified") {
				t.Fatalf("burst gate admits %s verdict", got)
			}
		})
	}
}

// streamTrustRun compiles src as dir/s.xsl, so that its includes and imports
// resolve, and runs template "main" with bursts enabled.
func streamTrustRun(t *testing.T, src, dir string) (string, error) {
	base := fileURIOf(filepath.Join(dir, "s.xsl"))
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	burstRuns = false
	sheet, err := Compile(tree.Root, CompileOptions{BaseURI: base, Resolver: docs})
	if err != nil {
		return "", err
	}
	traceBursts = func(string) { burstRuns = true }
	defer func() { traceBursts = nil }()
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main", Documents: docs})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}
