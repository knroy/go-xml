package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStreamForkFused: a fork whose reading prongs are folds reads the
// document once (streamfuse.go), not once per prong: one walk for the four
// prongs, where the tee parsed it four times (4 prongs: 3.2x the tree's CPU
// teed, 1.5x fused). The output is the tree's.
func TestStreamForkFused(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 3000 {
		fmt.Fprintf(&b, `<rec k="%c" v="%d"><sub x="%d">t</sub></rec>`, "abc"[i%3], i, i)
	}
	b.WriteString("<other/></doc>")
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	four := coreSheet(`<xsl:fork><xsl:sequence><n><xsl:value-of select="count(/doc/rec)"/></n></xsl:sequence>` +
		`<xsl:sequence><a><xsl:value-of select="count(/doc/rec[@k = 'a'])"/></a></xsl:sequence>` +
		`<xsl:sequence><s><xsl:value-of select="count(//sub)"/></s></xsl:sequence>` +
		`<xsl:sequence><o><xsl:value-of select="exists(//other)"/></o></xsl:sequence></xsl:fork>`)
	var walks []int
	traceFused = func(n int) { walks = append(walks, n) }
	defer func() { traceFused = nil }()
	tree, _, _ := coreRun(four, dir, false, TransformOptions{})
	got, errText, _ := coreRun(four, dir, true, TransformOptions{})
	if got != tree || errText != "" || !strings.Contains(got, "<n>3000</n>") {
		t.Fatalf("streamed %q (%s), tree %q", got, errText, tree)
	}
	if len(walks) != 1 || walks[0] != 4 {
		t.Errorf("fused walks %v, want one for 4 prongs", walks)
	}
}
