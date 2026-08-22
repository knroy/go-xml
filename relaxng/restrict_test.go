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
