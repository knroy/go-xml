package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSerializeXMLIndentConstraints pins fn:serialize to the indent
// constraints of Serialization 3.1 §5.1.4. Each case is one constraint the
// serializer broke before: suppress-indentation stopped one level down,
// xml:space="preserve" was ignored, and typed simple and mixed content was
// indented (the xs:string value "  " came back as "\n      \n  ").
func TestSerializeXMLIndentConstraints(t *testing.T) {
	cases := []struct {
		name, doc, want string
		suppress        string
		annotate        func(root *xdm.Node)
	}{
		{name: "suppress-indentation covers the whole content",
			doc: `<a><s><b><c/></b></s></a>`, suppress: "s",
			want: "<a>\n  <s><b><c/></b></s>\n</a>"},
		{name: "xml:space=preserve covers the whole content",
			doc:  `<a><p xml:space="preserve"><b><c/></b></p></a>`,
			want: "<a>\n  <p xml:space=\"preserve\"><b><c/></b></p>\n</a>"},
		{name: "typed simple content is never indented",
			doc: `<r><e>  </e></r>`, annotate: func(r *xdm.Node) {
				r.TypeAnnotation, r.NoTypedValue = "anyType", true
				r.Children[0].TypeAnnotation = "string"
			},
			want: "<r>\n  <e>  </e>\n</r>"},
		{name: "typed mixed content is not indented",
			doc: `<r><m> <b/> </m></r>`, annotate: func(r *xdm.Node) {
				r.TypeAnnotation, r.NoTypedValue = "anyType", true
				m := r.Children[0]
				m.TypeAnnotation, m.MixedContent = "anyType", true
			},
			want: "<r>\n  <m> <b/> </m>\n</r>"},
		{name: "an element with no element children is not indented",
			doc:  `<r><e> <!--c--> </e></r>`,
			want: "<r>\n  <e> <!--c--> </e>\n</r>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			root := tree.Root.Children[0]
			if c.annotate != nil {
				c.annotate(root)
			}
			opts := serializeOptions{method: "xml", indent: true}
			if c.suppress != "" {
				opts.suppressIndent = map[xdm.QName]bool{{Local: c.suppress}: true}
			}
			sb := newSink(NewContext(nil, Builtins()))
			serializeNode(sb, root, opts, 0)
			if got := sb.String(); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}
