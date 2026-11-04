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
}

// TestStreamL7MatchesTree runs l7Rows on every core document and
// whitespace declaration against the tree.
func TestStreamL7MatchesTree(t *testing.T) {
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
