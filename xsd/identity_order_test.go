package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A duplicate key carried up from nested scopes is reported on its later
// occurrences in document order, the same on every run. Twenty sibling
// scopes each define the same key; the enclosing g sees all twenty through
// the table adopted from below, keeps the first and fails the other
// nineteen. Walked in map order, the kept one and the order both vary.
func TestDuplicateKeyFromBelowReportedInDocumentOrder(t *testing.T) {
	st, _ := xdm.ParseString(krSchema, xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	doc := `<root><g>` + strings.Repeat("\n<g><def a=\"x\" b=\"y\"/></g>", 20) + `</g></root>`
	tr, err := xdm.ParseString(doc, xdm.ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	// The defs sit on lines 2..21; the first is kept, the rest fail.
	for line := 3; line <= 21; line++ {
		fmt.Fprintf(&want, "|%d", line)
	}
	for run := 0; run < 20; run++ {
		err := s.Validate(tr.Root, ValidateOptions{MaxErrors: -1})
		if err == nil {
			t.Fatal("duplicate keys accepted")
		}
		var got strings.Builder
		for _, e := range err.(*ValidationErrors).Errors {
			if e.Code != "cvc-identity-constraint.4.2.2" {
				t.Fatalf("unexpected failure %v", e)
			}
			fmt.Fprintf(&got, "|%d", e.Line)
		}
		if got.String() != want.String() {
			t.Fatalf("run %d: duplicates reported on lines %s, want %s", run, got.String(), want.String())
		}
	}
}
