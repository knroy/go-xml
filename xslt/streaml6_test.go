package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// l6Bodies are container bodies of lane L6's shapes (streamsplit_l6.go);
// stream says the core must take them.
var l6Bodies = []struct {
	body   string
	stream bool
}{
	// A loop over attributes or leaves whose body reads the item whole (leafLoopBody).
	{`<out><xsl:for-each select="doc/rec/@k"><xsl:document><xsl:sequence select="data(.)"/></xsl:document></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//sub/text()"><xsl:document><xsl:copy-of select="."/></xsl:document></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//rec/@*"><a n="{name()}"><xsl:value-of select="."/></a></xsl:for-each></out>`, true},
	{`<xsl:variable name="d" as="document-node()*"><xsl:for-each select="doc/rec[@v gt 1]/@v"><xsl:document><xsl:sequence select="data(.)"/></xsl:document></xsl:for-each></xsl:variable><out><xsl:copy-of select="data($d)"/></out>`, true},
}

func TestStreamL6MatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range l6Bodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
