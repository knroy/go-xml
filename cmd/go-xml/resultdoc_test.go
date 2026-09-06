package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hrefLessStylesheet is the reduction from GitHub issue #3, reported against a
// stylesheet XSpec had transpiled: an xsl:result-document with no href at all.
const hrefLessStylesheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  version="3.0" exclude-result-prefixes="#all" expand-text="yes">
  <xsl:output method="xml" indent="yes"/>
  <xsl:template match="/" name="xsl:initial-template">
    <xsl:result-document>
      <test>hello</test>
    </xsl:result-document>
  </xsl:template>
</xsl:stylesheet>
`

// buildCLI compiles the command once so the tests below drive the real binary
// rather than a reimplementation of main's plumbing. The bug was entirely in
// that plumbing -- the engine already emitted the document correctly -- so a
// test that bypassed it would have passed against the broken code.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "go-xml")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the command failed: %v\n%s", err, out)
	}
	return bin
}

const initialTemplate = "{http://www.w3.org/1999/XSL/Transform}initial-template"

// TestResultDocumentWithoutHrefGoesToPrincipalOutput is issue #3.
//
// XSLT 3.0 section 24.3 changes the current output URI only "during execution
// of an xsl:result-document instruction with an href attribute". With no href
// it stays the base output URI, so the instruction writes to the principal
// output -- stdout here -- and must not demand -result-dir. Both the bare
// invocation and the one that supplies -result-dir used to fail outright.
func TestResultDocumentWithoutHrefGoesToPrincipalOutput(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "rd.xsl")
	if err := os.WriteFile(xsl, []byte(hrefLessStylesheet), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no result-dir", nil},
		// -result-dir must not change the answer: the document does not go
		// to a file whether or not a directory was offered for one.
		{"with result-dir", []string{"-result-dir", dir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-xsl", xsl, "-initial-template", initialTemplate}, tc.args...)
			out, err := exec.Command(bin, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("the transform failed: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "<test>hello</test>") {
				t.Errorf("the result document is missing from stdout; got:\n%s", out)
			}
		})
	}
}

// TestResultDocumentWithoutHrefHonoursOutputFile checks that -o captures it,
// since the principal output is the file when one is named.
func TestResultDocumentWithoutHrefHonoursOutputFile(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "rd.xsl")
	if err := os.WriteFile(xsl, []byte(hrefLessStylesheet), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out.xml")

	out, err := exec.Command(bin, "-xsl", xsl, "-initial-template", initialTemplate,
		"-o", dest).CombinedOutput()
	if err != nil {
		t.Fatalf("the transform failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<test>hello</test>") {
		t.Errorf("-o did not receive the result document; got:\n%s", got)
	}
}

// TestResultDocumentWithoutHrefUsesItsOwnFormat covers the case the issue was
// actually reduced from: XSpec's transpiled stylesheet names a serialization
// format on an href-less instruction. The document must be serialized with
// that declaration's settings, not the principal tree's -- here the named
// xsl:output omits the XML declaration and the unnamed one does not.
func TestResultDocumentWithoutHrefUsesItsOwnFormat(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "fmt.xsl")
	const sheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:output method="xml" indent="no"/>
  <xsl:output name="rep" method="xml" indent="yes" omit-xml-declaration="yes"/>
  <xsl:template match="/" name="xsl:initial-template">
    <xsl:result-document format="rep"><r><c/></r></xsl:result-document>
  </xsl:template>
</xsl:stylesheet>
`
	if err := os.WriteFile(xsl, []byte(sheet), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "-xsl", xsl, "-initial-template", initialTemplate).CombinedOutput()
	if err != nil {
		t.Fatalf("the transform failed: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "<?xml") {
		t.Errorf("omit-xml-declaration from @format was not applied; got:\n%s", got)
	}
	if !strings.Contains(got, "<r>\n  <c/>\n</r>") {
		t.Errorf("indent from @format was not applied; got:\n%s", got)
	}
}

// TestResultDocumentWithHrefStillNeedsResultDir pins the behaviour that must
// not change. An href names a file, so it still requires -result-dir, and the
// containment check that keeps a stylesheet-controlled href inside that
// directory is a security control rather than a convenience.
func TestResultDocumentWithHrefStillNeedsResultDir(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "href.xsl")
	const sheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template match="/" name="xsl:initial-template">
    <xsl:result-document href="{$dest}"><test>hello</test></xsl:result-document>
  </xsl:template>
  <xsl:param name="dest" select="'sub/out.xml'"/>
</xsl:stylesheet>
`
	if err := os.WriteFile(xsl, []byte(sheet), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "-xsl", xsl, "-initial-template", initialTemplate).CombinedOutput()
	if err == nil {
		t.Fatalf("an href without -result-dir should have failed; got:\n%s", out)
	}
	if !strings.Contains(string(out), "-result-dir") {
		t.Errorf("the error should name -result-dir; got:\n%s", out)
	}

	// Escaping the directory stays refused.
	out, err = exec.Command(bin, "-xsl", xsl, "-initial-template", initialTemplate,
		"-result-dir", filepath.Join(dir, "results"),
		"-p", "dest=../../escaped.xml").CombinedOutput()
	if err == nil {
		t.Fatalf("an href escaping -result-dir should have failed; got:\n%s", out)
	}
	if !strings.Contains(string(out), "outside") {
		t.Errorf("the error should report the containment failure; got:\n%s", out)
	}
}
