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

// Every raw write inside an html <script> is held to the text-node SERE0007
// rule, not only text: a descendant's attribute value, a comment, a
// processing instruction and a nested <script> each closed the outer one
// and let the rest of the value through as live markup.
func TestSerializerRawTextBreakouts(t *testing.T) {
	cases := map[string]func(sc *xdm.Node){
		"attribute": func(sc *xdm.Node) {
			sc.AppendElement(xdm.QName{Local: "div"}).AppendAttr(xdm.QName{Local: "title"}, "</script><img src=x onerror=alert(1)>")
		},
		"comment": func(sc *xdm.Node) {
			sc.AppendComment("</script><img src=x onerror=alert(2)>")
		},
		"pi": func(sc *xdm.Node) {
			sc.AppendPI("x", "</script ")
		},
		"nested": func(sc *xdm.Node) {
			sc.AppendElement(xdm.QName{Local: "script"})
			sc.AppendText("<img src=x onerror=alert(3)>")
		},
	}
	for name, build := range cases {
		sc := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "script"}, "")
		build(sc)
		var sb strings.Builder
		err := Serialize(&sb, xdm.Sequence{sc}, OutputSettings{Method: "html", OmitXMLDecl: true}, nil)
		if err == nil || !strings.Contains(err.Error(), "SERE0007") {
			t.Errorf("%s: got %v, output %q; want SERE0007", name, err, sb.String())
		}
	}

	// The same content outside a raw-text element, and a raw attribute
	// value with no "</", still serialise.
	sc := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "script"}, "")
	sc.AppendElement(xdm.QName{Local: "p"}).AppendAttr(xdm.QName{Local: "class"}, "a<b")
	var sb strings.Builder
	if err := Serialize(&sb, xdm.Sequence{sc}, OutputSettings{Method: "html", OmitXMLDecl: true}, nil); err != nil {
		t.Fatal(err)
	}
	if want := `<script><p class="a<b"></p></script>`; sb.String() != want {
		t.Errorf("got %s, want %s", sb.String(), want)
	}
}
