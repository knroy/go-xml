package dtd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The path is rendered only for a recorded failure. Concatenated at every
// element it cost the square of the depth: a valid 990-deep chain of 4 KB
// names allocated 2 GB. The text of a reported path is unchanged, including
// for an IDREF, which is resolved after the walk has left the element.
func TestPathBuiltOnlyForFailures(t *testing.T) {
	d, err := Parse(`<!DOCTYPE r [
	<!ELEMENT r (a)*> <!ELEMENT a ANY> <!ELEMENT b EMPTY> <!ELEMENT c EMPTY>
	<!ATTLIST b x CDATA #FIXED "0"> <!ATTLIST c ref IDREF #IMPLIED>]>`)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(`<r><a><b x="1"/></a><a><c ref="nope"/></a></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "2 validity errors:\n" +
		"  /r/a/b: attribute x is #FIXED \"0\" but is \"1\"\n" +
		"  /r/a/c: IDREF \"nope\" matches no ID in the document"
	if err := Validate(tree.Root, d, Options{}); err == nil || err.Error() != want {
		t.Fatalf("got\n%v\nwant\n%s", err, want)
	}

	nm := "n" + strings.Repeat("x", 4095)
	d, err = Parse(`<!DOCTYPE ` + nm + ` [<!ELEMENT ` + nm + ` ANY>]>`)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = xdm.ParseString(strings.Repeat("<"+nm+">", 990)+strings.Repeat("</"+nm+">", 990), xdm.ParseOptions{})
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
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 16<<20 {
		t.Errorf("validating a valid deep chain allocated %d MB, want under 16 MB", got>>20)
	}
}
