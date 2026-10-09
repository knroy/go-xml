package xslt

import (
	"context"
	"strings"
	"testing"
)

// XSLT's dynamic state (the runtime, fn:current(), the dynamic-call marker and
// the known-cleared components) rides on the XPath context's host state rather
// than in reserved variables. These cases sit on the boundaries where the two
// could part: a dynamic call, a named reference's retained focus, an inline
// function's closure, and a stylesheet function's cleared grouping, merge and
// regex components.

const hostSheetHead = `<xsl:stylesheet version="3.0"
	xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	xmlns:xs="http://www.w3.org/2001/XMLSchema"
	xmlns:f="urn:f" exclude-result-prefixes="xs f">
	<xsl:output omit-xml-declaration="yes"/>`

func runHostSheet(t *testing.T, body, doc string) (string, error) {
	t.Helper()
	s := compileString(t, hostSheetHead+body+`</xsl:stylesheet>`)
	res, err := s.Transform(context.Background(), parseDoc(t, doc), TransformOptions{})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

func wantOutput(t *testing.T, body, doc, want string) {
	t.Helper()
	got, err := runHostSheet(t, body, doc)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func wantError(t *testing.T, body, doc, code, text string) {
	t.Helper()
	got, err := runHostSheet(t, body, doc)
	if err == nil {
		t.Fatalf("got %q, want %s", got, code)
	}
	if !strings.Contains(err.Error(), code) || !strings.Contains(err.Error(), text) {
		t.Fatalf("got %v, want %s (%s)", err, code, text)
	}
}

// current#0 called dynamically is XTDE1360, whether the item was made by a
// named reference (whose retained focus must not carry current() along) or
// by function-lookup, and however deep the dynamic call sits.
func TestCurrentAcrossDynamicCall(t *testing.T) {
	const doc = `<d><e/></d>`
	for _, sel := range []string{
		`current#0()`,
		`let $f := current#0 return $f()`,
		`function-lookup(QName('http://www.w3.org/2005/xpath-functions', 'current'), 0)()`,
		`for-each(1, function($x) { current#0() })`,
	} {
		wantError(t, `<xsl:template match="e"><r><xsl:value-of select="`+sel+
			`"/></r></xsl:template>`, doc, "XTDE1360", "dynamic function call")
	}
	// The same reference held in a variable and called inside a predicate,
	// where the focus has moved: still absent, not the reference's node.
	wantError(t, `<xsl:template match="e"><xsl:variable name="f" select="current#0"/>
		<r><xsl:value-of select="//e[$f()]"/></r></xsl:template>`, doc,
		"XTDE1360", "dynamic function call")
}

// An inline function's body runs in the scope it was written in, current()
// included: the dynamic call that invokes it does not reach the body.
func TestCurrentInInlineFunctionIsCaptured(t *testing.T) {
	wantOutput(t, `<xsl:template match="e">
		<xsl:variable name="f" select="function() { name(current()) }"/>
		<r><xsl:value-of select="//d[$f() = 'e']/name()"/></r>
	</xsl:template>`, `<d><e/></d>`, `<r>d</r>`)
}

// A stylesheet function called from inside a grouping sees no group, and
// neither does a function it calls from inside a grouping of its own: the
// outer function's own group must not leak into the inner call through the
// knowledge that the grouping was cleared.
func TestGroupingClearedInNestedFunctionBodies(t *testing.T) {
	const doc = `<d><i k="a"/><i k="b"/></d>`
	const funcs = `
		<xsl:function name="f:inner"><xsl:sequence select="count(current-group())"/></xsl:function>
		<xsl:function name="f:outer">
			<xsl:for-each-group select="$items" group-by="@k">
				<xsl:sequence select="f:inner()"/>
			</xsl:for-each-group>
		</xsl:function>`
	wantError(t, `<xsl:variable name="items" select="//i"/>`+funcs+
		`<xsl:template match="/"><r><xsl:value-of select="f:outer()"/></r></xsl:template>`,
		doc, "XTDE1061", "")
	// Directly inside the function's own grouping the group is there.
	wantOutput(t, `<xsl:variable name="items" select="//i"/>
		<xsl:function name="f:g">
			<xsl:for-each-group select="$items" group-by="@k">
				<xsl:sequence select="count(current-group())"/>
			</xsl:for-each-group>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="f:g()"/></r></xsl:template>`,
		doc, `<r>1 1</r>`)
}

// The same for the captured substrings: regex-group() in a function called
// from inside another function's xsl:analyze-string is the empty string.
func TestRegexGroupsClearedInNestedFunctionBodies(t *testing.T) {
	wantOutput(t, `
		<xsl:function name="f:inner"><xsl:sequence select="'[' || regex-group(1) || ']'"/></xsl:function>
		<xsl:function name="f:outer">
			<xsl:analyze-string select="'ab'" regex="(a)">
				<xsl:matching-substring>
					<xsl:sequence select="regex-group(1) || f:inner()"/>
				</xsl:matching-substring>
			</xsl:analyze-string>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="f:outer()"/></r></xsl:template>`,
		`<d/>`, `<r>a[]</r>`)
}

// And for merging: current-merge-group() in a function called from an
// xsl:merge-action inside another function is XTDE3480.
func TestMergeClearedInNestedFunctionBodies(t *testing.T) {
	wantError(t, `
		<xsl:function name="f:inner"><xsl:sequence select="count(current-merge-group())"/></xsl:function>
		<xsl:function name="f:outer">
			<xsl:merge>
				<xsl:merge-source select="(1, 2)">
					<xsl:merge-key select="."/>
				</xsl:merge-source>
				<xsl:merge-action><xsl:sequence select="f:inner()"/></xsl:merge-action>
			</xsl:merge>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="f:outer()"/></r></xsl:template>`,
		`<d/>`, "XTDE3480", "")
}
