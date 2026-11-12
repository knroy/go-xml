package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamInstanceOfStar: "P instance of T*" at the document node counts
// the matches failing T on their views (streamsplit_instance.go) where it
// held every match and, the document element among them, fell back to the
// tree (sx-instance-of-001, -102, -103). The answer is the tree's.
func TestStreamInstanceOfStar(t *testing.T) {
	doc := `<doc><a>x</a><!--c--><a><b/></a></doc>`
	for _, c := range []struct {
		sel      string
		fallback bool
	}{
		{`(.//node()) instance of element()*`, false},
		{`(.//*) instance of element()*`, false},
		{`(.//a) instance of element(a)*`, false},
		{`(.//a) instance of element(b)*`, false},
		{`(doc/a, $v) instance of node()*`, false},
		{`($v, doc/a) instance of node()*`, false},
		{`(doc/a, $e) instance of node()*`, false},
		{`(doc/missing) instance of text()*`, false},
		// A union: each operand filtered (withLastPredicate).
		{`(.//* | .//text()) instance of text()*`, false},
		{`(.//* | .//text()) instance of node()*`, false},
		{`(doc/a | .//comment()) instance of element(a)*`, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		sheet := coreSheet(`<o><xsl:variable name="v" select="1"/><xsl:variable name="e" select="()"/><xsl:value-of select="` + c.sel + `"/></o>`)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		where := c.sel
		if so != to || se != te {
			t.Errorf("%s:\ncore %q %q\ntree %q %q", where, so, se, to, te)
		}
		if st.Runs[0] == 0 || st.Guards != 0 {
			t.Errorf("%s: core runs %d, guards %d", where, st.Runs[0], st.Guards)
		}
		if fell := st.Fallbacks > 0; fell != c.fallback {
			t.Errorf("%s: fell back %v, want %v", where, fell, c.fallback)
		}
	}
}

// TestStreamTreatAsStar: "O treat as T*" streams (§19.8.8.6: the operand's
// posture and sweep) where the split held every item and, the document
// element among them, fell back to the tree (sx-treat-010, -102, -103,
// si-try-001, -010, -022, -023). Each match is checked on its view as it
// comes, in the tree's order; the answer, or the XPDY0050, is the tree's.
func TestStreamTreatAsStar(t *testing.T) {
	small := `<doc><a>x</a><!--c--><a><b/></a></doc>`
	for _, c := range []struct{ doc, sel string }{
		// A motionless item is checked before the walk: the matches the
		// tree would not hold beyond are not held first (sx-treat-102, -103).
		{coreDocs["wide"], `(doc/rec, $v) treat as node()*`},
		{coreDocs["wide"], `($v, doc/rec) treat as node()*`},
	} {
		treatAsCase(t, c.doc, c.sel)
	}
	for _, sel := range []string{
		`(doc/a, $v) treat as node()*`,
		`($v, doc/a) treat as node()*`,
		`(doc/a, $e) treat as node()*`,
		`(.//* | .//text()) treat as text()*`,
		`count((.//* | .//text()) treat as node()*)`,
		// The tree tests every item: exists() reads on past its answer.
		`exists((.//a) treat as element(a)*)`,
		`exists(.//node() treat as element()*)`,
		// Only the outermost matches are items: the text inside is not one.
		`count(outermost(.//node()) treat as element()*)`,
		`outermost(.//node()) treat as comment()*`,
		`(.//text()) treat as text()*`,
		`(.//comment()) treat as comment()*`,
		`(.//a) treat as element(a)*`,
		`(.//a) treat as element(b)*`,
	} {
		treatAsCase(t, small, sel)
	}
}

// treatAsCase runs sel over doc streamed and on the tree, bare and in an
// xsl:try catching XPDY0050: the same output or error, with no fallback.
func treatAsCase(t *testing.T, doc, sel string) {
	t.Helper()
	for _, try := range []bool{false, true} {
		body := `<xsl:value-of select="` + sel + `"/>`
		if try {
			body = `<xsl:try>` + body + `<xsl:catch errors="*:XPDY0050" select="'caught'"/></xsl:try>`
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		sheet := coreSheet(`<o><xsl:variable name="v" select="1"/><xsl:variable name="e" select="()"/>` + body + `</o>`)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		if so != to || se != te {
			t.Errorf("%s (try %v):\ncore %q %q\ntree %q %q", sel, try, so, se, to, te)
		}
		// An error outside xsl:try is the tree's: the container runs again.
		if st.Runs[0] == 0 || st.Guards != 0 || st.Fallbacks != 0 || st.Reruns != 0 && (try || se == "") {
			t.Errorf("%s (try %v): core runs %d, guards %d, fallbacks %d, re-runs %d", sel, try, st.Runs[0], st.Guards, st.Fallbacks, st.Reruns)
		}
	}
}
