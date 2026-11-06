package xslt

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamMergeManyInputsTree: a streamed merge holds every input open
// until it ends (a file, a cursor and a coroutine each), so a for-each-source
// of more than mergeStreamsMax URIs, repeats included, runs on the tree
// before anything is read; one of few still streams. Both as the tree does.
func TestStreamMergeManyInputsTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.xml"), []byte(`<r><i k="1"/><i k="2"/></r>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		n      int
		stream bool
	}{{2, true}, {mergeStreamsMax, true}, {mergeStreamsMax + 1, false}} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><out><xsl:merge>
 <xsl:merge-source for-each-source="(1 to ` + strconv.Itoa(c.n) + `) ! 'in.xml'" streamable="yes" select="r/i"><xsl:merge-key select="xs:integer(@k)"/></xsl:merge-source>
 <xsl:merge-action><g k="{current-merge-key()}" n="{count(current-merge-group())}"/></xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		if so != to || se != te || se != "" {
			t.Errorf("n=%d:\nstream %q %q\ntree   %q %q", c.n, so, se, to, te)
		}
		if ran := st.Runs[streamtrace.Merge] > 0 && st.Reruns == 0; ran != c.stream {
			t.Errorf("n=%d: streamed %v, want %v", c.n, ran, c.stream)
		}
	}
}
