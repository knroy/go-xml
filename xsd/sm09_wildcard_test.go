package xsd

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSM09WildcardListsIndexed: long namespace and disallowed-name lists are
// answered from a hash set, so a match (and each wildcard set operation that
// tests one list against another) no longer scans the list.
func TestSM09WildcardListsIndexed(t *testing.T) {
	const n = 4096
	ns := make([]string, n)
	names := make([]xdm.QName, n)
	for i := range ns {
		ns[i] = fmt.Sprintf("urn:n%d", i)
		names[i] = xdm.QName{URI: "urn:d", Local: fmt.Sprintf("e%d", i)}
	}
	w := &Wildcard{Kind: NSEnumerated, Namespace: ns, DisallowedNames: names}
	if !w.Allows("urn:n4095") || w.Allows("urn:other") {
		t.Fatal("enumerated membership wrong")
	}
	if !w.Disallows(names[n-1], nil) || w.Disallows(xdm.QName{URI: "urn:d", Local: "x"}, nil) {
		t.Fatal("disallowed membership wrong")
	}
	ix, _ := w.index.Load().(*wildcardIndex)
	if ix == nil || len(ix.ns) != n || len(ix.names) != n {
		t.Fatalf("lists not indexed: %+v", ix)
	}

	// A list replaced after the index was built is not answered stale.
	w.Namespace = append([]string{"urn:new"}, ns[1:]...)
	if !w.Allows("urn:new") || w.Allows("urn:n0") {
		t.Fatal("index answered from a replaced list")
	}
	not := &Wildcard{Kind: NSNot, Namespace: ns}
	if not.Allows("urn:n7") || !not.Allows("urn:other") || !not.Allows("") {
		t.Fatal("negated membership wrong")
	}
}
