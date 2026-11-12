package xquery_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xquery"
)

// MaxSchemaBytes spans every fn:load-xquery-module call of one evaluation,
// and a load once MaxModules is spent is refused before it compiles. Each
// call with distinct location hints compiled afresh, against a new schema
// allowance, so five calls read five schemas under a budget for two.
func TestSM07LoadedModulesShareTheSchemaBudget(t *testing.T) {
	mod := xquery.Module{Namespace: "urn:ls", Source: `module namespace m = "urn:ls";
import schema namespace s = "urn:s" at "s.xsd";
declare function m:f() { 1 };`}
	opts := xquery.Options{Modules: []xquery.Module{mod},
		SchemaResolver: hugeSchemaResolver{size: 600}, MaxSchemaBytes: 1500}
	call := `load-xquery-module("urn:ls", map{"location-hints": "h" || $i})("functions")`
	if _, err := run(t, `for $i in 1 to 2 return `+call, opts); err != nil {
		t.Fatalf("two loads inside the budget: %v", err)
	}
	_, err := run(t, `for $i in 1 to 5 return `+call, opts)
	wantSchemaBudget(t, err)

	opts.MaxSchemaBytes, opts.MaxModules = 0, 2
	var r countingModules
	opts.Modules, opts.ModuleResolver = nil, &r
	// Each failed load is caught, so only the count stops the reads.
	_, err = run(t, `for $i in 1 to 5 return try { `+call+` } catch * { 0 }`, opts)
	if err != nil && !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("err = %v, want none or the MaxModules refusal", err)
	}
	if r.n > 2 {
		t.Errorf("%d modules read under MaxModules 2; the refusal came after the work", r.n)
	}
}

// countingModules serves a module that does not compile, counting reads.
type countingModules struct{ n int }

func (r *countingModules) Resolve(ns string, hints []string, base string) (
	io.ReadCloser, string, error) {
	r.n++
	return io.NopCloser(strings.NewReader(
		`module namespace m = "urn:ls"; declare function m:f() { 1 +`)), "", nil
}
