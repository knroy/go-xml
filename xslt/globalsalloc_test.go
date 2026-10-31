package xslt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Binding a stylesheet's globals at the start of a transform costs about one
// allocation per global, the closure that forces it, rather than also a
// LazyVar and the entries of two more maps. DocBook declares several hundred global parameters and reads few.
func TestGlobalBindingAllocs(t *testing.T) {
	run := func(n int) float64 {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, `<xsl:param name="p%d" select="%d"/>`, i, i)
		}
		src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + b.String() +
			`<xsl:template name="main"><xsl:sequence select="$p0"/></xsl:template></xsl:stylesheet>`
		d, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(d.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(5, func() {
			if _, err := st.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	got := (run(201) - run(1)) / 200
	// 1.0 at the time of writing (the closure); it was 2.1.
	if got > 1.5 {
		t.Errorf("an unread global costs %.1f allocations per transform, want at most 1.5", got)
	}
}
