package xslt

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// errCountCtx is a context whose Err answers Canceled after limit calls,
// and which counts the calls.
type errCountCtx struct {
	context.Context
	calls, limit int64
}

func (c *errCountCtx) Err() error {
	if atomic.AddInt64(&c.calls, 1) > c.limit {
		return context.Canceled
	}
	return nil
}

func numberAnyRun(t *testing.T, body string, n int, ctx context.Context) (string, error) {
	t.Helper()
	sd, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/"><out>`+body+`</out></xsl:template></xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := xdm.ParseString(`<r>`+strings.Repeat(`<e/>`, n)+`</r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Transform(ctx, d.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		return "", err
	}
	_, out, _ := strings.Cut(b.String(), "?>")
	return out, nil
}

// One xsl:number walks everything before its node, testing @count at each:
// level="any" the whole document prefix, level="single" the preceding
// siblings. A name-only pattern evaluates no expression, so nothing in the
// walk consulted the context; it now stops when the context is cancelled.
func TestNumberWalkCancellable(t *testing.T) {
	// The calls the rest of the transform makes, so that the limit is
	// crossed only inside the walk.
	base := &errCountCtx{Context: context.Background(), limit: 1 << 40}
	if _, err := numberAnyRun(t, `<xsl:for-each select="r/e[last()]"/>`, 5000, base); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"any", "single"} {
		ctx := &errCountCtx{Context: context.Background(), limit: base.calls + 100}
		_, err := numberAnyRun(t,
			`<xsl:for-each select="r/e[last()]"><xsl:number level="`+level+`" count="e"/></xsl:for-each>`, 5000, ctx)
		if err == nil {
			t.Errorf("level=%s: want the cancellation, got success after %d Err calls", level, ctx.calls)
		}
	}
}

// Numbering every node with a name-only pattern numbers the tree once, not
// once per node: Err is consulted per node walked, so the walks total about
// n, at least the one walk and far short of n*n/2.
func TestNumberAnyMemoised(t *testing.T) {
	ctx := &errCountCtx{Context: context.Background(), limit: 1 << 40}
	got, err := numberAnyRun(t,
		`<xsl:for-each select="r/e"><xsl:number level="any" from="r" count="e"/>,</xsl:for-each>`, 2000, ctx)
	if err != nil || !strings.HasSuffix(got, ",1999,2000,</out>") || !strings.HasPrefix(got, "<out>1,2,") {
		t.Fatalf("got %q, %v", got, err)
	}
	if ctx.calls < 2000 || ctx.calls > 20000 {
		t.Fatalf("%d Err calls for 2000 numbers", ctx.calls)
	}
}
