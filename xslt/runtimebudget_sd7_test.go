package xslt

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// TestTransformFromPlainXPathKeepsCallerBytes: a bare XPath caller's byte
// budget is unheld (per expression), and the nested transform's runtime holds
// its own; arming that hold must not erase what the calling expression has
// already charged. 700,000 bytes per step under a 1 MiB bound is refused at
// the second step, with or without the fn:transform beside it.
func TestTransformFromPlainXPathKeepsCallerBytes(t *testing.T) {
	const tr = `transform(map{'stylesheet-text': '<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"><xsl:template name="xsl:initial-template"><r/></xsl:template></xsl:stylesheet>'})`
	for _, step := range []string{`'y'`, tr} {
		ctx := plainContext(nil)
		xpathleaf.SetLimits(ctx, xpathleaf.Limits{Bytes: 1 << 20})
		_, err := xpath.Eval(`count((1 to 3) ! (string-join((1 to 70000) ! '0123456789'), `+step+`))`, ctx, nil)
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("step %.20s: err = %v, want the byte budget's refusal", step, err)
		}
	}
}
