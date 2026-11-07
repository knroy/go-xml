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
		// A union is not rewritten: it holds, and gives up at the document element.
		{`(.//* | .//text()) instance of text()*`, true},
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
