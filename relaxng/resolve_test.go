package relaxng

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// mapResolver serves schemas from a fixed map, standing in for a caller that
// knows which documents exist. It records what it was asked for, so the
// base-URI contract can be asserted rather than assumed.
type mapResolver struct {
	docs map[string]string
	seen []string
}

func (r *mapResolver) ResolveSchema(href string) (*xdm.Node, error) {
	r.seen = append(r.seen, href)
	src, ok := r.docs[href]
	if !ok {
		return nil, fmt.Errorf("no such schema")
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return tree.Root, nil
}

// The default must stay closed. An href is an instruction to go and read
// something, and a schema does not get to decide that for the caller.
func TestExternalReferencesNeedAResolver(t *testing.T) {
	for _, src := range []string{
		`<externalRef` + rngNS + ` href="other.rng"/>`,
		`<grammar` + rngNS + `><include href="other.rng"/>
			<start><element name="foo"><empty/></element></start></grammar>`,
	} {
		_, err := compileSrc(t, src)
		if err == nil {
			t.Errorf("an external reference was followed with no Resolver:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "Resolver") {
			t.Errorf("error %q should say a Resolver is needed", err)
		}
	}
}

func compileWith(t *testing.T, src string, opts Options) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return CompileWithOptions(tree.Root, opts)
}

func TestExternalRef(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"other.rng": `<element` + rngNS + ` name="foo"><empty/></element>`,
	}}
	s, err := compileWith(t, `<externalRef`+rngNS+` href="other.rng"/>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<foo/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the referenced schema should accept <foo/>: %v", err)
	}
}

// xml:base composes outward, so a nested one resolves against the one above.
func TestExternalRefResolvesAgainstBase(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"http://example.com/sub/other.rng": `<element` + rngNS +
			` name="foo"><empty/></element>`,
	}}
	const src = `<div` + rngNS + ` xml:base="sub/">
		<externalRef href="other.rng"/></div>`
	if _, err := compileWith(t, src, Options{
		Resolver: r, BaseURI: "http://example.com/schema.rng",
	}); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(r.seen) != 1 || r.seen[0] != "http://example.com/sub/other.rng" {
		t.Errorf("resolver saw %v, want the base-resolved href", r.seen)
	}
}

// An include merges the named grammar's definitions, and a definition written
// inside the include replaces rather than combines with the one it names.
// That override is the whole point: a schema adopts another and changes the
// parts it needs.
func TestIncludeOverrides(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"base.rng": `<grammar` + rngNS + `>
			<start><element name="root"><ref name="body"/></element></start>
			<define name="body"><element name="original"><empty/></element></define>
		</grammar>`,
	}}
	const src = `<grammar` + rngNS + `>
		<include href="base.rng">
			<define name="body"><element name="replaced"><empty/></element></define>
		</include>
	</grammar>`
	s, err := compileWith(t, src, Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		doc  string
		want bool
	}{
		{`<root><replaced/></root>`, true},
		{`<root><original/></root>`, false},
	}
	for _, c := range cases {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := s.Validate(doc.Root) == nil
		if got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// Without an override the included definitions are used as written.
func TestIncludeWithoutOverride(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"base.rng": `<grammar` + rngNS + `>
			<start><element name="root"><ref name="body"/></element></start>
			<define name="body"><element name="original"><empty/></element></define>
		</grammar>`,
	}}
	s, err := compileWith(t, `<grammar`+rngNS+`><include href="base.rng"/></grammar>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<root><original/></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the included definition should apply: %v", err)
	}
}

// A cycle of includes must stop rather than recurse forever. With a resolver
// reading from the network this would be a request loop, not merely a hang.
func TestIncludeCycleTerminates(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"a.rng": `<grammar` + rngNS + `><include href="b.rng"/></grammar>`,
		"b.rng": `<grammar` + rngNS + `><include href="a.rng"/></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="a.rng"/></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a cycle of includes should be refused")
	}
	// It is reported as the cycle it is, not as a depth overrun: see
	// include_cycle_test.go for the two failures held apart.
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one naming the cycle", err)
	}
}

// An href names a whole document, so a fragment is refused rather than
// silently ignored — ignoring it would include more than was asked for.
func TestHrefFragmentRefused(t *testing.T) {
	r := &mapResolver{docs: map[string]string{}}
	_, err := compileWith(t, `<externalRef`+rngNS+` href="other.rng#part"/>`,
		Options{Resolver: r})
	if err == nil || !strings.Contains(err.Error(), "fragment") {
		t.Errorf("error = %v, want one naming the fragment", err)
	}
	if len(r.seen) != 0 {
		t.Errorf("the resolver was consulted with %v; it should not have been", r.seen)
	}
}

// parentRef reaches from a nested grammar into the one enclosing it, which is
// the only door between the two scopes.
func TestParentRef(t *testing.T) {
	const src = `<grammar` + rngNS + `>
		<start><element name="root"><ref name="inner"/></element></start>
		<define name="inner">
			<grammar><start><parentRef name="leaf"/></start></grammar>
		</define>
		<define name="leaf"><element name="leaf"><empty/></element></define>
	</grammar>`
	s, err := compileSrc(t, src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<root><leaf/></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("parentRef should reach the outer definition: %v", err)
	}
}

// A parentRef with nothing to reach out to is an error, not an empty pattern.
func TestParentRefNeedsAnEnclosingGrammar(t *testing.T) {
	_, err := compileSrc(t, `<grammar`+rngNS+`>
		<start><parentRef name="x"/></start></grammar>`)
	if err == nil {
		t.Fatal("a parentRef at the outermost grammar should be refused")
	}
}

// Section 4.10: an attribute's name takes its namespace differently
// depending on which of the two spellings the schema uses.
//
// name= does not inherit ns= from an ancestor — XML's own rule is that an
// unprefixed attribute is in no namespace, however the surrounding document
// declares defaults. A <name> child is an ordinary name class and does
// inherit. Getting either wrong is invisible until a namespaced schema is
// used, at which point whole classes of attribute become unmatchable.
func TestAttributeNamespaceRules(t *testing.T) {
	const eg = "http://www.example.com"
	cases := []struct {
		name, schema, doc string
		want              bool
	}{
		{"name= does not inherit ns=",
			`<element` + rngNS + ` ns="` + eg + `" name="foo">
				<attribute name="bar"/></element>`,
			`<eg:foo xmlns:eg="` + eg + `" bar="x"/>`, true},

		{"name= with an inherited ns does not match a prefixed attribute",
			`<element` + rngNS + ` ns="` + eg + `" name="foo">
				<attribute name="bar"/></element>`,
			`<eg:foo xmlns:eg="` + eg + `" eg:bar="x"/>`, false},

		{"ns= on the attribute itself does apply",
			`<element` + rngNS + ` name="foo">
				<attribute ns="` + eg + `" name="bar"/></element>`,
			`<foo xmlns:eg="` + eg + `" eg:bar="x"/>`, true},

		{"a <name> child inherits ns=",
			`<element` + rngNS + ` ns="` + eg + `" name="foo">
				<attribute><name>bar</name></attribute></element>`,
			`<eg:foo xmlns:eg="` + eg + `" eg:bar="x"/>`, true},

		{"a <name> child inheriting ns does not match an unprefixed attribute",
			`<element` + rngNS + ` ns="` + eg + `" name="foo">
				<attribute><name>bar</name></attribute></element>`,
			`<eg:foo xmlns:eg="` + eg + `" bar="x"/>`, false},
	}
	for _, c := range cases {
		s, err := compileSrc(t, c.schema)
		if err != nil {
			t.Errorf("%s: compile: %v", c.name, err)
			continue
		}
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.name, got, c.want)
		}
	}
}

// Several patterns in one <except> mean their choice. Grouping them instead
// excludes only the concatenation, which excludes nothing at all — a data
// excepting x, y and z would still admit y.
func TestDataExceptIsAChoice(t *testing.T) {
	s, err := compileSrc(t, `<element`+rngNS+` name="foo">
		<data type="token"><except>
			<value>x</value><value>y</value><value>z</value>
		</except></data></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		text string
		want bool
	}{{"y", false}, {"x", false}, {"z", false}, {"other", true}} {
		doc, err := xdm.ParseString("<foo>"+c.text+"</foo>", xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("<foo>%s</foo>: valid=%v, want %v", c.text, got, c.want)
		}
	}
}

// The ns= in force where a reference is written reaches into the schema it
// names, when that schema sets none of its own. This is how one schema is
// written once and used in several namespaces.
func TestReferencedSchemaInheritsNs(t *testing.T) {
	const eg = "http://www.example.com"
	r := &mapResolver{docs: map[string]string{
		"x": `<element` + rngNS + ` name="foo"><empty/></element>`,
	}}
	s, err := compileWith(t, `<externalRef`+rngNS+` href="x" ns="`+eg+`"/>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		doc  string
		want bool
	}{
		{`<foo xmlns="` + eg + `"/>`, true},
		{`<foo/>`, false},
	}
	for _, c := range cases {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// The xml prefix is bound whether or not a schema declares it, because XML
// Namespaces binds it by fiat.
func TestXmlPrefixIsAlwaysBound(t *testing.T) {
	s, err := compileSrc(t, `<element`+rngNS+` name="foo">
		<attribute name="xml:lang"/></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		doc  string
		want bool
	}{
		{`<foo xml:lang="en"/>`, true},
		{`<foo lang="en"/>`, false},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// An override must have something to override. A <define> inside an <include>
// naming nothing in the included grammar is a mistake — usually a typo — and
// treating it as an addition silently leaves the definition the author meant
// to replace in force.
func TestIncludeOverrideMustExist(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"x": `<grammar` + rngNS + `>
			<define name="foo"><element name="foo"><empty/></element></define>
		</grammar>`,
	}}
	// Overriding <start>, which the included grammar does not define.
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="x"><start><ref name="foo"/></start></include></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Error("overriding a start the included grammar does not define was accepted")
	}

	// Overriding a name it does not define.
	_, err = compileWith(t, `<grammar`+rngNS+`>
		<include href="x">
			<define name="nosuch"><element name="a"><empty/></element></define>
		</include>
		<start><ref name="foo"/></start></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Error("overriding an undefined name was accepted")
	}
}

// ns= on an <include> reaches the definitions it brings in, the same way it
// reaches an <externalRef>'s schema.
//
// The definitions are collected while that ns is in force and compiled later,
// when it is not, so the namespace has to be recorded per definition rather
// than held in a field that has moved on by then.
func TestIncludeInheritsNs(t *testing.T) {
	const eg = "http://www.example.com"
	r := &mapResolver{docs: map[string]string{
		"x": `<grammar` + rngNS + `>
			<start><element name="foo"><empty/></element></start></grammar>`,
	}}
	s, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="x" ns="`+eg+`"/></grammar>`, Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		doc  string
		want bool
	}{
		{`<foo xmlns="` + eg + `"/>`, true},
		{`<foo/>`, false},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(doc.Root) == nil; got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// A nested <grammar> is a scope of its own, so a <ref> inside it names that
// grammar's definitions. Checking one against the outer definitions finds a
// different pattern under the same name.
func TestNestedGrammarIsItsOwnScope(t *testing.T) {
	mustAccept(t, "same name defined in both grammars",
		`<grammar`+rngNS+`>
			<start><ref name="foo"/></start>
			<define name="foo">
				<grammar>
					<start><ref name="foo"/></start>
					<define name="foo">
						<element name="foo"><empty/></element></define>
				</grammar>
			</define></grammar>`)
}

// A <value> with no type= is the built-in token, whatever datatypeLibrary is
// in force: the library names where a *named* type comes from, and a plain
// <value> asks it for nothing.
func TestValueWithoutTypeIgnoresTheLibrary(t *testing.T) {
	mustAccept(t, "value under an unknown library",
		`<element`+rngNS+` name="foo">
			<value datatypeLibrary="http://example.com/nonexistent">bar</value>
		</element>`)
}
