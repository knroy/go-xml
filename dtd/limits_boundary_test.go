package dtd

import (
	"math"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Options.MaxErrors at its edges. See xdm/limits_boundary_test.go for why this
// class of test exists.
//
// This is the field xsd.ValidateOptions.MaxErrors should match and does not.
// Both document "zero means the default"; this one also documents "a negative
// value means no limit" and implements it, with a `v.max > 0 &&` guard in
// validate.go. The xsd equivalent has no such guard, and a negative value
// there makes an invalid document validate clean -- see the skipped
// TestValidateMaxErrorsNegativeApprovesInvalidDocuments in xsd. The negative
// case below is therefore pinning the correct behaviour of the pair.
func TestValidateMaxErrorsBoundaries(t *testing.T) {
	const doctype = `r [<!ELEMENT r (a)><!ELEMENT a EMPTY>]`
	const src = `<!DOCTYPE r [<!ELEMENT r (a)><!ELEMENT a EMPTY>]><r><x/><x/><x/></r>`

	d, err := Parse(doctype)
	if err != nil {
		t.Fatalf("parsing DTD: %v", err)
	}
	doc, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}

	// The document produces four validity errors. The assertion is on how many
	// are reported, because that is what MaxErrors bounds; a document this
	// wrong must never validate clean at any setting.
	const total = 4

	tests := []struct {
		name string
		max  int
		want int // exact number of errors expected
	}{
		// Deliberate: zero means DefaultMaxErrors (100), above the four this
		// document produces, so all four are reported.
		{"zero is the default", 0, total},
		// Deliberate: negative means no limit, documented and implemented.
		// This is the case xsd gets wrong.
		{"negative is unlimited", -1, total},
		{"one reports exactly one", 1, 1},
		{"one under the total reports that many", total - 1, total - 1},
		{"exactly at the total reports them all", total, total},
		{"one over the total reports them all", total + 1, total},
		{"MaxInt does not overflow", math.MaxInt, total},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, total},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(doc.Root, d, Options{MaxErrors: tt.max})
			if err == nil {
				t.Fatal("an invalid document validated clean")
			}
			if n := countErrors(err); n != tt.want {
				t.Errorf("reported %d errors, want %d (%v)", n, tt.want, err)
			}
		})
	}
}

// countErrors reports how many validity failures err carries. A single failure
// is returned bare rather than wrapped, so the one-error case is not an
// *Errors.
func countErrors(err error) int {
	if es, ok := err.(*Errors); ok {
		return len(es.Errors)
	}
	return 1
}
