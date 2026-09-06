package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// currentOutputURIStylesheet is Martin Honnen's stylesheet from GitHub issue
// #3, reduced to the expression that revealed the second defect: the
// current-output-uri() call inside an href-less xsl:result-document. The
// earlier reduction dropped it, which is why the wrong URI survived the first
// fix.
const currentOutputURIStylesheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  version="3.0" exclude-result-prefixes="#all" expand-text="yes">
  <xsl:output method="xml" indent="no"/>
  <xsl:template match="/" name="xsl:initial-template">
    <xsl:result-document>
      <test>{current-output-uri()}</test>
    </xsl:result-document>
  </xsl:template>
</xsl:stylesheet>
`

// principalOutputURIStylesheet asks the same question of the principal tree,
// with no xsl:result-document anywhere. Section 24.3 sets the current output
// URI to the base output URI "on initial invocation of a stylesheet
// component", so the answer must be the destination here too -- it used to be
// the empty sequence, because the CLI supplied no base output URI at all.
const principalOutputURIStylesheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  version="3.0" expand-text="yes">
  <xsl:output method="xml" indent="no"/>
  <xsl:template match="/" name="xsl:initial-template">
    <test>{current-output-uri()}</test>
  </xsl:template>
</xsl:stylesheet>
`

// reportedOutputURI runs the stylesheet and returns the URI it printed.
func reportedOutputURI(t *testing.T, bin, xsl, readBack string, args ...string) string {
	t.Helper()
	full := append([]string{"-xsl", xsl, "-initial-template", initialTemplate}, args...)
	out, err := exec.Command(bin, full...).CombinedOutput()
	if err != nil {
		t.Fatalf("the transform failed: %v\n%s", err, out)
	}
	got := string(out)
	if readBack != "" {
		b, err := os.ReadFile(readBack)
		if err != nil {
			t.Fatal(err)
		}
		got = string(b)
	}
	open := strings.Index(got, "<test>")
	close := strings.Index(got, "</test>")
	if open < 0 || close < open {
		t.Fatalf("no <test> element in the output; got:\n%s", got)
	}
	return strings.TrimSpace(got[open+len("<test>") : close])
}

// TestCurrentOutputURIReportsTheDestination is the defect Martin's exact
// stylesheet exposed: fn:current-output-uri() answered the *stylesheet's* own
// URI, which is neither where the output goes nor absent.
//
// Section 19.1 makes the base output URI implementation-defined and notes it
// "will often be convenient" for it to be "the same as the location to which
// the principal result document is serialized". Saxon HE 13 reports the
// output destination; the engine's default of none is right for a library
// that never writes files, but the CLI knows the destination and must say so.
func TestCurrentOutputURIReportsTheDestination(t *testing.T) {
	bin := buildCLI(t)
	// The CLI reports the destination as it was handed to it, without
	// resolving symlinks: -o names a file the caller asked for, and rewriting
	// that spelling would report a URI the caller never wrote. So the
	// expectations below are built from the same unresolved path.
	dir := t.TempDir()

	xsl := filepath.Join(dir, "cou.xsl")
	if err := os.WriteFile(xsl, []byte(currentOutputURIStylesheet), 0o644); err != nil {
		t.Fatal(err)
	}
	principalXSL := filepath.Join(dir, "principal.xsl")
	if err := os.WriteFile(principalXSL, []byte(principalOutputURIStylesheet), 0o644); err != nil {
		t.Fatal(err)
	}

	outFile := filepath.Join(dir, "out.xml")
	resultDir := filepath.Join(dir, "results")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		sheet string
		// readBack names a file to read the result from instead of stdout.
		readBack string
		args     []string
		want     string
	}{
		// With neither flag the result goes to stdout, which has no URI. The
		// working directory is what a relative href on that command line
		// would mean, and it is what Saxon reports.
		{"no flags", xsl, "", nil, dirURI(mustGetwd(t))},
		// -o names the destination exactly, so it is reported as a file.
		{"output file", xsl, outFile, []string{"-o", outFile}, fileURI(outFile)},
		// -result-dir is a directory, and a directory carries a trailing
		// slash so that a relative href resolves inside it.
		{"result dir", xsl, "", []string{"-result-dir", resultDir}, dirURI(resultDir)},
		// The principal tree with no xsl:result-document at all: section 24.3
		// starts the current output URI at the base output URI, so the answer
		// is the same destination rather than the empty sequence.
		{"principal tree", principalXSL, "", nil, dirURI(mustGetwd(t))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := reportedOutputURI(t, bin, tc.sheet, tc.readBack, tc.args...)
			if got != tc.want {
				t.Errorf("current-output-uri() = %q, want %q", got, tc.want)
			}
			// The stylesheet's own location is the value the bug reported.
			// Naming it keeps a future regression recognisable rather than
			// leaving it as an unexplained mismatch.
			if strings.HasSuffix(got, ".xsl") {
				t.Errorf("current-output-uri() reported the stylesheet %q, "+
					"not the output destination", got)
			}
		})
	}
}

// TestCurrentOutputURIDirectoryEndsInSlash pins the property that makes a
// relative href resolve inside -result-dir rather than beside it.
func TestCurrentOutputURIDirectoryEndsInSlash(t *testing.T) {
	if got := dirURI("/tmp/results"); got != "file:///tmp/results/" {
		t.Errorf("dirURI = %q, want a trailing slash", got)
	}
	// Already slashed stays single-slashed.
	if got := dirURI("/tmp/results/"); got != "file:///tmp/results/" {
		t.Errorf("dirURI doubled the slash: %q", got)
	}
	// A Windows path must still be a well-formed file URI with an empty
	// authority. dirURI runs the path through filepath.Abs, which is relative
	// to the *host* -- a C: path is not absolute on a unix test runner -- so
	// the drive-letter spelling is checked at the layer uri_test.go uses,
	// with only the trailing slash added on top.
	if got := absPathToFileURI("C:/Users/marti/out") + "/"; got != "file:///C:/Users/marti/out/" {
		t.Errorf("a Windows directory URI = %q", got)
	}
}

// TestBaseOutputURIPrefersTheOutputFile checks the precedence directly: -o
// names the principal result, so it wins over -result-dir, which only says
// where the *secondary* documents land.
func TestBaseOutputURIPrefersTheOutputFile(t *testing.T) {
	got := baseOutputURI("/tmp/out.xml", "/tmp/results")
	if want := "file:///tmp/out.xml"; got != want {
		t.Errorf("baseOutputURI = %q, want %q", got, want)
	}
	if got := baseOutputURI("", "/tmp/results"); got != "file:///tmp/results/" {
		t.Errorf("baseOutputURI with only -result-dir = %q", got)
	}
}

// TestCurrentOutputURIDoesNotWeakenContainment is the security check. Setting
// a base output URI changes what a *relative* href resolves to for
// base-uri() purposes, so it must be shown not to have loosened the
// -result-dir containment that refuses an href escaping the directory.
//
// The containment check works on the raw @href rather than on the resolved
// URI, so it is unaffected by the base output URI -- this test is what says
// so out loud.
func TestCurrentOutputURIDoesNotWeakenContainment(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "esc.xsl")
	const sheet = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:param name="dest" select="'../escape.xml'"/>
  <xsl:template match="/" name="xsl:initial-template">
    <xsl:result-document href="{$dest}"><test>escaped</test></xsl:result-document>
  </xsl:template>
</xsl:stylesheet>
`
	if err := os.WriteFile(xsl, []byte(sheet), 0o644); err != nil {
		t.Fatal(err)
	}
	resultDir := filepath.Join(dir, "results")

	out, err := exec.Command(bin, "-xsl", xsl, "-initial-template", initialTemplate,
		"-result-dir", resultDir).CombinedOutput()
	if err == nil {
		t.Fatalf("href %q escaping -result-dir should have failed; got:\n%s", "../escape.xml", out)
	}
	if !strings.Contains(string(out), "outside") {
		t.Errorf("the error should report the containment failure; got:\n%s", out)
	}
	// Nothing may have been written outside the directory.
	if _, err := os.Stat(filepath.Join(dir, "escape.xml")); !os.IsNotExist(err) {
		t.Errorf("the escaping document was written to %s", filepath.Join(dir, "escape.xml"))
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
