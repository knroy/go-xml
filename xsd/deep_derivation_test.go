package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Twelve iterative walks up a type's base chain terminated on a step count
// (`seen > 64`, `seen > 256`) rather than on a repeated component. A count
// cannot tell a cyclic chain from a merely deep one, and every one of these
// walks returned a definite answer on running out of count — so a legal chain
// longer than the constant got the truncated verdict.
//
// An earlier probe of these guards drove a 300-link restriction chain carrying
// a facet and found no truncation, and that negative result was recorded as
// evidence the counters were unreachable. It was measuring the wrong walks. A
// facet chain collapses during parsing — SimpleType.Primitive is filled in as
// each link is built, so primitiveOf returns on its first iteration whatever
// the depth. The walks that do iterate are the ones asking a question the
// parser did not pre-answer: which built-in a type descends from, and whether
// one type derives from another. TestDeepFacetChainCollapses below pins that
// distinction so the negative result is not re-derived from the same shape.
//
// Depths bracket both constants from either side. The failures below appear at
// exactly 64, 65 or 257 depending on whether the walk counts links or types
// and whether it starts at 0 or 1 — which is itself the point: the cliff sits
// at an arbitrary place nobody would think to test.
var deepDepths = []int{1, 2, 31, 32, 63, 64, 65, 128, 255, 256, 257, 300, 512}

// deepLoad parses and loads a schema, failing the test on either error.
func deepLoad(t *testing.T, src string) *Schema {
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

// deepSimpleChain builds T0 restricting a built-in, then n links restricting
// their predecessor, so Tn sits n restrictions above the built-in.
func deepSimpleChain(n int, builtin, facets string) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	fmt.Fprintf(&b, `<xs:simpleType name="T0"><xs:restriction base="%s">%s</xs:restriction></xs:simpleType>`,
		builtin, facets)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b,
			`<xs:simpleType name="T%d"><xs:restriction base="T%d"/></xs:simpleType>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xs:element name="root" type="T%d"/>`, n)
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// deepComplexChain builds C0 with one child element, then n extensions of it.
func deepComplexChain(n int, rootType, rootAttrs string) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	b.WriteString(`<xs:complexType name="C0"><xs:sequence>` +
		`<xs:element name="a" type="xs:string"/></xs:sequence></xs:complexType>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
			`<xs:extension base="C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xs:element name="root" type="%s"%s/>`, rootType, rootAttrs)
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// TestDeepIDKindOfRestriction covers idKind (validate_simple.go).
//
// The walk decides whether a value is registered as an ID, an IDREF or
// neither. Truncation returned "", meaning "not an ID" — so past the bound a
// type that is an xs:ID stopped behaving like one, and the ID/IDREF bookkeeping
// silently stopped seeing it. REAL BUG: false accept, see TestDeepDuplicateID.
func TestDeepIDKindOfRestriction(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepSimpleChain(n, "xs:ID", ""))
		st, _ := s.Types[xdm.QName{Local: fmt.Sprintf("T%d", n)}].(*SimpleType)
		if st == nil {
			t.Fatalf("depth %d: type not found", n)
		}
		if got := idKind(st, "abc"); got != "ID" {
			t.Errorf("depth %d: idKind = %q, want \"ID\"", n, got)
		}
	}
}

// TestDeepDuplicateID is the instance-level consequence of the walk above: two
// elements carrying the same ID value must be rejected however deep the
// restriction chain under xs:ID runs. Before the fix this document was ACCEPTED
// at depth >= 64 — a false accept, the direction that matters.
func TestDeepDuplicateID(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
		b.WriteString(`<xs:simpleType name="T0"><xs:restriction base="xs:ID"/></xs:simpleType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b,
				`<xs:simpleType name="T%d"><xs:restriction base="T%d"/></xs:simpleType>`, i, i-1)
		}
		fmt.Fprintf(&b, `<xs:element name="root"><xs:complexType><xs:sequence>`+
			`<xs:element name="a" maxOccurs="2"><xs:complexType>`+
			`<xs:attribute name="k" type="T%d"/></xs:complexType></xs:element>`+
			`</xs:sequence></xs:complexType></xs:element>`, n)
		b.WriteString(`</xs:schema>`)

		s := deepLoad(t, b.String())
		d, err := xdm.ParseString(`<root><a k="dup"/><a k="dup"/></root>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: duplicate ID accepted", n)
		}
	}
}

// TestDeepDescendsFromInteger covers descendsFromInteger (validate_simple.go).
//
// The walk gates the integer lexical check. Truncation returned false, so past
// the bound "1.5" was accepted as a value whose type descends from xs:integer.
// REAL BUG: false accept.
func TestDeepDescendsFromInteger(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepSimpleChain(n, "xs:integer", ""))
		st, _ := s.Types[xdm.QName{Local: fmt.Sprintf("T%d", n)}].(*SimpleType)
		if !descendsFromInteger(st) {
			t.Errorf("depth %d: descendsFromInteger = false", n)
		}
		d, err := xdm.ParseString(`<root>1.5</root>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: \"1.5\" accepted as an integer", n)
		}
	}
}

// TestDeepTypeDerivedFrom covers typeDerivedFrom (upa.go), the schema-time
// derivation walk. Truncation returned false — "not derived" — which refuses
// legal schemas rather than admitting bad ones. REAL BUG: false reject.
func TestDeepTypeDerivedFrom(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepComplexChain(n, "C0", ""))
		deep := s.Types[xdm.QName{Local: fmt.Sprintf("C%d", n)}]
		base := s.Types[xdm.QName{Local: "C0"}]
		if deep == nil || base == nil {
			t.Fatalf("depth %d: types not found", n)
		}
		if !typeDerivedFrom(deep, base) {
			t.Errorf("depth %d: typeDerivedFrom = false", n)
		}
	}
}

// TestDeepXsiTypeSubstitution covers derivedFrom (validate.go) through
// cvc-elt.4.3. A type named by xsi:type that genuinely extends the declared
// type must be accepted at any chain length. REAL BUG: false reject at >= 257.
func TestDeepXsiTypeSubstitution(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepComplexChain(n, "C0", ""))
		doc := fmt.Sprintf(
			`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
				`xsi:type="C%d"><a>x</a></root>`, n)
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err != nil {
			t.Errorf("depth %d: legal xsi:type rejected: %v", n, err)
		}
	}
}

// TestDeepXsiTypeBlocked is the other direction on the same walk, and on
// substitutionBlocked (validate.go): block="extension" on the declaration must
// still refuse an extension reached through a long chain. Guards against a fix
// that makes derivedFrom permissive by simply always answering true.
func TestDeepXsiTypeBlocked(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepComplexChain(n, "C0", ` block="extension"`))
		doc := fmt.Sprintf(
			`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
				`xsi:type="C%d"><a>x</a></root>`, n)
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: blocked extension accepted", n)
		}
	}
}

// TestDeepSubstitutionGroupDerivation covers derivationMethodsTo
// (parse_decl.go), which backs e-props-correct.4. This walk was not on the
// original list of eleven; it surfaced because a legal schema failed to LOAD at
// depth >= 65, the member's type having been judged not derived from the
// head's. REAL BUG: false reject, at schema load rather than validation.
func TestDeepSubstitutionGroupDerivation(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" ` +
			`xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">`)
		b.WriteString(`<xs:complexType name="C0"><xs:sequence>` +
			`<xs:element name="a" type="xs:string"/></xs:sequence></xs:complexType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
				`<xs:extension base="t:C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
		}
		b.WriteString(`<xs:element name="head" type="t:C0"/>`)
		fmt.Fprintf(&b,
			`<xs:element name="mem" type="t:C%d" substitutionGroup="t:head"/>`, n)
		b.WriteString(`<xs:element name="root"><xs:complexType><xs:sequence>` +
			`<xs:element ref="t:head"/></xs:sequence></xs:complexType></xs:element>`)
		b.WriteString(`</xs:schema>`)

		st, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse schema: %v", err)
		}
		s, err := Load(st.Root, "", Options{})
		if err != nil {
			t.Errorf("depth %d: legal schema rejected at load: %v", n, err)
			continue
		}
		doc := `<t:root xmlns:t="urn:t"><t:mem><t:a>x</t:a></t:mem></t:root>`
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err != nil {
			t.Errorf("depth %d: legal substitution rejected: %v", n, err)
		}
	}
}

// TestDeepSubstitutionGroupBlocked covers substitutionMemberBlocked
// (assemble.go): block="extension" on the head must refuse a member whose type
// extends it, however long the chain. Reachable only once
// derivationMethodsTo's false reject is fixed, since the schema previously
// failed to load first.
func TestDeepSubstitutionGroupBlocked(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" ` +
			`xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">`)
		b.WriteString(`<xs:complexType name="C0"><xs:sequence>` +
			`<xs:element name="a" type="xs:string"/></xs:sequence></xs:complexType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
				`<xs:extension base="t:C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
		}
		b.WriteString(`<xs:element name="head" type="t:C0" block="extension"/>`)
		fmt.Fprintf(&b,
			`<xs:element name="mem" type="t:C%d" substitutionGroup="t:head"/>`, n)
		b.WriteString(`<xs:element name="root"><xs:complexType><xs:sequence>` +
			`<xs:element ref="t:head"/></xs:sequence></xs:complexType></xs:element>`)
		b.WriteString(`</xs:schema>`)

		s := deepLoad(t, b.String())
		doc := `<t:root xmlns:t="urn:t"><t:mem><t:a>x</t:a></t:mem></t:root>`
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: blocked substitution accepted", n)
		}
	}
}

// TestDeepFacetChainCollapses records why the earlier 300-link probe found
// nothing, so the negative result is not re-derived from the same shape.
//
// primitiveOf (facet.go) walks until it finds a non-nil Primitive, and the
// parser fills that field in on every link as it builds the chain — so the walk
// returns on its first iteration and the count is never approached. The facet
// itself is enforced from the merged FacetSet, not by walking. Both hold at 512
// links, and held before the fix too. The guard was converted for uniformity,
// not because it was reachable.
func TestDeepFacetChainCollapses(t *testing.T) {
	for _, n := range deepDepths {
		s := deepLoad(t, deepSimpleChain(n, "xs:string", `<xs:maxLength value="3"/>`))
		st, _ := s.Types[xdm.QName{Local: fmt.Sprintf("T%d", n)}].(*SimpleType)
		if st == nil {
			t.Fatalf("depth %d: type not found", n)
		}
		if primitiveOf(st) == nil {
			t.Errorf("depth %d: primitiveOf = nil", n)
		}
		d, err := xdm.ParseString(`<root>toolong</root>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: maxLength violation accepted", n)
		}
	}
}

// TestDeepUnionAndListCollapse covers unionMemberTypesOf and listItemTypeOf
// (assert.go). Like primitiveOf these terminate on the first link: a
// restriction of a union or a list inherits MemberTypes/ItemType eagerly during
// parsing, so the loop body runs once. Proven correct at every depth, before
// and after the fix; converted for uniformity.
func TestDeepUnionAndListCollapse(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
		b.WriteString(`<xs:simpleType name="U0">` +
			`<xs:union memberTypes="xs:int xs:date"/></xs:simpleType>`)
		b.WriteString(`<xs:simpleType name="L0">` +
			`<xs:list itemType="xs:int"/></xs:simpleType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b,
				`<xs:simpleType name="U%d"><xs:restriction base="U%d"/></xs:simpleType>`, i, i-1)
			fmt.Fprintf(&b,
				`<xs:simpleType name="L%d"><xs:restriction base="L%d"/></xs:simpleType>`, i, i-1)
		}
		b.WriteString(`<xs:element name="root" type="xs:string"/></xs:schema>`)

		s := deepLoad(t, b.String())
		u, _ := s.Types[xdm.QName{Local: fmt.Sprintf("U%d", n)}].(*SimpleType)
		l, _ := s.Types[xdm.QName{Local: fmt.Sprintf("L%d", n)}].(*SimpleType)
		if u == nil || l == nil {
			t.Fatalf("depth %d: types not found", n)
		}
		if len(unionMemberTypesOf(u)) == 0 {
			t.Errorf("depth %d: unionMemberTypesOf returned no members", n)
		}
		if listItemTypeOf(l) == nil {
			t.Errorf("depth %d: listItemTypeOf = nil", n)
		}
	}
}

// TestDeepInheritedSimpleContent covers inheritedSimpleContent (parse_type.go).
// A simpleContent extension chain also collapses — SimpleContent is filled in
// per link — so the walk returns immediately. The semantic property checked is
// that the inherited xs:int is still enforced at 512 links.
func TestDeepInheritedSimpleContent(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
		b.WriteString(`<xs:complexType name="C0"><xs:simpleContent>` +
			`<xs:extension base="xs:int"/></xs:simpleContent></xs:complexType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:simpleContent>`+
				`<xs:extension base="C%d"/></xs:simpleContent></xs:complexType>`, i, i-1)
		}
		fmt.Fprintf(&b, `<xs:element name="root" type="C%d"/></xs:schema>`, n)

		s := deepLoad(t, b.String())
		ct, _ := s.Types[xdm.QName{Local: fmt.Sprintf("C%d", n)}].(*ComplexType)
		if ct == nil {
			t.Fatalf("depth %d: type not found", n)
		}
		if inheritedSimpleContent(ct) == nil {
			t.Errorf("depth %d: inheritedSimpleContent = nil", n)
		}
		d, err := xdm.ParseString(`<root>notanint</root>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err == nil {
			t.Errorf("depth %d: non-integer accepted for xs:int content", n)
		}
	}
}

// TestDeepBaseDeclaredType covers baseDeclaredType (validate.go), which backs
// Element Declarations Consistent. An extension's particle is spliced to hold
// the base's members, so the declaration is found on the first step whatever
// the chain length. Proven correct at every depth; converted for uniformity.
func TestDeepBaseDeclaredType(t *testing.T) {
	for _, n := range deepDepths {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
		b.WriteString(`<xs:complexType name="C0"><xs:sequence>` +
			`<xs:element name="x" type="xs:int"/></xs:sequence></xs:complexType>`)
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
				`<xs:extension base="C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
		}
		fmt.Fprintf(&b, `<xs:element name="root" type="C%d"/></xs:schema>`, n)

		s := deepLoad(t, b.String())
		ct, _ := s.Types[xdm.QName{Local: fmt.Sprintf("C%d", n)}].(*ComplexType)
		if ct == nil {
			t.Fatalf("depth %d: type not found", n)
		}
		v := &validator{schema: s}
		if v.baseDeclaredType(ct, xdm.QName{Local: "x"}) == nil {
			t.Errorf("depth %d: baseDeclaredType = nil", n)
		}
	}
}

// TestDeepChainCycleStillTerminates is the property the counters were
// protecting: a base chain that reaches itself must not hang. The visited sets
// identify the cycle exactly where the counts identified it only approximately.
// A cyclic chain is ill-formed and rejected at load, which is the correct
// outcome — what matters is that Load returns at all.
func TestDeepChainCycleStillTerminates(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:simpleType name="A"><xs:restriction base="B"/></xs:simpleType>` +
		`<xs:simpleType name="B"><xs:restriction base="A"/></xs:simpleType>` +
		`<xs:element name="root" type="A"/></xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if s, err := Load(st.Root, "", Options{}); err == nil && s != nil {
			d, err := xdm.ParseString(`<root>x</root>`, xdm.ParseOptions{})
			if err == nil {
				s.Validate(d.Root, ValidateOptions{})
			}
		}
	}()
	<-done
}
