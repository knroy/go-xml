package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestSM03StreamAccumsCharged: the values a streamed recorder keeps per node
// -- a merge source's between matches, a copy-of's with
// copy-accumulators="yes" -- are charged to the item budget as the tree's
// accumulator tables are: past MaxItems the streamed run is refused with
// XPDY0130, under it the output is the tree's.
func TestSM03StreamAccumsCharged(t *testing.T) {
	dir := t.TempDir()
	pads := strings.Repeat("<pad/>", 600)
	for n, d := range map[string]string{
		"m.xml": `<log><e t="1"/>` + pads + `<e t="2"/></log>`,
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const acc = `<xsl:accumulator name="c" as="xs:integer" initial-value="0" streamable="yes">` +
		`<xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>`
	merge := mergeSheet(mergeSrc("A", "'m.xml'", "log/e", "xs:integer(@t)", `use-accumulators="c"`),
		`<g><xsl:for-each select="current-merge-group()"><i c="{accumulator-before('c')}"/></xsl:for-each></g>`)
	merge = merge[:len(merge)-len("</xsl:stylesheet>")] + acc + "</xsl:stylesheet>"
	for _, lim := range []int{0, 400} {
		so, se, st := coreRun(merge, dir, true, TransformOptions{MaxItems: lim})
		to, te, _ := coreRun(merge, dir, false, TransformOptions{MaxItems: lim})
		switch {
		case lim == 0 && (se != "" || so != to || te != "" || st.Runs[streamtrace.Merge] == 0):
			t.Errorf("merge: stream %q %s\ntree %q %s (trace %+v)", so, se, to, te, st)
		case lim != 0 && !strings.Contains(se, "XPDY0130"):
			t.Errorf("merge, MaxItems %d: streamed %q %s, want XPDY0130", lim, so, se)
		}
	}
	// A grounded copy of the principal input's w (groundAccums).
	copyOf := mainSheet(acc + `<xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>` +
		`<xsl:mode name="m" use-accumulators="#all"/>` +
		`<xsl:template match="w" mode="m"><z v="{(.//pad)[last()]/accumulator-after('c')}"/></xsl:template>` +
		`<xsl:template match="/"><out><xsl:variable name="c" as="element()*"><xsl:copy-of select="doc/w" copy-accumulators="yes"/></xsl:variable>` +
		`<xsl:apply-templates select="$c" mode="m"/></out></xsl:template>`)
	doc := `<doc><w>` + pads + `</w></doc>`
	so, st := mainRun(copyOf, doc, true, TransformOptions{})
	to, _ := mainRun(copyOf, doc, false, TransformOptions{})
	if so != to || !strings.Contains(so, `v="602"`) || st.Runs[streamtrace.Main] == 0 {
		t.Errorf("copy-of: streamed %q\ntree %q (trace %+v)", so, to, st)
	}
	if so, _ := mainRun(copyOf, doc, true, TransformOptions{MaxItems: 400}); !strings.Contains(so, "XPDY0130") {
		t.Errorf("copy-of, MaxItems 400: streamed %q, want XPDY0130", so)
	}
}
