package c14n

import (
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// canon.namespaceNodes made a map per element of a namespace node set; the
// maps of finished elements are reused now.
func TestNamespaceNodesReuseAxisMaps(t *testing.T) {
	const kids = 2000
	var b strings.Builder
	b.WriteString(`<r xmlns:a="urn:a">`)
	for range kids {
		b.WriteString(`<e/>`)
	}
	b.WriteString(`</r>`)
	tr, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := FromXPathFilter(tr.Root, "true()", nil)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(3, func() {
		if err := WriteNodeSet(io.Discard, set, Options{Algorithm: Inclusive10}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.0f allocations for %d elements", allocs, kids)
	if allocs > kids/2 {
		t.Errorf("%.0f allocations for %d elements, want a map reused across them", allocs, kids)
	}
}
