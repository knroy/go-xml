package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// cdataTree builds an element with a text child that has to be escaped, which
// is the only shape cdata-section-elements has anything to say about: without
// the parameter the "<" is written as &lt;, with it the run is wrapped in a
// CDATA section instead.
func cdataTree(local, uri, prefix string) *xdm.Node {
	root := &xdm.Node{
		Kind: xdm.KindElement,
		Name: xdm.QName{Prefix: prefix, URI: uri, Local: local},
	}
	root.Children = []*xdm.Node{{
		Kind:   xdm.KindText,
		Value:  "x<y",
		Parent: root,
	}}
	return root
}

// TestSerializeParamElementCdataSectionElements pins the element form of
// fn:serialize to cdata-section-elements.
//
// The map form honours the parameter and the element form had it on the
// accept-and-ignore list, so fn:serialize($n, map{...}) wrote a CDATA section
// and fn:serialize($n, $params) wrote an escaped run for the same request --
// the same split that undeclare-prefixes and version were fixed for. The
// element form carries lexical names rather than resolved QNames, which is why
// it was skipped; the parameter element is a node in a tree, so the prefixes
// resolve against its own in-scope namespaces.
func TestSerializeParamElementCdataSectionElements(t *testing.T) {
	for _, tc := range []struct {
		name  string
		param string
		want  bool
	}{
		{"named", "a", true},
		{"not named", "other", false},
		{"one of several", "p q a r", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(nil, Builtins())
			ctx.Version, ctx.LibraryVersion = XPath31, XPath31
			c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(cdataTree("a", "", "")))
			c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
				map[string]string{"cdata-section-elements": tc.param})))
			seq, err := Eval(`serialize($n, $p)`, c, nil)
			if err != nil {
				t.Fatalf("cdata-section-elements=%q: %v", tc.param, err)
			}
			got := seq[0].(*xdm.Atomic).String()
			if has := strings.Contains(got, "<![CDATA["); has != tc.want {
				t.Errorf("cdata-section-elements=%q gave %q, CDATA present = %v, want %v",
					tc.param, got, has, tc.want)
			}
		})
	}
}

// TestSerializeParamElementCdataMatchesMapForm pins the two forms together.
//
// The defect was not that either form was wrong on its own but that they
// disagreed, so the property worth holding is that the same request gets the
// same answer whichever way it is written.
func TestSerializeParamElementCdataMatchesMapForm(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(cdataTree("a", "", "")))
	c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
		map[string]string{"cdata-section-elements": "a"})))

	elemSeq, err := Eval(`serialize($n, $p)`, c, nil)
	if err != nil {
		t.Fatalf("element form: %v", err)
	}
	mapSeq, err := Eval(
		`serialize($n, map{'cdata-section-elements':xs:QName('a')})`, c, nil)
	if err != nil {
		t.Fatalf("map form: %v", err)
	}
	elem := elemSeq[0].(*xdm.Atomic).String()
	mapf := mapSeq[0].(*xdm.Atomic).String()
	if elem != mapf {
		t.Errorf("element form = %q, map form = %q, want the same", elem, mapf)
	}
	// A control: with the parameter dropped the output differs, so the
	// agreement above is agreement on the parameter having been applied and
	// not on both forms ignoring it.
	plain, err := Eval(`serialize($n, map{})`, c, nil)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if got := plain[0].(*xdm.Atomic).String(); got == elem {
		t.Errorf("control = %q, same as the parameterised output: the test "+
			"cannot tell whether the parameter was applied", got)
	}
}

// TestSerializeParamElementCdataResolvesPrefix pins prefix resolution.
//
// The name is lexical, so a prefixed one means the element in whatever
// namespace that prefix is bound to on the parameter element -- and a prefix
// bound to nothing names no element, which is refused rather than skipped.
func TestSerializeParamElementCdataResolvesPrefix(t *testing.T) {
	const ns = "http://example.com/n"
	params := paramsElement(map[string]string{"cdata-section-elements": "e:a"})
	params.Namespaces = []*xdm.Node{{
		Kind:   xdm.KindNamespace,
		Name:   xdm.QName{Local: "e"},
		Value:  ns,
		Parent: params,
	}}

	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "p"}, xdm.One(params))

	// The element in that namespace matches; the one with the same local name
	// in no namespace does not, because QName equality is URI plus local.
	for _, tc := range []struct {
		name string
		node *xdm.Node
		want bool
	}{
		{"same namespace", cdataTree("a", ns, "e"), true},
		{"no namespace", cdataTree("a", "", ""), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seq, err := Eval(`serialize($n, $p)`,
				c.WithVar(xdm.QName{Local: "n"}, xdm.One(tc.node)), nil)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := seq[0].(*xdm.Atomic).String()
			if has := strings.Contains(got, "<![CDATA["); has != tc.want {
				t.Errorf("%s gave %q, CDATA present = %v, want %v",
					tc.name, got, has, tc.want)
			}
		})
	}
}

// TestSerializeParamElementCdataUndeclaredPrefix pins the refusal.
//
// Dropping the name silently is the defect this whole file is about, one level
// down: the caller asked for a CDATA section around an element the parameter
// could not name, and would have got escaped text with no indication why.
func TestSerializeParamElementCdataUndeclaredPrefix(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(cdataTree("a", "", "")))
	c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
		map[string]string{"cdata-section-elements": "zz:a"})))
	_, err := Eval(`serialize($n, $p)`, c, nil)
	if err == nil {
		t.Fatal("undeclared prefix: want SEPM0017, got no error")
	}
	if !strings.Contains(err.Error(), "SEPM0017") {
		t.Errorf("undeclared prefix: want SEPM0017, got %v", err)
	}
}

// TestSerializeParamElementJSONNodeOutputMethod pins the element form to
// json-node-output-method.
//
// Same split as cdata-section-elements: the map form selects the method a node
// nested in a JSON value is serialised with, and the element form accepted the
// parameter and wrote the "xml" default regardless.
func TestSerializeParamElementJSONNodeOutputMethod(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(cdataTree("a", "", "")))
	c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(map[string]string{
		"method": "json", "json-node-output-method": "text"})))

	seq, err := Eval(`serialize([$n], $p)`, c, nil)
	if err != nil {
		t.Fatalf("element form: %v", err)
	}
	got := seq[0].(*xdm.Atomic).String()
	// With method=text the node contributes its string value; with the "xml"
	// default it contributes serialised markup.
	if strings.Contains(got, "<a>") {
		t.Errorf("json-node-output-method=text gave %q, want the string "+
			"value rather than markup", got)
	}
	if !strings.Contains(got, "x<y") {
		t.Errorf("json-node-output-method=text gave %q, want the text", got)
	}
}
