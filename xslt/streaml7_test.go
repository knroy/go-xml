package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// l7Rows are lane L7's container bodies, with the top-level declarations
// they need; stream says the core must take them.
var l7Rows = []struct {
	decl, body string
	stream     bool
}{
	// A non-streamable accumulator read over the streamed document:
	// XTDE3362 in both modes (nonStreamableAccumulator).
	{`<xsl:accumulator name="na" initial-value="0" streamable="no"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`,
		`<out><xsl:value-of select="accumulator-before('na')"/><xsl:value-of select="count(//rec)"/></out>`, true},
	{`<xsl:accumulator name="na" initial-value="0" streamable="no"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`,
		`<out><xsl:value-of select="count(//rec)"/><xsl:value-of select="accumulator-after('na')"/></out>`, true},
	// xsl:copy of an attribute of the stream against a type (si-copy-116,
	// -117): a detached copy is assessed (streamvalid_l7.go).
	{``, `<o><xsl:copy select="doc/rec[1]/@v" type="xs:decimal"/></o>`, true},
	{``, `<o><xsl:copy select="doc/rec[1]/@k" type="xs:date"/></o>`, true},
	{``, `<o><xsl:copy select="/*/*/@v" type="xs:decimal"/></o>`, true},
}

// l7Analysis are §19.8 verdicts lane L7 models (streamability_l7.go).
var l7Analysis = []struct {
	src   string
	want  props
	known bool
}{
	// root() of a grounded node: the node's posture and sweep (§19.8.9.18).
	{`<xsl:for-each select="copy-of(a)"><xsl:value-of select="count(root()//b)"/></xsl:for-each>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:value-of select="count(root(copy-of(a))//b)"/>`, props{postureGrounded, sweepConsuming}, true},
	// root() of a streamed node stays unmodelled.
	{`<xsl:for-each select="a"><xsl:value-of select="count(root()//b)"/></xsl:for-each>`, props{}, false},
	// fold-right over a grounded, consuming first argument (§19.8.9.9).
	{`<xsl:value-of select="fold-right(a/b/string(), '', concat#2)"/>`, props{postureGrounded, sweepConsuming}, true},
	// Any other first argument stays unmodelled.
	{`<xsl:value-of select="fold-right((), 'd', concat#2)"/>`, props{}, false},
	{`<xsl:value-of select="fold-right(a/b, '', concat#2)"/>`, props{}, false},
}

// TestStreamL7MatchesTree runs l7Rows on every core document and
// whitespace declaration against the tree.
func TestStreamL7MatchesTree(t *testing.T) {
	// The analysis rows (l7Analysis): verdicts, no run.
	for _, c := range l7Analysis {
		p, known := analyzeInstrSource(t, c.src)
		if known != c.known || known && p != c.want {
			t.Errorf("%s: %v/%v known %v, want %v/%v known %v", c.src, p.posture, p.sweep, known, c.want.posture, c.want.sweep, c.known)
		}
	}
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range l7Rows {
				coreCheck(t, coreSheetWith(d+b.decl, b.body), dir, fmt.Sprintf("%s decl %d row %d", name, di, bi), b.stream, TransformOptions{})
			}
		}
	}
}
