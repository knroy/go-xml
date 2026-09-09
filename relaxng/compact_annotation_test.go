package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A "##" documentation comment on a <ref> must survive into the tree, and the
// XML syntax must accept the tree it produces.
//
// RELAX NG §5.2 removes foreign elements, and everything inside them, before
// any content-model rule is applied. checkChildren used to read a
// no-content element's text with StringValue(), which reaches into a foreign
// child and reported an annotation's text as the element's own — so a <ref>
// carrying an a:documentation was refused as "<ref> takes no content", and the
// compact parser worked around it by silently dropping the comment.
//
// The slides schema in testdata is the witness: it writes "## Status attribute
// for the block element" above a bare reference, and its trang-generated .rng
// twin keeps the annotation there.
func TestDocumentationOnRefSurvives(t *testing.T) {
	src := "start = a\n" +
		"a = ## documented reference\n" +
		"  b\n" +
		"b = element x { empty }"
	doc, err := ParseCompact(src)
	if err != nil {
		t.Fatalf("ParseCompact: %v", err)
	}
	var sb strings.Builder
	normalise(firstElement(doc), &sb, 0)
	got := sb.String()
	if !strings.Contains(got, "documentation") ||
		!strings.Contains(got, "documented reference") {
		t.Errorf("the annotation on <ref> was dropped:\n%s", got)
	}
	// It must also compile: an annotation the parser emits but the syntax
	// check refuses is worse than one it drops.
	if _, err := Compile(doc); err != nil {
		t.Errorf("compile a schema whose <ref> is documented: %v", err)
	}
}

// The XML syntax must accept a foreign annotation on a no-content element, and
// must still refuse the element's own character data.
//
// The negative arm is the point: the fix narrows what "content" means, and a
// narrowing that went too far would accept <ref>stray text</ref>, which is a
// typo the author would never otherwise see.
func TestNoContentElementRejectsOwnTextButNotAnnotation(t *testing.T) {
	const head = `<grammar xmlns="http://relaxng.org/ns/structure/1.0"` +
		` xmlns:a="http://relaxng.org/ns/compatibility/annotations/1.0">` +
		`<start><ref name="b"/></start><define name="b">`
	const tail = `</define><define name="c">` +
		`<element name="x"><empty/></element></define></grammar>`

	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"annotation on ref",
			`<ref name="c"><a:documentation>hi</a:documentation></ref>`, true},
		// §7.1.5 forbids start//empty and start//text outright, so these
		// two are wrapped in an <element> to isolate the annotation.
		{"annotation on empty",
			`<element name="x"><empty>` +
				`<a:documentation>hi</a:documentation></empty></element>`, true},
		{"annotation on text",
			`<element name="x"><text>` +
				`<a:documentation>hi</a:documentation></text></element>`, true},
		{"character data on ref",
			`<ref name="c">stray</ref>`, false},
		{"character data on empty",
			`<element name="x"><empty>stray</empty></element>`, false},
		{"RELAX NG child on ref",
			`<ref name="c"><empty/></ref>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := xdm.ParseString(head+tc.body+tail, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Compile(tree.Root)
			if tc.ok && err != nil {
				t.Errorf("refused a legal schema: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("accepted %s, which is not a legal schema", tc.body)
			}
		})
	}
}

// A documented <ref> must still validate the same documents it did before: an
// annotation is a comment, and a comment may not change what a schema accepts.
func TestDocumentationDoesNotChangeValidation(t *testing.T) {
	schema, err := CompileCompact("start = ## a comment\n  b\n"+
		"b = element x { attribute k { text } }", Options{})
	if err != nil {
		t.Fatalf("CompileCompact: %v", err)
	}
	for _, tc := range []struct {
		doc string
		ok  bool
	}{
		{`<x k="v"/>`, true},
		{`<x/>`, false},           // the required attribute is missing
		{`<y k="v"/>`, false},     // the wrong element
		{`<x k="v">t</x>`, false}, // text where the schema allows none
	} {
		tree, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse %s: %v", tc.doc, err)
		}
		err = schema.Validate(tree.Root)
		if tc.ok && err != nil {
			t.Errorf("%s should be valid: %v", tc.doc, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s should be invalid, but was accepted", tc.doc)
		}
	}
}
