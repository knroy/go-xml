package xslt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamRerunMessages: a streamed merge whose action (or a rule its
// xsl:apply-templates reaches), or a streamed container, that tells a
// message and then fails re-runs on the tree, or returns its final error; under an xsl:try the
// transformation succeeds, and each message is told once, as the tree
// tells it (effectsMark at both re-run points).
func TestStreamRerunMessages(t *testing.T) {
	dir := t.TempDir()
	for n, d := range streamMergeDocs {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srcs := mergeSrc("A", "'a.xml'", "log/e", "xs:integer(@t)", "") + mergeSrc("B", "'b.xml'", "log/e", "xs:integer(@t)", "")
	fail := `<g v="{if (current-merge-key() = 7) then error() else 1}"/>`
	actions := []string{
		// A rule the action's apply-templates reaches tells the message.
		`<xsl:apply-templates select="current-merge-group()" mode="m"/>` + fail,
		// The action tells it itself (refused by mergeActionOK: on the tree).
		`<xsl:message select="current-merge-key()"/>` + fail,
		// No error: no re-run.
		`<xsl:apply-templates select="current-merge-group()" mode="m"/>`,
	}
	for i, a := range actions {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template match="e" mode="m"><xsl:message select="string(@v)"/><m v="{@v}"/></xsl:template>
<xsl:template name="main"><xsl:try><out><xsl:merge>` + srcs + `<xsl:merge-action>` + a +
			`</xsl:merge-action></xsl:merge></out><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template></xsl:stylesheet>`
		so, se, sm, st := msgRun(sheet, dir, true)
		to, te, tm, _ := msgRun(sheet, dir, false)
		if so != to || se != te || sm != tm {
			t.Errorf("action %d:\nstream %q %q %q\ntree   %q %q %q", i, so, se, sm, to, te, tm)
		}
		if st.Guards != 0 {
			t.Errorf("action %d: %d guard hits", i, st.Guards)
		}
		if i == 0 && st.Runs[streamtrace.Merge] == 0 {
			t.Errorf("action %d: the merge did not stream", i)
		}
	}
	bodies := []struct {
		b     string
		rerun bool
	}{
		// A message, then FORX0002 (a regex from the data, not final:
		// finalCodes): the container fails and re-runs.
		{`<xsl:message select="'m'"/><n><xsl:value-of select="log/e/@v ! matches('a', '(' || .)"/></n>`, true},
		{`<xsl:for-each select="log/e"><xsl:message select="string(@v)"/><r v="{matches('a', '(' || @v)}"/></xsl:for-each>`, true},
		// A message, then a value that is no integer (a1): FORG0001 is
		// final, and the messages told before it are the tree's.
		{`<xsl:message select="'m'"/><n><xsl:value-of select="sum(log/e/xs:integer(@v))"/></n>`, false},
		{`<xsl:for-each select="log/e"><xsl:message select="string(@v)"/><r v="{xs:integer(@t) div (3 - xs:integer(@t))}"/></xsl:for-each>`, false},
	}
	for i, c := range bodies {
		b := c.b
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><xsl:try><out><xsl:source-document streamable="yes" href="a.xml">` + b +
			`</xsl:source-document></out><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template></xsl:stylesheet>`
		so, se, sm, st := msgRun(sheet, dir, true)
		to, te, tm, _ := msgRun(sheet, dir, false)
		if so != to || se != te || sm != tm {
			t.Errorf("container %d:\nstream %q %q %q\ntree   %q %q %q", i, so, se, sm, to, te, tm)
		}
		if st.Guards != 0 || st.Runs[streamtrace.Core] == 0 || (st.Reruns != 0) != c.rerun {
			t.Errorf("container %d: guards %d, core runs %d, re-runs %d (want a streamed run, re-run %v)", i, st.Guards, st.Runs[streamtrace.Core], st.Reruns, c.rerun)
		}
	}
}
