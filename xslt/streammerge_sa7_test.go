package xslt

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		n, max int // max is TransformOptions.MaxMergeStreams
		stream bool
	}{
		{2, 0, true}, {mergeStreamsMax, 0, true}, {mergeStreamsMax + 1, 0, false},
		// Raised, lowered and unlimited.
		{mergeStreamsMax + 1, 100, true}, {100, 100, true}, {101, 100, false},
		{3, 2, false}, {2, 2, true}, {mergeStreamsMax * 2, -1, true},
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><out><xsl:merge>
 <xsl:merge-source for-each-source="(1 to ` + strconv.Itoa(c.n) + `) ! 'in.xml'" streamable="yes" select="r/i"><xsl:merge-key select="xs:integer(@k)"/></xsl:merge-source>
 <xsl:merge-action><g k="{current-merge-key()}" n="{count(current-merge-group())}"/></xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
		opts := TransformOptions{MaxMergeStreams: c.max}
		so, se, st := coreRun(sheet, dir, true, opts)
		to, te, _ := coreRun(sheet, dir, false, opts)
		if so != to || se != te || se != "" {
			t.Errorf("n=%d max=%d:\nstream %q %q\ntree   %q %q", c.n, c.max, so, se, to, te)
		}
		if ran := st.Runs[streamtrace.Merge] > 0 && st.Reruns == 0; ran != c.stream {
			t.Errorf("n=%d max=%d: streamed %v, want %v", c.n, c.max, ran, c.stream)
		}
	}
}

// TestStreamMergeNestedInputsCounted: a merge action that runs another
// streamed merge does so while every input of the enclosing one is still
// open, so mergeStreamsMax bounds the documents open at once across the
// nest, not each merge: of three levels of 32 inputs, the third (96 open)
// runs on the tree, and the output is the tree's.
func TestStreamMergeNestedInputsCounted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.xml"), []byte(`<r><i k="1"/></r>`), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><out><xsl:call-template name="r"><xsl:with-param name="d" select="3"/></xsl:call-template></out></xsl:template>
<xsl:template name="r"><xsl:param name="d" as="xs:integer"/>
 <xsl:if test="$d gt 0"><xsl:merge>
  <xsl:merge-source for-each-source="(1 to ` + strconv.Itoa(mergeStreamsMax/2) + `) ! 'in.xml'" streamable="yes" select="r/i"><xsl:merge-key select="xs:integer(@k)"/></xsl:merge-source>
  <xsl:merge-action><g d="{$d}" n="{count(current-merge-group())}"><xsl:call-template name="r"><xsl:with-param name="d" select="$d - 1"/></xsl:call-template></g></xsl:merge-action>
 </xsl:merge></xsl:if>
</xsl:template></xsl:stylesheet>`
	so, se, st := coreRun(sheet, dir, true, TransformOptions{})
	to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
	if so != to || se != te || se != "" {
		t.Errorf("stream %q %q\ntree   %q %q", so, se, to, te)
	}
	if got := st.Runs[streamtrace.Merge]; got != 2 || st.Reruns != 0 {
		t.Errorf("%d merges streamed, %d re-runs; want 2 and 0", got, st.Reruns)
	}
}

// TestStreamMergeStreamsBoundInherited: a transform fn:transform starts keeps
// its caller's MaxMergeStreams, so a merge of three inputs in the nested
// transform runs on the tree under a bound of 2 and streams under the default.
func TestStreamMergeStreamsBoundInherited(t *testing.T) {
	dir := t.TempDir()
	inner := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="xsl:initial-template"><out><xsl:merge>
 <xsl:merge-source for-each-source="(1 to 3) ! 'in.xml'" streamable="yes" select="r/i"><xsl:merge-key select="xs:integer(@k)"/></xsl:merge-source>
 <xsl:merge-action><g n="{count(current-merge-group())}"/></xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
	for name, data := range map[string]string{"in.xml": `<r><i k="1"/></r>`, "inner.xsl": inner} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="main"><xsl:sequence select="transform(map{
 'stylesheet-location': 'inner.xsl',
 'initial-template': QName('http://www.w3.org/1999/XSL/Transform', 'initial-template')})?output"/></xsl:template></xsl:stylesheet>`
	for _, c := range []struct {
		max    int
		stream bool
	}{{0, true}, {2, false}} {
		out, errText, st := coreRun(sheet, dir, true, TransformOptions{MaxMergeStreams: c.max})
		if errText != "" || !strings.HasSuffix(out, `<out><g n="3"/></out>`) {
			t.Fatalf("max=%d: got %q %q", c.max, out, errText)
		}
		if ran := st.Runs[streamtrace.Merge] > 0; ran != c.stream {
			t.Errorf("max=%d: streamed %v, want %v", c.max, ran, c.stream)
		}
	}
}
