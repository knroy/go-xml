package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// forkBodies are container bodies holding an xsl:fork or xsl:map. stream says
// the core must take the container.
var forkBodies = []struct {
	body   string
	stream bool
}{
	// Several prongs read the document: a tee.
	{`<xsl:fork><xsl:sequence><a><xsl:value-of select="sum(/doc/rec/@v)"/></a></xsl:sequence>` +
		`<xsl:sequence><b><xsl:value-of select="count(//sub)"/></b></xsl:sequence></xsl:fork>`, true},
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a><xsl:value-of select="avg(/doc/rec/@v)"/></a></xsl:sequence>` +
		`<xsl:sequence><b><xsl:copy-of select="/doc/rec[@k = 'a']"/></b></xsl:sequence><xsl:sequence select="']'"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="string-join(//sub/@x, '-')"/>` +
		`<xsl:sequence select="max(//rec/@v)"/><xsl:sequence select="exists(//other)"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence><xsl:for-each select="/doc/rec"><r k="{@k}"/></xsl:for-each></xsl:sequence>` +
		`<xsl:sequence><xsl:for-each select="/doc/rec/sub"><s x="{@x}"><xsl:value-of select="."/></s></xsl:for-each></xsl:sequence></xsl:fork></out>`, true},
	// A tee in a streamed xsl:try's body: the try cannot order its prongs'
	// errors, so the container runs on the tree (it panicked at the planner
	// once fork and try met).
	{`<out><xsl:try><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="sum(/doc/rec/@v)"/></xsl:fork>` +
		`<xsl:catch><caught/></xsl:catch></xsl:try></out>`, false},
	// A prong's error, the first in prong order is the fork's: xs:integer
	// of v="x" fails in prong 2, the sum over k fails (FORG0001) in prong 1.
	{`<out><xsl:fork><xsl:sequence select="sum(/doc/rec/@k)"/><xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/></xsl:fork></out>`, true},
	// A map of entries that each read the document.
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(//rec)"/>` +
		`<xsl:map-entry key="'s'" select="sum(//rec/@v)"/><xsl:map-entry key="'k'" select="string-join(//rec/@k)"/></xsl:map></xsl:variable>` +
		`<m n="{$m?n}" s="{$m?s}" k="{$m?k}"/>`, true},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'a'" select="count(//rec)"/>` +
		`<xsl:map-entry key="'a'" select="count(//sub)"/></xsl:map></xsl:variable><m n="{$m?a}"/>`, true}, // XTDE3365
	// At most one prong reads: a sequence constructor.
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a/></xsl:sequence><xsl:sequence select="']'"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a><xsl:value-of select="count(//rec)"/></a></xsl:sequence></xsl:fork></out>`, true},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(//rec)"/><xsl:map-entry key="'c'" select="1"/></xsl:map></xsl:variable><m n="{$m?n}" c="{$m?c}"/>`, true},
	// Several prongs read each record: on a grounded copy of it.
	{`<out><xsl:for-each select="/doc/rec"><r><xsl:fork><xsl:sequence><xsl:attribute name="k" select="@k"/></xsl:sequence>` +
		`<xsl:sequence><xsl:attribute name="n" select="count(sub)"/></xsl:sequence><xsl:sequence><xsl:attribute name="s" select="string(.)"/></xsl:sequence></xsl:fork></r></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="/doc/rec"><xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(sub)"/>` +
		`<xsl:map-entry key="'s'" select="string(.)"/></xsl:map></xsl:variable><r n="{$m('n')}" s="{$m?s}"/></xsl:for-each></out>`, true},
	// Refused: prongs returning streamed nodes (XTSE3430 in both modes), a
	// consuming map key.
	{`<out><xsl:fork><xsl:sequence select="/doc/rec"/><xsl:sequence select="//sub"/></xsl:fork></out>`, false},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="string(/doc/rec[1])" select="1"/><xsl:map-entry key="'b'" select="count(//sub)"/></xsl:map></xsl:variable><m b="{$m?b}"/>`, false},
}

// TestStreamForkMatchesTree: every fork and map body over every document,
// under every whitespace declaration, gives on the core the tree's output or
// the tree's error; the ones that must stream ran on the core without a
// re-run, and the tees among them did run (KindFork).
func TestStreamForkMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	before := streamtrace.Snapshot()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range forkBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
	if n := streamtrace.Snapshot().Sub(before).Kinds[streamtrace.KindFork]; n == 0 {
		t.Error("no tee ran")
	}
}

// TestStreamForkErrorOrder: the error a tee returns, before the re-run on the
// tree replaces it, is the tree's: that of the first prong in prong order
// that fails, though a later prong fails earlier in the document. The
// document: rec 1 has v="x", where the early prong's cast fails; the late
// prong's fails once it has read every @k.
func TestStreamForkErrorOrder(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><rec k="a" v="x"/><rec k="b" v="2"/><rec k="zz" v="3"/></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	late := `<xsl:sequence select="xs:integer(string-join(/doc/rec/@k))"/>`
	early := `<xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/>`
	fine := `<xsl:sequence select="count(//rec)"/>`
	for _, prongs := range [][]string{{late, early}, {early, late}, {fine, late, early}, {fine, early, fine, late}} {
		body := "<out><xsl:fork>" + strings.Join(prongs, "") + "</xsl:fork></out>"
		var streamErr error
		traceRerun = func(err error) { streamErr = err }
		_, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
		traceRerun = nil
		_, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
		if st.Kinds[streamtrace.KindFork] != 1 || st.Reruns != 1 || streamErr == nil {
			t.Fatalf("%s: trace %+v, stream error %v", body, st, streamErr)
		}
		if se != te || !strings.Contains(te, streamErr.Error()) {
			t.Errorf("%s:\nstream %v\ntree   %s", body, streamErr, te)
		}
	}
}
