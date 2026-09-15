package xquery

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// "import schema" folds every imported schema into ONE aggregate *xsd.Schema
// (§2.1.1 makes the in-scope schema definitions one set), and that aggregate
// is what a validate expression assesses against -- so it is the aggregate's
// TypeEnvironment that gets stamped on every node the assessment annotates.
// mergeXSDSchema copies the type DEFINITIONS across; without
// dst.TypeEnv().Merge(src.TypeEnv()) beside them the aggregate holds a type
// that no longer knows what it derives from, and the derivation walk that
// decides element(*, xs:decimal) has an empty table to walk.
//
// The direction of the failure is what makes it matter. A lost chain answers
// "does not derive from xs:decimal", so "treat as" refuses a node that is in
// fact a restriction of xs:decimal, and "instance of" quietly answers false
// for a subtype the query imported precisely in order to name.
//
// The query is COMPILED ONCE and EVALUATED TWICE, with an unrelated shadowing
// schema loaded in between. That ordering is the whole point: compiling again
// would re-register this query's own {urn:xq-agg-a}T into the process-global
// table and paper over the collision, which is exactly how this bug hides in
// a test that compiles per run.
//
// The assertion is on the ERROR CODE -- XPDY0050, absent when the chain is
// intact and present when it is lost -- rather than on the value. Both
// schemas spell the element's content "10", so any lexical assertion passes
// while the bug is live; only the derivation verdict distinguishes them.

// aggDecimalSchema defines {urn:xq-agg-a}T as a restriction of xs:decimal and
// an element carrying it. This is the schema whose derivation fact has to
// survive the merge into the aggregate.
const aggDecimalSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="urn:xq-agg-a" xmlns:a="urn:xq-agg-a"
   elementFormDefault="qualified">
  <xs:simpleType name="T"><xs:restriction base="xs:decimal"/></xs:simpleType>
  <xs:element name="e" type="a:T"/>
</xs:schema>`

// aggSecondSchema is an unrelated second namespace. It exists so that the
// aggregate is genuinely a MERGE of more than one schema, which is the only
// shape in which Merge can matter: a lone import into an empty xsd.NewSchema
// is still a merge, but two of them are what §2.1.1's one-set rule is about.
const aggSecondSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="urn:xq-agg-b" xmlns:b="urn:xq-agg-b"
   elementFormDefault="qualified">
  <xs:simpleType name="U"><xs:restriction base="xs:string"/></xs:simpleType>
  <xs:element name="f" type="b:U"/>
</xs:schema>`

// aggShadowSchema defines the SAME QName over a different base. Loading it
// rewrites the process-global entry for {urn:xq-agg-a}T to xs:string, which is
// not a decimal at all.
const aggShadowSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="urn:xq-agg-a" xmlns:a="urn:xq-agg-a"
   elementFormDefault="qualified">
  <xs:simpleType name="T"><xs:restriction base="xs:string"/></xs:simpleType>
  <xs:element name="e" type="a:T"/>
</xs:schema>`

// aggQuery imports two schemas -- so the aggregate is a merge -- validates an
// element against the first, and treats the result as element(*, xs:decimal).
// Reaching that decimal requires walking {urn:xq-agg-a}T's derivation chain in
// the environment stamped on the validated node, which is the AGGREGATE's.
const aggQuery = `import schema namespace b = "urn:xq-agg-b";
import schema namespace a = "urn:xq-agg-a";
validate { <a:e xmlns:a="urn:xq-agg-a">10</a:e> } treat as element(*, xs:decimal)`

// TestImportedSchemaDerivationSurvivesTheAggregateMerge is the regression for
// mergeXSDSchema's TypeEnv().Merge.
func TestImportedSchemaDerivationSurvivesTheAggregateMerge(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "urn:xq-agg-a", Source: aggDecimalSchema},
		{Namespace: "urn:xq-agg-b", Source: aggSecondSchema},
	}}
	q, err := Compile(aggQuery, opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	aggT := xdm.AnnotationName("urn:xq-agg-a", "T")

	// Precondition: with nothing shadowing it, the treat succeeds. This pins
	// that the test reaches the code under test at all -- without it, a change
	// that broke the whole path would read as a pass.
	if _, err := q.Eval(nil); err != nil {
		t.Fatalf("precondition: a node validated against a restriction of "+
			"xs:decimal must satisfy element(*, xs:decimal); got %v", err)
	}
	if got := xdm.DerivedBase(aggT); got != "decimal" {
		t.Fatalf("precondition: the global entry for %q is %q, want %q",
			aggT, got, "decimal")
	}

	// An unrelated query imports a schema redefining {urn:xq-agg-a}T over
	// xs:string, exactly as a host sharing one process between two queries
	// would load it.
	if _, err := Eval(`import schema namespace a = "urn:xq-agg-a"; 1`, nil,
		Options{Schemas: []Schema{
			{Namespace: "urn:xq-agg-a", Source: aggShadowSchema},
		}}); err != nil {
		t.Fatalf("loading the shadowing schema: %v", err)
	}
	if got := xdm.DerivedBase(aggT); got != "string" {
		t.Fatalf("precondition: the shadowing schema did not take the global "+
			"entry: DerivedBase(%q) = %q, want %q", aggT, got, "string")
	}

	// The compiled query is unchanged and its own aggregate still says
	// xs:decimal, so its verdict must be unchanged too.
	if _, err := q.Eval(nil); err != nil {
		t.Errorf("the imported schema's derivation did not survive the "+
			"aggregate merge: got %v\nwant no error (the query's own "+
			"aggregate schema still restricts xs:decimal, so the validated "+
			"node satisfies element(*, xs:decimal))", err)
		if strings.Contains(err.Error(), "XPDY0050") {
			t.Logf("XPDY0050 here is the derivation chain being walked in an "+
				"aggregate whose TypeEnvironment never received %q", aggT)
		}
	}
}
