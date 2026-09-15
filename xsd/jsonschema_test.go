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
		// §C.2 gives j:boolean a named complex type of its own, exactly as it
		// does the other five. An earlier copy of the schema declared it
		// type="xs:boolean" instead, which annotated the element with the
		// built-in's bare local name and made json-to-xml-046 and -047 answer
		// false on their boolean arm.
		{`<array xmlns="http://www.w3.org/2005/xpath-functions"><boolean>true</boolean></array>`,
			j + "arrayType", j + "booleanType"},
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

// TestSchemaForJSONWithinMapAnnotations checks a validated map's children carry
// the WithinMap type §C.2 declares for them.
//
// A map's children are LOCAL element declarations with their own named types —
// j:stringWithinMapType and the rest — which extend the global ones so that
// they can also carry @key. An array's children are refs to the global
// declarations and so carry the global types directly. json-to-xml-046 and
// -047 are the pair of cases that tell the two apart, and an earlier copy of
// the schema inlined the within-map types anonymously, which left them with no
// name for either case to ask about.
func TestSchemaForJSONWithinMapAnnotations(t *testing.T) {
	schema, err := SchemaForJSON()
	if err != nil {
		t.Fatal(err)
	}
	const j = "{http://www.w3.org/2005/xpath-functions}"
	const in = `<map xmlns="http://www.w3.org/2005/xpath-functions">` +
		`<string key="a">foo</string><number key="b">123</number>` +
		`<null key="c"/><boolean key="d">true</boolean>` +
		`<array key="f"><number>1</number></array><map key="g"/></map>`
	want := map[string]string{
		"string":  j + "stringWithinMapType",
		"number":  j + "numberWithinMapType",
		"null":    j + "nullWithinMapType",
		"boolean": j + "booleanWithinMapType",
		"array":   j + "arrayWithinMapType",
		"map":     j + "mapWithinMapType",
	}
	tree, err := xdm.ParseString(in, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(tree.Root, ValidateOptions{
		Annotate: true, SkipIDConstraints: true,
	}); err != nil {
		t.Fatalf("a map of every kind should be valid: %v", err)
	}
	root := tree.Root.Children[0]
	if root.TypeAnnotation != j+"mapType" {
		t.Errorf("root annotated %q, want %q", root.TypeAnnotation, j+"mapType")
	}
	seen := 0
	for _, c := range root.Children {
		if c.Kind != xdm.KindElement {
			continue
		}
		seen++
		if got := c.TypeAnnotation; got != want[c.Name.Local] {
			t.Errorf("%s annotated %q, want %q", c.Name.Local, got, want[c.Name.Local])
		}
	}
	if seen != len(want) {
		t.Errorf("validated %d children, want %d", seen, len(want))
	}
}

// TestSchemaForJSONDerivationChains checks each WithinMap type records its step
// to the global type it extends, and that the chain still reaches the built-in.
//
// Both facts are read from the one chain. The step to the named base is what
// makes "instance of element(fn:string, fn:stringType)" true for a map's child,
// which is json-to-xml-046's first assertion; the continuation to xs:string is
// what makes the node atomise as a string rather than as untypedAtomic.
//
// j:nullWithinMapType is deliberately absent from the derived set: §C.2 gives
// it no base at all — it is a bare complex type carrying the key attributes,
// not an extension of j:nullType — which is why -046 asserts NOT(... instance
// of element(fn:null, fn:nullType)) where -047 asserts the positive.
func TestSchemaForJSONDerivationChains(t *testing.T) {
	if _, err := SchemaForJSON(); err != nil {
		t.Fatal(err)
	}
	const j = "{http://www.w3.org/2005/xpath-functions}"
	for _, tc := range []struct {
		name  string
		chain []string
	}{
		{j + "stringWithinMapType", []string{j + "stringType", "string"}},
		{j + "booleanWithinMapType", []string{j + "booleanType", "boolean"}},
		{j + "numberWithinMapType", []string{j + "numberType", j + "finiteNumberType", "double"}},
		{j + "mapWithinMapType", []string{j + "mapType"}},
		{j + "arrayWithinMapType", []string{j + "arrayType"}},
		{j + "stringType", []string{"string"}},
		{j + "booleanType", []string{"boolean"}},
		{j + "numberType", []string{j + "finiteNumberType", "double"}},
		{j + "nullWithinMapType", nil},
		{j + "nullType", nil},
		{j + "mapType", nil},
	} {
		var got []string
		for cur := xdm.DerivedBase(tc.name); cur != ""; cur = xdm.DerivedBase(cur) {
			got = append(got, cur)
			if len(got) > 8 {
				t.Fatalf("%s: chain did not terminate: %v", tc.name, got)
			}
		}
		if len(got) != len(tc.chain) {
			t.Errorf("%s: chain %v, want %v", tc.name, got, tc.chain)
			continue
		}
		for i := range got {
			if got[i] != tc.chain[i] {
				t.Errorf("%s: chain %v, want %v", tc.name, got, tc.chain)
				break
			}
		}
	}
}
