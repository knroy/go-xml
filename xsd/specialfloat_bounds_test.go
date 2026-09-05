package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The floating types have three values with no rational form, and §3.3.5 and
// §3.3.6 place two of them on the order: negativeInfinity below every finite
// value, positiveInfinity above every one, with NaN incomparable to everything
// including itself.
//
// checkBounds compared with big.Rat alone and read an unparsable lexical as
// "no opinion", so INF satisfied maxInclusive="100", -INF satisfied
// minInclusive="0", and NaN satisfied every ordering facet there is. Being
// outside every bound a schema can write is exactly what makes these values
// invalid, so the premise was right and the conclusion inverted.

// loadSpecialFloat assembles a schema with no resolver reachable, so that a
// test here can never depend on the filesystem.
func loadSpecialFloat(t *testing.T, src string, v Version) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema: %v", err)
	}
	s, err := Load(tree.Root, "t.xsd", Options{
		Resolver: &MapResolver{},
		Version:  v,
	})
	if err != nil {
		t.Fatalf("loading the test schema: %v", err)
	}
	return s
}

// validateDoc reports the verdict and, when invalid, the facet that refused.
func validateDoc(t *testing.T, s *Schema, doc string) (bool, string) {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test instance %q: %v", doc, err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// TestSpecialFloatBoundsElement asserts the verdict for each special value
// against each of the four ordering facets, for both floating types.
func TestSpecialFloatBoundsElement(t *testing.T) {
	for _, prim := range []string{"double", "float"} {
		t.Run(prim, func(t *testing.T) {
			cases := []struct {
				facet, bound string
				value        string
				wantValid    bool
				wantFacet    string
			}{
				// A finite value keeps behaving as before; a fix
				// that refused these would be no fix at all.
				{"maxInclusive", "100", "50", true, ""},
				{"minInclusive", "0", "50", true, ""},

				// INF is above every finite value.
				{"maxInclusive", "100", "INF", false, "maxInclusive"},
				{"maxExclusive", "100", "INF", false, "maxExclusive"},
				{"minInclusive", "0", "INF", true, ""},
				{"minExclusive", "0", "INF", true, ""},

				// -INF is below every finite value.
				{"minInclusive", "0", "-INF", false, "minInclusive"},
				{"minExclusive", "0", "-INF", false, "minExclusive"},
				{"maxInclusive", "100", "-INF", true, ""},
				{"maxExclusive", "100", "-INF", true, ""},

				// NaN is incomparable, so it satisfies no
				// ordering facet — min or max, inclusive or
				// exclusive. This is the case a comparison
				// written as "not less than" would get wrong.
				{"minInclusive", "0", "NaN", false, "minInclusive"},
				{"maxInclusive", "100", "NaN", false, "maxInclusive"},
				{"minExclusive", "0", "NaN", false, "minExclusive"},
				{"maxExclusive", "100", "NaN", false, "maxExclusive"},

				// A bound may itself be INF, and then it bounds
				// nothing on that side. The JSON-schema mapping
				// in jsonschema.go writes exactly this pair.
				{"maxInclusive", "INF", "INF", true, ""},
				{"maxExclusive", "INF", "INF", false, "maxExclusive"},
				{"maxExclusive", "INF", "1e300", true, ""},
				{"minInclusive", "-INF", "-INF", true, ""},
				{"minExclusive", "-INF", "-INF", false, "minExclusive"},
				{"minExclusive", "-INF", "-1e300", true, ""},

				// NaN on either side of the comparison leaves it
				// unsatisfiable, so a NaN bound admits nothing.
				{"maxInclusive", "NaN", "NaN", false, "maxInclusive"},
			}
			for _, c := range cases {
				src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
				  <xs:element name="a">
				    <xs:simpleType>
				      <xs:restriction base="xs:` + prim + `">
				        <xs:` + c.facet + ` value="` + c.bound + `"/>
				      </xs:restriction>
				    </xs:simpleType>
				  </xs:element>
				</xs:schema>`
				s := loadSpecialFloat(t, src, Version10)
				valid, why := validateDoc(t, s, "<a>"+c.value+"</a>")
				if valid != c.wantValid {
					t.Errorf("%s=%q with value %q: valid=%v, want %v (%s)",
						c.facet, c.bound, c.value, valid, c.wantValid, why)
					continue
				}
				if !valid && !strings.Contains(why, c.wantFacet) {
					t.Errorf("%s=%q with value %q: refused by %q, want the %s facet",
						c.facet, c.bound, c.value, why, c.wantFacet)
				}
			}
		})
	}
}

// TestSpecialFloatBoundsAttribute is the same check on the attribute path,
// which reaches checkBounds through validateSimpleValueIn rather than through
// the element's type.
func TestSpecialFloatBoundsAttribute(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a">
	    <xs:complexType>
	      <xs:attribute name="v">
	        <xs:simpleType>
	          <xs:restriction base="xs:double">
	            <xs:minInclusive value="0"/>
	            <xs:maxInclusive value="100"/>
	          </xs:restriction>
	        </xs:simpleType>
	      </xs:attribute>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	s := loadSpecialFloat(t, src, Version10)
	for _, c := range []struct {
		value     string
		wantValid bool
	}{
		{"50", true},
		{"INF", false},
		{"-INF", false},
		{"NaN", false},
	} {
		valid, why := validateDoc(t, s, `<a v="`+c.value+`"/>`)
		if valid != c.wantValid {
			t.Errorf("attribute v=%q: valid=%v, want %v (%s)",
				c.value, valid, c.wantValid, why)
		}
	}
}

// TestSpecialFloatBoundsDerivationChain checks that a bound inherited from
// several levels up still refuses. checkBounds walks a chain of facetSteps, and
// a fix applied to only the innermost step would pass the flat cases above.
func TestSpecialFloatBoundsDerivationChain(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="l1">
	    <xs:restriction base="xs:double">
	      <xs:minInclusive value="0"/>
	      <xs:maxInclusive value="1000"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="l2">
	    <xs:restriction base="l1"><xs:maxInclusive value="500"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="l3">
	    <xs:restriction base="l2"><xs:maxInclusive value="100"/></xs:restriction>
	  </xs:simpleType>
	  <xs:element name="a" type="l3"/>
	</xs:schema>`
	s := loadSpecialFloat(t, src, Version10)
	for _, c := range []struct {
		value     string
		wantValid bool
	}{
		{"50", true},
		{"INF", false},
		{"-INF", false},
		{"NaN", false},
	} {
		valid, why := validateDoc(t, s, "<a>"+c.value+"</a>")
		if valid != c.wantValid {
			t.Errorf("three-level chain, value %q: valid=%v, want %v (%s)",
				c.value, valid, c.wantValid, why)
		}
	}
}

// TestSpecialFloatBoundsListAndUnion covers the two constructions that reach
// the facet check for something other than the whole value: a list checks each
// item, and a union checks each member type in turn.
func TestSpecialFloatBoundsListAndUnion(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="bounded">
	    <xs:restriction base="xs:double">
	      <xs:minInclusive value="0"/>
	      <xs:maxInclusive value="100"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="blist">
	    <xs:list itemType="bounded"/>
	  </xs:simpleType>
	  <xs:simpleType name="bunion">
	    <xs:union memberTypes="bounded xs:date"/>
	  </xs:simpleType>
	  <xs:element name="l" type="blist"/>
	  <xs:element name="u" type="bunion"/>
	</xs:schema>`
	s := loadSpecialFloat(t, src, Version10)

	for _, c := range []struct {
		el, value string
		wantValid bool
	}{
		// A list is valid only if every item is, so one INF among
		// finite items must take the whole list down.
		{"l", "1 2 3", true},
		{"l", "1 INF 3", false},
		{"l", "1 -INF 3", false},
		{"l", "1 NaN 3", false},

		// A union is valid if any member accepts. Neither member
		// accepts INF: the bounded double refuses it and xs:date has no
		// such lexical, so the union must refuse it too. Before the fix
		// the bounded member accepted and the union passed.
		{"u", "50", true},
		{"u", "2020-01-01", true},
		{"u", "INF", false},
		{"u", "-INF", false},
		{"u", "NaN", false},
	} {
		valid, why := validateDoc(t, s, "<"+c.el+">"+c.value+"</"+c.el+">")
		if valid != c.wantValid {
			t.Errorf("<%s>%s</%s>: valid=%v, want %v (%s)",
				c.el, c.value, c.el, valid, c.wantValid, why)
		}
	}
}

// TestSpecialFloatBoundsVersions checks both schema versions. The value space
// of the floating types is the same in each; only the lexical space differs,
// with 1.1 adding "+INF" as a second spelling of positiveInfinity.
func TestSpecialFloatBoundsVersions(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a">
	    <xs:simpleType>
	      <xs:restriction base="xs:double">
	        <xs:maxInclusive value="100"/>
	      </xs:restriction>
	    </xs:simpleType>
	  </xs:element>
	</xs:schema>`
	for _, v := range []Version{Version10, Version11} {
		s := loadSpecialFloat(t, src, v)
		if valid, _ := validateDoc(t, s, "<a>INF</a>"); valid {
			t.Errorf("version %v: INF satisfied maxInclusive=100", v)
		}
		if valid, _ := validateDoc(t, s, "<a>NaN</a>"); valid {
			t.Errorf("version %v: NaN satisfied maxInclusive=100", v)
		}
		if valid, why := validateDoc(t, s, "<a>50</a>"); !valid {
			t.Errorf("version %v: 50 should satisfy maxInclusive=100: %s", v, why)
		}
	}

	// "+INF" is positiveInfinity under 1.1 and not a lexical at all under
	// 1.0. Under 1.1 it must be refused by the bound, not accepted by it.
	s11 := loadSpecialFloat(t, src, Version11)
	if valid, _ := validateDoc(t, s11, "<a>+INF</a>"); valid {
		t.Error("1.1: +INF satisfied maxInclusive=100")
	}
}

// TestSpecialFloatEnumerationUnaffected confirms the auditor's reading that
// enumeration is a separate path and was already right. It is here so that a
// later change to the bound comparison cannot quietly take enumeration with it.
func TestSpecialFloatEnumerationUnaffected(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a">
	    <xs:simpleType>
	      <xs:restriction base="xs:double">
	        <xs:enumeration value="INF"/>
	        <xs:enumeration value="1"/>
	      </xs:restriction>
	    </xs:simpleType>
	  </xs:element>
	</xs:schema>`
	s := loadSpecialFloat(t, src, Version10)
	for _, c := range []struct {
		value     string
		wantValid bool
	}{
		// Enumeration is equality, not order, so INF is a member
		// because it was listed — the bound fix must not change that.
		{"INF", true},
		{"1", true},
		{"2", false},
		// NaN is not equal to itself, but enumeration on the floating
		// types matches the lexical-derived value, and NaN was not
		// listed here in any case.
		{"NaN", false},
		{"-INF", false},
	} {
		valid, why := validateDoc(t, s, "<a>"+c.value+"</a>")
		if valid != c.wantValid {
			t.Errorf("enumeration with value %q: valid=%v, want %v (%s)",
				c.value, valid, c.wantValid, why)
		}
	}
}

// TestSpecialFloatBoundOrderDiagnostic covers the schema-load side.
//
// compareBoundValues returned "unordered" for a bound with no rational form and
// checkBoundOrder reads unordered as satisfied, so a restriction could widen
// the base's maxInclusive="10" to "INF" and the schema loaded. §4.3.7.4 forbids
// widening. This has no instance consequence — facetChain still enforces the
// base's bound at validation time — so it is a missing diagnostic, not a false
// accept.
func TestSpecialFloatBoundOrderDiagnostic(t *testing.T) {
	load := func(body string) error {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			body + `</xs:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		_, err = Load(tree.Root, "t.xsd", Options{Resolver: &MapResolver{}})
		return err
	}

	cases := []struct {
		name, body, want string
	}{
		{
			name: "widen maxInclusive to INF",
			body: `<xs:simpleType name="b">
			         <xs:restriction base="xs:double">
			           <xs:maxInclusive value="10"/></xs:restriction></xs:simpleType>
			       <xs:simpleType name="d">
			         <xs:restriction base="b">
			           <xs:maxInclusive value="INF"/></xs:restriction></xs:simpleType>`,
			want: "maxInclusive-valid-restriction",
		},
		{
			name: "widen minInclusive to -INF",
			body: `<xs:simpleType name="b">
			         <xs:restriction base="xs:double">
			           <xs:minInclusive value="10"/></xs:restriction></xs:simpleType>
			       <xs:simpleType name="d">
			         <xs:restriction base="b">
			           <xs:minInclusive value="-INF"/></xs:restriction></xs:simpleType>`,
			want: "minInclusive-valid-restriction",
		},
		{
			name: "minInclusive INF above maxInclusive 10",
			body: `<xs:simpleType name="d">
			         <xs:restriction base="xs:double">
			           <xs:minInclusive value="INF"/>
			           <xs:maxInclusive value="10"/></xs:restriction></xs:simpleType>`,
			want: "minInclusive-less-than-equal-to-maxInclusive",
		},
		{
			// Narrowing to INF from an INF base is not widening,
			// and a bound of INF on an unbounded base is the pair
			// jsonschema.go writes. Both must still load.
			name: "narrowing within INF bounds loads",
			body: `<xs:simpleType name="d">
			         <xs:restriction base="xs:double">
			           <xs:minExclusive value="-INF"/>
			           <xs:maxExclusive value="INF"/></xs:restriction></xs:simpleType>`,
			want: "",
		},
		{
			// NaN orders against nothing, so no ordering constraint
			// can be shown to fail and the schema loads. Refusing
			// here would be asserting an order NaN does not have.
			name: "NaN bound is unordered, not an error",
			body: `<xs:simpleType name="b">
			         <xs:restriction base="xs:double">
			           <xs:maxInclusive value="10"/></xs:restriction></xs:simpleType>
			       <xs:simpleType name="d">
			         <xs:restriction base="b">
			           <xs:maxInclusive value="NaN"/></xs:restriction></xs:simpleType>`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := load(c.body)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("should load, but: %v", err)
			case c.want != "" && err == nil:
				t.Errorf("should be refused with %s, but loaded", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Errorf("refused with %v, want %s", err, c.want)
			}
		})
	}
}
