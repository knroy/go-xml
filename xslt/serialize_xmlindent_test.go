package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestXMLIndentRespectsTypedContent pins the type-aware constraints of
// Serialization 3.1 §5.1.4: whitespace MUST NOT be added in simple or empty
// content and SHOULD NOT in typed mixed content, while untyped and typed
// element-only content may still be indented. Before, a validated <e> of
// type xs:string holding "  " was written as "<e>  \n  </e>", changing its
// value.
func TestXMLIndentRespectsTypedContent(t *testing.T) {
	tree, err := xdm.ParseString(`<r><e>  </e><m> <b/> </m><k><c/></k></r>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.Children[0]
	r.TypeAnnotation, r.NoTypedValue = "anyType", true // anonymous element-only
	r.Children[0].TypeAnnotation = "string"
	m := r.Children[1]
	m.TypeAnnotation, m.MixedContent = "anyType", true // anonymous mixed
	r.Children[2].TypeAnnotation = "anyType"           // genuine xs:anyType

	var sb strings.Builder
	err = Serialize(&sb, xdm.One(r),
		OutputSettings{Method: "xml", Indent: true, OmitXMLDecl: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "<r>\n  <e>  </e>\n  <m> <b/> </m>\n  <k>\n    <c/>\n  </k>\n</r>"
	if got := sb.String(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
