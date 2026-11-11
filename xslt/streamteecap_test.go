package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// A tee runs every consumer at once, a parse and a goroutine each, for the
// whole document: a map or fork of more than maxTeeParts reading parts is
// not teed, and gives the tree's result.
func TestStreamTeeFanOutBounded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(`<doc><rec/><rec/></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}
	mapBody := func(n int) string {
		var e []string
		for k := range n {
			e = append(e, fmt.Sprintf("'k%d': count(//rec)", k))
		}
		return `<xsl:variable name="m" as="map(*)" select="map{` + strings.Join(e, ", ") + `}"/><n><xsl:value-of select="sum($m?*)"/></n>`
	}
	forkBody := func(n int) string {
		return `<out><xsl:fork>` + strings.Repeat(`<xsl:sequence select="count(//rec)"/>`, n) + `</xsl:fork></out>`
	}
	for _, c := range []struct {
		name string
		body func(int) string
	}{{"map", mapBody}, {"fork", forkBody}} {
		for _, n := range []int{3, maxTeeParts + 1} {
			body := c.body(n)
			out, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
			tout, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
			if se != te || out != tout {
				t.Errorf("%s %d: stream %q %s, tree %q %s", c.name, n, out, se, tout, te)
			}
			if teed := st.Runs[streamtrace.Core] == 1; teed != (n <= maxTeeParts) {
				t.Errorf("%s %d: ran on the core %v, trace %+v", c.name, n, teed, st)
			}
		}
	}
}
