package relaxng

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestBuffersDoNotLeakBetweenLevels: the validator reuses one child stack and
// one attribute buffer for every element, so a nested element's children or
// attributes must not show up as its parent's or its next sibling's.
func TestBuffersDoNotLeakBetweenLevels(t *testing.T) {
	s := compileBoundarySchema(t, `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
		<attribute name="ra"><text/></attribute>
		<text/>
		<element name="a"><attribute name="aa"><text/></attribute>
			<element name="x"><attribute name="xa"><text/></attribute><text/></element>
			<element name="y"><empty/></element></element>
		<element name="b"><empty/></element>
		<text/>
	</element>`)
	for _, c := range []struct {
		doc   string
		valid bool
	}{
		{`<r ra="1">t1<a aa="2"><x xa="3">u</x><y/></a><b/>t2</r>`, true},
		{`<r ra="1">t1<a aa="2"><x xa="3">u</x><y/></a><b/>t2<!--c-->t3</r>`, true},
		// y's place taken by a second x: the outer level must not see it.
		{`<r ra="1">t1<a aa="2"><x xa="3">u</x><x xa="3"/></a><b/>t2</r>`, false},
		// b missing: a's children must not stand in for r's.
		{`<r ra="1">t1<a aa="2"><x xa="3">u</x><y/></a>t2</r>`, false},
		// xa on b: an attribute of a deeper element must not carry over.
		{`<r ra="1">t1<a aa="2"><x xa="3">u</x><y/></a><b xa="3"/>t2</r>`, false},
		// ra missing on r with aa present below.
		{`<r>t1<a aa="2"><x xa="3">u</x><y/></a><b/>t2</r>`, false},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%s: got %v, want valid=%v", c.doc, err, c.valid)
		}
	}
}
