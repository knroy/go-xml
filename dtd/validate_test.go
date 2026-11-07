package dtd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
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

// checkDirective is check for a DOCTYPE xdm refuses as malformed (X3): the
// directive goes to Parse directly, as a caller holding one would pass it,
// and the document is parsed without it.
func checkDirective(t *testing.T, src string) error {
	t.Helper()
	i := strings.Index(src, "]>")
	tree, err := xdm.ParseString(src[i+2:], xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d, err := Parse(src[2 : i+1])
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
		`<r><b/></r>`,         // a is required first
		`<r></r>`,             // a is required
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

// XML 1.0 §3.3.1 VC Attribute Value Type: every attribute must be declared,
// and names are matched as written, prefix included, as a DTD declares them.
func TestUndeclaredAndPrefixedAttributes(t *testing.T) {
	const dt = `<!DOCTYPE order [<!ELEMENT order EMPTY>` +
		`<!ATTLIST order id CDATA #REQUIRED xml:lang (en) #IMPLIED>` +
		`<!ELEMENT r EMPTY>]>`
	for _, c := range []struct{ doc, want string }{
		{`<order id="1"/>`, ""},
		{`<order id="1" xml:lang="en"/>`, ""},
		{`<order xmlns="urn:other" id="1"/>`, "attribute xmlns is not declared"},
		{`<order/>`, "required attribute id is missing"},
		{`<order id="1" admin="true"/>`, "attribute admin is not declared"},
		{`<order id="1" xml:lang="zz"/>`, `attribute xml:lang = "zz" is not one of en`},
		{`<o:order xmlns:o="urn:other" id="1"/>`, "element o:order is not declared"},
		{`<r evil="1"/>`, "attribute evil is not declared"},
	} {
		err := check(t, dt+c.doc)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.doc, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.doc, err, c.want)
		}
	}
	tree, err := xdm.ParseString(dt+`<order id="1" admin="true"/>`, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(tree.DocType)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(tree.Root, d, Options{AllowUndeclared: true}); err != nil {
		t.Errorf("AllowUndeclared should skip an undeclared attribute: %v", err)
	}
}

// XML 1.0 §3.3.1 VC Attribute Value Type covers namespace declarations too:
// to XML 1.0 xmlns and xmlns:p are attributes, and Namespaces in XML makes no
// exception for validity, so a valid document declares them. Each want is
// what xmllint --valid (libxml2 2.9.13) reports for the same document.
func TestNamespaceDeclarationsAreAttributes(t *testing.T) {
	const dt = `<!DOCTYPE r [<!ELEMENT r (c)><!ELEMENT c EMPTY>` +
		`<!ATTLIST r xmlns CDATA #FIXED "urn:r" xmlns:p CDATA #REQUIRED>]>`
	for _, c := range []struct{ doc, want string }{
		{`<r xmlns="urn:r" xmlns:p="urn:p"><c/></r>`, ""},
		{`<r xmlns:p="urn:p"><c/></r>`, ""}, // #FIXED xmlns may be omitted
		{`<r xmlns="urn:x" xmlns:p="urn:p"><c/></r>`, `attribute xmlns is #FIXED "urn:r" but is "urn:x"`},
		{`<r xmlns="urn:r"><c/></r>`, "required attribute xmlns:p is missing"},
		// c redeclares p with the same URI; it is still an attribute of c.
		{`<r xmlns="urn:r" xmlns:p="urn:p"><c xmlns:p="urn:p"/></r>`, "attribute xmlns:p is not declared"},
	} {
		err := check(t, dt+c.doc)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.doc, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.doc, err, c.want)
		}
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

// A default declaration outside the closed set of XML 1.0 §3.3.2 was read as a
// bare default value, so "#REQUIRE" for "#REQUIRED" turned a required
// attribute into one defaulting to the string "#REQUIRE" and the missing
// attribute went unreported. A component may decline to answer; it must not
// turn "I could not check this" into "this is valid".
func TestUnrecognisedAttributeDefault(t *testing.T) {
	// xdm now refuses this DOCTYPE as malformed; Parse is reached directly.
	const bad = `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #REQUIRE>]><r/>`
	err := checkDirective(t, bad)
	if err == nil {
		t.Fatal("a #-prefixed token that is not a keyword must be reported")
	}
	if want := "unrecognised default declaration #REQUIRE"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	// The report does not depend on the attribute being absent: the
	// declaration itself is what could not be applied.
	if err := checkDirective(t, `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #REQUIRE>]><r a="x"/>`); err == nil {
		t.Error("the declaration is unenforceable whether or not the attribute is present")
	}

	// Every legal default declaration must keep working untouched.
	valid := []string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #REQUIRED>]><r a="x"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #IMPLIED>]><r/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #FIXED "v">]><r a="v"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA "dflt">]><r/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA "dflt">]><r a="other"/>`,
	}
	for _, src := range valid {
		if err := check(t, src); err != nil {
			t.Errorf("%s should be valid: %v", src, err)
		}
	}
	// And the constraints they express must still bite.
	invalid := map[string]string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #REQUIRED>]><r/>`:        "required attribute a is missing",
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #FIXED "v">]><r a="w"/>`: `attribute a is #FIXED "v" but is "w"`,
	}
	for src, want := range invalid {
		err := check(t, src)
		if err == nil {
			t.Errorf("%s should be invalid", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// The switch over the attribute type had no default, so a misspelt type was
// never enforced and never mentioned: "IDREFF" for "IDREF" let a dangling
// reference validate clean. An unrecognised type is a type this package cannot
// enforce, and the caller has to be told which of the two it got.
func TestUnrecognisedAttributeType(t *testing.T) {
	// xdm now refuses this DOCTYPE as malformed; Parse is reached directly.
	const bad = `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r ref IDREFF #IMPLIED>]><r ref="nowhere"/>`
	err := checkDirective(t, bad)
	if err == nil {
		t.Fatal("a type outside XML 1.0 3.3.1 must be reported, not skipped")
	}
	if want := "unrecognised type IDREFF"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if err := checkDirective(t, `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r n NMTOKENN #IMPLIED>]><r n="a b"/>`); err == nil {
		t.Error("a misspelt NMTOKEN must be reported")
	}

	// Every type the spec closes the set to must stay silent, so that the new
	// branch reports only what is genuinely unrecognised.
	valid := []string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #IMPLIED>]><r a="anything at all"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a ID #IMPLIED>]><r a="i1"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a NMTOKEN #IMPLIED>]><r a="tok"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a NMTOKENS #IMPLIED>]><r a="a b"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ENTITY e SYSTEM "e.gif" NDATA gif><!NOTATION gif SYSTEM "g"><!ATTLIST r a ENTITY #IMPLIED>]><r a="e"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ENTITY e SYSTEM "e.gif" NDATA gif><!ENTITY f SYSTEM "f.gif" NDATA gif><!NOTATION gif SYSTEM "g"><!ATTLIST r a ENTITIES #IMPLIED>]><r a="e f"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION n1 SYSTEM "1"><!NOTATION n2 SYSTEM "2"><!ATTLIST r a NOTATION (n1|n2) #IMPLIED>]><r a="n1"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a (x|y) #IMPLIED>]><r a="x"/>`,
		`<!DOCTYPE r [<!ELEMENT r (c)><!ELEMENT c EMPTY><!ATTLIST r ref IDREF #IMPLIED><!ATTLIST c id ID #IMPLIED>]><r ref="i1"><c id="i1"/></r>`,
		`<!DOCTYPE r [<!ELEMENT r (c)><!ELEMENT c EMPTY><!ATTLIST r ref IDREFS #IMPLIED><!ATTLIST c id ID #IMPLIED>]><r ref="i1 i1"><c id="i1"/></r>`,
	}
	for _, src := range valid {
		if err := check(t, src); err != nil {
			t.Errorf("%s should be valid: %v", src, err)
		}
	}
	// The types that are enforced must keep biting.
	invalid := map[string]string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r ref IDREF #IMPLIED>]><r ref="nowhere"/>`:                                                     `IDREF "nowhere" matches no ID`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r ref IDREFS #IMPLIED>]><r ref="a b"/>`:                                                        `IDREF "a" matches no ID`,
		`<!DOCTYPE r [<!ELEMENT r (c,c)><!ELEMENT c EMPTY><!ATTLIST c id ID #IMPLIED>]><r><c id="d"/><c id="d"/></r>`:                            `duplicate ID "d"`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a (x|y) #IMPLIED>]><r a="z"/>`:                                                               `attribute a = "z" is not one of x, y`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION n1 SYSTEM "1"><!NOTATION n2 SYSTEM "2"><!ATTLIST r a NOTATION (n1|n2) #IMPLIED>]><r a="n3"/>`: `attribute a = "n3" is not one of n1, n2`,
	}
	for src, want := range invalid {
		err := check(t, src)
		if err == nil {
			t.Errorf("%s should be invalid", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// XML 1.0 §3.3.1, Notation Attributes: every name in the enumeration of a
// NOTATION attribute must be declared by a <!NOTATION>. The gap this closes
// was that the enumeration was applied to the VALUE and never to itself, so a
// DTD restricting an attribute to notations that do not exist validated.
func TestNotationMustBeDeclared(t *testing.T) {
	// The negative arm first, and it is the one that matters: a check that
	// rejected every NOTATION attribute would pass the positive arm alone.
	valid := []string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION gif SYSTEM "g"><!NOTATION jpeg SYSTEM "j"><!ATTLIST r a NOTATION (gif|jpeg) #IMPLIED>]><r a="gif"/>`,
		// Absent is still valid: #IMPLIED means the attribute need not occur,
		// but the declaration is checked whether or not it does.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION gif SYSTEM "g"><!ATTLIST r a NOTATION (gif) #IMPLIED>]><r/>`,
		// PUBLIC and the two-identifier form declare a notation just as
		// SYSTEM does, and the identifiers are no part of the check.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION gif PUBLIC "-//x//NOTATION gif//EN"><!ATTLIST r a NOTATION (gif) #IMPLIED>]><r a="gif"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION gif PUBLIC "-//x//g//EN" "g.dtd"><!ATTLIST r a NOTATION (gif) #IMPLIED>]><r a="gif"/>`,
		// A plain enumeration is not a NOTATION type and its members are
		// values, not notation names — nothing declares them anywhere.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a (gif|jpeg) #IMPLIED>]><r a="gif"/>`,
		// The declaration order is not the reference order: a <!NOTATION>
		// after the <!ATTLIST> that names it still declares it.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a NOTATION (gif) #IMPLIED><!NOTATION gif SYSTEM "g">]><r a="gif"/>`,
	}
	for _, src := range valid {
		if err := check(t, src); err != nil {
			t.Errorf("%s should be valid: %v", src, err)
		}
	}

	invalid := map[string]string{
		// Neither name declared.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a NOTATION (gif|jpeg) #IMPLIED>]><r a="gif"/>`: `permits the notation gif, which no <!NOTATION> declares`,
		// One of two declared: the other must still be reported, or a DTD
		// need only declare one notation to pass.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!NOTATION gif SYSTEM "g"><!ATTLIST r a NOTATION (gif|jpeg) #IMPLIED>]><r a="gif"/>`: `permits the notation jpeg, which no <!NOTATION> declares`,
		// The fault is in the DTD, so it is reported even when the document
		// never carries the attribute.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a NOTATION (gif) #REQUIRED>]><r a="gif"/>`: `permits the notation gif, which no <!NOTATION> declares`,
	}
	for src, want := range invalid {
		err := check(t, src)
		if err == nil {
			t.Errorf("%s should be invalid", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// A DTD fault is a property of the declaration, so it is reported once however
// many elements carry the attribute. Without this the finding buries itself.
func TestUndeclaredNotationReportedOnce(t *testing.T) {
	err := check(t, `<!DOCTYPE r [<!ELEMENT r (c,c,c)><!ELEMENT c EMPTY>`+
		`<!ATTLIST c a NOTATION (gif) #IMPLIED>]>`+
		`<r><c a="gif"/><c a="gif"/><c a="gif"/></r>`)
	if err == nil {
		t.Fatal("an undeclared notation must be reported")
	}
	if n := strings.Count(err.Error(), "which no <!NOTATION> declares"); n != 1 {
		t.Errorf("reported %d times, want 1: %v", n, err)
	}
}

// XML 1.0 §3.3.1, Entity Name: the value of an ENTITY attribute must match the
// name of an unparsed entity — one declared external WITH an NDATA notation.
// ENTITIES is the same rule over a whitespace-separated list.
func TestEntityAttributesMustNameUnparsedEntities(t *testing.T) {
	const decls = `<!NOTATION gif SYSTEM "g">` +
		`<!ENTITY logo SYSTEM "logo.gif" NDATA gif>` +
		`<!ENTITY icon PUBLIC "-//x//icon//EN" "icon.gif" NDATA gif>`

	valid := []string{
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITY #IMPLIED>]><r a="logo"/>`,
		// The PUBLIC form declares an unparsed entity just as SYSTEM does.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITY #IMPLIED>]><r a="icon"/>`,
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITIES #IMPLIED>]><r a="logo icon"/>`,
		// Irregular whitespace between the names is still whitespace.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITIES #IMPLIED>]><r a="  logo` + "\n\t" + `icon  "/>`,
		// A repeated name is a name that resolves twice, not an error.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITIES #IMPLIED>]><r a="logo logo"/>`,
		// Absent is valid; the value rule has nothing to apply to.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITY #IMPLIED>]><r/>`,
		// A declaration order that puts the entity after the attribute list.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a ENTITY #IMPLIED>` + decls + `]><r a="logo"/>`,
	}
	for _, src := range valid {
		if err := check(t, src); err != nil {
			t.Errorf("%s should be valid: %v", src, err)
		}
	}

	invalid := map[string]string{
		// Never declared at all.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITY #IMPLIED>]><r a="nope"/>`: `attribute a = "nope" names no unparsed entity`,
		// Declared, but INTERNAL — it has replacement text, so it is parsed
		// and naming it here is as wrong as naming nothing. This is the arm
		// that separates "is it declared" from "is it unparsed".
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ENTITY p "text"><!ATTLIST r a ENTITY #IMPLIED>]><r a="p"/>`: `attribute a = "p" names no unparsed entity`,
		// Declared external but with no NDATA: a parsed external entity.
		`<!DOCTYPE r [<!ELEMENT r EMPTY><!ENTITY x SYSTEM "x.xml"><!ATTLIST r a ENTITY #IMPLIED>]><r a="x"/>`: `attribute a = "x" names no unparsed entity`,
		// One good name and one bad in an ENTITIES list: the bad one must
		// still be found, or a list need only start well to pass.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITIES #IMPLIED>]><r a="logo nope"/>`: `attribute a = "nope" names no unparsed entity`,
		// An empty ENTITIES value names nothing, and Names requires one.
		`<!DOCTYPE r [<!ELEMENT r EMPTY>` + decls + `<!ATTLIST r a ENTITIES #IMPLIED>]><r a="   "/>`: `is ENTITIES but is empty`,
	}
	for src, want := range invalid {
		err := check(t, src)
		if err == nil {
			t.Errorf("%s should be invalid", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// A parameter entity is not a general entity and can never be unparsed, so its
// name must not enter the table — "%pe;" would otherwise let "pe" pass as the
// value of an ENTITY attribute.
func TestParameterEntityIsNotUnparsed(t *testing.T) {
	err := check(t, `<!DOCTYPE r [<!ELEMENT r EMPTY>`+
		`<!ENTITY % pe "NDATA gif"><!ATTLIST r a ENTITY #IMPLIED>]><r a="pe"/>`)
	if err == nil {
		t.Fatal("a parameter entity must not satisfy an ENTITY attribute")
	}
	if want := `attribute a = "pe" names no unparsed entity`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// A DTD-supplied default is a value like any other: xdm materialises it onto
// the element, so §3.3.1 applies to it. A check that read only attributes
// written in the instance would miss it.
func TestDefaultedEntityValueIsChecked(t *testing.T) {
	if err := check(t, `<!DOCTYPE r [<!ELEMENT r EMPTY>`+
		`<!NOTATION gif SYSTEM "g"><!ENTITY logo SYSTEM "l.gif" NDATA gif>`+
		`<!ATTLIST r a ENTITY "logo">]><r/>`); err != nil {
		t.Errorf("a default naming a declared unparsed entity is valid: %v", err)
	}
	err := check(t, `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a ENTITY "nope">]><r/>`)
	if err == nil {
		t.Fatal("a default naming no unparsed entity must be reported")
	}
	if want := `attribute a = "nope" names no unparsed entity`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// DTD's name sets are exported maps, so a caller may assemble a DTD by hand
// and leave them nil. Nil must mean "not recorded" rather than "empty", or
// hand-built rulesets would report every notation and entity as undeclared.
func TestHandBuiltDTDWithoutNameSets(t *testing.T) {
	tree, err := xdm.ParseString(
		`<!DOCTYPE r [<!ELEMENT r EMPTY>]><r a="gif" e="logo"/>`,
		xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	d := &DTD{
		Elements: map[string]*Element{"r": {Name: "r", Kind: ContentEmpty}},
		Attributes: map[string][]*Attribute{"r": {
			{Element: "r", Name: "a", Type: "NOTATION", Enum: []string{"gif"}},
			{Element: "r", Name: "e", Type: "ENTITY"},
		}},
		// Notations and Unparsed deliberately nil.
	}
	if err := Validate(tree.Root, d, Options{}); err != nil {
		t.Errorf("nil name sets must not make everything undeclared: %v", err)
	}
}

// SECURITY. XML 1.0 §2.8 VC Root Element Type: the root must be the element
// the DOCTYPE names. Without the check a document could send any other
// declared element as its root and skip the named one's required attribute
// and content model.
func TestRootElementTypeIsChecked(t *testing.T) {
	const subset = `<!ELEMENT envelope (item)><!ATTLIST envelope sig CDATA #REQUIRED><!ELEMENT item EMPTY>`
	if err := check(t, `<!DOCTYPE envelope [`+subset+`]><item/>`); err == nil ||
		!strings.Contains(err.Error(), "DOCTYPE") {
		t.Errorf("a root other than the DOCTYPE's was accepted: %v", err)
	}
	if err := check(t, `<!DOCTYPE envelope [`+subset+`]><envelope sig="s"><item/></envelope>`); err != nil {
		t.Errorf("the named root was refused: %v", err)
	}
	// Load records the name too.
	err := loadCheck(t, `<!DOCTYPE envelope SYSTEM "e.dtd"><item/>`,
		mapOpts(map[string]string{"e.dtd": subset}))
	if err == nil || !strings.Contains(err.Error(), "DOCTYPE") {
		t.Errorf("Load: a root other than the DOCTYPE's was accepted: %v", err)
	}
}

// A DTD has no UPA gate, so its content models are not bounded by follow
// width: a repeated choice of n alternatives is n wide in every state, is
// deterministic, and XML 1.0 allows it at any width. Its follow relation has
// n² edges, and copying every follow set refused n=1100 at the edge budget;
// the follow sets of a repeated group now share one reference to its first
// set, so the model costs O(n).
func TestWideRepeatedChoiceValidates(t *testing.T) {
	const n = 2000
	var b strings.Builder
	b.WriteString("<!DOCTYPE r [\n<!ELEMENT r (")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "a%d", i)
	}
	b.WriteString(")*>\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<!ELEMENT a%d EMPTY>\n", i)
	}
	b.WriteString("]><r><a1999/><a0/><a500/><a0/></r>")
	if err := check(t, b.String()); err != nil {
		t.Fatalf("a repeated %d-alternative choice must validate: %v", n, err)
	}
	bad := strings.Replace(b.String(), "<a500/>", "<b/>", 1)
	if err := check(t, bad); err == nil {
		t.Fatal("an undeclared child of the same model must be rejected")
	}
}
