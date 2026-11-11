package xquery

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// TestSMB4BuilderRefusalIsReturned: a node-budget refusal latched by a
// builder call with no error return (a top-level text constructor) ends the
// query with the error instead of a silently shorter result.
func TestSMB4BuilderRefusalIsReturned(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	xpathleaf.SetLimits(ctx, xpathleaf.Limits{Nodes: 50})
	seq, err := Eval(`count(for $i in 1 to 100 return text {'x'})`, ctx, Options{})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("got %v, %v; want the node budget's refusal", seq, err)
	}
}

// TestSMB4ContentCopyBytesAreCharged: each copy of a node into element content
// duplicates its value bytes, and is charged them.
func TestSMB4ContentCopyBytesAreCharged(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	xpathleaf.SetLimits(ctx, xpathleaf.Limits{Bytes: 1 << 20})
	// 2,000 copies of a 1,000-byte text node: 2 MB against a 1 MB budget.
	_, err := Eval(`let $d := <a>{string-join((1 to 100) ! 'abcdefghij')}</a>
		return count(<r>{for $i in 1 to 2000 return $d}</r>/a)`, ctx, Options{})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("err = %v, want the byte budget's refusal", err)
	}
}
