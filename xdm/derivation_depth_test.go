package xdm

import (
	"fmt"
	"testing"
	"time"
)

// The derivation registries are package-level and process-global — that is the
// documented design, because xsd populates them as it loads a schema and xdm
// reads them on every typed value. Tests therefore share them, and two cases
// registering the same annotation name would answer each other's questions.
//
// Every case here builds its names in a namespace that carries its own depth
// and its own walk, so no two cases can collide however they are ordered or
// interleaved.
func chainNS(walk string, depth int) string {
	return fmt.Sprintf("urn:derivation-depth:%s:%d", walk, depth)
}

// registerChain registers depth user-defined restrictions ending at base, and
// returns the annotation name of the OUTERMOST one — the type a document would
// actually be annotated with.
//
// The chain is T1 -> T2 -> ... -> Tdepth -> base, so walking from the returned
// name to a built-in takes exactly depth steps.
func registerChain(walk string, depth int, base string) string {
	ns := chainNS(walk, depth)
	for i := 1; i <= depth; i++ {
		next := base
		if i < depth {
			next = AnnotationName(ns, fmt.Sprintf("T%d", i+1))
		}
		RegisterDerivedType(AnnotationName(ns, fmt.Sprintf("T%d", i)), next)
	}
	return AnnotationName(ns, "T1")
}

// chainDepths spans the old cutoff on both sides. 31 and 32 passed before the
// fix and must keep passing; 33 is the first depth the counter silently
// dropped, and the powers of two above it show the answer does not depend on
// the length of a legal chain at all.
var chainDepths = []int{1, 2, 31, 32, 33, 64, 65, 128, 256, 512, 1024}

// TestAtomizeDeepDerivationChain pins atomicForDerivedAnnotation, the walk
// Node.Atomize uses for a user-defined type.
//
// The semantic property is the one a stylesheet can observe: a node whose
// schema type restricts xs:int, however many named restrictions deep, atomises
// to an xs:integer holding the value — not to xs:untypedAtomic. Asserting the
// call returned non-nil would not catch the bug, because the untyped fallback
// is also non-nil; the type and the value are what change.
func TestAtomizeDeepDerivationChain(t *testing.T) {
	for _, depth := range chainDepths {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			name := registerChain("atomize", depth, "int")

			// An element's string value comes from its descendants, not from
			// Value, so the lexical form has to be a text child.
			n := &Node{Kind: KindElement}
			n.Children = []*Node{{Kind: KindText, Value: "42", Parent: n}}
			n.SetTypeAnnotation(name)

			a := n.Atomize()
			if a == nil {
				t.Fatalf("depth %d: Atomize returned nil", depth)
			}
			if got := a.TypeName(); got != "xs:integer" {
				t.Errorf("depth %d: atomised to %s, want xs:integer "+
					"(the chain stopped being followed)", depth, got)
			}
			if got := a.String(); got != "42" {
				t.Errorf("depth %d: value = %q, want %q", depth, got, "42")
			}
			// The typed value keeps the OUTERMOST name, so that
			// "instance of my:T1" is true rather than only "instance of
			// xs:integer".
			if got := a.Derived(); got != name {
				t.Errorf("depth %d: Derived() = %q, want %q", depth, got, name)
			}
		})
	}
}

// TestAtomizeListDeepItemDerivation pins atomicForLexical, which AtomizeList
// calls once per token.
//
// The semantic property is that every token of the list becomes an
// xs:integer carrying the item type's name — the item type being reached
// through a deep chain must not turn the tokens back into untypedAtomic,
// because "data(@list) instance of my:itemType*" is exactly what that breaks.
func TestAtomizeListDeepItemDerivation(t *testing.T) {
	for _, depth := range chainDepths {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			item := registerChain("lexical", depth, "int")
			list := AnnotationName(chainNS("lexical", depth), "L")
			RegisterListType(list, item)

			n := &Node{Kind: KindAttribute, Value: "1 2 3"}
			n.SetTypeAnnotation(list)

			seq, ok := n.AtomizeList()
			if !ok {
				t.Fatalf("depth %d: AtomizeList reported not-a-list", depth)
			}
			if len(seq) != 3 {
				t.Fatalf("depth %d: got %d items, want 3", depth, len(seq))
			}
			for i, it := range seq {
				a, isAtomic := it.(*Atomic)
				if !isAtomic {
					t.Fatalf("depth %d: item %d is %T, want *Atomic",
						depth, i, it)
				}
				if got := a.TypeName(); got != "xs:integer" {
					t.Errorf("depth %d: item %d atomised to %s, "+
						"want xs:integer", depth, i, got)
				}
				if want := fmt.Sprint(i + 1); a.String() != want {
					t.Errorf("depth %d: item %d = %q, want %q",
						depth, i, a.String(), want)
				}
				if got := a.Derived(); got != item {
					t.Errorf("depth %d: item %d Derived() = %q, want %q",
						depth, i, got, item)
				}
			}
		})
	}
}

// TestListItemTypeDeepDerivation pins listItemType, reached through
// AtomizeList's decision that the annotation names a list at all.
//
// Here the LIST type is the deep one: a chain of restrictions of a list type
// is still a list, and the item type has to be recovered through it. The
// semantic property is that the recovered item type is the declared one, and
// that a node so annotated atomises to the several items of that type rather
// than to one string.
func TestListItemTypeDeepDerivation(t *testing.T) {
	for _, depth := range chainDepths {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			ns := chainNS("listitem", depth)
			base := AnnotationName(ns, "BaseList")
			item := AnnotationName(ns, "Item")
			RegisterListType(base, item)
			RegisterDerivedType(item, "int")

			// A chain of named restrictions standing above the list type.
			name := registerChain("listitem", depth, base)

			if got := listItemType(name); got != item {
				t.Fatalf("depth %d: listItemType(%q) = %q, want %q "+
					"(the walk gave up before reaching the list)",
					depth, name, got, item)
			}

			n := &Node{Kind: KindAttribute, Value: "7 8"}
			n.SetTypeAnnotation(name)
			seq, ok := n.AtomizeList()
			if !ok {
				t.Fatalf("depth %d: AtomizeList reported not-a-list", depth)
			}
			if len(seq) != 2 {
				t.Fatalf("depth %d: got %d items, want 2", depth, len(seq))
			}
			for i, want := range []string{"7", "8"} {
				a := seq[i].(*Atomic)
				if a.TypeName() != "xs:integer" || a.String() != want {
					t.Errorf("depth %d: item %d = %s %q, want xs:integer %q",
						depth, i, a.TypeName(), a.String(), want)
				}
			}
		})
	}
}

// TestAnnotationIDKindDeepDerivation pins annotationIDKind, which decides the
// data model's is-id and is-idrefs properties.
//
// The semantic property is that an attribute whose type restricts xs:ID —
// however deep — is still an ID, so it can be found by the id() function and
// counts toward the uniqueness the schema declared. A deep xs:IDREFS chain has
// to keep answering is-idrefs for the same reason, and neither may be
// mistaken for the other.
func TestAnnotationIDKindDeepDerivation(t *testing.T) {
	for _, depth := range chainDepths {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			idName := registerChain("idkind-id", depth, "ID")
			refName := registerChain("idkind-refs", depth, "IDREFS")

			id := &Node{Kind: KindAttribute, Value: "x1"}
			id.SetTypeAnnotation(idName)
			if !id.IsID {
				t.Errorf("depth %d: a chain over xs:ID lost is-id", depth)
			}
			if id.IsIDREFS {
				t.Errorf("depth %d: a chain over xs:ID gained is-idrefs",
					depth)
			}

			ref := &Node{Kind: KindAttribute, Value: "x1"}
			ref.SetTypeAnnotation(refName)
			if !ref.IsIDREFS {
				t.Errorf("depth %d: a chain over xs:IDREFS lost is-idrefs",
					depth)
			}
			if ref.IsID {
				t.Errorf("depth %d: a chain over xs:IDREFS gained is-id",
					depth)
			}
		})
	}
}

// TestHasSimpleTypeAnnotationDeepDerivation pins HasSimpleTypeAnnotation,
// which XSLT 2.0 section 4.4 consults to decide whether whitespace-only text
// survives xsl:strip-space.
//
// The semantic property is that a deep restriction of xs:string is still a
// simple type — answering false would strip text that is the element's entire
// validated typed value. A type registered as no derivation at all stands as
// the control: it must still answer false, so the test cannot pass by having
// made the function answer true unconditionally.
func TestHasSimpleTypeAnnotationDeepDerivation(t *testing.T) {
	for _, depth := range chainDepths {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			name := registerChain("simpletype", depth, "string")
			if !HasSimpleTypeAnnotation(name) {
				t.Errorf("depth %d: a chain over xs:string was not "+
					"recognised as a simple type", depth)
			}

			unregistered := AnnotationName(chainNS("simpletype", depth),
				"ComplexElementOnly")
			if HasSimpleTypeAnnotation(unregistered) {
				t.Errorf("depth %d: an unregistered complex type was "+
					"called simple", depth)
			}
		})
	}
}

// TestCyclicDerivationTerminates is the case the step counters nominally
// existed for, and the one thing the visited sets must not regress.
//
// A schema registering A -> B and B -> A makes every one of these walks
// traverse a cycle. Each must stop and answer negatively rather than spin; the
// counter did so by exhausting its budget, and a visited set has to do so by
// recognising the repeat. The whole test runs behind a watchdog because the
// failure mode is a hang, which no assertion can catch.
func TestCyclicDerivationTerminates(t *testing.T) {
	const ns = "urn:derivation-depth:cyclic"
	a := AnnotationName(ns, "A")
	b := AnnotationName(ns, "B")
	RegisterDerivedType(a, b)
	RegisterDerivedType(b, a)

	// A list type whose item type cycles, so listItemType walks the cycle
	// too rather than finding the list on its first step.
	cyclicList := AnnotationName(ns, "L")
	RegisterDerivedType(cyclicList, a)

	done := make(chan struct{})
	go func() {
		defer close(done)

		n := &Node{Kind: KindElement}
		n.Children = []*Node{{Kind: KindText, Value: "42", Parent: n}}
		n.TypeAnnotation = a
		// Atomize -> atomicForDerivedAnnotation. Nothing in the cycle is a
		// built-in, so no typed value can be built and the node falls back
		// to untypedAtomic — the documented answer for a type this package
		// cannot construct.
		if got := n.Atomize().TypeName(); got != "xs:untypedAtomic" {
			t.Errorf("cyclic chain atomised to %s, want xs:untypedAtomic",
				got)
		}

		if got := atomicForLexical(a, "42"); got != nil {
			t.Errorf("atomicForLexical on a cyclic chain = %v, want nil", got)
		}
		if got := listItemType(cyclicList); got != "" {
			t.Errorf("listItemType on a cyclic chain = %q, want %q", got, "")
		}
		if isID, isRefs := annotationIDKind(a); isID || isRefs {
			t.Errorf("annotationIDKind on a cyclic chain = (%v, %v), "+
				"want (false, false)", isID, isRefs)
		}
		if HasSimpleTypeAnnotation(a) {
			t.Error("HasSimpleTypeAnnotation on a cyclic chain = true, " +
				"want false")
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// Deliberately fatal rather than a hung test: a spinning walk would
		// otherwise be reported only as the whole package timing out, with
		// nothing naming which walk failed to terminate.
		t.Fatal("a derivation walk did not terminate on a cyclic registration")
	}
}
