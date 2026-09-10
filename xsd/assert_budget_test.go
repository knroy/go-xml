package xsd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// assertionSheet builds a schema whose element carries n assertions, each
// materialising a range of size items.
//
// Each assertion is individually well under xpath.MaxItems; only their sum
// exceeds it. That is the shape the per-assertion context could not see: a
// fresh 5,000,000-item allowance per assertion means no n is ever too many.
func assertionSchema(n, size int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" ` +
		`vc:minVersion="1.1" ` +
		`xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning">` +
		`<xs:element name="r"><xs:complexType>` +
		`<xs:attribute name="a" type="xs:string"/>`)
	for i := 0; i < n; i++ {
		// A "for" over the range is what actually materialises it:
		// count() alone answers from the range's known length without
		// building anything, so it charges nothing.
		fmt.Fprintf(&b,
			`<xs:assert test="count(for $i in 1 to %d return $i) eq %d"/>`,
			size, size)
	}
	b.WriteString(`</xs:complexType></xs:element></xs:schema>`)
	return b.String()
}

func loadAssertionSchema(t *testing.T, src string) *Schema {
	t.Helper()
	sd, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	s, err := Load(sd.Root, "", Options{Version: Version11})
	if err != nil {
		t.Fatalf("loading schema: %v", err)
	}
	return s
}

func validateAssertionDoc(t *testing.T, s *Schema) error {
	t.Helper()
	doc, err := xdm.ParseString(`<r a="x"/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing document: %v", err)
	}
	return s.Validate(doc.Root, ValidateOptions{})
}

// The defect: xsd/assert.go called xpath.NewContext per assertion, so every
// one of them got a brand new 5,000,000-item allowance and a schema with many
// assertions over a large document had no aggregate bound. Only a budget owned
// by the validation episode sees the sum.
func TestAssertionsShareOneItemBudget(t *testing.T) {
	// 1,200,000 items apiece: one assertion is comfortably legal, eight of
	// them together are 9,600,000 and must be refused.
	s := loadAssertionSchema(t, assertionSchema(8, 1_200_000))
	err := validateAssertionDoc(t, s)
	if err == nil {
		t.Fatal("eight assertions of 1,200,000 items each were accepted; " +
			"the item budget did not bind across the validation, so each " +
			"assertion got a fresh allowance")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from an invalid document", err)
	}
	if !strings.Contains(err.Error(), "materialised more than") {
		t.Errorf("message %q is not the item-budget refusal", err)
	}
}

// One assertion of the same size must still pass, so the test above is
// measuring the aggregate and not simply a limit any single assertion trips.
func TestSingleLargeAssertionStillValidates(t *testing.T) {
	s := loadAssertionSchema(t, assertionSchema(1, 1_200_000))
	if err := validateAssertionDoc(t, s); err != nil {
		t.Fatalf("a single 1,200,000-item assertion was refused: %v", err)
	}
}

// The episode budget must not break assertion support: a schema with many
// small assertions is legitimate and has to keep validating.
func TestManySmallAssertionsStillValidate(t *testing.T) {
	s := loadAssertionSchema(t, assertionSchema(500, 10))
	if err := validateAssertionDoc(t, s); err != nil {
		t.Fatalf("500 ten-item assertions were refused: %v", err)
	}
}
