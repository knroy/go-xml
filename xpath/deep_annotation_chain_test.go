package xpath

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Five walks up the derivation chain a schema recorded stopped after a fixed
// 32 steps:
//
//	for i := 0; i < 32 && a != ""; i++ { a = xdm.DerivedBase(a); ... }
//
// derivedSubtypeOfThroughSchema and schemaTypeNameMatches (typeexpr.go),
// annotationDerivesFrom (fn_misc.go), nodeTypeMatches and declaredTypeMatches
// (ast_string.go). Every one of them answers a subtype question, and running
// out of steps returned FALSE — a definite negative, not a refusal. So a legal
// acyclic chain of 33 user-defined types stopped being a subtype of its own
// base: "instance of xs:integer" false, element(*, T) refusing a node the
// schema annotated with a restriction of T, fn:id blind to a deep restriction
// of xs:ID.
//
// The count was there to stop a schema whose derivations somehow formed a
// cycle from spinning. A visited set on the annotation name does that exactly
// — a repeated name IS the cycle — and imposes no bound on a legal chain.
//
// xdm's derivation registry is process-global, so every case below builds its
// chain in a namespace carrying its own depth and label. Two tests sharing a
// URI would share types.

var annotationChainDepths = []int{1, 2, 31, 32, 33, 64, 65, 128, 256, 512}

// registerChain builds T1 derived from base, T2 from T1, ... Tn from T{n-1},
// all in ns, and returns the annotation name of Tn. With base "integer" the
// chain is grounded in a built-in, so the built-in hierarchy above it
// (integer restricts decimal) is reachable through the schema chain.
func registerChain(ns, base string, n int) string {
	prev := base
	for i := 1; i <= n; i++ {
		name := xdm.AnnotationName(ns, fmt.Sprintf("T%d", i))
		xdm.RegisterDerivedType(name, prev)
		prev = name
	}
	return prev
}

func chainNS(label string, n int) string {
	return fmt.Sprintf("http://example.com/deepchain/%s/%d", label, n)
}

// derivedSubtypeOfThroughSchema decides whether an atomic VALUE annotated with
// a schema type satisfies a built-in facet type — it is what makes
// "data(e) instance of xs:decimal" true for a restriction of xs:integer.
// Truncation returned false: a definite "not an instance".
func TestDeepChainDerivedSubtypeOfThroughSchema(t *testing.T) {
	for _, n := range annotationChainDepths {
		top := registerChain(chainNS("through", n), "integer", n)
		if !derivedSubtypeOfThroughSchema(top, "integer") {
			t.Errorf("depth %d: a type %d links above xs:integer is not a "+
				"subtype of it", n, n)
		}
		// One further step, through the built-in table: integer restricts
		// decimal. This is the crossing the function exists for.
		if !derivedSubtypeOfThroughSchema(top, "decimal") {
			t.Errorf("depth %d: a type %d links above xs:integer is not a "+
				"subtype of xs:decimal", n, n)
		}
		// The relation must not become vacuously true either.
		if derivedSubtypeOfThroughSchema(top, "date") {
			t.Errorf("depth %d: an integer-derived type reported as a subtype "+
				"of xs:date", n)
		}
	}
}

// schemaTypeNameMatches decides "instance of my:T" for an atomic value:
// a value is an instance of every type its own derives from. Truncation
// returned false, so "$v instance of my:T1" was false for a value annotated
// T33 — the value is not an instance of its own ancestor.
func TestDeepChainSchemaTypeNameMatches(t *testing.T) {
	for _, n := range annotationChainDepths {
		ns := chainNS("atomic", n)
		top := registerChain(ns, "integer", n)
		// Every ancestor in the chain, and the built-in it is grounded in.
		for _, want := range []string{
			xdm.AnnotationName(ns, "T1"),
			xdm.AnnotationName(ns, fmt.Sprintf("T%d", (n+1)/2)),
			xdm.AnnotationName(ns, fmt.Sprintf("T%d", n)),
			"integer",
		} {
			if !schemaTypeNameMatches(top, want) {
				t.Errorf("depth %d: a value annotated %s is not an instance "+
					"of %s", n, top, want)
			}
		}
		// A sibling namespace at the same depth must not match: the visited
		// set must not have widened the relation.
		other := registerChain(chainNS("atomic-other", n), "integer", n)
		if schemaTypeNameMatches(top, other) {
			t.Errorf("depth %d: %s reported as an instance of an unrelated "+
				"chain's %s", n, top, other)
		}
	}
}

// annotationDerivesFrom decides whether an attribute is an xs:ID or xs:IDREF,
// which is what fn:id and fn:idref select on. Truncation returned false, so a
// document whose IDs are named by a deeply derived type had no IDs at all —
// fn:id selected nothing, silently.
func TestDeepChainAnnotationDerivesFrom(t *testing.T) {
	for _, n := range annotationChainDepths {
		top := registerChain(chainNS("id", n), "ID", n)
		if !isIDAnnotation(top) {
			t.Errorf("depth %d: a type %d links above xs:ID is not an ID, so "+
				"fn:id cannot see it", n, n)
		}
		if isIDREFAnnotation(top) {
			t.Errorf("depth %d: an ID-derived type reported as an IDREF", n)
		}

		ref := registerChain(chainNS("idref", n), "IDREF", n)
		if !isIDREFAnnotation(ref) {
			t.Errorf("depth %d: a type %d links above xs:IDREF is not an "+
				"IDREF, so fn:idref cannot see it", n, n)
		}
		if isIDAnnotation(ref) {
			t.Errorf("depth %d: an IDREF-derived type reported as an ID", n)
		}
	}
}

// nodeTypeMatches is the element(*, T) / attribute(*, T) test. Truncation
// returned false, so a node the schema annotated with a deep restriction of T
// was refused by element(*, T) — a match that a shallower schema accepts.
func TestDeepChainNodeTypeMatches(t *testing.T) {
	for _, n := range annotationChainDepths {
		ns := chainNS("node", n)
		top := registerChain(ns, "integer", n)
		node := &xdm.Node{Kind: xdm.KindElement, TypeAnnotation: top}

		// Its own type, an ancestor in the chain, and the built-ins above it.
		for _, want := range []string{
			top,
			xdm.AnnotationName(ns, "T1"),
			xdm.AnnotationName(ns, fmt.Sprintf("T%d", (n+1)/2)),
			"integer",
			"decimal",
		} {
			if !nodeTypeMatches(node, want) {
				t.Errorf("depth %d: element(*, %s) does not match a node "+
					"annotated %s", n, want, top)
			}
		}
		if nodeTypeMatches(node, "date") {
			t.Errorf("depth %d: element(*, xs:date) matched an "+
				"integer-derived node", n)
		}
	}
}

// declaredTypeMatches compares a schema-element() declaration's type, which
// arrives as a bare local name, against a node's annotation chain. Truncation
// returned false the same way.
func TestDeepChainDeclaredTypeMatches(t *testing.T) {
	for _, n := range annotationChainDepths {
		ns := chainNS("declared", n)
		top := registerChain(ns, "integer", n)
		node := &xdm.Node{Kind: xdm.KindElement, TypeAnnotation: top}

		for _, want := range []string{
			"T1",
			fmt.Sprintf("T%d", (n+1)/2),
			fmt.Sprintf("T%d", n),
		} {
			if !declaredTypeMatches(node, want) {
				t.Errorf("depth %d: a declaration of type %s does not match a "+
					"node annotated %s", n, want, top)
			}
		}
		if declaredTypeMatches(node, "NotInTheChain") {
			t.Errorf("depth %d: a declaration of an unrelated type matched", n)
		}
	}
}

// The step counts were nominally there so a schema whose derivations formed a
// cycle could not spin. That must still hold: a visited set is only a fix if
// it terminates on the input the count was written for.
//
// Each case registers a genuine ring — Tn derives from T1 — which no correct
// schema loader would build but which nothing in this package can rule out.
// Every walk must return, and must return the SAFE answer: a ring that never
// reaches the wanted type must answer false rather than looping or guessing.
func TestCyclicAnnotationChainTerminates(t *testing.T) {
	for _, n := range []int{1, 2, 33, 64, 128} {
		ns := fmt.Sprintf("http://example.com/deepchain/cycle/%d", n)
		names := make([]string, n+1)
		for i := 1; i <= n; i++ {
			names[i] = xdm.AnnotationName(ns, fmt.Sprintf("C%d", i))
		}
		for i := 2; i <= n; i++ {
			xdm.RegisterDerivedType(names[i], names[i-1])
		}
		// Close the ring: C1 derives from Cn. With n == 1 that is C1 deriving
		// from itself, the tightest cycle there is.
		xdm.RegisterDerivedType(names[1], names[n])

		top := names[n]
		node := &xdm.Node{Kind: xdm.KindElement, TypeAnnotation: top}

		// Nothing in the ring is grounded in a built-in, so every one of these
		// is a question the ring cannot answer yes to. The requirement is that
		// each returns at all.
		if derivedSubtypeOfThroughSchema(top, "date") {
			t.Errorf("n=%d: a cyclic chain reported as a subtype of xs:date", n)
		}
		if schemaTypeNameMatches(top, "date") {
			t.Errorf("n=%d: a cyclic chain reported as an instance of xs:date", n)
		}
		if isIDAnnotation(top) || isIDREFAnnotation(top) {
			t.Errorf("n=%d: a cyclic chain reported as an ID or IDREF", n)
		}
		if nodeTypeMatches(node, "date") {
			t.Errorf("n=%d: element(*, xs:date) matched a cyclic chain", n)
		}
		if declaredTypeMatches(node, "NotInTheRing") {
			t.Errorf("n=%d: an unrelated declaration matched a cyclic chain", n)
		}

		// The ring still relates its own members to each other: a cycle is a
		// reason to stop, not a reason to answer nothing.
		if n > 1 && !schemaTypeNameMatches(top, names[1]) {
			t.Errorf("n=%d: %s is not an instance of its own base %s",
				n, top, names[1])
		}
	}
}
