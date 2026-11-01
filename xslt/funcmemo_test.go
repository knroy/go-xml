package xslt

import (
	"context"
	"testing"
	"unsafe"

	"github.com/knroy/go-xml/v2/xdm"
)

// The function memo keeps a 16-byte hash of each call's key, not the key. On
// DocBook table-cals.049 the keys of map and array arguments were 209 MB of a
// 365 MB live heap.
func TestFunctionMemoKeepsHashNotKey(t *testing.T) {
	s := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"/></xsl:stylesheet>`)
	rt, err := newRuntime(s, context.Background(), nil, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var k callHash
	if got := unsafe.Sizeof(k); got != 16 {
		t.Errorf("memo key is %d bytes, want 16", got)
	}
	rt.funcResults[hashCallKey([]byte("a"))] = xdm.Sequence{}
	if _, ok := rt.funcResults[hashCallKey([]byte("b"))]; ok {
		t.Error("distinct keys share a hash")
	}
	if _, ok := rt.funcResults[hashCallKey([]byte("a"))]; !ok {
		t.Error("equal keys do not share a hash")
	}
}
