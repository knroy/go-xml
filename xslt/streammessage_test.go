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

// msgRun is coreRun that also returns the messages.
func msgRun(sheet, dir string, core bool) (out, errText, msgs string, tr streamtrace.Counts) {
	before := streamtrace.Snapshot()
	defer func() { noBursts = false; tr = streamtrace.Snapshot().Sub(before) }()
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		return "", "SHEET: " + err.Error(), "", tr
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "", "COMPILE: " + err.Error(), "", tr
	}
	docs, _ := NewFileResolver(dir)
	noBursts = !core
	res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
	if err != nil {
		return "", err.Error(), "", tr
	}
	return res.String(), "", strings.Join(res.Messages, "|"), tr
}

// TestStreamMessagesMatchTree: xsl:message in streamed bodies -- motionless,
// consuming by select or content, in loops, in fork prongs, with errors in
// its content (swallowed, as 3.0 says, the rest of the context skipped), with
// terminate, and in a container that re-runs on the tree -- gives the tree's
// output, error and messages, each told once, in the tree's order.
func TestStreamMessagesMatchTree(t *testing.T) {
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<xsl:message select="'start'"/><n><xsl:value-of select="count(doc/rec)"/></n><xsl:message>end</xsl:message>`, true},
		{`<xsl:message select="sum(doc/rec/@v)"/>`, true},
		{`<xsl:message><m><xsl:value-of select="doc/rec/@k"/></m></xsl:message><x/>`, true},
		{`<xsl:for-each select="doc/rec"><xsl:message select="string(@k)"/><r v="{@v}"/></xsl:for-each>`, true},
		{`<xsl:for-each select="doc/rec"><xsl:message><xsl:copy-of select="."/></xsl:message></xsl:for-each>`, true},
		// The content fails at v="x" (nested): swallowed, the rest read on.
		{`<xsl:message select="sum(//rec/xs:integer(@v))"/><after/>`, true},
		{`<xsl:message><xsl:value-of select="doc/rec/xs:integer(@v)"/></xsl:message><after/>`, true},
		// A message, then an error: the container re-runs, the message once.
		{`<xsl:message select="'before'"/><n><xsl:value-of select="sum(//rec/xs:integer(@v))"/></n>`, true},
		{`<xsl:message select="'stop'" terminate="{if (true()) then 'yes' else 'no'}"/>`, true},
		{`<xsl:fork><xsl:sequence><xsl:message select="'p1'"/><c><xsl:value-of select="count(doc/rec)"/></c></xsl:sequence>` +
			`<xsl:sequence><xsl:message select="'p2'"/><s><xsl:value-of select="sum(doc/rec/@v)"/></s></xsl:sequence>` +
			`<xsl:sequence><xsl:message select="'p3'"/></xsl:sequence></xsl:fork>`, true},
		{`<xsl:fork><xsl:sequence><xsl:message select="'q1'"/><xsl:value-of select="sum(doc/rec/xs:integer(@v))"/></xsl:sequence>` +
			`<xsl:sequence><xsl:message select="'q2'"/><xsl:value-of select="count(doc/rec)"/></xsl:sequence></xsl:fork>`, true},
	}
	n := 0
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range bodies {
				sheet := coreSheetWith(d, b.body)
				so, se, sm, st := msgRun(sheet, dir, true)
				to, te, tm, _ := msgRun(sheet, dir, false)
				if so != to || se != te || sm != tm {
					t.Errorf("%s decl %d body %d:\ncore %q %q %q\ntree %q %q %q", name, di, bi, so, se, sm, to, te, tm)
				}
				if st.Guards != 0 {
					t.Errorf("%s decl %d body %d: %d guard hits", name, di, bi, st.Guards)
				}
				if st.Reruns != 0 && te == "" {
					t.Errorf("%s decl %d body %d: re-run where the tree succeeds", name, di, bi)
				}
				if b.stream && name == "flat" && st.Runs[streamtrace.Core] == 0 {
					t.Errorf("%s decl %d body %d: not streamed", name, di, bi)
				}
				n += int(st.Runs[streamtrace.Core])
			}
		}
	}
	if n == 0 {
		t.Fatal("nothing streamed")
	}
}
