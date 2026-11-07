package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// resultDocRun is coreRun with the secondary results rendered after the
// principal one, and the error's code only.
func resultDocRun(t *testing.T, body, dir string, core bool) (string, streamtrace.Counts) {
	t.Helper()
	before := streamtrace.Snapshot()
	defer func() { noBursts = false }()
	tree, err := xdm.ParseString(coreSheet(body), xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "COMPILE " + xdm.ErrorCode(err), streamtrace.Counts{}
	}
	docs, _ := NewFileResolver(dir)
	noBursts = !core
	res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
	tr := streamtrace.Snapshot().Sub(before)
	if err != nil {
		return "ERROR " + xdm.ErrorCode(err), tr
	}
	var b strings.Builder
	b.WriteString(res.String())
	for _, sr := range res.Secondary {
		b.WriteString("\n--- " + sr.Href + "\n" + sr.String())
	}
	return b.String(), tr
}

// TestStreamResultDocMatchesTree: xsl:result-document in a streamed body
// streams, its secondary results and errors the tree's; a re-run on the
// tree does not see the failed run's secondary results (XTDE1490).
func TestStreamResultDocMatchesTree(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><rec k="a" v="1"><sub>s</sub></rec><rec k="b" v="2"/><rec k="c" v="x"/></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, body string
		rerun      bool // the stream fails and the tree answers
	}{
		{"body", `<xsl:result-document href="r.xml"><out><xsl:copy-of select="doc/rec"/></out></xsl:result-document>`, false},
		{"loop", `<xsl:for-each select="doc/rec"><xsl:result-document href="r{position()}.xml" indent="yes"><r k="{@k}"><xsl:copy-of select="sub"/></r></xsl:result-document></xsl:for-each>`, false},
		{"href", `<xsl:result-document href="{count(doc/rec)}.xml"><n/></xsl:result-document><done/>`, false},
		{"motionless", `<xsl:result-document href="m.xml" method="text">m</xsl:result-document><xsl:value-of select="count(doc/rec)"/>`, false},
		{"same-href", `<xsl:for-each select="doc/rec"><xsl:result-document href="same.xml"><x/></xsl:result-document></xsl:for-each>`, true},
		{"kept", `<xsl:for-each select="doc/rec"><xsl:result-document href="k{position()}.xml"><xsl:sequence select="."/></xsl:result-document></xsl:for-each>`, true},
		{"rollback", `<xsl:result-document href="a.xml"><a/></xsl:result-document><xsl:value-of select="doc/rec/@v eq '1'"/>`, true},
		{"final", `<xsl:result-document href="a.xml"><a/></xsl:result-document><xsl:value-of select="sum(doc/rec/@v)"/>`, false}, // FORG0001: finalCodes
	} {
		want, _ := resultDocRun(t, c.body, dir, false)
		got, tr := resultDocRun(t, c.body, dir, true)
		if got != want {
			t.Errorf("%s: streamed\n%s\ntree\n%s", c.name, got, want)
		}
		if tr.Runs[streamtrace.Core] == 0 || tr.Guards != 0 || (tr.Reruns > 0) != c.rerun {
			t.Errorf("%s: core runs %d, guards %d, re-runs %d (want re-run %v)", c.name,
				tr.Runs[streamtrace.Core], tr.Guards, tr.Reruns, c.rerun)
		}
	}
}
