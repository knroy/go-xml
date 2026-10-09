package xslt

import "testing"

// XSLT 3.0 section 6.8: "The current template rule is cleared (becomes
// absent) by any instruction that evaluates an operand with changed focus. It
// is therefore cleared when evaluating instructions contained within:
// xsl:for-each, xsl:for-each-group, xsl:analyze-string, xsl:iterate,
// xsl:source-document, xsl:merge, xsl:sort, xsl:key, xsl:copy if and only if
// there is a select attribute, ..." XSLT 2.0 section 6.7 lists xsl:sort as
// well. Each case below keeps the rule's own item in the focus, so only the
// clearing, and not a changed context item, can raise XTDE0560.
func TestCurrentRuleClearedByFocusChangingInstructions(t *testing.T) {
	const rest = `<xsl:template match="e" priority="1"><low/></xsl:template>
		<xsl:template match="/" priority="-5"><xsl:apply-templates select="e"/></xsl:template>`
	for name, rule := range map[string]string{
		"iterate": `<xsl:iterate select="."><xsl:next-match/></xsl:iterate>`,
		"iterate apply-imports": `<xsl:iterate select="."><xsl:apply-imports/></xsl:iterate>`,
		"merge-action": `<xsl:merge><xsl:merge-source select="."><xsl:merge-key select="1"/></xsl:merge-source>
			<xsl:merge-action><xsl:next-match/></xsl:merge-action></xsl:merge>`,
		"merge-key": `<xsl:merge><xsl:merge-source select="."><xsl:merge-key><xsl:next-match/></xsl:merge-key></xsl:merge-source>
			<xsl:merge-action><x/></xsl:merge-action></xsl:merge>`,
		"copy select": `<xsl:copy select="."><xsl:next-match/></xsl:copy>`,
		"perform-sort": `<xsl:perform-sort select="(., .)"><xsl:sort><xsl:next-match/></xsl:sort></xsl:perform-sort>`,
		"for-each sort": `<xsl:for-each select="(., .)"><xsl:sort><xsl:next-match/></xsl:sort><x/></xsl:for-each>`,
		"apply-templates sort": `<xsl:apply-templates select="(., .)" mode="m"><xsl:sort><xsl:next-match/></xsl:sort></xsl:apply-templates>`,
	} {
		t.Run(name, func(t *testing.T) {
			wantError(t, `<xsl:template match="e" priority="2">`+rule+`</xsl:template>`+rest+
				`<xsl:template match="e" mode="m"><m/></xsl:template>`, `<e/>`, "XTDE0560", "outside a template rule")
		})
	}
	// Without select, xsl:copy keeps the rule.
	wantOutput(t, `<xsl:template match="e" priority="2"><xsl:copy><xsl:next-match/></xsl:copy></xsl:template>`+rest,
		`<e/>`, `<e><low/></e>`)
}
