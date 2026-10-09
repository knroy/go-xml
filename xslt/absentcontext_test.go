package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// A template or function call skips re-clearing context components that are
// already absent. These are the boundaries that shortcut must not move:
// sections 5.4, 14.4 (XTDE1061), 15.6 (XTDE3480, XTDE3510) and 16.6.1. Each
// case clears a component, re-establishes it, then clears it again through a
// second call, so a cleared state carried across the re-binding would show.
func TestAbsentContextAfterRebinding(t *testing.T) {
	const head = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="f xs">
		<xsl:output method="text"/>`
	const src = `<r><item n="1"/><item n="2"/></r>`
	cases := []struct{ name, body, want, wantErr string }{
		{"current-group in a function called from a grouping in a function", `
			<xsl:function name="f:outer"><xsl:for-each-group select="1, 2" group-by=".">
				<xsl:sequence select="f:inner()"/></xsl:for-each-group></xsl:function>
			<xsl:function name="f:inner"><xsl:sequence select="current-group()"/></xsl:function>
			<xsl:template match="/"><xsl:value-of select="f:outer()"/></xsl:template>`,
			"", "XTDE1061"},
		{"current-group in the grouping itself still works", `
			<xsl:function name="f:outer"><xsl:for-each-group select="1, 2" group-by=".">
				<xsl:sequence select="current-group()"/></xsl:for-each-group></xsl:function>
			<xsl:template match="/"><xsl:value-of select="f:outer()"/></xsl:template>`,
			"1 2", ""},
		{"regex-group in a function called from analyze-string in a function", `
			<xsl:function name="f:outer"><xsl:analyze-string select="'ab'" regex="(a)">
				<xsl:matching-substring><xsl:sequence select="'[' || regex-group(1) || f:inner() || ']'"/></xsl:matching-substring>
			</xsl:analyze-string></xsl:function>
			<xsl:function name="f:inner" as="xs:string"><xsl:sequence select="string(regex-group(1))"/></xsl:function>
			<xsl:template match="/"><xsl:value-of select="f:outer()"/></xsl:template>`,
			"[a]", ""},
		{"regex-group in a grouping pattern inside analyze-string in a function", `
			<xsl:function name="f:outer"><xsl:param name="r"/>
				<xsl:analyze-string select="'2'" regex="(2)"><xsl:matching-substring>
					<xsl:for-each-group select="$r/item" group-starting-with="item[@n = string(regex-group(1))]">
						<xsl:sequence select="'g'"/></xsl:for-each-group>
				</xsl:matching-substring></xsl:analyze-string></xsl:function>
			<xsl:template match="/"><xsl:value-of select="f:outer(r)" separator=""/></xsl:template>`,
			"g", ""},
		{"current-merge-group in a template called from a merge in a called template", `
			<xsl:template name="outer">
				<xsl:merge><xsl:merge-source select="1, 2"><xsl:merge-key select="."/></xsl:merge-source>
				<xsl:merge-action><xsl:call-template name="inner"/></xsl:merge-action></xsl:merge></xsl:template>
			<xsl:template name="inner"><xsl:value-of select="current-merge-group()"/></xsl:template>
			<xsl:template match="/"><xsl:call-template name="outer"/></xsl:template>`,
			"", "XTDE3480"},
		{"current-merge-key in a template called from a merge in a called template", `
			<xsl:template name="outer">
				<xsl:merge><xsl:merge-source select="1, 2"><xsl:merge-key select="."/></xsl:merge-source>
				<xsl:merge-action><xsl:call-template name="inner"/></xsl:merge-action></xsl:merge></xsl:template>
			<xsl:template name="inner"><xsl:value-of select="current-merge-key()"/></xsl:template>
			<xsl:template match="/"><xsl:call-template name="outer"/></xsl:template>`,
			"", "XTDE3510"},
		{"current() in a pattern is the node being matched", `
			<xsl:template match="/"><xsl:for-each select="r"><xsl:apply-templates select="item"/></xsl:for-each></xsl:template>
			<xsl:template match="item[current() is .][empty(current-output-uri())]">M</xsl:template>
			<xsl:template match="item">-</xsl:template>`,
			"MM", ""},
		{"current() in xsl:number count compares each candidate with itself", `
			<xsl:template match="/"><xsl:for-each select="r/item[2]">
				<xsl:number count="item[@n = current()/@n]"/></xsl:for-each></xsl:template>`,
			"2", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := runErr(t, head+c.body+`</xsl:stylesheet>`, src)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %q, %v; want %s", got, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The shortcuts themselves: a second clear of an absent component returns the
// runtime it was given, rebinding the component makes the next clear real
// again, and only a pattern that evaluates no expression skips its bindings.
func TestAbsentClearShortcut(t *testing.T) {
	rt := &runtime{ctx: xpath.NewContext(nil, nil)}
	a := rt.clearFunctionContext()
	if a.clearFunctionContext() != a || a.clearMergeContext() != a ||
		a.withoutGroupingScope() != a || a.clearRegexGroups() != a {
		t.Error("clearing absent components copied the runtime")
	}
	one := xdm.One(xdm.NewBoolean(true))
	if b := a.withVar(currentMergeKeyVar, one); b.clearMergeContext() == b {
		t.Error("clearMergeContext skipped after the merge key was rebound")
	}
	if b := a.withGroupingScope(one, one); b.withoutGroupingScope() == b {
		t.Error("withoutGroupingScope skipped after a grouping was bound")
	}
	if b := a.withVar(regexGroupsVar, one); b.clearRegexGroups() == b {
		t.Error("clearRegexGroups skipped after the groups were rebound")
	}
	for src, want := range map[string]bool{
		"item": true, "a/b | c//d": true, "/": true,
		"item[1]": false, "a[@x]/b": false, "key('k', 'v')": false,
	} {
		p, err := CompilePattern(src, nil)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got := p.predicateFree(); got != want {
			t.Errorf("%s: predicateFree = %v, want %v", src, got, want)
		}
	}
}
