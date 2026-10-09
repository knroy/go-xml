package xslt

import "testing"

// A prefixed $calendar is expanded against the statically known namespaces of
// the expression that holds the format-date call (F&O 9.8.4.3), not those of
// whatever expression happens to be evaluating it: here the prefix is bound
// only where the function is declared, and the call is made from a template
// that does not bind it.
func TestCalendarPrefixUsesExpressionNamespaces(t *testing.T) {
	wantOutput(t, `
		<xsl:function name="f:fmt" xmlns:c="urn:cal">
			<xsl:sequence select="format-date(xs:date('2020-01-02'), '[D]', (), 'c:x', ())"/>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="f:fmt()"/></r></xsl:template>`,
		`<d/>`, `<r>[Calendar: AD]2</r>`)
	// The answer is the one the call gives where it is written with the
	// prefix in scope.
	wantOutput(t, `<xsl:template match="/" xmlns:c="urn:cal" exclude-result-prefixes="c"><r><xsl:value-of
		select="format-date(xs:date('2020-01-02'), '[D]', (), 'c:x', ())"/></r></xsl:template>`,
		`<d/>`, `<r>[Calendar: AD]2</r>`)
}
