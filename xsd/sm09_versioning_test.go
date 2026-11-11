package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSM09VersioningListResolvesFromOneMap: a vc:typeAvailable list resolves
// every word against one in-scope namespace map, gathered once, with the same
// answers the per-word lookup gave (shadowing and unknown names included).
func TestSM09VersioningListResolvesFromOneMap(t *testing.T) {
	const vc = `xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning"`
	d, err := xdm.ParseString(`<o xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p">`+
		`<a `+vc+` vc:typeAvailable="`+strings.Repeat("xs:integer ", 5000)+`xs:dateTimeStamp"/>`+
		`<b `+vc+` vc:typeAvailable="xs:integer p:x"/>`+
		`<c `+vc+` xmlns:xs="urn:not-schema" vc:typeUnavailable="xs:integer"/>`+
		`<d `+vc+` vc:typeUnavailable="xs:nosuch"/>`+
		`</o>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]bool{ // element: {included under 1.0, under 1.1}
		"a": {false, true},  // dateTimeStamp is 1.1-only
		"b": {false, false}, // p:x is unknown: typeAvailable undecided
		"c": {true, true},   // xs rebound: unknown, typeUnavailable keeps it
		"d": {true, true},   // no version has it: typeUnavailable holds
	}
	o := d.Root.ChildElements()[0]
	for _, el := range o.ChildElements() {
		w := want[el.Name().Local]
		for i, v := range []Version{Version10, Version11} {
			if got := includeElement(el, v); got != w[i] {
				t.Errorf("%s under %v: included=%v, want %v", el.Name().Local, v, got, w[i])
			}
		}
	}
	r := vcResolver{el: o.ChildElements()[0]}
	r.resolve("xs:a")
	r.resolve("p:b")
	if r.ns == nil {
		t.Fatal("in-scope namespaces not gathered")
	}
}
