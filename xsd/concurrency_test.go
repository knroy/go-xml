package xsd

import (
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A Schema is documented as safe to share once loaded, and the content-model
// cache is a sync.Map on the strength of that. This exercises the claim under
// -race: many goroutines validating against one Schema, including types whose
// models are compiled lazily on first use, so the cache is written and read
// concurrently rather than only read.
func TestSchemaSharedAcrossGoroutines(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" type="xs:int" maxOccurs="unbounded"/>
	        <xs:element name="b" minOccurs="0">
	          <xs:complexType>
	            <xs:choice maxOccurs="unbounded">
	              <xs:element name="x" type="xs:string"/>
	              <xs:element name="y" type="xs:date"/>
	            </xs:choice>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	      <xs:attribute name="id" type="xs:ID"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	s, err := parseSchemaString(t, src)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	docs := []struct {
		xml   string
		valid bool
	}{
		{`<root id="i1"><a>1</a><a>2</a></root>`, true},
		{`<root><a>1</a><b><x>s</x><y>2020-01-01</y></b></root>`, true},
		{`<root><a>notanint</a></root>`, false},
		{`<root><b/></root>`, false}, // "a" is required
		{`<root><a>3</a><b><y>2020-13-01</y></b></root>`, false},
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := docs[i%len(docs)]
			tree, err := xdm.ParseString(c.xml, xdm.ParseOptions{})
			if err != nil {
				t.Errorf("parse %q: %v", c.xml, err)
				return
			}
			err = s.Validate(tree.Root, ValidateOptions{})
			if c.valid && err != nil {
				t.Errorf("%q: unexpected error: %v", c.xml, err)
			}
			if !c.valid && err == nil {
				t.Errorf("%q: expected an error", c.xml)
			}
		}(i)
	}
	wg.Wait()
}

// The harder case: every goroutine reaches a *cold* schema at once, so the
// content-model cache is written by competing goroutines rather than read from
// a warm one. A schema validated once before being shared would never exercise
// that path.
func TestSchemaColdStartUnderConcurrency(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:sequence>
	              <xs:element name="deep" type="xs:string" maxOccurs="unbounded"/>
	            </xs:sequence>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	for round := 0; round < 20; round++ {
		s, err := parseSchemaString(t, src)
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start // release them together, onto a cold cache
				tree, err := xdm.ParseString(
					`<root><a><deep>x</deep><deep>y</deep></a></root>`,
					xdm.ParseOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
					t.Errorf("valid document rejected: %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
}

// Identity constraints and ID/IDREF accumulate state across a whole document —
// key tables, the set of IDs seen, unresolved references. If any of that were
// held on the Schema rather than per-validation, two documents validated at
// once would see each other's identifiers: the first would poison the second
// with a duplicate-ID error for an ID it never contained.
func TestIdentityStateIsPerValidation(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:attribute name="id" type="xs:ID" use="required"/>
	            <xs:attribute name="ref" type="xs:IDREF"/>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath="item"/>
	      <xs:field xpath="@id"/>
	    </xs:key>
	  </xs:element>
	</xs:schema>`

	s, err := parseSchemaString(t, src)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Every goroutine uses the *same* identifiers. Shared state shows up as
	// a spurious duplicate-ID or duplicate-key failure.
	const doc = `<root><item id="a" ref="b"/><item id="b"/></root>`

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
				t.Errorf("identity state leaked between validations: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
}

// XSD 1.1 assertions evaluate XPath, which reaches the compiled-expression and
// regex caches shared across the process. They also annotate a subtree with
// type information before evaluating, which is the kind of thing that would be
// unsafe if it wrote back into the shared schema.
func TestAssertionsUnderConcurrency(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="lo" type="xs:int"/>
	        <xs:element name="hi" type="xs:int"/>
	      </xs:sequence>
	      <xs:assert test="xs:int(lo) le xs:int(hi)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	s := load11(t, src)

	cases := []struct {
		doc   string
		valid bool
	}{
		{`<root><lo>1</lo><hi>2</hi></root>`, true},
		{`<root><lo>5</lo><hi>5</hi></root>`, true},
		{`<root><lo>9</lo><hi>2</hi></root>`, false},
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := cases[i%len(cases)]
			<-start
			tree, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			err = s.Validate(tree.Root, ValidateOptions{})
			if c.valid && err != nil {
				t.Errorf("%q: unexpected error: %v", c.doc, err)
			}
			if !c.valid && err == nil {
				t.Errorf("%q: expected the assertion to fail", c.doc)
			}
		}(i)
	}
	close(start)
	wg.Wait()
}

// Loading is the other half of the claim. Several goroutines building separate
// schemas at once share the built-in tables, which are behind a sync.Once, and
// each writes its own component maps — so this catches a built-in table being
// mutated by a schema that declares over it, as the XML namespace's attributes
// may be.
func TestConcurrentSchemaLoading(t *testing.T) {
	srcs := []string{
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		   <xs:element name="a" type="xs:string"/>
		 </xs:schema>`,
		// Declares the XML namespace's own attributes, which are
		// otherwise supplied as built-ins.
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		            targetNamespace="http://www.w3.org/XML/1998/namespace">
		   <xs:attribute name="lang" type="xs:string"/>
		   <xs:attribute name="base" type="xs:string"/>
		 </xs:schema>`,
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		   <xs:simpleType name="t">
		     <xs:restriction base="xs:int"><xs:maxInclusive value="9"/></xs:restriction>
		   </xs:simpleType>
		   <xs:element name="b" type="t"/>
		 </xs:schema>`,
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := srcs[i%len(srcs)]
			<-start
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := Load(tree.Root, "s.xsd", Options{}); err != nil {
				t.Errorf("concurrent load failed: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// A schema loaded afterwards must still see the built-in declarations
	// unchanged — the one that declared over them must not have written
	// into the shared table.
	s, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:string"/>
	</xs:schema>`)
	if err != nil {
		t.Fatalf("later load: %v", err)
	}
	d, ok := s.Attributes[xdm.QName{URI: NSXML, Local: "lang"}]
	if !ok {
		t.Fatal("the built-in xml:lang went missing")
	}
	if d.Type == nil || d.Type.TypeName().Local != "language" {
		t.Errorf("built-in xml:lang was overwritten: %v", d.Type)
	}
}

// A 1.1 construct is parsed under either version but only *honoured* under
// 1.1, so loading a 1.1 schema with the default options gives a working 1.0
// validator for it that silently misses the 1.1 constraints. That is a sharp
// edge worth pinning: it is documented in docs/xsd.md, and a change that made
// either half of it false would need to change the documentation too.
func TestVersionSelectsWhetherAssertionsAreHonoured(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r">
	    <xs:complexType>
	      <xs:sequence><xs:element name="a" type="xs:int"/></xs:sequence>
	      <xs:assert test="xs:int(a) gt 100"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	const doc = `<r><a>1</a></r>` // violates the assertion

	for _, c := range []struct {
		version Version
		fails   bool
	}{
		{Version10, false}, // parsed, not run
		{Version11, true},  // run
	} {
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Load(tree.Root, "s.xsd", Options{Version: c.version})
		if err != nil {
			t.Fatalf("version %d: a schema using xs:assert must load: %v",
				c.version, err)
		}
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		err = s.Validate(d.Root, ValidateOptions{})
		if c.fails && err == nil {
			t.Errorf("version %d: the assertion was not honoured", c.version)
		}
		if !c.fails && err != nil {
			t.Errorf("version %d: the assertion was run anyway: %v",
				c.version, err)
		}
	}

	// notQName is the exception: an error under 1.0 rather than ignored,
	// because it narrows a wildcard — ignoring it would admit documents the
	// schema means to exclude.
	nq, err := xdm.ParseString(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence/>
	    <xs:anyAttribute notQName="##defined"/>
	  </xs:complexType>
	</xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(nq.Root, "s.xsd", Options{}); err == nil {
		t.Error("notQName under 1.0 was accepted; it narrows a wildcard " +
			"and must not be silently ignored")
	}
}

// <xs:all minOccurs="0"> as the whole content model says the group may be
// absent, so an element with no children owes it nothing. The all-or-nothing
// rule still applies once anything from the group appears.
//
// This was checked only for a *nested* optional all group, so the members of a
// top-level one were each demanded individually and an empty element failed.
func TestTopLevelOptionalAllGroup(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:all minOccurs="0">
	        <xs:element name="t1"/>
	        <xs:element name="t2"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	assertValid(t, schema, `<doc/>`)                 // the group is absent
	assertValid(t, schema, `<doc><t1/><t2/></doc>`)  // the group is present, in full
	// Present but incomplete: minOccurs="0" makes the group optional, not
	// each member independently so.
	assertInvalid(t, schema, `<doc><t1/></doc>`, "cvc-complex-type.2.4.b")
}

// A key sequence compares values, and values drawn from different primitives
// are never equal however their spellings compare. Comparing the lexical forms
// alone made the boolean 1 a duplicate of the decimal 1.
func TestKeyComparisonIsByPrimitive(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element ref="uid" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:unique name="u">
	      <xs:selector xpath=".//uid"/>
	      <xs:field xpath="."/>
	    </xs:unique>
	  </xs:element>
	  <xs:element name="uid" type="xs:anyType"/>
	</xs:schema>`
	const ns = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema"`

	// Different primitives, same lexical form: not a duplicate.
	assertValid(t, schema, `<root`+ns+`>`+
		`<uid xsi:type="xs:boolean">1</uid>`+
		`<uid xsi:type="xs:decimal">1</uid></root>`)
	assertValid(t, schema, `<root`+ns+`>`+
		`<uid xsi:type="xs:float">1</uid>`+
		`<uid xsi:type="xs:unsignedByte">1</uid></root>`)

	// The same primitive still compares by value, so two spellings of one
	// decimal are a duplicate — xs:int and xs:integer are both decimal.
	assertInvalid(t, schema, `<root`+ns+`>`+
		`<uid xsi:type="xs:int">1</uid>`+
		`<uid xsi:type="xs:integer">1</uid></root>`,
		"cvc-identity-constraint.4.1")
}

// A list takes its item type's primitive rather than one of its own, because a
// singleton list is equal to the atomic value it contains. Giving a list a
// primitive of its own separated a keyref typed as a list of xs:Name from a
// key typed xs:Name, which saxonData's id022 is built to catch.
func TestSingletonListMatchesAtomicKey(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence><xs:element ref="para" maxOccurs="unbounded"/></xs:sequence>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath="para"/><xs:field xpath="@key"/>
	    </xs:key>
	    <xs:keyref name="r" refer="k">
	      <xs:selector xpath="para"/><xs:field xpath="@ref"/>
	    </xs:keyref>
	  </xs:element>
	  <xs:element name="para">
	    <xs:complexType>
	      <xs:attribute name="key" type="xs:Name" use="required"/>
	      <xs:attribute name="ref" type="names"/>
	    </xs:complexType>
	  </xs:element>
	  <xs:simpleType name="names"><xs:list itemType="xs:Name"/></xs:simpleType>
	</xs:schema>`
	assertValid(t, schema,
		`<doc><para key="alpha"/><para key="beta" ref="alpha"/></doc>`)
}

// xpathDefaultNamespace="##defaultNamespace" means the default namespace in
// scope where the *expression* is written, which is not always where the
// attribute is: the attribute is commonly on <xs:schema> and the xmlns= on the
// element carrying the test. saxonData's cta0005 is that shape.
//
// Resolving at the attribute found no default there, so every unprefixed name
// in the test went to the absent namespace and matched nothing — the
// alternative never fired and the more permissive declared type was used.
func TestXPathDefaultNamespaceResolvesAtTheExpression(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	  targetNamespace="urn:c" xmlns:c="urn:c"
	  elementFormDefault="qualified"
	  xpathDefaultNamespace="##defaultNamespace">
	  <xs:complexType name="t">
	    <xs:sequence><xs:element name="e" minOccurs="0" type="xs:decimal"/></xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="treq">
	    <xs:complexContent>
	      <xs:restriction base="c:t">
	        <xs:sequence><xs:element name="e" minOccurs="1" type="xs:decimal"/></xs:sequence>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="message" type="c:t">
	    <xs:alternative test="self::message" type="c:treq" xmlns="urn:c"/>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<message xmlns="urn:c"><e>1</e></message>`); err != nil {
		t.Errorf("the alternative's type rejected a document it permits: %v", err)
	}
	// The alternative selects treq, which requires e; an empty element
	// fails only if the alternative actually fired.
	if err := check11(t, s, `<message xmlns="urn:c"/>`); err == nil {
		t.Error("the alternative did not fire; the unprefixed name in " +
			"its test resolved to the wrong namespace")
	}
}

// processContents="strict" asks for the element to be assessed, not for it to
// have a declaration. §3.3.4 clause 1.2 assesses an element against the type
// xsi:type names, so one a strict wildcard matches is valid when it says which
// type it means — msData's test75092 declares none of its children and puts
// xsi:type on each.
func TestStrictWildcardAcceptsXSIType(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="foo">
	    <xs:sequence>
	      <xs:element name="a"/>
	      <xs:any namespace="##any" processContents="strict"
	              minOccurs="0" maxOccurs="unbounded"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:element name="foo" type="foo"/>
	</xs:schema>`
	const ns = ` xmlns:xs="http://www.w3.org/2001/XMLSchema"` +
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`

	assertValid(t, schema, `<foo`+ns+`><a/>`+
		`<b xsi:type="xs:string">abc</b>`+
		`<c xsi:type="xs:int">123</c></foo>`)

	// The type still has to hold: xsi:type does not waive validation.
	assertInvalid(t, schema, `<foo`+ns+`><a/>`+
		`<c xsi:type="xs:int">notanint</c></foo>`,
		"cvc-datatype-valid.1")

	// With neither a declaration nor an xsi:type there is nothing to
	// assess against, which is what strict refuses.
	assertInvalid(t, schema, `<foo`+ns+`><a/><b/></foo>`,
		"cvc-complex-type.2.4.c")
}

// A repeated group whose branches carry their own bounds: <choice
// maxOccurs="unbounded"> over elements with minOccurs="3" maxOccurs="5". The
// sixth foo is not a sixth repetition of the inner counter but the first of a
// second choice, and treating the inner bound as a total refused it.
func TestInnerBoundIsNotATotal(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:choice minOccurs="0" maxOccurs="unbounded">
	        <xs:element name="foo" minOccurs="3" maxOccurs="5"/>
	        <xs:element name="sg" minOccurs="3" maxOccurs="5"/>
	      </xs:choice>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	for _, n := range []int{3, 4, 5, 6, 8, 10, 13} {
		doc := "<root>" + strings.Repeat("<foo/>", n) + "</root>"
		if err := validateString(t, schema, doc); err != nil {
			t.Errorf("%d foo rejected: %v", n, err)
		}
	}
	// Below the branch minimum there is no round to make, so it still fails.
	for _, n := range []int{1, 2} {
		doc := "<root>" + strings.Repeat("<foo/>", n) + "</root>"
		if err := validateString(t, schema, doc); err == nil {
			t.Errorf("%d foo accepted; the branch requires 3", n)
		}
	}
}

// The integer branch narrows the lexical space, not only the value space:
// xs:integer has no decimal point at all. fractionDigits="0" alone counts the
// digits after the point and finds none in "+0.0", so the facet passes a
// literal the lexical space excludes (integer006).
func TestIntegerHasNoDecimalPoint(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:integer"/>
	</xs:schema>`
	assertValid(t, schema, `<e>0</e>`)
	assertValid(t, schema, `<e>+42</e>`)
	assertValid(t, schema, `<e>-7</e>`)
	for _, bad := range []string{`+0.0`, `1.0`, `0.`, `1e3`} {
		if err := validateString(t, schema, `<e>`+bad+`</e>`); err == nil {
			t.Errorf("%q accepted as xs:integer", bad)
		}
	}
	// A type derived from xs:integer inherits the narrower lexical space.
	const derived = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="t">
	    <xs:restriction base="xs:int"><xs:maxInclusive value="99"/></xs:restriction>
	  </xs:simpleType>
	  <xs:element name="e" type="t"/>
	</xs:schema>`
	if err := validateString(t, derived, `<e>1.0</e>`); err == nil {
		t.Error("a restriction of xs:int accepted a decimal point")
	}
}

// Only the seconds field of a duration may carry a fraction: every other field
// is unsigned integer digits, because a year is not a fixed number of months
// and the fraction would have nowhere to go (duration011).
func TestOnlyDurationSecondsAreFractional(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:duration"/>
	</xs:schema>`
	assertValid(t, schema, `<e>PT0.5S</e>`)
	assertValid(t, schema, `<e>P1Y2M3DT4H5M6.75S</e>`)
	for _, bad := range []string{`P200.5Y`, `P1.5M`, `P1.5D`, `PT1.5H`,
		`PT1.5M`, `PT.5S`, `PT1.2.3S`} {
		if err := validateString(t, schema, `<e>`+bad+`</e>`); err == nil {
			t.Errorf("%q accepted as xs:duration", bad)
		}
	}
}

// XSD 1.1 added the leading plus to the lexical space of xs:float and
// xs:double; 1.0 admits only "INF". Accepting it under 1.0 was defended as
// harmless — one extra spelling of a value 1.0 already has — but the suite
// checks the lexical space rather than the value, and float018 and double018
// both expect it refused there.
//
// The version is threaded as a parameter rather than stored on the type,
// because the built-in types are a process-wide singleton: two schemas of
// different versions share the same *SimpleType, so a version stored there
// would be whichever schema loaded last.
func TestPlusINFIsVersionDependent(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:double"/>
	</xs:schema>`
	assertValid(t, schema, `<e>INF</e>`)
	assertValid(t, schema, `<e>-INF</e>`)
	assertValid(t, schema, `<e>NaN</e>`)
	if err := validateString(t, schema, `<e>+INF</e>`); err == nil {
		t.Error("+INF accepted under XSD 1.0")
	}
	if err := check11(t, load11(t, schema), `<e>+INF</e>`); err != nil {
		t.Errorf("+INF refused under XSD 1.1: %v", err)
	}
}

// Loading two schemas at once must not mutate anything they share.
//
// Schema.Types is seeded from a process-wide singleton, so the *ComplexType
// for xs:anyType is reachable from every schema ever loaded. The final
// attribute-resolution sweep in parser.finish *writes* to each type it visits,
// so before the built-ins were excluded two concurrent Load calls wrote to that
// one shared value. The attrsDone guard did not help: it is per-parser, so each
// Load believed it was the first to touch the type.
//
// The race is invisible without -race and invisible without concurrency, which
// is why it survived the plain test run.
func TestConcurrentLoadDoesNotMutateBuiltins(t *testing.T) {
	// A type with a prohibited use, since dropping those is what the sweep
	// writes, and one deriving from a built-in so the walk reaches it.
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:attribute name="a" type="xs:string" use="prohibited"/>
	  </xs:complexType>
	  <xs:element name="e" type="t"/>
	</xs:schema>`

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := Load(tree.Root, "s.xsd", Options{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
