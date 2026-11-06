package xquery_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xquery"
)

// wantSchemaBudget asserts err is the MaxSchemaBytes refusal.
func wantSchemaBudget(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("want a MaxSchemaBytes refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("a budget refusal must not claim the schema was not found: %v", err)
	}
}

// MaxSchemaBytes is cumulative across the compilation, library modules
// included. Each library module's parser used to build a loader with a fresh
// counter, so five modules each importing 600 bytes compiled under a budget
// of 1024.
func TestMaxSchemaBytesSpansLibraryModules(t *testing.T) {
	var mods []xquery.Module
	var imports strings.Builder
	for i := 1; i <= 5; i++ {
		ns := fmt.Sprintf("urn:m%d", i)
		mods = append(mods, xquery.Module{Namespace: ns, Source: fmt.Sprintf(
			`module namespace m=%q; import schema namespace s="urn:s%d" at "s.xsd";
			declare function m:f() { 1 };`, ns, i)})
		fmt.Fprintf(&imports, "import module namespace m%d=%q;\n", i, ns)
	}
	opts := xquery.Options{Modules: mods,
		SchemaResolver: hugeSchemaResolver{size: 600}, MaxSchemaBytes: 1024}
	_, err := xquery.Compile(imports.String()+"1", opts)
	wantSchemaBudget(t, err)

	opts.Modules = mods[:1]
	if _, err := xquery.Compile(`import module namespace m1="urn:m1"; 1`,
		opts); err != nil {
		t.Fatalf("one module inside the budget was refused: %v", err)
	}
}

// The documents an imported schema reaches through its own xs:include are
// charged too. Only the top document used to be, so importing one small
// top.xsd under many namespaces re-read its whole include closure for free.
func TestMaxSchemaBytesChargesTheIncludeClosure(t *testing.T) {
	r := &recordingResolver{docs: map[string]string{
		"top.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			`<xs:include schemaLocation="big.xsd"/></xs:schema>`,
		"big.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><!--` +
			strings.Repeat("x", 600) + `--></xs:schema>`,
	}}
	opts := xquery.Options{SchemaResolver: r, MaxSchemaBytes: 1024}
	if _, err := xquery.Compile(
		`import schema namespace a="urn:a" at "top.xsd"; 1`, opts); err != nil {
		t.Fatalf("one import inside the budget was refused: %v", err)
	}
	_, err := xquery.Compile(`import schema namespace a="urn:a" at "top.xsd";
		import schema namespace b="urn:b" at "top.xsd"; 1`, opts)
	wantSchemaBudget(t, err)
}
