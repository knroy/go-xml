package xslt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

func TestSerializeRejectsMalformedCommentAndPI(t *testing.T) {
	for _, n := range []*xdm.Node{
		xdm.NewNode(xdm.KindComment, xdm.QName{}, "a--b"),
		xdm.NewNode(xdm.KindComment, xdm.QName{}, "a-"),
		xdm.NewNode(xdm.KindPI, xdm.QName{Local: "p"}, "a?>b"),
		xdm.NewNode(xdm.KindPI, xdm.QName{Local: "xml"}, "a"),
		xdm.NewNode(xdm.KindComment, xdm.QName{}, "a\x01b"),
	} {
		var out bytes.Buffer
		err := Serialize(&out, xdm.Sequence{n}, OutputSettings{Method: "xml", OmitXMLDecl: true}, nil)
		if err == nil {
			t.Errorf("Serialize(%v) succeeded with %q", n.Kind(), out.String())
			continue
		}
		if !strings.Contains(err.Error(), "SERE000") {
			t.Errorf("Serialize(%v) error = %v, want SERE code", n.Kind(), err)
		}
	}
}

// TestSerializeBindsElementPrefixOnce pins the repair for namespace-alias-2620.
// xsl:namespace-alias can leave an element whose own prefix is also carried as
// a namespace node bound elsewhere; writing both produced two xmlns:y
// attributes and a result that is not well-formed XML. It passed the suite
// only because the malformed output failed to parse and the judge fell back to
// comparing text, which happened to match.
func TestSerializeBindsElementPrefixOnce(t *testing.T) {
	n := xdm.NewNode(xdm.KindElement, xdm.QName{Prefix: "y", Local: "transform", URI: "urn:wanted"}, "")
	// Two bindings for one prefix, which is the shape the real case has: the
	// competing aliases resolve to the SAME uri, so a guard that only drops a
	// differing one writes both. The differing binding is added as well, since
	// dropping that is the other half of the rule.
	n.AddNamespace("y", "urn:wanted")
	n.AddNamespace("y", "urn:wanted")
	var out bytes.Buffer
	if err := Serialize(&out, xdm.Sequence{n},
		OutputSettings{Method: "xml", OmitXMLDecl: true}, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "xmlns:y=") != 1 {
		t.Errorf("Serialize wrote %q, want exactly one xmlns:y", got)
	}
	if !strings.Contains(got, `xmlns:y="urn:wanted"`) {
		t.Errorf("Serialize wrote %q, want the element's own binding to survive", got)
	}
	if _, err := xdm.ParseString(got, xdm.ParseOptions{}); err != nil {
		t.Errorf("Serialize produced XML that does not parse: %v", err)
	}

	// The other half of the rule: when the competing binding names a
	// different uri, the element's own must still be the one written, or the
	// name it was serialised under is unresolvable.
	m := xdm.NewNode(xdm.KindElement, xdm.QName{Prefix: "y", Local: "transform", URI: "urn:wanted"}, "")
	m.AddNamespace("y", "urn:other")
	var out2 bytes.Buffer
	if err := Serialize(&out2, xdm.Sequence{m},
		OutputSettings{Method: "xml", OmitXMLDecl: true}, nil); err != nil {
		t.Fatal(err)
	}
	got2 := out2.String()
	if strings.Count(got2, "xmlns:y=") != 1 {
		t.Errorf("Serialize wrote %q, want exactly one xmlns:y", got2)
	}
	if !strings.Contains(got2, `xmlns:y="urn:wanted"`) {
		t.Errorf("Serialize wrote %q, want the element's own binding", got2)
	}
}

// TestSerializeRejectsHTMLCommentBreakout: an HTML parser ends a comment that
// opens with ">" or "->" right there, as "<!-->" or "<!--->", so what follows
// is live markup. The html and xhtml methods refuse such a comment; the xml
// method writes it, and a ">" elsewhere in a comment is ordinary text.
func TestSerializeRejectsHTMLCommentBreakout(t *testing.T) {
	for _, c := range []struct {
		text, method string
		refuse       bool
	}{
		{"><img src=x onerror=alert(1)> ", "html", true},
		{"->x", "html", true},
		{">x", "xhtml", true},
		{"->x", "xhtml", true},
		{">x", "xml", false},
		{" >x", "html", false},
		{"a->b", "html", false},
	} {
		doc, err := xdm.ParseString(`<p><!--`+c.text+`--></p>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = Serialize(&out, xdm.Sequence{doc.Root}, OutputSettings{Method: c.method, OmitXMLDecl: true}, nil)
		if refused := err != nil && strings.Contains(err.Error(), "SERE0003"); refused != c.refuse {
			t.Errorf("%s comment %q: wrote %q, err = %v; want refused %v", c.method, c.text, out.String(), err, c.refuse)
		}
	}
}

// TestSerializeHTML5DefaultNamespaceOnce: HTML5 prefix normalisation writes
// <svg:svg> unprefixed, with a default namespace for SVG. A default
// declaration the element carries for another URI is the binding that name
// displaces, as a prefixed one is: writing both put two xmlns attributes on
// one start tag. The child in that other namespace re-declares it.
func TestSerializeHTML5DefaultNamespaceOnce(t *testing.T) {
	doc, err := xdm.ParseString(`<svg:svg xmlns:svg="http://www.w3.org/2000/svg" xmlns="urn:x"><y/></svg:svg>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []OutputSettings{
		{Method: "xhtml", HTMLVersion: "5.0", OmitXMLDecl: true},
		{Method: "html", OmitXMLDecl: true},
	} {
		var out bytes.Buffer
		if err := Serialize(&out, xdm.Sequence{doc.Root}, o, nil); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, `<svg xmlns="http://www.w3.org/2000/svg">`) ||
			!strings.Contains(got, `<y xmlns="urn:x"`) {
			t.Errorf("%s: wrote %q", o.Method, got)
		}
		if o.Method == "xhtml" {
			if _, err := xdm.ParseString(got, xdm.ParseOptions{}); err != nil {
				t.Errorf("xhtml output does not parse: %v", err)
			}
		}
	}
}
