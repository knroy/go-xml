package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestBoundsSkippedWithoutBoundFacets pins checkBounds' early return: a chain
// with no min/max facet is never compared, so validating an xs:integer does
// not parse it into a big.Rat. The allocation bound is what fails if the parse
// comes back; the table is what fails if the early return swallows a bound.
func TestBoundsSkippedWithoutBoundFacets(t *testing.T) {
	integer := BuiltinType("integer")
	if hasBoundFacet(facetChain(integer)) {
		t.Fatal("xs:integer carries no bound facet")
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := validateAtomicValueVersion("123456789012", integer, Version10); err != nil {
			t.Fatal(err)
		}
	})
	// 23 with both rational parses (bounds, then the fractionDigits="0"
	// every xs:integer carries); what remains is the rest of the check.
	if allocs > 1 {
		t.Errorf("validating an xs:integer allocated %v times, want at most 1", allocs)
	}

	for _, c := range []struct {
		typ, value string
		valid      bool
	}{
		{"byte", "127", true},
		{"byte", "128", false},
		{"unsignedLong", "18446744073709551615", true},
		{"unsignedLong", "18446744073709551616", false},
		{"nonNegativeInteger", "-1", false},
		{"integer", "-99999999999999999999", true},
	} {
		_, err := validateAtomicValueVersion(c.value, BuiltinType(c.typ), Version10)
		if (err == nil) != c.valid {
			t.Errorf("%s %q: err=%v, want valid=%v", c.typ, c.value, err, c.valid)
		}
	}

	// User-declared bounds on the other primitives checkBounds dispatches to.
	schema := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r"><xs:complexType><xs:sequence>
	    <xs:element name="d" minOccurs="0"><xs:simpleType><xs:restriction base="xs:double">
	      <xs:maxInclusive value="100"/></xs:restriction></xs:simpleType></xs:element>
	    <xs:element name="t" minOccurs="0"><xs:simpleType><xs:restriction base="xs:date">
	      <xs:minInclusive value="2000-01-01"/></xs:restriction></xs:simpleType></xs:element>
	    <xs:element name="p" minOccurs="0"><xs:simpleType><xs:restriction base="xs:duration">
	      <xs:maxExclusive value="P1D"/></xs:restriction></xs:simpleType></xs:element>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	assertValid(t, schema, `<r><d>100</d><t>2000-01-01</t><p>PT23H</p></r>`)
	assertInvalid(t, schema, `<r><d>INF</d></r>`, "maxInclusive")
	assertInvalid(t, schema, `<r><d>NaN</d></r>`, "maxInclusive")
	assertInvalid(t, schema, `<r><t>1999-12-31</t></r>`, "minInclusive")
	assertInvalid(t, schema, `<r><p>PT24H</p></r>`, "maxExclusive")
}

// TestCollapseFastPath pins Normalize's early return for a value that is
// already collapsed. It must return exactly what building the copy returns —
// including U+FFFD for invalid UTF-8, which the copy produces — and it must not
// allocate when it applies.
func TestCollapseFastPath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"a", "a"},
		{"a b c", "a b c"},
		{"é ü", "é ü"},
		{" ", ""},
		{" a", "a"},
		{"a ", "a"},
		{"a  b", "a b"},
		{"a\tb", "a b"},
		{"a\nb", "a b"},
		{"a\rb", "a b"},
		{" a ", " a "},
		{"a\xffb", "a�b"},
		{"\xff", "�"},
	} {
		if got := WhiteCollapse.Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = WhiteCollapse.Normalize("already collapsed value")
	})
	if allocs != 0 {
		t.Errorf("collapsing a collapsed value allocated %v times, want 0", allocs)
	}
}

// TestIdentityBookkeepingOnlyInsideAScope pins that the per-node records only
// identity constraints read — key values, complex-typed elements, nested-scope
// declarations, merged tables — are kept inside a constraint's element and
// nowhere else. Outside every scope nothing can select a node, so recording
// there was work for no reader, paid on every value of every document.
func TestIdentityBookkeepingOnlyInsideAScope(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="I"><xs:attribute name="v" type="xs:int"/></xs:complexType>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="out" type="I" maxOccurs="unbounded"/>
	        <xs:element name="in">
	          <xs:complexType>
	            <xs:sequence><xs:element name="i" type="I" maxOccurs="unbounded"/></xs:sequence>
	          </xs:complexType>
	          <xs:unique name="u"><xs:selector xpath="i"/><xs:field xpath="@v"/></xs:unique>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)
	tree, err := xdm.ParseString(
		`<root><out v="1"/><out v="1"/><in><i v="2"/><i v="02"/></in></root>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root.ChildElements()[0]
	in := root.ChildElements()[2]
	v := &validator{schema: s, opts: ValidateOptions{MaxErrors: DefaultMaxErrors,
		MaxDepth: DefaultMaxDepth}, ids: map[string]int{}}
	v.validateElement(root, s.Elements[xdm.QName{Local: "root"}])

	// 2 and 02 are one xs:int, so the unique is violated: the scope's
	// records were kept and compared by value.
	if len(v.errs) != 1 || v.errs[0].Code != "cvc-identity-constraint.4.1" {
		t.Fatalf("errors = %v, want one cvc-identity-constraint.4.1", v.errs)
	}
	for n := range v.keyValues {
		if n.Parent() == nil || n.Parent().Parent() != in {
			t.Errorf("key value recorded for %s outside the constraint's scope", n.Name().Local)
		}
	}
	if len(v.keyValues) != 2 {
		t.Errorf("%d key values recorded, want the 2 inside the scope", len(v.keyValues))
	}
	for n := range v.complexTyped {
		if n != in && n.Parent() != in {
			t.Errorf("complexTyped recorded %s outside the constraint's scope", n.Name().Local)
		}
	}
	for n := range v.declFor {
		if n != in && n.Parent() != in {
			t.Errorf("declFor recorded %s outside the constraint's scope", n.Name().Local)
		}
	}
	if tbl := mergeTables([]icTables{nil, {}, nil}); tbl != nil {
		t.Errorf("merging no tables gave %v, want nil", tbl)
	}
}

// TestValidateAllocationFlatInChildCount: a content model with occurrence
// bounds carries its count vectors in buffers reused across children and
// across walks (walkScratch, scratchVec), deduplicated as they are built
// (pushVec), and the walk ranges over el.Children instead of a ChildElements
// slice. Before, each child cost a fresh vector and the walk a fresh slice:
// the catalog workload made 101,505 allocations a pass, now 341.
func TestValidateAllocationFlatInChildCount(t *testing.T) {
	s, err := parseSchemaString(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="g" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
      <xs:sequence minOccurs="1" maxOccurs="3">
        <xs:element name="a" type="xs:string" minOccurs="2" maxOccurs="900"/>
      </xs:sequence>
    </xs:complexType></xs:element>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	allocs := func(groups int) float64 {
		var b strings.Builder
		b.WriteString("<r>")
		for i := 0; i < groups; i++ {
			b.WriteString("<g>\n  " + strings.Repeat("<a>x</a>\n  ", 40) + "</g>\n")
		}
		b.WriteString("</r>")
		doc, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return testing.AllocsPerRun(5, func() {
			if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
				t.Fatalf("document should be valid: %v", err)
			}
		})
	}
	small, large := allocs(10), allocs(100)
	// 900 more children; a vector per child or a slice per group shows up
	// as hundreds of allocations.
	if large-small > 20 {
		t.Errorf("validating 100 groups of 40 children allocated %.0f times, 10 groups %.0f; "+
			"the walk is allocating per child or per element again", large, small)
	}
}
