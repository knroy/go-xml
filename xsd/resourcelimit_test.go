package xsd

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// recursiveSchema is a schema whose only element contains itself, so an
// instance can be nested to any depth without the schema growing.
const recursiveSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="a"><xs:complexType><xs:sequence>
    <xs:element ref="a" minOccurs="0"/>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`

func mustLoadSchema(t *testing.T, src string) *Schema {
	t.Helper()
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return s
}

func nestedDoc(t *testing.T, n int) *xdm.Node {
	t.Helper()
	dt, err := xdm.ParseString(
		strings.Repeat("<a>", n)+strings.Repeat("</a>", n),
		xdm.ParseOptions{MaxDepth: n + 10})
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	return dt.Root
}

// A MaxDepth refusal was reported as cvc-elt.1, which means "the element is
// not valid against its declaration" -- and nothing here was assessed against
// any declaration. The document may be perfectly valid; this processor
// declined to find out. A cvc- code is a validity verdict, so the refusal
// carries none: it has an empty Code, like the package's other processor-side
// failures, and xdm.ErrResourceLimit says what it is.
func TestValidateDepthRefusalIsNotAValidityVerdict(t *testing.T) {
	s := mustLoadSchema(t, recursiveSchema)
	root := nestedDoc(t, 40)

	err := s.Validate(root, ValidateOptions{MaxDepth: 10})
	if err == nil {
		t.Fatal("expected a refusal at MaxDepth 10")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell this refusal from an invalid document", err)
	}
	var ve *ValidationErrors
	if !errors.As(err, &ve) || len(ve.Errors) == 0 {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	if c := ve.Errors[0].Code; c != "" {
		t.Errorf("code = %q, want none; a cvc- code asserts the document "+
			"is invalid, and it was never assessed", c)
	}
	if !strings.Contains(err.Error(), "element nesting exceeds 10 levels") {
		t.Errorf("message %q lost its text", err)
	}

	// The same document under no depth cap is valid, which is the whole
	// point: the refusal said nothing about the document.
	if err := s.Validate(root, ValidateOptions{}); err != nil {
		t.Fatalf("the document is valid without the cap, got %v", err)
	}
}

// A genuinely invalid document must NOT report as a resource limit.
func TestInvalidDocumentIsNotAResourceLimit(t *testing.T) {
	s := mustLoadSchema(t, recursiveSchema)
	dt, err := xdm.ParseString(`<a><b/></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Validate(dt.Root, ValidateOptions{})
	if got == nil {
		t.Fatal("expected a validation failure")
	}
	if errors.Is(got, xdm.ErrResourceLimit) {
		t.Errorf("an invalid document %v reports as a resource limit; a "+
			"caller would retry a request that can never succeed", got)
	}
}
