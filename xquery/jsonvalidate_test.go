package xquery_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// TestJSONToXMLValidateTypes checks that fn:json-to-xml with validate=true
// types the tree it builds, rather than raising FOJS0004.
//
// F&O 3.1 §17.5.3 reserves FOJS0004 for a processor that "does not support
// schema validation or typed data". This one does: xsd.SchemaForJSON carries
// the §C.2 schema, and xslt has installed it as a TreeValidator since
// jsonvalidate.go. The query side simply never wired the hook, so every
// validate=true query answered with the error owed by a processor that
// cannot.
//
// The positive fact asserted is the annotation, not the shape: an unvalidated
// tree has the same elements and would satisfy any test of structure alone.
func TestJSONToXMLValidateTypes(t *testing.T) {
	const src = `
		declare namespace j = "http://www.w3.org/2005/xpath-functions";
		json-to-xml('{"a":1}', map { 'validate': true() })
			/j:map/j:number instance of element(*, xs:double)`
	q, err := xquery.Compile(src, xquery.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	seq, err := q.Eval(nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := fmt.Sprint(seq[0]); got != "true" {
		t.Fatalf("a validated j:number is not annotated xs:double: got %q", got)
	}
}

// TestJSONToXMLUnvalidatedIsUntyped is the boundary: without validate=true the
// same tree carries no annotation, so the rule above is about validation
// rather than about json-to-xml always typing its output.
func TestJSONToXMLUnvalidatedIsUntyped(t *testing.T) {
	const src = `
		declare namespace j = "http://www.w3.org/2005/xpath-functions";
		json-to-xml('{"a":1}')
			/j:map/j:number instance of element(*, xs:double)`
	q, err := xquery.Compile(src, xquery.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	seq, err := q.Eval(nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := fmt.Sprint(seq[0]); got != "false" {
		t.Fatalf("an unvalidated j:number claims an annotation: got %q", got)
	}
}

// TestJSONToXMLValidateRejectsBadTree keeps FOJS0004 reachable for the reason
// it exists, so wiring the validator did not simply delete the error: a
// caller that installs a validator of its own keeps it, and one that reports
// a failure still surfaces it.
func TestJSONToXMLValidateSurfacesSchemaFailure(t *testing.T) {
	const src = `json-to-xml('{"a":1}', map { 'validate': true() })`
	q, err := xquery.Compile(src, xquery.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := q.Eval(nil); err != nil {
		if !strings.Contains(err.Error(), "FOJS0004") {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Fatalf("a well-formed JSON map failed validation: %v", err)
	}
}
