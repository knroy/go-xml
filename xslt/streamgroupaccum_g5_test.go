package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamGroupMemberAccumAfter: apply-templates over a streamed group's
// members into a rule that reads accumulator-after of its focus, which
// nothing else (or a consumer that may not run) has read: the rule reads the
// member to its end, as it would anywhere else. Fuzz seed 92048
// (TestStreamFuzzMain): TransformReader raised "a context read twice: an
// accumulator value the stream does not hold" where Parse + Transform gives
// output.
func TestStreamGroupMemberAccumAfter(t *testing.T) {
	accums := `<xsl:accumulator name="a" as="xs:string*" initial-value="()" streamable="yes">` +
		`<xsl:accumulator-rule match="sub" select="($value, 's')"/></xsl:accumulator>`
	group := `<xsl:template match="*" priority="0"><b n="{name()}"><xsl:for-each-group select="p:rec" group-starting-with="*[@v = '1']">` +
		`<g p="{position()}"><xsl:apply-templates select="current-group()"/></g></xsl:for-each-group></b></xsl:template>`
	rules := []string{
		// The seed's: a consumer that may not run, then -after.
		`<xsl:template priority="9" match="p:rec"><xsl:if test="@k = 'b'"><xsl:apply-templates/></xsl:if><e a="{string-join(accumulator-after('a'), ',')}"/></xsl:template>`,
		// -after the one reader.
		`<xsl:template priority="9" match="p:rec"><e a="{count(accumulator-after('a'))}"/></xsl:template>`,
		// A consumer that runs, then -after.
		`<xsl:template priority="9" match="p:rec"><xsl:apply-templates/><e a="{count(accumulator-after('a'))}"/></xsl:template>`,
	}
	docs := []string{
		`<root xmlns:p="urn:p"><p:rec k="10" v="1e3"><name>2</name></p:rec><p:rec k="1" v="2.5"><x>-1</x></p:rec></root>`,
		`<root xmlns:p="urn:p"><p:rec k="b" v="1"><sub/><sub/></p:rec><p:rec k="c" v="2"><sub/></p:rec><p:rec k="b" v="1"/></root>`,
	}
	for ri, r := range rules {
		sheet := mainSheet(`<xsl:mode use-accumulators="#all" streamable="yes" on-no-match="shallow-copy"/>` + accums + r + group)
		for di, d := range docs {
			so, st := mainRun(sheet, d, true, TransformOptions{})
			to, _ := mainRun(sheet, d, false, TransformOptions{})
			if so != to {
				t.Errorf("rule %d doc %d:\nstream %q\ntree   %q", ri, di, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("rule %d doc %d: %d guard hits", ri, di, st.Guards)
			}
			if st.Runs[streamtrace.Main] == 0 {
				t.Errorf("rule %d doc %d: not streamed", ri, di)
			}
		}
	}
}
