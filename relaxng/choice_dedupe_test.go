package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A oneOrMore nested in a oneOrMore used to double its derivative with every
// child, because choice kept both copies of the identical continuation it was
// handed; forty children overran the 100,000-node pattern bound and a valid
// document was refused. With the copies merged the pattern stays small, so
// the document validates, and the allocation bound catches a return to the
// doubling. Measured: about 0.8 MB for five hundred children, linear in n.
func TestNestedOneOrMoreDoesNotDouble(t *testing.T) {
	s, err := compileString(t, `<element name="r"`+rngNS+`>
		<oneOrMore><oneOrMore><oneOrMore>
			<element name="a"><empty/></element>
		</oneOrMore></oneOrMore></oneOrMore></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, n := range []int{40, 500} {
		doc, err := xdm.ParseString("<r>"+strings.Repeat("<a/>", n)+"</r>", xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var verr error
		b := allocated(func() { verr = s.Validate(doc.Root) })
		if verr != nil {
			t.Errorf("%d children: %v", n, verr)
		}
		if b > 4<<20 {
			t.Errorf("%d children allocated %d bytes; the derivative is growing", n, b)
		}
	}
}

// choice returns its left operand when the right one is already among its
// alternatives, at any position in the chain.
func TestChoiceMergesEqualAlternatives(t *testing.T) {
	a := &elementPat{Name: qnamePat{xdm.QName{Local: "a"}}, Pattern: emptyPat{}}
	b := &elementPat{Name: qnamePat{xdm.QName{Local: "b"}}, Pattern: textPat{}}
	ab := choice(a, b)
	if got := choice(ab, a); !patEq(got, ab) {
		t.Errorf("choice(a|b, a) = %#v, want a|b", got)
	}
	if got := choice(ab, b); !patEq(got, ab) {
		t.Errorf("choice(a|b, b) = %#v, want a|b", got)
	}
	if got := choice(ab, ab); !patEq(got, ab) {
		t.Errorf("choice(a|b, a|b) = %#v, want a|b", got)
	}
	// Values are never merged: equal-looking ones may differ in their
	// bindings, and the fields are not comparable anyway.
	v := &valuePat{Value: "x", Prefixes: map[string]string{}}
	if _, ok := choice(v, v).(*choicePat); !ok {
		t.Error("choice(value, value) merged two values")
	}
}
