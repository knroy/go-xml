package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A bound facet's value is a lexical form of the type it constrains, and every
// type carrying minInclusive/maxInclusive has whiteSpace="collapse" fixed.
// Collapse trims XML S only, so a no-break space in a facet value is part of
// the lexical form and makes it unparsable.
//
// This test is driven through Load rather than through trimXMLSpace. An
// earlier version composed trimXMLSpace with the parse functions by hand and
// asserted the result, which is a tautology about the helper rather than a
// statement about the code under test: reverting all five call sites in
// validate_simple.go back to strings.TrimSpace left every case green, and left
// the whole xsd package green.
//
// What the subtests below pin is that an NBSP-bearing bound is REFUSED, and
// refused by the lexical constraint rather than by some unrelated fault.
//
// NOTE on what this can and cannot pin. checkBounds's own trim is NOT what
// decides these cases: checkFacetValueSpace validates every bound facet
// against the base type's value space at schema load, and that check refuses
// an NBSP bound on its own. Reverting checkBounds's trim therefore leaves
// these cases green -- they pin the load-time constraint, not the trim. The
// trim in checkBounds is defensive for a schema that already errored. The two
// call sites where the trim IS the whole decision are the QName resolvers,
// pinned by TestFacetQNameTrimUsesXMLWhitespaceOnly below, which does fail
// when the fix is reverted.
func TestBoundLexicalsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0; a mangled literal "+
			"makes every case below assert nothing", nbsp)
	}

	// A bound facet on a restriction of base, and an instance value that the
	// bound (once the NBSP is gone) would refuse.
	mk := func(base, bound string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="v" type="t"/>
		  <xs:simpleType name="t">
		    <xs:restriction base="` + base + `">
		      <xs:minInclusive value="` + bound + `"/>
		    </xs:restriction>
		  </xs:simpleType>
		</xs:schema>`
	}
	load := func(t *testing.T, src string) error {
		t.Helper()
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the schema as XML: %v", err)
		}
		_, lerr := Load(tree.Root, "s.xsd", Options{})
		return lerr
	}

	for _, c := range []struct{ name, base, bound, xmlS string }{
		{"numeric", "xs:decimal", "10", " \t10\n"},
		{"temporal", "xs:date", "2026-06-15", " 2026-06-15\t"},
		{"duration", "xs:duration", "P10D", " P10D\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// XML S around the bound is the collapse trim and must be
			// accepted: the bound is the same bound.
			if err := load(t, mk(c.base, c.xmlS)); err != nil {
				t.Errorf("XML S around a %s bound must be trimmed: %v",
					c.name, err)
			}
			// A no-break space is not XML S. It is part of the lexical form,
			// and no lexical form of the base type contains one, so the bound
			// is not in the base type's value space.
			err := load(t, mk(c.base, nbsp+c.bound))
			if err == nil {
				t.Fatalf("a %s bound of %q was accepted; a no-break space is "+
					"not whitespace and makes the bound unparsable",
					c.name, nbsp+c.bound)
			}
			if !strings.Contains(err.Error(), "minInclusive-valid-restriction") {
				t.Errorf("a %s bound of %q was refused by %v; it used to be "+
					"minInclusive-valid-restriction, the lexical check. If "+
					"that guard has moved, checkBounds's own trim is now what "+
					"decides this and must stay trimXMLSpace.",
					c.name, nbsp+c.bound, err)
			}
		})
	}
}

// expandFacetQName and resolveInstanceQName trim their value before splitting
// off the prefix. That trim is the whiteSpace="collapse" edge trim, so it must
// take XML S only: strings.TrimSpace also stripped an NBSP, making
// "<NBSP>a" resolve to the name "a".
//
// NOTE: this pins the trim, not the whole QName lexical rule. isNCName in
// validate_attr.go accepts any rune >= 0x80 as a name character, so an NBSP
// that survives the trim is still accepted as an NCName. That is a separate
// defect in the NCName grammar and is deliberately not addressed here.
// These are driven through Validate, not through trimXMLSpace. Asserting the
// helper's own output is a tautology: it left both call sites free to call
// strings.TrimSpace, which is exactly what they did before 4fd0df5, and the
// test stayed green either way. These two subtests are the pair that does
// fail when the fix is reverted -- with strings.TrimSpace the NBSP is stripped
// and both NBSP cases below are silently ACCEPTED.
func TestFacetQNameTrimUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0; a mangled literal "+
			"makes every case below assert nothing", nbsp)
	}

	// resolveInstanceQName: the value of an xs:QName element. XML S around it
	// is the collapse trim and resolves as normal; an NBSP is part of the
	// prefix, and no such prefix is in scope.
	t.Run("resolveInstanceQName", func(t *testing.T) {
		schema := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="v" type="xs:QName"/>
		</xs:schema>`
		if err := validateString(t, schema, `<v xmlns:p="urn:x">p:local</v>`); err != nil {
			t.Errorf("a plain QName must resolve: %v", err)
		}
		if err := validateString(t, schema, `<v xmlns:p="urn:x"> p:local </v>`); err != nil {
			t.Errorf("XML S around a QName must be trimmed: %v", err)
		}
		err := validateString(t, schema, `<v xmlns:p="urn:x">`+nbsp+`p:local</v>`)
		if err == nil {
			t.Fatal(`"<NBSP>p:local" was accepted as an xs:QName; the NBSP ` +
				`is part of the prefix, so no in-scope declaration binds it. ` +
				`resolveInstanceQName's trim must be trimXMLSpace.`)
		}
		if !strings.Contains(err.Error(), "cvc-datatype-valid") {
			t.Errorf(`"<NBSP>p:local" was refused by %v, not by the datatype `+
				`check; a different fault is deciding this case`, err)
		}
	})

	// expandFacetQName: an enumeration facet value on a QName type. With the
	// NBSP trimmed the facet would expand to the same name as the instance
	// and the document would validate, which is the silent acceptance.
	t.Run("expandFacetQName", func(t *testing.T) {
		mk := func(enum string) string {
			return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
			         xmlns:p="urn:x">
			  <xs:element name="v" type="t"/>
			  <xs:simpleType name="t">
			    <xs:restriction base="xs:QName">
			      <xs:enumeration value="` + enum + `"/>
			    </xs:restriction>
			  </xs:simpleType>
			</xs:schema>`
		}
		const doc = `<v xmlns:p="urn:x">p:local</v>`
		if err := validateString(t, mk("p:local"), doc); err != nil {
			t.Errorf("a plain QName enumeration must match: %v", err)
		}
		if err := validateString(t, mk(" p:local "), doc); err != nil {
			t.Errorf("XML S around a QName enumeration must be trimmed: %v", err)
		}
		err := validateString(t, mk(nbsp+"p:local"), doc)
		if err == nil {
			t.Fatal(`an enumeration of "<NBSP>p:local" matched "p:local"; ` +
				`the NBSP is part of the prefix and the two are different ` +
				`names. expandFacetQName's trim must be trimXMLSpace.`)
		}
		if !strings.Contains(err.Error(), "enumeration") {
			t.Errorf("the NBSP enumeration was refused by %v, not by the "+
				"enumeration facet; a different fault is deciding this", err)
		}
	})
}

// splitFields is the XSD package's list tokenizer and already recognizes only
// XML S. This pins that, so a later "simplification" to strings.Fields is
// caught: an NBSP inside a list item is data and must not split it.
func TestSplitFieldsIsNotUnicodeFields(t *testing.T) {
	const nbsp = "\u00a0"
	if got := splitFields("a" + nbsp + "b c"); len(got) != 2 {
		t.Errorf("splitFields = %q, want 2 tokens", got)
	}
	if got := splitFields(" a\tb\nc\r"); len(got) != 3 {
		t.Errorf("splitFields on XML S = %q, want 3 tokens", got)
	}
	if got := strings.Join(splitFields("a"+nbsp+"b"), "|"); got != "a"+nbsp+"b" {
		t.Errorf("splitFields split on an NBSP: %q", got)
	}
}

// The nine schema-parsing and xsi:type paths that still reached XML Schema
// lexical values through Go's Unicode whitespace.
//
// Every value below is governed by a datatype whose whiteSpace facet is
// "collapse", and XML Schema's whitespace is exactly #x20 #x9 #xD #xA.
// strings.TrimSpace and strings.Fields use unicode.IsSpace, which also matches
// U+00A0 -- so a no-break space in these attributes was stripped as though it
// were whitespace, and a schema the grammar rejects was accepted. In each case
// the downstream check (a decimal scan, an enum switch, a prefix lookup) does
// reject the NBSP once it survives the trim, which is what makes the trim
// itself the whole admission.
//
// The NBSP is written as an escape rather than as a literal byte: a raw U+00A0
// in source is invisible and was silently degraded to an ordinary space in
// this tree once already, which made a test assert nothing while passing.
func TestSchemaParsingUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0; a mangled literal "+
			"makes every case below assert nothing", nbsp)
	}

	// occursValue backs both minOccurs and maxOccurs (xs:nonNegativeInteger
	// and xs:allNNI). It trims internally, so it is checked directly: fixing
	// only its caller would leave the inner trim admitting the same value.
	t.Run("occursValue", func(t *testing.T) {
		if n, _, ok := occursValue("1"); !ok || n != 1 {
			t.Errorf(`occursValue("1") = %d,%v; want 1,true`, n, ok)
		}
		if n, _, ok := occursValue(" 1\t"); !ok || n != 1 {
			t.Errorf(`XML whitespace must still be trimmed: got %d,%v`, n, ok)
		}
		if _, _, ok := occursValue(nbsp + "1"); ok {
			t.Error(`occursValue("<NBSP>1") was accepted; a no-break space is ` +
				`not whitespace in xs:nonNegativeInteger, so the lexical form ` +
				`is invalid`)
		}
		if _, _, ok := occursValue(nbsp + "unbounded"); ok {
			t.Error(`occursValue("<NBSP>unbounded") was accepted; the enum ` +
				`member is "unbounded" exactly`)
		}
	})

	// The list-valued sites tokenize on XML S: a no-break space is data inside
	// a token, not a separator, so it yields ONE token and not two.
	t.Run("splitFields keeps an NBSP inside a token", func(t *testing.T) {
		got := splitFields("extension" + nbsp + "restriction")
		if len(got) != 1 {
			t.Errorf("splitFields split on a no-break space into %d tokens: "+
				"%q; XML S is the only separator", len(got), got)
		}
		if two := splitFields("extension restriction"); len(two) != 2 {
			t.Errorf("XML whitespace must still separate: got %q", two)
		}
	})

	// trimXMLSpace is what the seven scalar sites now call; this pins the
	// property all of them rely on.
	t.Run("trimXMLSpace", func(t *testing.T) {
		if got := trimXMLSpace(" \t\r\ntrue\n"); got != "true" {
			t.Errorf("XML S must be trimmed: %q", got)
		}
		for _, v := range []string{nbsp + "true", "true" + nbsp, nbsp + "1.1",
			nbsp + "extension", nbsp + "xs:string"} {
			if got := trimXMLSpace(v); got != v {
				t.Errorf("trimXMLSpace(%q) = %q; a no-break space is part of "+
					"the lexical form and must survive", v, got)
			}
		}
	})
}

// The parse-level half: a schema document carrying a no-break space where the
// datatype permits only XML S must not be read as though the space were not
// there. Each of these silently produced a valid schema before.
func TestSchemaDocumentRejectsNBSPLexicals(t *testing.T) {
	const nbsp = "\u00a0"

	// A control and an NBSP spelling of the same schema. The control must
	// parse and carry the value; the NBSP spelling must not carry it.
	t.Run("boolean attribute", func(t *testing.T) {
		ok := mustParseSchema(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="r" type="xs:string" nillable=" true "/>
		</xs:schema>`)
		if e := ok.Elements[xdm.QName{Local: "r"}]; e == nil || !e.Nillable {
			t.Fatal(`nillable=" true " with XML whitespace must still be true`)
		}
		bad, err := parseSchemaString(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="r" type="xs:string" nillable="`+nbsp+`true"/>
		</xs:schema>`)
		if err == nil && bad != nil {
			if e := bad.Elements[xdm.QName{Local: "r"}]; e != nil && e.Nillable {
				t.Error(`nillable="<NBSP>true" was read as true; xs:boolean ` +
					`collapses XML S only, so this lexical form is invalid`)
			}
		}
	})

	// This subtest previously had no assertion at all: its whole body was a
	// t.Log guarded by `if err == nil`, and the schema does NOT load, so the
	// branch never ran and the subtest passed unconditionally. maxOccurs is
	// xs:allNNI, whose whiteSpace is a fixed "collapse", so XML S around the
	// value is trimmed and an NBSP is part of the lexical form -- which
	// matches neither "unbounded" nor a non-negative integer.
	t.Run("maxOccurs", func(t *testing.T) {
		mk := func(v string) string {
			return `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
			  <xs:element name="r">
			    <xs:complexType><xs:sequence>
			      <xs:element name="c" type="xs:string" maxOccurs="` + v + `"/>
			    </xs:sequence></xs:complexType>
			  </xs:element>
			</xs:schema>`
		}
		// XML S around either spelling is trimmed by the collapse facet.
		for _, ok := range []string{"unbounded", " unbounded ", "3", " 3\t"} {
			if _, err := parseSchemaString(t, mk(ok)); err != nil {
				t.Errorf("maxOccurs=%q must be accepted: %v", ok, err)
			}
		}
		// A no-break space is not whitespace, in either spelling.
		for _, bad := range []string{nbsp + "unbounded", nbsp + "3"} {
			_, err := parseSchemaString(t, mk(bad))
			if err == nil {
				t.Errorf("maxOccurs=%q was accepted; a no-break space is not "+
					"XML whitespace, so this is not a value of xs:allNNI", bad)
				continue
			}
			if !strings.Contains(err.Error(), "p-props-correct.1") {
				t.Errorf("maxOccurs=%q was refused by %v; it used to be "+
					"p-props-correct.1, the lexical check. If that guard has "+
					"moved, occursValue's own trim is now what decides this "+
					"and must stay trimXMLSpace.", bad, err)
			}
		}
	})
}

// The instance-validation and facet paths, which the schema-parsing sweep did
// not cover.
//
// These drive the real call sites rather than the helpers. An earlier version
// of this test asserted trimXMLSpace's own behaviour, which is a tautology:
// reverting every fix below left it green, because the helper was never the
// thing that was broken -- the call sites not using it were.
func TestInstanceLexicalsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0; a mangled literal "+
			"makes every case below assert nothing", nbsp)
	}

	// A facet value is a lexical xs:nonNegativeInteger, whiteSpace="collapse".
	// An NBSP makes the lexical form invalid, so the schema must report it.
	t.Run("facet value", func(t *testing.T) {
		ok := `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="t">
		    <xs:restriction base="xs:string"><xs:maxLength value=" 5 "/></xs:restriction>
		  </xs:simpleType>
		</xs:schema>`
		if _, err := parseSchemaString(t, ok); err != nil {
			t.Errorf(`maxLength value=" 5 " must parse: %v`, err)
		}
		bad := `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="t">
		    <xs:restriction base="xs:string"><xs:maxLength value="` + nbsp + `5"/></xs:restriction>
		  </xs:simpleType>
		</xs:schema>`
		if _, err := parseSchemaString(t, bad); err == nil {
			t.Error(`maxLength value="<NBSP>5" was accepted; a no-break space ` +
				`is not whitespace in xs:nonNegativeInteger, so the facet ` +
				`value has no valid lexical form`)
		}
	})

	// xsi:nil is xs:boolean. With an NBSP the attribute is not the boolean
	// "true", so the empty element it would have excused is invalid.
	t.Run("xsi:nil", func(t *testing.T) {
		const schema = `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="r" type="xs:int" nillable="true"/>
		</xs:schema>`
		const xsi = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`
		assertValid(t, schema, `<r`+xsi+` xsi:nil=" true "/>`)
		if err := validateString(t, schema,
			`<r`+xsi+` xsi:nil="`+nbsp+`true"/>`); err == nil {
			t.Error(`xsi:nil="<NBSP>true" was honoured; xs:boolean collapses ` +
				`XML S only, so this lexical form is invalid and the empty ` +
				`element is not excused`)
		}
	})

	// xsi:schemaLocation is a list of (namespace, location) pairs, tokenized
	// on XML S. An NBSP is data inside a token, so the pair does not split.
	t.Run("schemaLocation list", func(t *testing.T) {
		docOf := func(v string) *xdm.Node {
			t.Helper()
			tree, err := xdm.ParseString(
				`<r xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
					`xsi:schemaLocation="`+v+`"/>`, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parsing the instance: %v", err)
			}
			return tree.Root
		}
		policy := InstanceLocationPolicy{
			AllowNamespace: func(string) bool { return true },
		}
		if got := instanceLocations(docOf("urn:a a.xsd"), policy); len(got) != 1 {
			t.Errorf("XML whitespace must separate the pair: got %q", got)
		}
		// One malformed token, not a pair: nothing to load.
		if got := instanceLocations(docOf("urn:a"+nbsp+"a.xsd"), policy); len(got) != 0 {
			t.Errorf("a no-break space was treated as the separator of a "+
				"(namespace, location) pair and yielded %q; an XSD list "+
				"tokenizes on XML S alone", got)
		}
	})
}

// The end-to-end half: the empty-content check decides a VALIDATION VERDICT,
// not merely a lexical parse, so it is worth driving through Validate rather
// than through the helper alone.
//
// An element declared with empty content that holds a no-break space is
// INVALID -- cvc-complex-type.2.1 -- because U+00A0 is character content. It
// was reported valid, because strings.TrimSpace erased the NBSP before the
// emptiness test and the element looked like it held nothing.
//
// The XML-whitespace control is what makes this a whitespace test rather than
// a "reject everything" test: an element holding only spaces and a newline IS
// empty for this purpose and must stay valid.
func TestEmptyContentTreatsNBSPAsCharacterContent(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0", nbsp)
	}
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r">
	    <xs:complexType>
	      <xs:attribute name="a" type="xs:string"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	// XML whitespace only: empty, and valid.
	assertValid(t, schema, "<r a=\"x\"> \n\t</r>")

	// A no-break space is content, so the element is not empty.
	if err := validateString(t, schema, "<r a=\"x\">"+nbsp+"</r>"); err == nil {
		t.Error("an element declared with empty content held a no-break " +
			"space and validated; U+00A0 is character content, so " +
			"cvc-complex-type.2.1 must report it")
	}
}

// Two lexical paths have been reported as using Unicode whitespace where XML
// Schema whitespace applies, and NEITHER is a defect. This records why, so the
// next report can be answered by running a test.
//
// Both DO call strings.TrimSpace on a value governed by whiteSpace="collapse",
// which is what makes them look like the real defects fixed alongside them.
// The difference is reachability: an earlier check already rejects the
// no-break space, so the trim never gets to misjudge it.
//
//	xsd/parse_type.go  checkAllGroupRefOccurs compares minOccurs against "1"
//	                   for a group reference inside a named xs:all group.
//	                   p-props-correct.1 has already parsed the attribute as
//	                   an xs:nonNegativeInteger and refused "<NBSP>1".
//	xsd/versioning.go  versionAtLeast trims vc:minVersion before comparing.
//	                   parse.go's vc: handling has already refused
//	                   "<NBSP>1.2" with src-schema.1, "is not an xs:decimal"
//	                   -- a guard added by the earlier pass of this same sweep.
//
// Both were measured by applying the change and re-probing: the observable
// outcome is byte-identical either way.
//
// The general rule, which has now produced four false positives across this
// audit: a strings.TrimSpace on a lexical value is a CANDIDATE, not a finding.
// It is a defect only where nothing downstream rejects the character it
// wrongly strips. Establish that reachability before filing -- and before
// fixing, since an inert change still costs a reviewer the time to verify it.
//
// If either guard above is ever removed, the corresponding trim becomes
// load-bearing and the fix becomes real. These assertions are what would fail.
func TestOccursAndVersioningRefuseNBSPBeforeTheTrim(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0", nbsp)
	}

	// A group reference inside a NAMED xs:all group is the only shape that
	// reaches checkAllGroupRefOccurs; on a complex type's inline xs:all the
	// check is never called.
	t.Run("xs:all group reference occurs", func(t *testing.T) {
		mk := func(occ string) string {
			return `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
			           targetNamespace="urn:t" xmlns:t="urn:t">
			  <xs:group name="inner">
			    <xs:all><xs:element name="a" type="xs:string"/></xs:all>
			  </xs:group>
			  <xs:group name="outer">
			    <xs:all><xs:group ref="t:inner" minOccurs="` + occ + `"/></xs:all>
			  </xs:group>
			  <xs:element name="r">
			    <xs:complexType><xs:group ref="t:outer"/></xs:complexType>
			  </xs:element>
			</xs:schema>`
		}
		load := func(src string) error {
			t.Helper()
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parsing the schema as XML: %v", err)
			}
			_, err = Load(tree.Root, "", Options{Version: Version11})
			return err
		}
		if err := load(mk(" 1 ")); err != nil {
			t.Errorf(`minOccurs=" 1 " must be accepted: %v`, err)
		}
		if err := load(mk("0")); err == nil {
			t.Error("minOccurs=0 on a group reference inside xs:all must " +
				"fail cos-all-limited.1")
		}
		err := load(mk(nbsp + "1"))
		if err == nil {
			t.Fatal(`minOccurs="<NBSP>1" was accepted; some check must ` +
				`refuse it, whether the occurrence rule or the lexical one`)
		}
		if !strings.Contains(err.Error(), "p-props-correct.1") {
			t.Errorf("minOccurs=\"<NBSP>1\" was refused by %v; it used to be "+
				"p-props-correct.1, the lexical check. If that guard has "+
				"moved, checkAllGroupRefOccurs's own trim is now what decides "+
				"this and must become trimXMLSpace.", err)
		}
	})

	// vc:minVersion is an xs:decimal, and parse.go validates the lexical form
	// before versionAtLeast ever sees it.
	t.Run("vc:minVersion", func(t *testing.T) {
		mk := func(v string) string {
			return `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
			           xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning">
			  <xs:element name="r" type="xs:string" vc:minVersion="` + v + `"/>
			</xs:schema>`
		}
		load := func(src string) (*Schema, error) {
			t.Helper()
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parsing the schema as XML: %v", err)
			}
			return Load(tree.Root, "", Options{Version: Version11})
		}
		// A 1.1 processor keeps an element asking for at most 1.1 and drops
		// one asking for 1.2, with XML whitespace trimmed either way.
		if s, err := load(mk("1.1")); err != nil {
			t.Errorf(`vc:minVersion="1.1": %v`, err)
		} else if _, ok := s.Elements[xdm.QName{Local: "r"}]; !ok {
			t.Error(`vc:minVersion="1.1" dropped the element on a 1.1 processor`)
		}
		if s, err := load(mk(" 1.2 ")); err != nil {
			t.Errorf(`vc:minVersion=" 1.2 ": %v`, err)
		} else if _, ok := s.Elements[xdm.QName{Local: "r"}]; ok {
			t.Error(`vc:minVersion=" 1.2 " kept the element; XML whitespace ` +
				`is trimmed by the collapse facet, so this asks for 1.2`)
		}
		_, err := load(mk(nbsp + "1.2"))
		if err == nil {
			t.Fatal(`vc:minVersion="<NBSP>1.2" was accepted; a no-break ` +
				`space is not an xs:decimal lexical form`)
		}
		if !strings.Contains(err.Error(), "src-schema.1") {
			t.Errorf("vc:minVersion=\"<NBSP>1.2\" was refused by %v; it used "+
				"to be src-schema.1, the lexical check. If that guard has "+
				"moved, versionAtLeast's own trim is now what decides this "+
				"and must become trimXMLSpace.", err)
		}
	})
}
