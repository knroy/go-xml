package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// k2Bodies are container bodies of lane K2's shapes (streamsplit_k2.go);
// stream says the core must take them.
var k2Bodies = []struct {
	body   string
	stream bool
}{
	// (F | P)/T as (F union ())/T union P/T (foreignPath).
	{`<xsl:variable name="ins" as="element()*"><rec k="i"><sub x="9"/></rec></xsl:variable><out><xsl:copy-of select="($ins | /doc/rec)/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><rec k="i"><sub x="9"/></rec></xsl:variable><out><xsl:value-of select="(/doc/rec union $ins)/sub/@x" separator=","/></out>`, true},
	{`<xsl:variable name="ins" select="1"/><out><xsl:copy-of select="($ins | /doc/rec)/sub"/></out>`, true},
}

func TestStreamK2MatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range k2Bodies {
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
