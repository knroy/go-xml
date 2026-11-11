package xdmbuild_test

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// TestSM11AppendOpaqueFlattenRefusal: an array whose shared members describe
// 2^25 leaves is refused by AppendOpaque's error return, not by a panic.
func TestSM11AppendOpaqueFlattenRefusal(t *testing.T) {
	a := xdm.NewArray(xdm.Sequence{xdm.NewInteger(1)})
	for range 25 {
		a = xdm.NewArray(xdm.Sequence{a}, xdm.Sequence{a})
	}
	eb := xdmbuild.New(xsltLike{}).StartElement(qn("r"))
	err := eb.AppendOpaque(a)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("AppendOpaque = %v, want ErrResourceLimit", err)
	}
}
