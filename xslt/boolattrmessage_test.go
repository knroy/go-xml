package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// XSLT 3.0 types xsl:output/@indent as yes-or-no, which the schema for
// stylesheets says also admits "true", "false", "1" and "0"; 2.0 admits only
// "yes" and "no". The check already accepted by version, but its XTSE0020
// message always listed the 2.0 pair, so a 3.0 author refused for
// indent="maybe" was told that indent="true" -- which the same check accepts
// -- was not an option either.
func TestBoolAttrMessageNamesTheVersionsSpellings(t *testing.T) {
	compile := func(version string) error {
		src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="` + version + `">
			<xsl:output indent="maybe"/>
		</xsl:stylesheet>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{})
		return err
	}
	for _, tc := range []struct {
		version, want string
	}{
		{"3.0", "is not one of yes, no, true, false, 1, 0 (XTSE0020)"},
		{"2.0", "is not one of yes, no (XTSE0020)"},
	} {
		err := compile(tc.version)
		if err == nil {
			t.Fatalf("version %s: indent=\"maybe\" compiled, want XTSE0020", tc.version)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("version %s: got %q, want it to contain %q", tc.version, err, tc.want)
		}
	}
}
