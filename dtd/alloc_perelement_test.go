package dtd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// With no ATTLIST for an element, checkAttributes built a map anyway, and the
// child-name slice was rebuilt for every element.
func TestValidateAllocsPerElement(t *testing.T) {
	d, err := Parse(`<!DOCTYPE r [<!ELEMENT r (p)*> <!ELEMENT p (a,b)>
	<!ELEMENT a EMPTY> <!ELEMENT b EMPTY>]>`)
	if err != nil {
		t.Fatal(err)
	}
	const n = 2000
	tree, err := xdm.ParseString("<r>"+strings.Repeat("<p><a/><b/></p>", n)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	err = Validate(tree.Root, d, Options{})
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	// 6001 elements: 8062 allocations with the fix, 12062 without.
	if got := m1.Mallocs - m0.Mallocs; got > 10000 {
		t.Errorf("%d allocations for %d elements, want at most 10000", got, 3*n+1)
	}
}
