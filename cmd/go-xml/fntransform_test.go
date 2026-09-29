package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/internal/fileuri"
)

// TestFnTransformSourceLocationFollowsAllowDir is GitHub issue #12 through the
// command. fn:transform's source-location reads through the resolver fn:doc
// uses, so a document beside the stylesheet is readable and one elsewhere is
// readable only once -allow-dir names its directory.
func TestFnTransformSourceLocationFollowsAllowDir(t *testing.T) {
	bin := buildCLI(t)
	dir, other := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "inner.xsl"), `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template match="/"><got><xsl:value-of select="/r"/></got></xsl:template>
</xsl:stylesheet>`)
	write(filepath.Join(dir, "near.xml"), `<r>near</r>`)
	write(filepath.Join(other, "far.xml"), `<r>far</r>`)
	sheet := func(loc string) string {
		xsl := filepath.Join(dir, "outer.xsl")
		write(xsl, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template name="xsl:initial-template">
    <xsl:sequence select="transform(map{'stylesheet-location': 'inner.xsl', 'source-location': '`+loc+`'})?output"/>
  </xsl:template>
</xsl:stylesheet>`)
		return xsl
	}
	far := fileuri.Of(filepath.Join(other, "far.xml"))

	out, err := exec.Command(bin, "-xsl", sheet("near.xml"),
		"-initial-template", initialTemplate).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "<got>near</got>") {
		t.Fatalf("relative source-location: %v\n%s", err, out)
	}

	out, err = exec.Command(bin, "-xsl", sheet(far),
		"-initial-template", initialTemplate).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "FOXT0002") {
		t.Fatalf("a source-location outside the roots was read: %v\n%s", err, out)
	}

	out, err = exec.Command(bin, "-xsl", sheet(far), "-allow-dir", other,
		"-initial-template", initialTemplate).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "<got>far</got>") {
		t.Fatalf("source-location under -allow-dir: %v\n%s", err, out)
	}
}
