package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestDropAttrs covers the helper that strips the param-only attributes off a
// declaration xsl:override has renamed from xsl:param to xsl:variable.
//
// XSLT 3.0 gives required and tunnel to xsl:param alone: 9.2's signature has
// them and 9.1's for xsl:variable does not. A renamed declaration that kept
// either carried an attribute the static grammar has to reject with XTSE0090,
// which is what override-v-010 and override-v-015 hit.
//
// A prefixed attribute of the same local name is a different attribute and
// must survive, which is why the helper tests the namespace too.
func TestDropAttrs(t *testing.T) {
	el := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{URI: xdm.NSXSL, Local: "variable"}}
	add := func(uri, local, val string) {
		el.Attrs = append(el.Attrs, &xdm.Node{
			Kind:  xdm.KindAttribute,
			Name:  xdm.QName{URI: uri, Local: local},
			Value: val,
		})
	}
	add("", "name", "v")
	add("", "required", "no")
	add("", "tunnel", "yes")
	add("", "select", "1")
	add("http://example.invalid/keep", "required", "kept")

	dropAttrs(el, "required", "tunnel")

	got := map[string]string{}
	for _, a := range el.Attrs {
		got[a.Name.URI+"|"+a.Name.Local] = a.Value
	}
	want := map[string]string{
		"|name":                                "v",
		"|select":                              "1",
		"http://example.invalid/keep|required": "kept",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}
