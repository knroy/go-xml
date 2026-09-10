package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xslt"
)

// The hostile stylesheet from the audit report, run through the public API
// exactly as a caller with default options would run it. Before the gate this
// produced <out><one>sk-live-DO-NOT-LEAK</one><count>92</count></out>.
//
// The variable is planted in the real process environment, so a regression
// that reaches os.Environ again shows up as the secret in the output rather
// than as a mocked stand-in for it.
const hostileEnvSheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:template match="/">
	    <out>
	      <one><xsl:value-of select="environment-variable('GOXML_TEST_SECRET')"/></one>
	      <count><xsl:value-of select="count(available-environment-variables())"/></count>
	    </out>
	  </xsl:template>
	</xsl:stylesheet>`

func runEnvSheet(t *testing.T, opts xslt.TransformOptions) string {
	t.Helper()
	tree, err := xdm.ParseString(hostileEnvSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), doc.Root, opts)
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	return xslt.SerializeAsXML(res)
}

// Default options -- nothing configured, which is how the defect was reached.
// The secret must not appear, and the enumeration must be empty rather than
// an error: a withheld variable has to look like an unset one.
func TestEnvironmentWithheldFromStylesheetByDefault(t *testing.T) {
	t.Setenv("GOXML_TEST_SECRET", "sk-live-DO-NOT-LEAK")

	got := runEnvSheet(t, xslt.TransformOptions{})
	if strings.Contains(got, "sk-live-DO-NOT-LEAK") {
		t.Fatalf("the process environment leaked into the output: %s", got)
	}
	if !strings.Contains(got, "<one/>") && !strings.Contains(got, "<one></one>") {
		t.Errorf("environment-variable did not yield the empty sequence: %s", got)
	}
	if !strings.Contains(got, "<count>0</count>") {
		t.Errorf("available-environment-variables enumerated the process: %s", got)
	}
	// Setting a document resolver is a different grant and must not imply
	// this one: fn:doc reads a confined URI space, this reads process state
	// no root bounds.
	got = runEnvSheet(t, xslt.TransformOptions{Documents: &xslt.FileResolver{Roots: []string{t.TempDir()}}})
	if strings.Contains(got, "sk-live-DO-NOT-LEAK") {
		t.Fatalf("a document resolver implied the environment grant: %s", got)
	}
}

// A caller who does opt in gets the environment, so the gate is a gate and
// not a removal.
func TestEnvironmentReadableWhenTheCallerOptsIn(t *testing.T) {
	t.Setenv("GOXML_TEST_SECRET", "sk-live-DO-NOT-LEAK")

	got := runEnvSheet(t, xslt.TransformOptions{Environment: xpath.OSEnvironment{}})
	if !strings.Contains(got, "<one>sk-live-DO-NOT-LEAK</one>") {
		t.Fatalf("opting in did not expose the environment: %s", got)
	}
	if strings.Contains(got, "<count>0</count>") {
		t.Errorf("opting in enumerated no variables: %s", got)
	}
}
