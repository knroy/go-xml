package xdmbuild_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// A builder made by InPlaceDocument builds children in place and refuses an
// attribute or namespace node the way ToDocument does.
func TestInPlaceDocument(t *testing.T) {
	b, root := xdmbuild.InPlaceDocument(xsltLike{})
	e := b.StartElement(xdm.QName{Local: "e"})
	e.AppendText("x")
	if root.FirstChild() == nil || root.FirstChild().Name().Local != "e" {
		t.Fatal("child not built in place")
	}
	err := b.AddAttribute(xdm.QName{Local: "a"}, "v")
	want := "XSLT-" + string(rune('0'+int(xdmbuild.FaultAttrOnDocument)))
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("attribute: got %v, want %s", err, want)
	}
	if err := b.AddNamespace("p", "urn:p"); err == nil {
		t.Error("namespace on document accepted")
	}
	if root.NumAttrs() != 0 {
		t.Error("attribute attached to the document node")
	}
}
