package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// compileRepeated compiles a stylesheet with twenty copies of decl (each
// formatted with its index) twenty times and expects want in every error.
func compileRepeated(t *testing.T, decl, want string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, decl, i)
	}
	b.WriteString(`<xsl:template name="xsl:initial-template"><out/></xsl:template></xsl:stylesheet>`)
	for i := 0; i < 20; i++ {
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}

// XTSE0545 with twenty conflicting modes names the least by name every time.
func TestModeConflictReportedInNameOrder(t *testing.T) {
	compileRepeated(t,
		`<xsl:mode name="m%[1]d" on-no-match="shallow-copy"/><xsl:mode name="m%[1]d" on-no-match="deep-skip"/>`,
		"conflicting values for mode m0 ")
}

// XTSE3350 with twenty tied accumulator names names the least every time.
func TestAccumulatorTieReportedInNameOrder(t *testing.T) {
	compileRepeated(t,
		`<xsl:accumulator name="a%[1]d" initial-value="0"><xsl:accumulator-rule match="x" select="1"/></xsl:accumulator>`+
			`<xsl:accumulator name="a%[1]d" initial-value="0"><xsl:accumulator-rule match="x" select="1"/></xsl:accumulator>`,
		"named a0 at the same import precedence")
}
