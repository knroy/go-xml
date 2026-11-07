package xsd

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// vbCheck validates doc in tree and stream mode and fails unless both give
// the verdict want (true = valid), and, for an invalid document, unless the
// tree's error names code.
func vbCheck(t *testing.T, label string, s *Schema, doc string, valid bool, code string) {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	terr := s.Validate(tree.Root, ValidateOptions{})
	_, serr := svRun(t, s, doc, xsdstream.Mode{Root: xsdstream.Strict, Doc: true}, true)
	if (terr == nil) != valid || (serr == nil) != valid {
		t.Errorf("%s: tree %v, stream %v, want valid %v", label, terr, serr, valid)
		return
	}
	if !valid && !strings.Contains(terr.Error(), code) {
		t.Errorf("%s: tree error %v, want %s", label, terr, code)
	}
	if !valid && terr.Error() != serr.Error() {
		t.Errorf("%s: tree error %q, stream error %q", label, terr, serr)
	}
}

func vbLoad(t *testing.T, src string, ver Version) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{Version: ver})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const vbXSI = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`

// xsi:nil suppresses only the content checks of a complex type: the
// attributes, the type's abstractness and its xs:ID attributes still count.
func TestNilledComplexTypeChecksAttributes(t *testing.T) {
	s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="d"><xs:complexType><xs:sequence><xs:element name="r" nillable="true" maxOccurs="unbounded"><xs:complexType><xs:sequence><xs:element name="c" type="xs:string"/></xs:sequence><xs:attribute name="req" type="xs:int" use="required"/><xs:attribute name="id" type="xs:ID"/><xs:attribute name="cur"><xs:simpleType><xs:restriction base="xs:string"><xs:enumeration value="USD"/></xs:restriction></xs:simpleType></xs:attribute></xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element></xs:schema>`, Version11)
	vbCheck(t, "valid nilled", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" cur="USD"/></d>`, true, "")
	vbCheck(t, "undeclared attribute", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" bogus="1"/></d>`, false, "cvc-complex-type.3.2.2")
	vbCheck(t, "required attribute missing", s, `<d`+vbXSI+`><r xsi:nil="true"/></d>`, false, "cvc-complex-type.4")
	vbCheck(t, "attribute value", s, `<d`+vbXSI+`><r xsi:nil="true" req="notanint"/></d>`, false, "req")
	vbCheck(t, "duplicate ID", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" id="x"/><r xsi:nil="true" req="1" id="x"/></d>`, false, "cvc-id.2")
	a := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="A" abstract="true"><xs:sequence/></xs:complexType><xs:element name="r" type="A" nillable="true"/></xs:schema>`, Version11)
	vbCheck(t, "abstract type", a, `<r`+vbXSI+` xsi:nil="true"/>`, false, "cvc-type.2")
	as := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r" nillable="true"><xs:complexType><xs:sequence><xs:element name="c"/></xs:sequence><xs:attribute name="a" type="xs:int"/><xs:assert test="@a = 1"/></xs:complexType></xs:element></xs:schema>`, Version11)
	vbCheck(t, "assertion holds", as, `<r`+vbXSI+` xsi:nil="true" a="1"/>`, true, "")
	vbCheck(t, "assertion fails", as, `<r`+vbXSI+` xsi:nil="true" a="2"/>`, false, "cvc-assertion")
}

// Under XSD 1.1 xsi:type must derive from the type the alternatives selected,
// not from the declared one, or naming the declared type escapes them.
func TestXSITypeDerivedFromSelectedAlternative(t *testing.T) {
	s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="base"><xs:sequence/><xs:attribute name="kind" type="xs:string"/><xs:attribute name="v" type="xs:anySimpleType"/></xs:complexType><xs:complexType name="strict"><xs:complexContent><xs:restriction base="base"><xs:attribute name="v" type="xs:integer"/></xs:restriction></xs:complexContent></xs:complexType><xs:element name="m" type="base"><xs:alternative test="@kind='forbidden'" type="xs:error"/><xs:alternative test="@kind='num'" type="strict"/></xs:element></xs:schema>`, Version11)
	vbCheck(t, "xsi:type base, xs:error selected", s, `<m`+vbXSI+` xsi:type="base" kind="forbidden"/>`, false, "cvc-elt.4.3")
	vbCheck(t, "xsi:type base, strict selected", s, `<m`+vbXSI+` xsi:type="base" kind="num" v="abc"/>`, false, "cvc-elt.4.3")
	vbCheck(t, "xsi:type strict, strict selected", s, `<m`+vbXSI+` xsi:type="strict" kind="num" v="5"/>`, true, "")
	vbCheck(t, "xsi:type base, no alternative", s, `<m`+vbXSI+` xsi:type="base" kind="other" v="abc"/>`, true, "")
}

// A nested scope of a key below a child a wildcard assessed without a
// declaration still reaches the outer scope's node table. The field's @id has
// a global declaration: one the lax wildcard leaves undeclared has no type and
// fails the field itself (TestFieldOnUnassessedWildcardAttribute).
func TestWildcardChildKeepsIdentityTables(t *testing.T) {
	for _, kind := range []string{"unique", "key"} {
		s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:attribute name="id" type="xs:string"/><xs:element name="R"><xs:complexType><xs:sequence><xs:any processContents="lax" minOccurs="0" maxOccurs="unbounded"/></xs:sequence></xs:complexType><xs:`+kind+` name="u"><xs:selector xpath=".//x"/><xs:field xpath="@id"/></xs:`+kind+`></xs:element></xs:schema>`, Version11)
		code := map[string]string{"unique": "cvc-identity-constraint.4.1", "key": "cvc-identity-constraint.4.2.2"}[kind]
		vbCheck(t, kind+" via undeclared wrapper", s, `<R><x id="1"/><W><R><x id="1"/></R></W></R>`, false, code)
		vbCheck(t, kind+" via xsi:type wrapper", s, `<R`+vbXSI+` xmlns:xs="http://www.w3.org/2001/XMLSchema"><x id="1"/><W xsi:type="xs:anyType"><R><x id="1"/></R></W></R>`, false, code)
		vbCheck(t, kind+" distinct via wrapper", s, `<R><x id="1"/><W><R><x id="2"/></R></W></R>`, true, "")
	}
}

// TestFieldOnUnassessedWildcardAttribute pins idZ015 (W3C bug 4063, settled
// "invalid" for 1.0): a field selecting an attribute an attribute wildcard did
// not assess. It has no type, so XSD 1.0 §3.11.4 clause 3 ("must have a simple
// type") fails, for lax and skip alike. XSD 1.1 clause 3 admits ·skipped·
// nodes and drops them, so there a skip match leaves the field empty: a key
// fails 4.2.1 and a unique holds. A lax match with no declaration is not
// skipped and still fails clause 3.
func TestFieldOnUnassessedWildcardAttribute(t *testing.T) {
	for _, c := range []struct {
		ver      Version
		pc, kind string
		valid    bool
		code     string
	}{
		{Version10, "skip", "key", false, "cvc-identity-constraint.3"},
		{Version10, "skip", "unique", false, "cvc-identity-constraint.3"},
		{Version10, "lax", "key", false, "cvc-identity-constraint.3"},
		{Version10, "lax", "unique", false, "cvc-identity-constraint.3"},
		{Version11, "skip", "key", false, "cvc-identity-constraint.4.2.1"},
		{Version11, "skip", "unique", true, ""},
		{Version11, "lax", "key", false, "cvc-identity-constraint.3"},
		{Version11, "lax", "unique", false, "cvc-identity-constraint.3"},
	} {
		s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="foo"><xs:complexType><xs:anyAttribute processContents="`+c.pc+`"/></xs:complexType><xs:`+c.kind+` name="k"><xs:selector xpath="."/><xs:field xpath="@bar"/></xs:`+c.kind+`></xs:element></xs:schema>`, c.ver)
		vbCheck(t, fmt.Sprintf("%v %s %s", c.ver, c.pc, c.kind), s, `<foo bar="abc"/>`, c.valid, c.code)
	}
}

// A field selecting a recursive complex-typed element is refused at its end
// tag; until then the stream must not keep the text below every open one.
func TestStreamComplexFieldTextNotBuffered(t *testing.T) {
	s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence><xs:element ref="t"/></xs:sequence></xs:complexType><xs:unique name="u"><xs:selector xpath=".//t"/><xs:field xpath="."/></xs:unique></xs:element><xs:element name="t"><xs:complexType mixed="true"><xs:sequence><xs:element ref="t" minOccurs="0"/></xs:sequence></xs:complexType></xs:element></xs:schema>`, Version11)
	const depth = 200
	doc := "<r>" + strings.Repeat("<t>", depth) + strings.Repeat("a", 16<<10) + strings.Repeat("</t>", depth) + "</r>"
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	_, err := svRun(t, s, doc, xsdstream.Mode{Root: xsdstream.Strict}, false)
	runtime.ReadMemStats(&b)
	if err == nil || !strings.Contains(err.Error(), "cvc-identity-constraint.3") {
		t.Fatalf("stream error %v, want cvc-identity-constraint.3", err)
	}
	// Kept per open field, the text alone is depth x 16 KiB = 3.2 MiB.
	if got := b.TotalAlloc - a.TotalAlloc; got > 1<<20 {
		t.Errorf("streaming allocated %d bytes for a %d-byte document", got, len(doc))
	}
}
