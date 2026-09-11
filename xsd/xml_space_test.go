package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A bound facet's value is a lexical form of the type it constrains, and every
// type carrying minInclusive/maxInclusive has whiteSpace="collapse" fixed.
// Collapse trims XML S only, so a no-break space in a facet value is part of
// the lexical form and makes it unparsable -- an unparsable bound constrains
// nothing. strings.TrimSpace stripped the NBSP instead, so a bound the schema
// wrote invalidly silently acted as a real bound.
//
// The three bound paths are checked through the parse functions they call,
// because the facet comparison treats an unparsable bound as "no opinion":
// going through Validate, a stripped and an unstripped NBSP can produce the
// same verdict for different reasons, which would not distinguish them.
func TestBoundLexicalsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"

	t.Run("numeric", func(t *testing.T) {
		// The numeric bound path trims and then parses as a big.Rat.
		if got := trimXMLSpace(" \t10\n"); got != "10" {
			t.Errorf("XML S must be trimmed from a numeric bound: %q", got)
		}
		if got := trimXMLSpace(nbsp + "10"); got != nbsp+"10" {
			t.Errorf("an NBSP must survive in a numeric bound: %q", got)
		}
	})

	t.Run("temporal", func(t *testing.T) {
		if _, ok := parseTemporal(trimXMLSpace(" 2026-06-15\t"), "date"); !ok {
			t.Error("XML S around a date bound must be trimmed")
		}
		if _, ok := parseTemporal(trimXMLSpace(nbsp+"2026-06-15"), "date"); ok {
			t.Error("an NBSP must make a date bound unparsable")
		}
	})

	t.Run("duration", func(t *testing.T) {
		if _, ok := parseDuration(trimXMLSpace(" P10D\n")); !ok {
			t.Error("XML S around a duration bound must be trimmed")
		}
		if _, ok := parseDuration(trimXMLSpace(nbsp + "P10D")); ok {
			t.Error("an NBSP must make a duration bound unparsable")
		}
	})
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
func TestFacetQNameTrimUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if got := trimXMLSpace(" \t a \n"); got != "a" {
		t.Errorf("XML S must be trimmed: got %q", got)
	}
	if got := trimXMLSpace(nbsp + "a" + nbsp); got != nbsp+"a"+nbsp {
		t.Errorf("an NBSP must not be trimmed: got %q", got)
	}
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

	t.Run("maxOccurs", func(t *testing.T) {
		bad, err := parseSchemaString(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="r">
		    <xs:complexType><xs:sequence>
		      <xs:element name="c" type="xs:string" maxOccurs="`+nbsp+`unbounded"/>
		    </xs:sequence></xs:complexType>
		  </xs:element>
		</xs:schema>`)
		if err == nil && bad != nil {
			t.Log("maxOccurs=\"<NBSP>unbounded\" parsed; occursValue now " +
				"refuses the lexical form, so the particle does not become " +
				"unbounded")
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
