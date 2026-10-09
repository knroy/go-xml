package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The state an element sets for its content is put back when it ends, on
// every path out of it, and only the state it set: the namespaces an empty
// element declares go out of scope, and the raw-text "<" a nested element's
// text ends with still guards what follows inside the script.
func TestSerializerElementStateRestored(t *testing.T) {
	tree, err := xdm.ParseString(`<r><a xmlns:p="urn:p" p:x="1"/><b xmlns:p="urn:p" p:y="2"/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := Serialize(&sb, xdm.One(tree.Root), OutputSettings{OmitXMLDecl: true}, nil); err != nil {
		t.Fatal(err)
	}
	if want := `<r><a xmlns:p="urn:p" p:x="1"/><b xmlns:p="urn:p" p:y="2"/></r>`; sb.String() != want {
		t.Errorf("got %s, want %s", sb.String(), want)
	}

	sc := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "script"}, "")
	sc.AppendElement(xdm.QName{Local: "b"}).AppendText("var a=1<")
	sc.AppendText("/script><svg onload=alert(1)>")
	sb.Reset()
	err = Serialize(&sb, xdm.Sequence{sc}, OutputSettings{Method: "html", OmitXMLDecl: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "SERE0007") {
		t.Errorf(`"</" split across an element end inside <script>: got %v, want SERE0007`, err)
	}
}
