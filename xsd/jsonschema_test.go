package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestSchemaForJSONAnnotates checks the built-in schema of F&O 3.1 §C.2 both
// parses and produces the annotations fn:json-to-xml's validate option
// promises.
//
// The annotation is the whole point: a stylesheet asks "instance of
// element(j:number, j:numberType)", which is false for a correctly shaped but
// unannotated tree.
func TestSchemaForJSONAnnotates(t *testing.T) {
	schema, err := SchemaForJSON()
	if err != nil {
		t.Fatalf("the built-in schema for JSON should load: %v", err)
	}

	const j = "{http://www.w3.org/2005/xpath-functions}"
	for _, tc := range []struct {
		in, root, child string
	}{
		{`<map xmlns="http://www.w3.org/2005/xpath-functions"/>`, j + "mapType", ""},
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"/>`, j + "arrayType", ""},
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"><number>1</number></array>`,
			j + "arrayType", j + "numberType"},
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"><string>a</string></array>`,
			j + "arrayType", j + "stringType"},
		// xs:boolean is a built-in, so the boolean element is annotated with
		// the bare local name the data model records built-ins under rather
		// than a Clark key.
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"><boolean>true</boolean></array>`,
			j + "arrayType", "boolean"},
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"><null/></array>`,
			j + "arrayType", j + "nullType"},
	} {
		tree, err := xdm.ParseString(tc.in, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing %s: %v", tc.in, err)
		}
		if err := schema.Validate(tree.Root, ValidateOptions{
			Annotate: true, SkipIDConstraints: true,
		}); err != nil {
			t.Errorf("%s should be valid against the schema for JSON: %v", tc.in, err)
			continue
		}
		root := tree.Root.Children[0]
		if root.TypeAnnotation != tc.root {
			t.Errorf("%s: root annotated %q, want %q", tc.in, root.TypeAnnotation, tc.root)
		}
		got := ""
		for _, c := range root.Children {
			if c.Kind == xdm.KindElement {
				got = c.TypeAnnotation
			}
		}
		if got != tc.child {
			t.Errorf("%s: child annotated %q, want %q", tc.in, got, tc.child)
		}
	}
}

// TestSchemaForJSONShared checks the schema is parsed once. It is immutable
// after assembly and the whole json-to-xml test set would otherwise pay for a
// fresh parse per call.
func TestSchemaForJSONShared(t *testing.T) {
	a, err := SchemaForJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := SchemaForJSON()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("SchemaForJSON should return the same schema every time")
	}
}
