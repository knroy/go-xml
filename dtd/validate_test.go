package dtd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func check(t *testing.T, src string) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d, err := Parse(tree.DocType)
	if err != nil {
		t.Fatalf("parse DTD: %v", err)
	}
	return Validate(tree.Root, d, Options{})
}

// The content model is the half AllowDOCTYPE never applied. A document that
// violates its own DTD must now be rejected.
func TestContentModel(t *testing.T) {
	const dt = `<!DOCTYPE r [
<!ELEMENT r (a, b*, (c|d)?)>
<!ELEMENT a (#PCDATA)>
<!ELEMENT b (#PCDATA)>
<!ELEMENT c (#PCDATA)>
<!ELEMENT d (#PCDATA)>
]>`
	valid := []string{
		`<r><a/></r>`,
		`<r><a/><b/></r>`,
		`<r><a/><b/><b/><c/></r>`,
		`<r><a/><d/></r>`,
	}
	for _, body := range valid {
		if err := check(t, dt+body); err != nil {
			t.Errorf("%s should be valid: %v", body, err)
		}
	}
	invalid := []string{
		`<r><b/></r>`,        // a is required first
		`<r></r>`,            // a is required
		`<r><a/><c/><d/></r>`, // the choice occurs at most once
		`<r><a/><b/><a/></r>`, // a may not repeat
	}
	for _, body := range invalid {
		if err := check(t, dt+body); err == nil {
			t.Errorf("%s should be invalid", body)
		}
	}
}

func TestEmptyAndAny(t *testing.T) {
	const dt = `<!DOCTYPE r [
<!ELEMENT r (e, x)>
<!ELEMENT e EMPTY>
<!ELEMENT x ANY>
<!ELEMENT y (#PCDATA)>
]>`
	if err := check(t, dt+`<r><e/><x><y>anything</y></x></r>`); err != nil {
		t.Errorf("EMPTY and ANY should hold: %v", err)
	}
	if err := check(t, dt+`<r><e>text</e><x/></r>`); err == nil {
		t.Error("an EMPTY element with content should be invalid")
	}
}

// Mixed content constrains which elements may appear, but not their order or
// number — that is what makes it mixed.
func TestMixedContent(t *testing.T) {
	const dt = `<!DOCTYPE r [
<!ELEMENT r (#PCDATA|b|i)*>
<!ELEMENT b (#PCDATA)>
<!ELEMENT i (#PCDATA)>
<!ELEMENT q (#PCDATA)>
]>`
	if err := check(t, dt+`<r>text <b>bold</b> more <i>it</i><i>x</i></r>`); err != nil {
		t.Errorf("mixed content should hold: %v", err)
	}
	if err := check(t, dt+`<r>text <q>no</q></r>`); err == nil {
		t.Error("an element outside the mixed set should be invalid")
	}
}

// Element-only content forbids character data, not merely stray elements.
func TestElementOnlyRejectsText(t *testing.T) {
	const dt = `<!DOCTYPE r [<!ELEMENT r (a)><!ELEMENT a (#PCDATA)>]>`
	if err := check(t, dt+`<r>stray<a/></r>`); err == nil {
		t.Error("character data in element-only content should be invalid")
	}
	// Whitespace between elements is not character data for this purpose.
	if err := check(t, dt+"<r>\n  <a/>\n</r>"); err != nil {
		t.Errorf("whitespace should be permitted: %v", err)
	}
}

func TestAttributes(t *testing.T) {
	const dt = `<!DOCTYPE r [
<!ELEMENT r EMPTY>
<!ATTLIST r
  req CDATA #REQUIRED
  opt CDATA #IMPLIED
  fix CDATA #FIXED "yes"
  col (red|green) "red">
]>`
	if err := check(t, dt+`<r req="x"/>`); err != nil {
		t.Errorf("a defaulted document should be valid: %v", err)
	}
	if err := check(t, dt+`<r/>`); err == nil {
		t.Error("a missing #REQUIRED attribute should be invalid")
	}
	if err := check(t, dt+`<r req="x" fix="no"/>`); err == nil {
		t.Error("a #FIXED attribute with another value should be invalid")
	}
	if err := check(t, dt+`<r req="x" col="blue"/>`); err == nil {
		t.Error("a value outside the enumeration should be invalid")
	}
}

// ID must be unique and IDREF must resolve — including forward, which is why
// references are checked after the whole document is read.
func TestIDAndIDREF(t *testing.T) {
	const dt = `<!DOCTYPE r [
<!ELEMENT r (a+)>
<!ELEMENT a EMPTY>
<!ATTLIST a id ID #IMPLIED ref IDREF #IMPLIED refs IDREFS #IMPLIED>
]>`
	if err := check(t, dt+`<r><a id="x"/><a ref="x"/></r>`); err != nil {
		t.Errorf("a resolving IDREF should be valid: %v", err)
	}
	if err := check(t, dt+`<r><a ref="x"/><a id="x"/></r>`); err != nil {
		t.Errorf("a forward IDREF should be valid: %v", err)
	}
	if err := check(t, dt+`<r><a id="x"/><a id="x"/></r>`); err == nil {
		t.Error("a duplicate ID should be invalid")
	}
	if err := check(t, dt+`<r><a id="x"/><a ref="nope"/></r>`); err == nil {
		t.Error("an unresolved IDREF should be invalid")
	}
	if err := check(t, dt+`<r><a id="x"/><a refs="x nope"/></r>`); err == nil {
		t.Error("an unresolved value in IDREFS should be invalid")
	}
}

func TestUndeclaredElement(t *testing.T) {
	const dt = `<!DOCTYPE r [<!ELEMENT r (a)><!ELEMENT a EMPTY>]>`
	if err := check(t, dt+`<r><a/><z/></r>`); err == nil {
		t.Error("an undeclared element should be invalid")
	}
}

// An external subset is not fetched, so it is recorded rather than silently
// treated as absent.
func TestExternalSubsetIsRecorded(t *testing.T) {
	tree, err := xdm.ParseString(
		`<!DOCTYPE r SYSTEM "r.dtd" [<!ELEMENT r EMPTY>]><r/>`,
		xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d, err := Parse(tree.DocType)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasExternalSubset {
		t.Error("a SYSTEM identifier should be recorded")
	}
}

// A document with no DOCTYPE has no constraints to violate.
func TestNoDocType(t *testing.T) {
	tree, err := xdm.ParseString(`<r><anything/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(tree.DocType)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(tree.Root, d, Options{}); err != nil {
		t.Errorf("a document with no DTD should be valid: %v", err)
	}
}

// Errors are bounded, and the message names the path.
func TestErrorsAreBounded(t *testing.T) {
	const dt = `<!DOCTYPE r [<!ELEMENT r (a*)><!ELEMENT a EMPTY>]>`
	body := "<r>" + strings.Repeat("<z/>", 50) + "</r>"
	err := check(t, dt+body)
	if err == nil {
		t.Fatal("expected errors")
	}
	var errs *Errors
	if e, ok := err.(*Errors); ok {
		errs = e
	} else {
		t.Fatalf("error type = %T, want *Errors", err)
	}
	if len(errs.Errors) == 0 {
		t.Fatal("no errors recorded")
	}
	if !strings.HasPrefix(errs.Errors[0].Path, "/r") {
		t.Errorf("path = %q, want it to start at /r", errs.Errors[0].Path)
	}
}

// A partial internal subset is the common real-world shape: a DOCTYPE names an
// external DTD and declares a few things locally. Strictly every other element
// is undeclared, which is spec-correct and useless, so a caller can say the
// subset is partial.
func TestAllowUndeclared(t *testing.T) {
	const src = `<!DOCTYPE r [<!ELEMENT p (#PCDATA)>]><r><p>text</p><other/></r>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(tree.DocType)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(tree.Root, d, Options{}); err == nil {
		t.Error("strictly, r and other are undeclared and that is an error")
	}
	if err := Validate(tree.Root, d, Options{AllowUndeclared: true}); err != nil {
		t.Errorf("with AllowUndeclared the document should pass: %v", err)
	}
	// What is declared is still enforced.
	const bad = `<!DOCTYPE r [<!ELEMENT p (#PCDATA)>]><r><p>text<a/></p></r>`
	tree2, _ := xdm.ParseString(bad, xdm.ParseOptions{AllowDOCTYPE: true})
	d2, _ := Parse(tree2.DocType)
	if err := Validate(tree2.Root, d2, Options{AllowUndeclared: true}); err == nil {
		t.Error("a declared element's content model must still be applied")
	}
}
