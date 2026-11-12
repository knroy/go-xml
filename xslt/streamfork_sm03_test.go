package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestSM03NestedForkBound: an xsl:fork in a prong of a teed fork tees the
// prong's cursor while the outer consumers stay live; past maxTeeParts
// consumers at once the plan is refused and the container runs on the tree,
// with the tree's result; within it the tees run.
func TestSM03NestedForkBound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	prong := `<xsl:sequence><xsl:for-each select="/doc/rec"><r k="{@k}"/></xsl:for-each></xsl:sequence>`
	body := func(inner int) string {
		return `<out><xsl:fork><xsl:sequence><xsl:fork>` + strings.Repeat(prong, inner) + `</xsl:fork></xsl:sequence>` +
			`<xsl:sequence select="count(//rec)"/></xsl:fork></out>`
	}
	for _, c := range []struct {
		inner  int
		stream bool
	}{{maxTeeParts - 2, true}, {maxTeeParts - 1, false}} {
		so, se, st := coreRun(coreSheet(body(c.inner)), dir, true, TransformOptions{})
		to, te, _ := coreRun(coreSheet(body(c.inner)), dir, false, TransformOptions{})
		if se != "" || te != "" || so != to {
			t.Fatalf("inner %d: stream %q %s\ntree %q %s", c.inner, so, se, to, te)
		}
		if teed := st.Kinds[streamtrace.KindFork] > 0 && st.Reruns == 0; teed != c.stream {
			t.Errorf("inner %d: teed %v, want %v (trace %+v)", c.inner, teed, c.stream, st)
		}
	}
}
