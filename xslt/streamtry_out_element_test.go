package xslt

import (
	"context"
	"io"
	"strings"
	"testing"
)

// tryOneElementSheet is a body of 200,000 elements, inside a try whose
// body's one top-level item is the element holding them
// (xdmbuild/tryfwd.go start) when rollback is set.
func tryOneElementSheet(t *testing.T, rollback string) *Stylesheet {
	body := `<all><xsl:for-each select="1 to 200000"><r n="{.}"><xsl:value-of select=". * 2"/></r></xsl:for-each></all>`
	if rollback != "" {
		body = `<xsl:try rollback-output="` + rollback + `">` + body + `<xsl:catch><bad/></xsl:catch></xsl:try>`
	}
	return compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:template name="main"><out>`+body+`</out></xsl:template></xsl:stylesheet>`)
}

// TestStreamTryOutOneElement: under Output, a try around one large element
// writes the element's content as it is made rather than holding it as a
// tree to the try's end. With rollback-output="no" the content is written
// through, so the nodes written are handed back to MaxNodes, as for any
// streamed element, where a held element counts them all. With "yes" the
// try holds bytes: its peak heap stays nearer the same body streamed
// without a try than the tree's (the sampled peak counts garbage not yet
// collected, so it is compared, not bounded).
func TestStreamTryOutOneElement(t *testing.T) {
	ctx := context.Background()
	run := func(s *Stylesheet, out io.Writer, maxNodes int) error {
		opts := TransformOptions{InitialTemplate: "main", MaxNodes: maxNodes, Output: out}
		res, err := s.Transform(ctx, nil, opts)
		if err == nil && out == nil {
			err = res.Serialize(io.Discard)
		}
		return err
	}
	// 600,000 result nodes are built; a few are held at once.
	no := tryOneElementSheet(t, "no")
	if err := run(no, io.Discard, 10000); err != nil {
		t.Errorf("rollback-output=\"no\" under Output, MaxNodes 10,000: %v", err)
	}
	if err := run(no, nil, 10000); err == nil || !strings.Contains(err.Error(), "XPDY0130") {
		t.Errorf("tree mode, MaxNodes 10,000: %v, want XPDY0130", err)
	}
	if testing.Short() || raceEnabled {
		return
	}
	peak := func(s *Stylesheet, out io.Writer) uint64 {
		return splitPeak(func() {
			if err := run(s, out, 0); err != nil {
				t.Error(err)
			}
		})
	}
	plain := tryOneElementSheet(t, "")
	base, tree := peak(plain, io.Discard), peak(plain, nil)
	got := peak(tryOneElementSheet(t, "yes"), io.Discard)
	t.Logf("peak heap: try under Output %.1f MB; no try: Output %.1f MB, tree %.1f MB",
		float64(got)/1e6, float64(base)/1e6, float64(tree)/1e6)
	if got > (base+tree)/2 {
		t.Errorf("the try under Output peaked at %.1f MB, nearer the tree's %.1f MB than %.1f MB",
			float64(got)/1e6, float64(tree)/1e6, float64(base)/1e6)
	}
}
