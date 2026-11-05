package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A stylesheet and source named through a symlinked directory take the
// resolved path as their URI, the identity the resolver gives every module
// and document it reads. Under the link's spelling the source was a second
// identity for its own file: XSpec's issue-987_parent.xspec, reached through
// a symlinked testdata, imported itself back and failed "Name conflicts with
// x:param".
func TestSymlinkedPrincipalHasOneIdentity(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for name, body := range map[string]string{
		"parent.xml": `<p href="child.xml"/>`,
		"child.xml":  `<c href="parent.xml"/>`,
		"s.xsl": `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:output method="text"/>
<xsl:template match="/">
<xsl:value-of select="document-uri(/) = document-uri(document(document(p/@href)/c/@href))"/>
<xsl:text> </xsl:text>
<xsl:value-of select="contains(document-uri(/), '/link/'), contains(static-base-uri(), '/link/')"/>
</xsl:template></xsl:stylesheet>`,
	} {
		if err := os.WriteFile(filepath.Join(real, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := buildCLI(t)
	for _, d := range []string{real, link} {
		out, err := exec.Command(bin, "-xsl", filepath.Join(d, "s.xsl"),
			filepath.Join(d, "parent.xml")).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", d, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "true false false" {
			t.Errorf("through %s: %q; want one document-uri for parent.xml, not the link's", d, got)
		}
	}
}
