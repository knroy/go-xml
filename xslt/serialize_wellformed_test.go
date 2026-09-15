package xslt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func TestSerializeRejectsMalformedCommentAndPI(t *testing.T) {
	for _, n := range []*xdm.Node{
		{Kind: xdm.KindComment, Value: "a--b"},
		{Kind: xdm.KindComment, Value: "a-"},
		{Kind: xdm.KindPI, Name: xdm.QName{Local: "p"}, Value: "a?>b"},
		{Kind: xdm.KindPI, Name: xdm.QName{Local: "xml"}, Value: "a"},
		{Kind: xdm.KindComment, Value: "a\x01b"},
	} {
		var out bytes.Buffer
		err := Serialize(&out, xdm.Sequence{n}, OutputSettings{Method: "xml", OmitXMLDecl: true}, nil)
		if err == nil {
			t.Errorf("Serialize(%v) succeeded with %q", n.Kind, out.String())
			continue
		}
		if !strings.Contains(err.Error(), "SERE000") {
			t.Errorf("Serialize(%v) error = %v, want SERE code", n.Kind, err)
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
	n := &xdm.Node{Kind: xdm.KindElement,
		Name: xdm.QName{Prefix: "y", Local: "transform", URI: "urn:wanted"}}
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
	m := &xdm.Node{Kind: xdm.KindElement,
		Name: xdm.QName{Prefix: "y", Local: "transform", URI: "urn:wanted"}}
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
