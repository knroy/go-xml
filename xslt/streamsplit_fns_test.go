package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamWholeFnsMatchTree: calls that take the consuming path whole
// (fold-left, fold-right, filter, for-each, distinct-values,
// codepoints-to-string, trace) and function items in the outer, against the
// tree, on every core document and whitespace declaration.
func TestStreamWholeFnsMatchTree(t *testing.T) {
	decls := `<xsl:function name="p:add" as="xs:double"><xsl:param name="x" as="xs:double"/>` +
		`<xsl:param name="y" as="xs:double"/><xsl:sequence select="$x + $y"/></xsl:function>` +
		`<xsl:function name="p:big" as="xs:boolean"><xsl:param name="a"/><xsl:sequence select="number($a) gt 1"/></xsl:function>`
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<v><xsl:value-of select="fold-left(doc/rec/@v/data(), 0, p:add#2)"/></v>`, true},
		{`<v><xsl:value-of select="fold-left(//rec/number(@v), 10, function($a, $b) { $a + $b })"/></v>`, true},
		{`<v><xsl:value-of select="fold-right(doc/rec/string(@k), '', function($a, $b) { concat($a, $b) })"/></v>`, false}, // the analysis does not verify fold-right,
		{`<v><xsl:value-of select="filter(doc/rec/@v/data(), p:big#1)"/></v>`, true},
		{`<v><xsl:value-of select="for-each(doc/rec/@k/data(), upper-case#1)"/></v>`, true},
		{`<v><xsl:value-of select="count(distinct-values(//rec/@k))"/></v>`, true},
		{`<v><xsl:value-of select="distinct-values(doc/rec/sub/@x)"/></v>`, true},
		{`<v><xsl:value-of select="codepoints-to-string(doc/rec/(64 + number(@v)))"/></v>`, true},
		{`<v><xsl:value-of select="sum(trace(doc/rec/@v/data(), 'v'))"/></v>`, true},
		// A function item that captures the focus is not motionless.
		{`<v><xsl:value-of select="for-each(doc/rec/@k/data(), name#0)"/></v>`, false},
		// A cast that fails at some record: the tree's error.
		{`<v><xsl:value-of select="fold-left(doc/rec/@v/xs:integer(.), 0, p:add#2)"/></v>`, true},
		// fn:trace(v, 'label') is v: a loop over it, a reducer of it.
		{`<xsl:for-each select="trace(doc/rec, 'r')"><r k="{@k}"/></xsl:for-each>`, true},
		{`<v><xsl:value-of select="count(trace(//rec, 'c'))"/></v>`, true},
	}
	n := 0
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range bodies {
				// Some documents have no rec or fail to parse: only the
				// output and error must agree there.
				stream := b.stream && name != "bad" && name != "recroot" && name != "empty"
				n += coreCheckLoose(t, coreSheetWith(d+decls, b.body), dir, name, di, bi, stream)
			}
		}
	}
	if n == 0 {
		t.Fatal("nothing streamed")
	}
}

// coreCheckLoose is coreCheck where an erroring document may re-run, and a
// body meant to stream need do so only on documents that are not empty.
func coreCheckLoose(t *testing.T, sheet, dir, doc string, di, bi int, stream bool) int {
	t.Helper()
	so, se, st := coreRun(sheet, dir, true, TransformOptions{})
	to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
	if so != to || se != te {
		t.Errorf("%s decl %d body %d:\ncore %q %q\ntree %q %q", doc, di, bi, so, se, to, te)
	}
	if st.Guards != 0 {
		t.Errorf("%s decl %d body %d: %d guard hits", doc, di, bi, st.Guards)
	}
	if st.Reruns != 0 && te == "" {
		t.Errorf("%s decl %d body %d: re-run where the tree succeeds", doc, di, bi)
	}
	if stream && te == "" && st.Runs[0] == 0 {
		t.Errorf("%s decl %d body %d: not streamed", doc, di, bi)
	}
	if !stream && bi == 9 && st.Runs[0] != 0 {
		t.Errorf("%s decl %d body %d: streamed", doc, di, bi)
	}
	return int(st.Runs[0])
}
