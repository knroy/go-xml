package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestForwardsCompatibleFollowsProcessorVersion pins that forwards compatible
// processing is decided against the version of XSLT the processor implements,
// not against a fixed 3.0.
//
// XSLT 3.0 section 3.9 puts an element in forwards compatible mode when its
// effective version is greater than the version the processor implements. A
// host chooses that version with CompileOptions.MaxVersion, so the same
// stylesheet is read two ways.
//
// The W3C suite pins exactly this with a pair of cases over one stylesheet
// written version="3.0" carrying the 4.0 attribute bind-group:
//
//	for-each-group-002  (spec XSLT30+) expects XTSE0090
//	for-each-group-002a (spec XSLT20)  expects XPST0008
//
// A 3.0 processor is not in forwards compatible mode for a 3.0 stylesheet, so
// the unknown attribute is a static error. A 2.0 processor is, so it must
// ignore the attribute and carry on — reaching the reference to $g, which the
// ignored attribute would have bound, and failing there instead.
func TestForwardsCompatibleFollowsProcessorVersion(t *testing.T) {
	const sheet = `<table xsl:version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:for-each-group select="cities/city" group-by="@country" bind-group="g">
    <xsl:value-of select="$g"/>
  </xsl:for-each-group>
</table>`

	for _, tc := range []struct {
		name       string
		maxVersion float64
		want       string
	}{
		// Zero means uncapped, and so 3.0.
		{"3.0 processor rejects the unknown attribute", 0, "XTSE0090"},
		{"3.0 processor, stated explicitly", 3.0, "XTSE0090"},
		// Forwards compatible: bind-group is ignored, and $g is then unbound.
		{"2.0 processor ignores it and fails on $g", 2.0, "XPST0008"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// XPST0008 for the unbound $g is a dynamic error, so the
			// 2.0 arm has to run the transform to reach it, exactly as
			// the suite does.
			st, err := Compile(doc.Root, CompileOptions{MaxVersion: tc.maxVersion})
			if err == nil {
				src, perr := xdm.ParseString(`<cities><city country="x"/></cities>`, xdm.ParseOptions{})
				if perr != nil {
					t.Fatalf("parse source: %v", perr)
				}
				_, err = st.Transform(context.Background(), src.Root, TransformOptions{})
			}
			if err == nil {
				t.Fatalf("MaxVersion %g: compiled and ran clean, want %s", tc.maxVersion, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("MaxVersion %g: got %v, want %s", tc.maxVersion, err, tc.want)
			}
		})
	}
}
