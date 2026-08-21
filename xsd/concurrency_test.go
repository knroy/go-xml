package xsd

import (
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
