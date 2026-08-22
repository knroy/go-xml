package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileSrc compiles a schema written as a string.
func compileSrc(t *testing.T, src string) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return Compile(tree.Root)
}

// mustReject asserts that a schema is refused, and that the message names the
// section it broke — an error that says only "invalid schema" leaves the
// author to guess which of section 7's rules they tripped.
func mustReject(t *testing.T, name, section, src string) {
	t.Helper()
	_, err := compileSrc(t, src)
	if err == nil {
		t.Errorf("%s: schema was accepted; it breaks section %s", name, section)
		return
	}
	if !strings.Contains(err.Error(), section) {
		t.Errorf("%s: error %q does not name section %s", name, err, section)
	}
}

func mustAccept(t *testing.T, name, src string) {
	t.Helper()
	if _, err := compileSrc(t, src); err != nil {
		t.Errorf("%s: schema was rejected: %v", name, err)
	}
}

const rngNS = ` xmlns="http://relaxng.org/ns/structure/1.0"`

// Section 7.1's prohibited paths, one case each.
func TestProhibitedPaths(t *testing.T) {
	cases := []struct{ name, section, src string }{
		{"attribute in attribute", "7.1.1",
			`<element` + rngNS + ` name="foo"><attribute name="a">
				<attribute name="b"/></attribute></element>`},

		{"attribute in oneOrMore in group", "7.1.2",
			`<element` + rngNS + ` name="foo"><oneOrMore><group>
				<attribute name="a"/><attribute name="b"/>
			</group></oneOrMore></element>`},

		{"list in list", "7.1.3",
			`<element` + rngNS + ` name="foo"><list><list>
				<data type="token"/></list></list></element>`},

		{"text in list", "7.1.3",
			`<element` + rngNS + ` name="foo"><list><text/></list></element>`},

		{"interleave in list", "7.1.3",
			`<element` + rngNS + ` name="foo"><list><interleave>
				<value>x</value><value>y</value></interleave></list></element>`},

		{"text in data except", "7.1.4",
			`<element` + rngNS + ` name="foo"><data type="string"><except>
				<text/></except></data></element>`},

		{"text in start", "7.1.5",
			`<grammar` + rngNS + `><start><text/></start></grammar>`},

		{"attribute in start", "7.1.5",
			`<grammar` + rngNS + `><start><attribute name="a"/></start></grammar>`},

		{"group in start", "7.1.5",
			`<grammar` + rngNS + `><start><group>
				<element name="a"><empty/></element>
				<element name="b"><empty/></element>
			</group></start></grammar>`},
	}
	for _, c := range cases {
		mustReject(t, c.name, c.section, c.src)
	}
}

// The paths apply to the *simplified* grammar, which is what makes these
// legal. Each would match a prohibited path read literally.
func TestSimplificationBeforeRestrictions(t *testing.T) {
	cases := []struct{ name, src string }{
		// notAllowed propagates outward (section 4.20), so the group is gone
		// and the attribute it appears to hold is not there to be checked.
		{"notAllowed erases the group",
			`<element` + rngNS + ` name="foo"><attribute name="a">
				<group><notAllowed/><attribute name="b"/></group>
			</attribute></element>`},

		// A group with one pattern child is that child (section 4.12), so
		// this is oneOrMore//attribute, which is allowed.
		{"single-child group is not a group",
			`<element` + rngNS + ` name="foo"><oneOrMore><group>
				<attribute><anyName/></attribute>
			</group></oneOrMore></element>`},

		// group(empty, p) is p, so this collapses the same way.
		{"empty does not make a group of two",
			`<element` + rngNS + ` name="foo"><oneOrMore><group>
				<attribute><anyName/></attribute><empty/>
			</group></oneOrMore></element>`},

		// A oneOrMore over a lone attribute is legal: a document cannot carry
		// the same attribute twice, so the repetition matches at most one.
		{"oneOrMore over one attribute",
			`<element` + rngNS + ` name="foo"><oneOrMore>
				<attribute name="bar"/></oneOrMore></element>`},

		// A nested grammar is inlined where it stands, so its start is
		// ordinary content and section 7.1.5 does not reach it.
		{"nested grammar start is not the schema start",
			`<element` + rngNS + ` name="foo"><grammar><start><text/></start>
			</grammar></element>`},
	}
	for _, c := range cases {
		mustAccept(t, c.name, c.src)
	}
}

// A ref is expanded in place, so a definition is legal or not according to
// where it is referenced from.
func TestRefExpandsIntoContext(t *testing.T) {
	// Legal: the definition holds only data, which a list admits.
	mustAccept(t, "ref to data inside list",
		`<grammar`+rngNS+`><start><element name="foo">
			<list><ref name="d"/></list></element></start>
		<define name="d"><data type="string"/></define></grammar>`)

	// Illegal: the same shape, but the definition holds text.
	mustReject(t, "ref to text inside list", "7.1.3",
		`<grammar`+rngNS+`><start><element name="foo">
			<list><ref name="t"/></list></element></start>
		<define name="t"><text/></define></grammar>`)
}

// Sections 7.3 and 7.4: two patterns that could match the same thing.
func TestCompetitionRules(t *testing.T) {
	cases := []struct{ name, section, src string }{
		{"attribute required twice", "7.3",
			`<element` + rngNS + ` name="foo">
				<attribute name="bar"/><attribute name="bar"/></element>`},

		{"attribute twice through interleave", "7.3",
			`<element` + rngNS + ` name="foo"><interleave>
				<attribute name="bar"/><attribute name="bar"/></interleave></element>`},

		{"anyName competes with a named attribute", "7.3",
			`<element` + rngNS + ` name="foo"><attribute name="bar"/>
				<oneOrMore><attribute><anyName/></attribute></oneOrMore></element>`},

		{"interleave branches share an element", "7.4",
			`<element` + rngNS + ` name="foo"><interleave>
				<element name="bar"><empty/></element>
				<element name="bar"><empty/></element></interleave></element>`},

		{"interleave branches both take text", "7.4",
			`<element` + rngNS + ` name="foo"><interleave>
				<text/><text/></interleave></element>`},
	}
	for _, c := range cases {
		mustReject(t, c.name, c.section, c.src)
	}

	// Alternatives do not compete: only one branch runs.
	mustAccept(t, "the same attribute in two choice branches",
		`<element`+rngNS+` name="foo"><choice>
			<attribute name="bar"/><attribute name="bar"/></choice></element>`)

	// Two repetitions over disjoint namespaces do not overlap.
	mustAccept(t, "disjoint nsName repetitions",
		`<element`+rngNS+` name="foo">
			<oneOrMore><attribute><nsName ns="http://a"/></attribute></oneOrMore>
			<oneOrMore><attribute><nsName ns="http://b"/></attribute></oneOrMore>
		</element>`)
}

// Section 4.10: a schema may not declare a namespace declaration as an
// attribute, at either spelling.
func TestNamespaceDeclarationIsNotAnAttribute(t *testing.T) {
	for _, src := range []string{
		`<element` + rngNS + ` name="foo">
			<attribute name="xmlns"><text/></attribute></element>`,
		`<element` + rngNS + ` name="foo">
			<attribute><name>xmlns</name><text/></attribute></element>`,
		`<element` + rngNS + ` name="foo">
			<attribute ns="http://www.w3.org/2000/xmlns" name="bar">
				<text/></attribute></element>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("a namespace declaration was accepted as an attribute:\n%s", src)
		}
	}

	// An attribute genuinely named xmlns-something is not a declaration.
	mustAccept(t, "xmlnsfoo is an ordinary attribute",
		`<element`+rngNS+` name="foo">
			<attribute name="xmlnsfoo"><text/></attribute></element>`)
}

// Section 7.2: a pattern that matches a child and a pattern that matches a
// single string may not be sequenced — only offered as alternatives.
//
// The rule exists because there is no way to say where the string ends and
// the child begins: <data type="int"/> followed by <element name="bar"/> asks
// the validator to split the element's content at a boundary the document
// does not mark.
func TestStringSequences(t *testing.T) {
	cases := []struct{ name, src string }{
		{"data then data",
			`<element` + rngNS + ` name="foo"><group>
				<data type="string"/><data type="string"/></group></element>`},

		{"data then element",
			`<element` + rngNS + ` name="foo"><group>
				<data type="string"/>
				<element name="bar"><empty/></element></group></element>`},

		{"data then text",
			`<element` + rngNS + ` name="foo"><group>
				<data type="string"/><text/></group></element>`},

		{"element inside a list",
			`<element` + rngNS + ` name="foo"><list>
				<element name="bar"><empty/></element></list></element>`},
	}
	for _, c := range cases {
		if _, err := compileSrc(t, c.src); err == nil {
			t.Errorf("%s: schema was accepted; it breaks section 7.2", c.name)
		}
	}

	// Alternatives are exactly what the rule permits.
	mustAccept(t, "data or element as alternatives",
		`<element`+rngNS+` name="foo"><choice>
			<data type="string"/>
			<element name="bar"><empty/></element></choice></element>`)

	// Inside a list the rule does not apply: a list is split on whitespace,
	// so a sequence of strings has a boundary after all.
	mustAccept(t, "two data patterns inside a list",
		`<element`+rngNS+` name="foo"><list>
			<data type="string"/><data type="string"/></list></element>`)
}

// Section 4.17: a name may be defined more than once, which is how a schema
// extends one it did not write.
func TestCombine(t *testing.T) {
	// Two plain definitions are a mistake: one would be silently lost.
	mustReject(t, "two plain definitions", "4.17",
		`<grammar`+rngNS+`><start><ref name="x"/></start>
			<define name="x"><element name="a"><empty/></element></define>
			<define name="x"><element name="b"><empty/></element></define>
		</grammar>`)

	mustReject(t, "two plain starts", "4.17",
		`<grammar`+rngNS+`>
			<start><element name="a"><empty/></element></start>
			<start><element name="b"><empty/></element></start>
		</grammar>`)

	// Definitions must agree on how they combine.
	mustReject(t, "choice and interleave disagree", "4.17",
		`<grammar`+rngNS+`><start><ref name="x"/></start>
			<define name="x"><element name="a"><empty/></element></define>
			<define name="x" combine="choice">
				<element name="b"><empty/></element></define>
			<define name="x" combine="interleave">
				<element name="c"><empty/></element></define>
		</grammar>`)

	// Exactly one definition may omit combine=: it is the base being
	// extended, and the others say how they extend it.
	mustAccept(t, "one plain definition among combining ones",
		`<grammar`+rngNS+`><start><ref name="x"/></start>
			<define name="x" combine="choice">
				<element name="a"><empty/></element></define>
			<define name="x" combine="choice">
				<element name="b"><empty/></element></define>
			<define name="x"><element name="c"><empty/></element></define>
		</grammar>`)
}

// Combining must actually join the definitions, not merely permit them: a
// schema that declares three alternatives and validates only the first is
// worse than one that refuses to compile.
func TestCombineJoinsDefinitions(t *testing.T) {
	const src = `<grammar` + rngNS + `>
		<start><element name="root"><ref name="x"/></element></start>
		<define name="x"><element name="a"><empty/></element></define>
		<define name="x" combine="choice">
			<element name="b"><empty/></element></define>
	</grammar>`
	s, err := compileSrc(t, src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, doc := range []string{`<root><a/></root>`, `<root><b/></root>`} {
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(tree.Root); err != nil {
			t.Errorf("%s should be valid against the combined definition: %v",
				doc, err)
		}
	}
}

// Section 4.16: what a name class may exclude.
func TestNameClassExcept(t *testing.T) {
	// The commonest use of except must work: every name but a few.
	mustAccept(t, "anyName excepting several names",
		`<element`+rngNS+`><anyName><except>
			<name>a</name><name>b</name><name>c</name>
		</except></anyName><empty/></element>`)

	// Excepting everything admitted leaves nothing, which notAllowed says
	// plainly and this does not.
	for _, src := range []string{
		`<element` + rngNS + `><anyName><except><anyName/></except></anyName>
			<empty/></element>`,
		`<element` + rngNS + `><nsName ns=""><except><nsName ns=""/></except>
			</nsName><empty/></element>`,
		`<element` + rngNS + `><nsName ns=""><except><anyName/></except>
			</nsName><empty/></element>`,
		// At most one except per name class.
		`<element` + rngNS + `><anyName>
			<except><name>a</name></except>
			<except><name>b</name></except></anyName><empty/></element>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("schema was accepted; it breaks section 4.16:\n%s", src)
		}
	}
}

// xmlns is not an attribute in the data model RELAX NG validates, so it is
// not a name a schema may mention where attribute names go — including inside
// an <except>.
//
// Excluding it looks reasonable and is not: there is nothing to exclude, and
// writing it suggests the author believes an open attribute class would
// otherwise match a namespace declaration. It would not.
func TestXmlnsCannotBeNamedInAnAttributeClass(t *testing.T) {
	for _, src := range []string{
		`<element` + rngNS + ` name="foo"><oneOrMore><attribute>
			<anyName><except><name>xmlns</name></except></anyName>
			<text/></attribute></oneOrMore></element>`,
		`<element` + rngNS + ` name="foo"><oneOrMore><attribute>
			<nsName ns=""><except><name>xmlns</name></except></nsName>
			<text/></attribute></oneOrMore></element>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("xmlns was accepted as an attribute name:\n%s", src)
		}
	}

	// An element name class may except anything, xmlns included: element
	// names have no such restriction.
	mustAccept(t, "an element class excepting xmlns",
		`<element`+rngNS+`><anyName><except><name>xmlns</name></except>
			</anyName><empty/></element>`)
}

// Section 7.3: an attribute with an open name class must say how many it
// matches.
func TestOpenAttributeNameNeedsRepetition(t *testing.T) {
	mustReject(t, "anyName attribute alone", "7.3",
		`<element`+rngNS+` name="foo"><attribute><anyName/><text/>
			</attribute></element>`)

	mustAccept(t, "anyName attribute under oneOrMore",
		`<element`+rngNS+` name="foo"><oneOrMore><attribute>
			<anyName/><text/></attribute></oneOrMore></element>`)
}

// Section 4.19: a definition may reach itself only through an <element>.
//
// Each level of such a recursion consumes an element, so a document ends it.
// One that reaches itself without crossing an element boundary describes
// nothing finite — there is no input that would stop it.
func TestRecursionNeedsAnElement(t *testing.T) {
	// Legal, and the ordinary way to write arbitrarily deep nesting.
	s, err := compileSrc(t, `<grammar`+rngNS+`>
		<start><element name="foo"><ref name="bar"/></element></start>
		<define name="bar">
			<element name="bar"><optional><ref name="bar"/></optional></element>
		</define></grammar>`)
	if err != nil {
		t.Fatalf("recursion through an element should compile: %v", err)
	}
	for _, doc := range []string{
		`<foo><bar/></foo>`,
		`<foo><bar><bar/></bar></foo>`,
		`<foo><bar><bar><bar/></bar></bar></foo>`,
	} {
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(tree.Root); err != nil {
			t.Errorf("%s should be valid: %v", doc, err)
		}
	}

	// Illegal: the recursion crosses no element.
	mustReject(t, "recursion with no element between", "4.19",
		`<grammar`+rngNS+`>
			<start><element name="foo"><ref name="bar"/></element></start>
			<define name="bar">
				<optional><element name="bar"><empty/></element>
					<ref name="bar"/></optional>
			</define></grammar>`)
}

// A definition nothing refers to is still part of the schema, so a datatype
// it names must exist. Finding that out only when a document happens to reach
// it is finding out too late.
func TestUnreferencedDefinitionsAreChecked(t *testing.T) {
	for _, src := range []string{
		`<grammar` + rngNS + `>
			<start><element name="foo"><empty/></element></start>
			<define name="unused"><data type="nosuchtype"/></define></grammar>`,
		`<grammar` + rngNS + `>
			<start><element name="foo"><empty/></element></start>
			<define name="unused">
				<data type="token"><param name="minLength">2</param></data>
			</define></grammar>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("a broken unreferenced definition was accepted:\n%s", src)
		}
	}

	// But an unreferenced definition that merely refers to itself is legal:
	// nothing expands it, so nothing fails to terminate.
	mustAccept(t, "unreferenced self-reference",
		`<grammar`+rngNS+`>
			<start><element name="foo"><empty/></element></start>
			<define name="unused"><ref name="unused"/></define></grammar>`)
}

// The built-in library defines no parameters, so a schema giving one is
// asking for a check that cannot happen.
func TestBuiltinTypesTakeNoParams(t *testing.T) {
	for _, typ := range []string{"string", "token"} {
		src := `<element` + rngNS + ` name="foo">
			<data type="` + typ + `"><param name="minLength">2</param></data>
		</element>`
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("a param on the built-in %q was accepted", typ)
		}
	}
}

// An <element> may offer several names for itself, written as a choice of
// name classes. Reading that choice as a choice of patterns loses the name and
// then reports the element as having no content.
func TestNameClassChoice(t *testing.T) {
	s, err := compileSrc(t, `<element`+rngNS+`>
		<choice><name ns="">foo</name><name ns="">bar</name></choice>
		<empty/></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		doc  string
		want bool
	}{{`<foo/>`, true}, {`<bar/>`, true}, {`<baz/>`, false}} {
		tree, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(tree.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// A grammar holds definitions, not patterns. An <element> written directly
// inside one is not a start — it is nothing, and reading past it validates
// against a grammar with no content where the author thought they had
// written some.
func TestGrammarHoldsOnlyDefinitions(t *testing.T) {
	mustReject(t, "element directly in a grammar", "4.18",
		`<grammar`+rngNS+`>
			<element name="foo"><empty/></element>
			<start><element name="foo"><empty/></element></start></grammar>`)

	// A <div> outside a grammar groups patterns, so the rule does not reach
	// it.
	mustAccept(t, "div grouping patterns",
		`<element`+rngNS+` name="foo"><div><empty/></div></element>`)
}

// A reference that names nothing is an error wherever it stands, including in
// a definition nothing refers to.
func TestUnresolvedRefsAreFound(t *testing.T) {
	for _, src := range []string{
		`<grammar` + rngNS + `>
			<start><element name="foo"><empty/></element></start>
			<define name="unused"><ref name="nosuch"/></define></grammar>`,
		`<grammar` + rngNS + `>
			<start><element name="foo"><empty/></element></start>
			<define name="unused">
				<grammar><start><parentRef name="nosuch"/></start></grammar>
			</define></grammar>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("an unresolved reference was accepted:\n%s", src)
		}
	}
}

// A schema that is not a grammar is a pattern standing where a start would,
// so section 7.1.5 constrains it the same way.
func TestBarePatternSchemaIsAStart(t *testing.T) {
	mustReject(t, "a schema that is only text", "7.1.5",
		`<text`+rngNS+`/>`)
}

// An attribute's value is a string, so its pattern may match one — but an
// element has nowhere to be inside it. The same holds for a data's except.
func TestElementCannotMatchWhereAStringDoes(t *testing.T) {
	for _, src := range []string{
		`<element` + rngNS + ` name="foo"><attribute name="bar">
			<element name="baz"><empty/></element></attribute></element>`,
		`<element` + rngNS + ` name="foo"><attribute name="bar">
			<choice><element name="baz"><empty/></element><text/></choice>
		</attribute></element>`,
		`<element` + rngNS + ` name="foo"><data type="string"><except>
			<element name="bar"><empty/></element></except></data></element>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("an element was accepted where only a string matches:\n%s", src)
		}
	}

	// <text/> is the ordinary content of an attribute and must stay legal.
	mustAccept(t, "text inside an attribute",
		`<element`+rngNS+` name="foo">
			<attribute name="bar"><text/></attribute></element>`)
}

// An empty element and one containing the empty string are the same document,
// so a pattern that matches strings must match <foo/>.
//
// This is easy to get wrong in the direction that rejects: the pattern at that
// point sits inside an After, which is never nullable however well its left
// half matched, so asking whether the derivative is nullable answers no even
// when the content is fine. What decides it is what the end tag makes of it.
func TestEmptyElementMatchesTheEmptyString(t *testing.T) {
	cases := []struct{ name, schema string }{
		{"data", `<element` + rngNS + ` name="foo"><data type="string"/></element>`},
		{"value", `<element` + rngNS + ` name="foo"><value type="string"/></element>`},
		{"empty value", `<element` + rngNS + ` name="foo"><value/></element>`},
		{"list of nothing",
			`<element` + rngNS + ` name="foo"><list><empty/></list></element>`},
	}
	for _, c := range cases {
		s, err := compileSrc(t, c.schema)
		if err != nil {
			t.Errorf("%s: compile: %v", c.name, err)
			continue
		}
		doc, err := xdm.ParseString(`<foo/>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); err != nil {
			t.Errorf("%s: <foo/> should match the empty string: %v", c.name, err)
		}
	}

	// A pattern that wanted an element is still not satisfied by emptiness.
	s, err := compileSrc(t, `<element`+rngNS+` name="foo">
		<element name="bar"><empty/></element></element>`)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString(`<foo/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err == nil {
		t.Error("<foo/> should not satisfy a schema requiring a child element")
	}
}

// A QName value is compared by what its prefix means, not by how it is
// spelled. The schema's prefixes and the document's are separate sets, and
// comparing the lexical forms gives the wrong answer in both directions.
func TestQNameValuesCompareByNamespace(t *testing.T) {
	const xsdLib = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	const schema = `<element` + rngNS + ` xmlns:s="http://example.com/ns"
		name="foo"><value type="QName"` + xsdLib + `>s:x</value></element>`
	s, err := compileSrc(t, schema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		name, doc string
		want      bool
	}{
		{"a different prefix for the same namespace",
			`<foo xmlns:d="http://example.com/ns">d:x</foo>`, true},
		{"the same prefix spelling, a different namespace",
			`<foo xmlns:s="http://example.com/other">s:x</foo>`, false},
		{"an unbound prefix names nothing",
			`<foo>s:x</foo>`, false},
		{"a different local name",
			`<foo xmlns:d="http://example.com/ns">d:y</foo>`, false},
	}
	for _, c := range cases {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.name, got, c.want)
		}
	}
}

// An element's character data is one string, however the parser split it.
// Deriving over each piece separately consumes the pattern on the first and
// fails on the second, so a document differing only in where its comments sit
// would validate differently.
func TestCharacterDataIsOneString(t *testing.T) {
	s, err := compileSrc(t, `<element`+rngNS+` name="foo">
		<data type="string"/></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, doc := range []string{
		`<foo>abc</foo>`,
		`<foo>a<!--c-->bc</foo>`,
		`<foo>a<?pi?>b<!--x-->c</foo>`,
	} {
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(tree.Root); err != nil {
			t.Errorf("%s should be valid: %v", doc, err)
		}
	}
}

// An attribute in the RELAX NG namespace is not foreign. The language puts
// its own attributes in no namespace, so r:a= is a misspelling of RELAX NG
// rather than an annotation from somewhere else, and passing it over as a
// foreign attribute hides the mistake.
func TestRelaxNgNamespacedAttributeIsNotForeign(t *testing.T) {
	const src = `<r:element xmlns:r="http://relaxng.org/ns/structure/1.0"
		name="foo" r:a="val"><r:empty/></r:element>`
	if _, err := compileSrc(t, src); err == nil {
		t.Error("an attribute in the RELAX NG namespace was ignored as foreign")
	}

	// One from an actual foreign namespace is an annotation and is ignored.
	mustAccept(t, "a foreign annotation",
		`<element`+rngNS+` xmlns:a="http://example.com/anno" name="foo"
			a:note="hello"><empty/></element>`)
}

// An unbound prefix names nothing. Dropping it and keeping the local name
// would silently match elements in no namespace, which is not what foo:bar
// was written to mean.
func TestUnboundPrefixIsRefused(t *testing.T) {
	if _, err := compileSrc(t, `<element`+rngNS+` name="foo:bar">
		<empty/></element>`); err == nil {
		t.Error("a name with an undeclared prefix was accepted")
	}
}

// A grammar with no start describes nothing, wherever it is written —
// including in a definition nothing refers to, which nothing would otherwise
// compile.
func TestNestedGrammarNeedsAStart(t *testing.T) {
	if _, err := compileSrc(t, `<grammar`+rngNS+`>
		<start><element name="foo"><empty/></element></start>
		<define name="unused">
			<grammar>
				<define name="foo"><element name="foo"><empty/></element></define>
			</grammar>
		</define></grammar>`); err == nil {
		t.Error("a nested grammar with no start was accepted")
	}
}

// The ordering facets compare numerically, not lexically. "0.90" and "0.9"
// are the same value and both below 1, which string ordering gets wrong in a
// way that would accept and reject arbitrary values.
func TestOrderingFacets(t *testing.T) {
	const lib = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	s, err := compileSrc(t, `<element`+rngNS+lib+` name="foo">
		<data type="double">
			<param name="minInclusive">0</param>
			<param name="maxInclusive">1</param>
		</data></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		text string
		want bool
	}{
		{"0.5", true},
		{"0", true},
		{"1", true},
		{"0.90", true},
		{"1.1", false},
		{"-0.9", false},
	} {
		doc, err := xdm.ParseString("<foo>"+c.text+"</foo>", xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("<foo>%s</foo>: valid=%v, want %v", c.text, got, c.want)
		}
	}
}

// A schema's names follow XML 1.0 fourth edition, which is the edition RELAX
// NG was specified against — not the fifth, which xdm implements.
//
// The fifth edition replaced the fourth's explicit character classes with
// broad ranges and made legal a great many names that were not, among them
// any name beginning with a combining mark. Both readings are right for their
// own purpose: an XML parser should accept what a conforming parser produces,
// and a schema language should accept what its own specification defines.
func TestSchemaNamesFollowFourthEdition(t *testing.T) {
	// U+0E35 THAI CHARACTER SARA II is a combining mark: legal within a name,
	// not at the start of one.
	for _, src := range []string{
		`<element` + rngNS + ` name="&#xE35;"><empty/></element>`,
		`<element` + rngNS + `><name>&#xE35;</name><empty/></element>`,
		`<element` + rngNS + ` name="foo">
			<attribute name="&#xE35;"/><empty/></element>`,
		`<element` + rngNS + ` name="x:&#xE35;"><empty/></element>`,
		`<grammar` + rngNS + `><start><ref name="&#xE35;"/></start>
			<define name="&#xE35;">
				<element name="foo"><empty/></element></define></grammar>`,
	} {
		if _, err := compileSrc(t, src); err == nil {
			t.Errorf("a name beginning with a combining mark was accepted:\n%s", src)
		}
	}

	// The same character after a letter is a legal name.
	mustAccept(t, "a combining mark after a letter",
		`<element`+rngNS+` name="foo">
			<element name="&#xE14;&#xE35;"><empty/></element></element>`)
}

// Validation is bounded by depth, and by its own limit rather than the
// parser's.
//
// Taking derivatives over a nested document costs time and memory *quadratic*
// in the depth: each level carries the pattern remaining at every level above
// it. Measured before this bound existed, depth 8000 cost 487ms and 911MB,
// and doubling the depth quadrupled both. The parser's MaxDepth capped it by
// accident; a caller who raises that to accept a deep document, or who builds
// a tree by transform rather than parsing, had nothing.
func TestValidationDepthIsBounded(t *testing.T) {
	s, err := compileSrc(t, `<grammar`+rngNS+`>
		<start><element name="r"><ref name="e"/></element></start>
		<define name="e">
			<element name="e"><optional><ref name="e"/></optional></element>
		</define></grammar>`)
	if err != nil {
		t.Fatal(err)
	}
	nested := func(n int) *xdm.Node {
		t.Helper()
		var sb strings.Builder
		sb.WriteString("<r>")
		for i := 0; i < n; i++ {
			sb.WriteString("<e>")
		}
		for i := 0; i < n; i++ {
			sb.WriteString("</e>")
		}
		sb.WriteString("</r>")
		tree, err := xdm.ParseString(sb.String(),
			xdm.ParseOptions{MaxDepth: n + 10, MaxBytes: -1})
		if err != nil {
			t.Fatal(err)
		}
		return tree.Root
	}

	// Within the default, a valid document validates.
	if err := s.Validate(nested(50)); err != nil {
		t.Errorf("a shallow document should validate: %v", err)
	}

	// Past it, the failure says so rather than reporting a validity error
	// that is really a limit.
	err = s.Validate(nested(DefaultMaxDepth + 100))
	if err == nil {
		t.Fatal("a document deeper than the limit was accepted")
	}
	if !strings.Contains(err.Error(), "nesting exceeds") {
		t.Errorf("error = %v, want one naming the depth limit", err)
	}

	// A caller who wants the depth can have it.
	if err := s.ValidateWithOptions(nested(DefaultMaxDepth+100),
		ValidateOptions{MaxDepth: DefaultMaxDepth + 500}); err != nil {
		t.Errorf("a raised limit should admit the document: %v", err)
	}
	if err := s.ValidateWithOptions(nested(DefaultMaxDepth+100),
		ValidateOptions{MaxDepth: -1}); err != nil {
		t.Errorf("an unbounded run should admit the document: %v", err)
	}
}
