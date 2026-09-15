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
// Each spelling is listed once. The twenty enumerations in elementtable.go
// that spell all six themselves used to have true, false, 1 and 0 appended a
// second time, because the builder added an alias whenever boolAliases mapped
// it onto a value already in the enumeration -- which for those is always
// true. xsl:output/@build-tree is one of them, and reported "not one of yes,
// no, true, false, 1, 0, true, false, 1, 0".
func TestBoolAttrMessageNamesTheVersionsSpellings(t *testing.T) {
	compile := func(version, attr string) error {
		src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="` + version + `">
			<xsl:output ` + attr + `="maybe"/>
		</xsl:stylesheet>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{})
		return err
	}
	for _, tc := range []struct {
		version, attr, want string
	}{
		// @indent carries only yes, no in the table: the aliases are added.
		{"3.0", "indent", "is not one of yes, no, true, false, 1, 0 (XTSE0020)"},
		{"2.0", "indent", "is not one of yes, no (XTSE0020)"},
		// @build-tree carries all six in the table: nothing is added, and the
		// exact message is asserted so a re-appended alias fails here.
		{"3.0", "build-tree", "is not one of yes, no, true, false, 1, 0 (XTSE0020)"},
	} {
		err := compile(tc.version, tc.attr)
		if err == nil {
			t.Fatalf("version %s: %s=\"maybe\" compiled, want XTSE0020",
				tc.version, tc.attr)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("version %s, %s: got %q, want it to contain %q",
				tc.version, tc.attr, err, tc.want)
		}
	}
}
