package xsd

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The read-path half of finding 21, pinned the way the annotation half is
// pinned next door in annotation_isolation_test.go.
//
// That file pins the per-node RESOLVED fields: what the node's OWN annotation
// erases to, and what a list's items are. Those cover atomisation and nothing
// else. Every other by-NAME question -- "instance of", "castable as", the
// element and attribute tests, fn:id, xsl:copy's namespace-sensitivity check
// -- has to WALK the annotation's derivation chain, and a chain is not one
// fact but a table. The node cannot carry the table in a field, so it carries
// a reference to the schema's TypeEnvironment instead, and the walks go there.
//
// Without that reference the walks went to the process-global table, which is
// keyed by type name across every schema in the process and holds whatever
// loaded LAST. A node validated against one schema then answered every chain
// question with an unrelated schema's derivations, silently, from the moment
// that schema loaded.

const nodeEnvNS = "urn:nodeenv"

// chainSchema defines {urn:nodeenv}Outer as a restriction of
// {urn:nodeenv}Inner, and Inner as a restriction of the named built-in.
//
// The TWO links are what this test needs. A one-link type is answered by the
// node's own DerivedPrimitive field, which validation records directly and
// which needs no environment; asking about the type one link ABOVE the
// annotation is what forces the walk.
func chainSchema(base string) string {
	return fmt.Sprintf(`<xs:schema
	    xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    xmlns:p="%s" targetNamespace="%s" elementFormDefault="qualified">
	  <xs:simpleType name="Inner"><xs:restriction base="xs:%s"/></xs:simpleType>
	  <xs:simpleType name="Outer"><xs:restriction base="p:Inner"/></xs:simpleType>
	  <xs:element name="e" type="p:Outer"/>
	</xs:schema>`, nodeEnvNS, nodeEnvNS, base)
}

func loadChain(t *testing.T, src string) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	s, err := Load(tree.Root, "chain.xsd", Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func validateChain(t *testing.T, s *Schema, value string) *xdm.Node {
	t.Helper()
	src := fmt.Sprintf(`<p:e xmlns:p="%s">%s</p:e>`, nodeEnvNS, value)
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return tree.Root.Children[0]
}

// TestNodeCarriesItsSchemasTypeEnvironment is the stamping itself: a node a
// schema validated must hold that schema's environment, not nil and not
// another's.
//
// It is asserted by IDENTITY rather than by content. Two schemas defining the
// same names produce environments that agree on every lookup, so comparing
// answers cannot tell them apart -- which is exactly the confusion the whole
// change exists to remove.
func TestNodeCarriesItsSchemasTypeEnvironment(t *testing.T) {
	s := loadChain(t, chainSchema("decimal"))
	el := validateChain(t, s, "10")

	if el.TypeEnv() == nil {
		t.Fatal("a node validated by a schema carries no TypeEnvironment, so " +
			"every derivation walk over its annotation falls back to the " +
			"process-global table")
	}
	if el.TypeEnv() != s.TypeEnv() {
		t.Errorf("a validated node carries a TypeEnvironment that is not its " +
			"own schema's")
	}
	// Attributes and defaulted attributes go through the same stamping, and
	// a node whose annotation came from somewhere else must still carry none.
	plain := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	plain.SetTypeAnnotation("decimal")
	if plain.TypeEnv() != nil {
		t.Errorf("a node no schema validated carries a TypeEnvironment; it " +
			"must carry none and fall back to the global table")
	}
	if xdm.TypeEnvOf(plain) != xdm.GlobalTypeEnvironment() {
		t.Errorf("TypeEnvOf did not fall back to the global environment for " +
			"a node no schema validated")
	}
}

// TestDerivationChainIsolatedFromLaterSchema is the regression the read-path
// migration closes.
//
// A node is validated against a schema whose Outer restricts a DECIMAL Inner.
// A second schema then defines the same two names over xs:string, taking the
// process-global entries. Nothing about the already-validated node changed, so
// no question about it may change either -- and the question asked is the one
// that needs the TABLE rather than the node's own fields: what Outer derives
// from, one link up.
//
// The assertion is on the ANSWER TO THE WALK, not on the annotation, which is
// "{urn:nodeenv}Outer" under either schema and so cannot distinguish them.
func TestDerivationChainIsolatedFromLaterSchema(t *testing.T) {
	decimal := loadChain(t, chainSchema("decimal"))
	el := validateChain(t, decimal, "10")

	outer := xdm.AnnotationName(nodeEnvNS, "Outer")
	inner := xdm.AnnotationName(nodeEnvNS, "Inner")
	if got := el.TypeAnnotation; got != outer {
		t.Fatalf("precondition: validation annotated the element %q, want %q",
			got, outer)
	}
	if got := xdm.TypeEnvOf(el).DerivedBase(inner); got != "decimal" {
		t.Fatalf("precondition: the node's environment says %s derives from "+
			"%q, want %q", inner, got, "decimal")
	}

	// The shadowing schema redefines BOTH names over xs:string and takes the
	// global entries, which is the collision the whole change is about.
	loadChain(t, chainSchema("string"))
	if got := xdm.DerivedBase(inner); got != "string" {
		t.Fatalf("precondition: the shadowing schema did not take the global "+
			"entry: DerivedBase(%q) = %q, want %q", inner, got, "string")
	}

	// The walk over the ALREADY-VALIDATED node must still reach decimal. This
	// is the whole regression: with the walk on the global table it reaches
	// string, and every "instance of" and "castable as" over this node is
	// decided by a schema it was never validated against.
	env := xdm.TypeEnvOf(el)
	if got := env.DerivedBase(outer); got != inner {
		t.Errorf("after an unrelated schema redefined %s, the node's "+
			"derivation walk says it derives from %q, want %q", outer, got, inner)
	}
	if got := env.DerivedBase(inner); got != "decimal" {
		t.Errorf("after an unrelated schema redefined %s over xs:string, a "+
			"node validated against the xs:decimal schema walks its chain to "+
			"%q, want %q -- the node is answering with a schema it was never "+
			"validated against", inner, got, "decimal")
	}
}

// TestAtomicValueCarriesTheNodesTypeEnvironment is the same property one step
// downstream.
//
// Atomising a node produces a value that keeps the annotation as its derived
// type name, and every "instance of" over that VALUE walks the same chain. A
// value that kept the name and dropped the environment would ask the global
// table the question the node was just protected from.
func TestAtomicValueCarriesTheNodesTypeEnvironment(t *testing.T) {
	decimal := loadChain(t, chainSchema("decimal"))
	el := validateChain(t, decimal, "10")

	a := el.Atomize()
	if a == nil {
		t.Fatal("precondition: the validated node did not atomise")
	}
	if a.TypeEnv() != decimal.TypeEnv() {
		t.Fatalf("an atomic value carries a TypeEnvironment that is not the " +
			"one of the schema that validated the node it came from")
	}

	inner := xdm.AnnotationName(nodeEnvNS, "Inner")
	loadChain(t, chainSchema("string"))

	if got := xdm.TypeEnvOfAtomic(a).DerivedBase(inner); got != "decimal" {
		t.Errorf("after an unrelated schema redefined %s over xs:string, a "+
			"value atomised from a node validated against the xs:decimal "+
			"schema walks its chain to %q, want %q", inner, got, "decimal")
	}
}
