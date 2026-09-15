package relaxng

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The compact syntax claims to be the same language as the XML syntax, spelled
// differently. TestCompactMatchesXMLSyntax asserts that as a property of the
// trees; this file asserts it where it is actually observable, which is the
// verdict on a document.
//
// Comparing trees and comparing verdicts catch different things. Two trees may
// legitimately differ — trang writes name= on an <element>, this parser writes
// a <name> child, and the slides schema in testdata shows both spellings of
// the same schema — while still accepting exactly the same documents. A
// verdict comparison sees through that, and it is the claim a user relies on.
//
// Every case carries a document that must be accepted and one that must be
// refused. A one-sided test would pass against a validator that accepts
// everything, which is the failure this is written to make impossible.
func TestCompactAndXMLAgreeOnDocuments(t *testing.T) {
	cases := []struct {
		name     string
		rnc, rng string
		valid    []string
		invalid  []string
	}{
		{
			name: "choice",
			rnc:  `element r { element a { empty } | element b { empty } }`,
			rng: `<element name="r"` + rngNS + `><choice>` +
				`<element name="a"><empty/></element>` +
				`<element name="b"><empty/></element></choice></element>`,
			valid:   []string{`<r><a/></r>`, `<r><b/></r>`},
			invalid: []string{`<r><c/></r>`, `<r><a/><b/></r>`, `<r/>`},
		},
		{
			name: "group",
			rnc:  `element r { element a { empty }, element b { empty } }`,
			rng: `<element name="r"` + rngNS + `><group>` +
				`<element name="a"><empty/></element>` +
				`<element name="b"><empty/></element></group></element>`,
			valid:   []string{`<r><a/><b/></r>`},
			invalid: []string{`<r><b/><a/></r>`, `<r><a/></r>`},
		},
		{
			name: "interleave",
			rnc:  `element r { element a { empty } & element b { empty } }`,
			rng: `<element name="r"` + rngNS + `><interleave>` +
				`<element name="a"><empty/></element>` +
				`<element name="b"><empty/></element></interleave></element>`,
			valid:   []string{`<r><a/><b/></r>`, `<r><b/><a/></r>`},
			invalid: []string{`<r><a/></r>`, `<r><a/><b/><a/></r>`},
		},
		{
			name: "zeroOrMore",
			rnc:  `element r { element a { empty }* }`,
			rng: `<element name="r"` + rngNS + `><zeroOrMore>` +
				`<element name="a"><empty/></element></zeroOrMore></element>`,
			valid:   []string{`<r/>`, `<r><a/></r>`, `<r><a/><a/></r>`},
			invalid: []string{`<r><b/></r>`},
		},
		{
			name: "oneOrMore",
			rnc:  `element r { element a { empty }+ }`,
			rng: `<element name="r"` + rngNS + `><oneOrMore>` +
				`<element name="a"><empty/></element></oneOrMore></element>`,
			valid:   []string{`<r><a/></r>`, `<r><a/><a/></r>`},
			invalid: []string{`<r/>`, `<r><b/></r>`},
		},
		{
			name: "optional",
			rnc:  `element r { element a { empty }? }`,
			rng: `<element name="r"` + rngNS + `><optional>` +
				`<element name="a"><empty/></element></optional></element>`,
			valid:   []string{`<r/>`, `<r><a/></r>`},
			invalid: []string{`<r><a/><a/></r>`},
		},
		{
			name: "grammar, start and define",
			rnc:  "grammar { start = element r { p }\n p = element a { empty } }",
			rng: `<grammar` + rngNS + `><start>` +
				`<element name="r"><ref name="p"/></element></start>` +
				`<define name="p"><element name="a"><empty/></element></define>` +
				`</grammar>`,
			valid:   []string{`<r><a/></r>`},
			invalid: []string{`<r><b/></r>`, `<r/>`},
		},
		{
			name: "combine with |=",
			rnc: "grammar { start = element r { p }\n" +
				" p |= element a { empty }\n p |= element b { empty } }",
			rng: `<grammar` + rngNS + `><start>` +
				`<element name="r"><ref name="p"/></element></start>` +
				`<define name="p" combine="choice">` +
				`<element name="a"><empty/></element></define>` +
				`<define name="p" combine="choice">` +
				`<element name="b"><empty/></element></define></grammar>`,
			valid:   []string{`<r><a/></r>`, `<r><b/></r>`},
			invalid: []string{`<r><c/></r>`},
		},
		{
			name: "combine with &=",
			rnc: "grammar { start = element r { p }\n" +
				" p &= element a { empty }\n p &= element b { empty } }",
			rng: `<grammar` + rngNS + `><start>` +
				`<element name="r"><ref name="p"/></element></start>` +
				`<define name="p" combine="interleave">` +
				`<element name="a"><empty/></element></define>` +
				`<define name="p" combine="interleave">` +
				`<element name="b"><empty/></element></define></grammar>`,
			valid:   []string{`<r><a/><b/></r>`, `<r><b/><a/></r>`},
			invalid: []string{`<r><a/></r>`},
		},
		{
			name: "parent reference from a nested grammar",
			rnc: "grammar { start = element r { grammar { start = parent p } }\n" +
				" p = element a { empty } }",
			rng: `<grammar` + rngNS + `><start><element name="r"><grammar>` +
				`<start><parentRef name="p"/></start></grammar></element></start>` +
				`<define name="p"><element name="a"><empty/></element></define>` +
				`</grammar>`,
			valid:   []string{`<r><a/></r>`},
			invalid: []string{`<r><b/></r>`},
		},
		{
			name: "datatypes declaration and a parameter",
			rnc: `datatypes xsd = "http://www.w3.org/2001/XMLSchema-datatypes"` +
				"\n" + `element r { xsd:string { maxLength = "3" } }`,
			rng: `<element name="r"` + rngNS +
				` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes">` +
				`<data type="string"><param name="maxLength">3</param></data>` +
				`</element>`,
			valid:   []string{`<r>ab</r>`, `<r>abc</r>`},
			invalid: []string{`<r>abcd</r>`},
		},
		{
			name: "name class with a wildcard and an except",
			rnc:  `element r { attribute * - x { text }* }`,
			rng: `<element name="r"` + rngNS + `><zeroOrMore><attribute>` +
				`<anyName><except><name>x</name></except></anyName>` +
				`<text/></attribute></zeroOrMore></element>`,
			valid:   []string{`<r y="1"/>`, `<r/>`, `<r y="1" z="2"/>`},
			invalid: []string{`<r x="1"/>`},
		},
		{
			name: "mixed and list",
			rnc:  `element r { mixed { element a { empty }* } }`,
			rng: `<element name="r"` + rngNS + `><mixed><zeroOrMore>` +
				`<element name="a"><empty/></element></zeroOrMore></mixed></element>`,
			valid:   []string{`<r>t<a/>t</r>`, `<r/>`},
			invalid: []string{`<r><b/></r>`},
		},
		{
			name: "comments and annotations are not content",
			rnc: "# a comment\n" +
				"[ a:x = \"1\" ]\n" +
				"## documentation\n" +
				"element r { element a { empty } }   # trailing\n",
			rng: `<element name="r"` + rngNS + `>` +
				`<element name="a"><empty/></element></element>`,
			valid:   []string{`<r><a/></r>`},
			invalid: []string{`<r><b/></r>`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compact, err := CompileCompact(tc.rnc, Options{})
			if err != nil {
				t.Fatalf("CompileCompact: %v\n%s", err, tc.rnc)
			}
			tree, err := xdm.ParseString(tc.rng, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parse XML syntax: %v", err)
			}
			xml, err := Compile(tree.Root)
			if err != nil {
				t.Fatalf("Compile: %v\n%s", err, tc.rng)
			}
			check(t, compact, xml, tc.valid, true)
			check(t, compact, xml, tc.invalid, false)
		})
	}
}

// check runs one set of documents past both schemas and requires the stated
// verdict from each. Disagreement between the two is reported as such, because
// the two notations disagreeing is a different defect from both being wrong.
func check(t *testing.T, compact, xml *Schema, docs []string, want bool) {
	t.Helper()
	for _, d := range docs {
		tree, err := xdm.ParseString(d, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse %s: %v", d, err)
		}
		cErr := compact.Validate(tree.Root)
		xErr := xml.Validate(tree.Root)
		if (cErr == nil) != (xErr == nil) {
			t.Errorf("the two notations disagree on %s: compact=%v xml=%v",
				d, cErr, xErr)
			continue
		}
		if want && cErr != nil {
			t.Errorf("%s should be valid: %v", d, cErr)
		}
		if !want && cErr == nil {
			t.Errorf("%s should be invalid, but both notations accepted it", d)
		}
	}
}

// An <include> in the compact syntax must reach the same schema the XML
// syntax's <include> reaches, and must do so through a Resolver reading real
// files.
//
// The file arm is deliberate. resolveHref composes hrefs as URI references,
// where the separator is "/" on every platform, so a Resolver that joins them
// onto a directory has to convert before it touches the filesystem — and a
// test that only ever used in-memory sources would never exercise that.
// t.TempDir and filepath.Join keep this correct on Windows, Linux and macOS.
func TestCompactIncludeFromFilesMatchesXML(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("common.rnc", "p = element a { empty }\n")
	write("common.rng", `<grammar`+rngNS+`>`+
		`<define name="p"><element name="a"><empty/></element></define>`+
		`</grammar>`)

	r := &twinFileResolver{dir: dir, t: t}
	compact, err := CompileCompact(
		"include \"common.rnc\"\nstart = element r { p }\n",
		Options{Resolver: r, BaseURI: "."})
	if err != nil {
		t.Fatalf("CompileCompact with an include: %v", err)
	}
	tree, err := xdm.ParseString(`<grammar`+rngNS+`>`+
		`<include href="common.rng"/>`+
		`<start><element name="r"><ref name="p"/></element></start>`+
		`</grammar>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	xml, err := CompileWithOptions(tree.Root, Options{Resolver: r, BaseURI: "."})
	if err != nil {
		t.Fatalf("Compile with an include: %v", err)
	}
	check(t, compact, xml, []string{`<r><a/></r>`}, true)
	check(t, compact, xml, []string{`<r><b/></r>`, `<r/>`}, false)
}

// twinFileResolver reads schemas out of one directory, choosing the parser by the
// extension, and refuses anything that would leave it.
type twinFileResolver struct {
	dir string
	t   *testing.T
}

func (r *twinFileResolver) ResolveSchema(href string) (*xdm.Node, error) {
	// An href is a URI reference, so its separator is "/" whatever the host
	// filesystem uses. filepath.FromSlash is what makes this work on Windows.
	name := filepath.Base(filepath.FromSlash(href))
	src, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return nil, err
	}
	if filepath.Ext(name) == ".rnc" {
		return ParseCompact(string(src))
	}
	tree, err := xdm.ParseString(string(src), xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return tree.Root, nil
}
