package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A validator built by the type-validation entry points carries no context,
// and checkCancelled must not read Err() off a nil interface.
//
// ValidateContext added a ctx field to validator and a cancellation check on
// the walk. ValidateElement and its siblings construct the same struct without
// one: they are XSLT's validation="strict" path, bounded by what the transform
// just built. The nil dereference panicked every schema-aware stylesheet --
// 53 cases on the XSLT 2.0 target and 77 on 3.0 -- while the XSD suites the
// change was measured against stayed at 39,347 and 41,532, because they never
// take this path.
func TestValidateElementWithoutContext(t *testing.T) {
	const sch = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="aType"><xs:sequence><xs:element name="b" type="xs:integer" maxOccurs="unbounded"/></xs:sequence></xs:complexType>
	  <xs:element name="a" type="aType">
	    </xs:element>
	</xs:schema>`
	stree, err := xdm.ParseString(sch, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the schema: %v", err)
	}
	s, err := Load(stree.Root, "", Options{})
	if err != nil {
		t.Fatalf("loading the schema: %v", err)
	}
	tree, err := xdm.ParseString(`<a><b>1</b><b>2</b></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	el := tree.Root.ChildElements()[0]

	// The assertion is that this returns rather than panics. A complex type is
	// what matters: validateElement walks the content model and is where the
	// cancellation check sits, so a simple-typed element never reaches it and
	// would not catch this.
	// ValidateAgainstType is the entry XSLT uses for validation="strict" on a
	// constructed element, and it is the one that builds a validator with no
	// context. ValidateElement does not reach the check.
	if err := s.ValidateAgainstType(el, xdm.QName{Local: "aType"},
		ValidateOptions{}); err != nil {
		_ = err // the verdict is incidental; not panicking is the assertion
	}
}
