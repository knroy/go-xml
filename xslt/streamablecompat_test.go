package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// XTSE3430: an instruction processed in XSLT 1.0 compatibility mode inside a
// streamable mode is roaming and free-ranging.
//
// Section 3.9.1: "Processing an instruction with XSLT 1.0 behavior is not
// compatible with streaming. More specifically, and notwithstanding anything
// stated in 19 Streamability, an instruction that is processed with XSLT 1.0
// behavior is roaming and free-ranging, which has the effect that any construct
// containing such an instruction is not guaranteed-streamable."
//
// The "notwithstanding" is what makes the rule checkable without the §19.8
// posture and sweep analysis, which this engine does not implement. The case is
// streamable-141 in the W3C suite.
func TestStreamableCompatXTSE3430(t *testing.T) {
	compile := func(t *testing.T, sheet string) error {
		t.Helper()
		stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the stylesheet: %v", err)
		}
		_, err = Compile(stree.Root, CompileOptions{})
		return err
	}

	// The shape streamable-141 has: version="1.0" on one instruction inside a
	// template belonging to a mode declared streamable.
	t.Run("reported", func(t *testing.T) {
		err := compile(t, `<xsl:stylesheet version="3.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:mode name="s" streamable="yes"/>
			<xsl:template match="*" mode="s">
			  <xsl:copy><xsl:apply-templates mode="s" version="1.0"/></xsl:copy>
			</xsl:template>
		</xsl:stylesheet>`)
		if err == nil {
			t.Fatal("version=\"1.0\" in a streamable mode should be XTSE3430")
		}
		if !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("want XTSE3430, got: %v", err)
		}
	})

	// The same stylesheet without the streamable declaration is ordinary 1.0
	// compatibility mode and must compile: the error is about streaming, not
	// about version="1.0", and 314 of the suite's own stylesheets are written
	// in 1.0 throughout.
	t.Run("not streamable is fine", func(t *testing.T) {
		if err := compile(t, `<xsl:stylesheet version="3.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:mode name="s"/>
			<xsl:template match="*" mode="s">
			  <xsl:copy><xsl:apply-templates mode="s" version="1.0"/></xsl:copy>
			</xsl:template>
		</xsl:stylesheet>`); err != nil {
			t.Fatalf("a non-streamable mode should not raise: %v", err)
		}
	})

	// A streamable mode with no 1.0 instruction is what the other cases in the
	// streamable set look like, and the check must leave them alone. This is
	// the boundary that would turn one gained case into many lost ones.
	t.Run("streamable without 1.0 is fine", func(t *testing.T) {
		if err := compile(t, `<xsl:stylesheet version="3.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:mode name="s" streamable="yes"/>
			<xsl:template match="*" mode="s">
			  <xsl:copy><xsl:apply-templates mode="s"/></xsl:copy>
			</xsl:template>
		</xsl:stylesheet>`); err != nil {
			t.Fatalf("a streamable mode with no 1.0 instruction should not raise: %v", err)
		}
	})

	// A 1.0 instruction in a template belonging to a *different*, non-streamable
	// mode is not the error either. The rule is scoped to the streamable mode.
	t.Run("other mode is fine", func(t *testing.T) {
		if err := compile(t, `<xsl:stylesheet version="3.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:mode name="s" streamable="yes"/>
			<xsl:template match="*" mode="other">
			  <xsl:copy><xsl:apply-templates mode="other" version="1.0"/></xsl:copy>
			</xsl:template>
		</xsl:stylesheet>`); err != nil {
			t.Fatalf("a 1.0 instruction outside the streamable mode should not raise: %v", err)
		}
	})
}
