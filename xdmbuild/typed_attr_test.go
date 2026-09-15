package xdmbuild_test

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// AddAttributeWithTyping is the typed builder entry point of audit finding 24.
// The finding is that AddAttributeTyped accepts a type NAME and nothing else,
// so everything an assessment concluded beyond the name -- the union member
// that accepted the value, the built-in the type erases to, the item type of a
// list, is-id, is-idrefs, nilled, has-no-typed-value -- had to be recovered
// afterwards from xdm's process-global derivation registries. Those are keyed
// by QName alone across the whole process, so they answer for whichever schema
// registered the name most recently rather than for the schema that validated
// the node.
//
// The tests below pin the two halves of the fix separately:
//
//   - the name-only path must behave exactly as it did, because it is a public
//     API under a stability guarantee, and
//   - the typed path must reach the right answer WITHOUT the registries, which
//     is the only thing that makes the new entry point worth having. A typed
//     attribute is therefore built for a type name that is never registered at
//     all; if the registries were still being consulted the answer would be
//     xs:untypedAtomic.

// unregisteredNS is a namespace no test in this package registers a type in,
// so that an annotation built from it cannot be resolved by any registry.
const unregisteredNS = "urn:go-xml:finding24:never-registered"

// attrBuiltWith runs one build through a name-only or typed call and returns
// the attribute that landed on the element.
func attrBuiltWith(t *testing.T, add func(*xdmbuild.Builder) error) *xdm.Node {
	t.Helper()
	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(xdm.QName{Local: "e"})
	if err := add(el); err != nil {
		t.Fatalf("adding the attribute failed: %v", err)
	}
	seq := b.Sequence()
	if len(seq) != 1 {
		t.Fatalf("builder produced %d items, want 1", len(seq))
	}
	got, ok := seq[0].(*xdm.Node)
	if !ok || got.Kind != xdm.KindElement {
		t.Fatalf("builder produced %T, want an element node", seq[0])
	}
	if len(got.Attrs) != 1 {
		t.Fatalf("element carries %d attributes, want 1", len(got.Attrs))
	}
	return got.Attrs[0]
}

// TestAddAttributeTypedAgreesWithTypedPath is the compatibility half. The same
// attribute built through the old name-only call and through the new typed
// call with only the annotation filled in must be indistinguishable: same
// eight PSVI properties, same atomised value, same cast result. The old call
// is documented as a convenience wrapper over the new one, and a wrapper that
// produced a different node would be a silent behaviour change in a public API
// promised to be additive.
func TestAddAttributeTypedAgreesWithTypedPath(t *testing.T) {
	name := xdm.QName{Local: "a"}
	const value = "10"

	// "decimal" is a built-in, so it resolves with or without any registry
	// and the two paths are being compared on equal footing.
	old := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
		return b.AddAttributeTyped(name, value, "decimal")
	})
	nu := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
		return b.AddAttributeWithTyping(name, value,
			xdm.Typing{TypeAnnotation: "decimal"})
	})

	if got, want := xdm.TypingOf(nu), xdm.TypingOf(old); got != want {
		t.Fatalf("typed path recorded %+v, name-only path recorded %+v",
			got, want)
	}

	oa, na := old.Atomize(), nu.Atomize()
	if oa == nil || na == nil {
		t.Fatalf("atomisation returned nil: name-only=%v typed=%v", oa, na)
	}
	if oa.Type != na.Type || oa.String() != na.String() ||
		oa.Derived() != na.Derived() || oa.DerivedMember() != na.DerivedMember() {
		t.Fatalf("atomised values differ: name-only %s/%q/%q/%q, typed %s/%q/%q/%q",
			oa.TypeName(), oa.String(), oa.Derived(), oa.DerivedMember(),
			na.TypeName(), na.String(), na.Derived(), na.DerivedMember())
	}

	oc, oerr := xpath.CastAtomic(oa, xdm.TypeInteger)
	nc, nerr := xpath.CastAtomic(na, xdm.TypeInteger)
	if (oerr == nil) != (nerr == nil) {
		t.Fatalf("casts disagree on success: name-only err=%v, typed err=%v",
			oerr, nerr)
	}
	if oerr == nil && (oc.Type != nc.Type || oc.String() != nc.String()) {
		t.Fatalf("cast results differ: name-only %s/%q, typed %s/%q",
			oc.TypeName(), oc.String(), nc.TypeName(), nc.String())
	}
}

// TestAddAttributeTypedNameOnlyStillNeedsTheRegistry states the problem the
// finding describes, so that the next test's result means something. The
// annotation names a type nothing ever registered, and the name-only call has
// no way to learn what it means: atomisation falls back to xs:untypedAtomic.
//
// This is not a bug being pinned -- falling back is the documented behaviour
// for a node whose annotation carries no resolved meaning. It is the BASELINE:
// it shows that the name genuinely is not enough, so that when the typed call
// gets it right in the next test, the resolution can only have come from the
// metadata the caller supplied.
func TestAddAttributeTypedNameOnlyStillNeedsTheRegistry(t *testing.T) {
	unknown := xdm.AnnotationName(unregisteredNS, "Money")
	a := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
		return b.AddAttributeTyped(xdm.QName{Local: "a"}, "10", unknown)
	})
	at := a.Atomize()
	if at == nil {
		t.Fatal("atomisation returned nil")
	}
	if at.Type != xdm.TypeUntypedAtomic {
		t.Fatalf("an unregistered annotation atomised as %s; the name-only "+
			"path was expected to have nothing to resolve it with, which is "+
			"what makes the typed path's success meaningful",
			at.TypeName())
	}
}

// TestAddAttributeWithTypingAvoidsTheGlobalRegistries is the finding. The type
// is never registered -- no RegisterDerivedType, no RegisterUnionType, no
// RegisterListType names it, and the previous test shows the registries cannot
// answer for it -- yet the attribute atomises as a decimal and casts like one,
// because the caller handed over the resolved primitive with the name.
//
// If this test ever passes only because some other test in the process
// registered the name, the previous test fails first and says so.
func TestAddAttributeWithTypingAvoidsTheGlobalRegistries(t *testing.T) {
	unknown := xdm.AnnotationName(unregisteredNS, "Money")
	a := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
		return b.AddAttributeWithTyping(xdm.QName{Local: "a"}, "10",
			xdm.Typing{TypeAnnotation: unknown, DerivedPrimitive: "decimal"})
	})

	if a.DerivedPrimitive != "decimal" {
		t.Fatalf("the builder dropped DerivedPrimitive: got %q, want %q",
			a.DerivedPrimitive, "decimal")
	}
	at := a.Atomize()
	if at == nil {
		t.Fatal("atomisation returned nil")
	}
	if at.Type != xdm.TypeDecimal {
		t.Fatalf("an unregistered type with a supplied primitive atomised as "+
			"%s, want xs:decimal: the typed path is still going through the "+
			"process-global registries", at.TypeName())
	}
	// The value keeps the name it was validated AS, not the primitive the
	// walk stopped at, so "instance of" answers for the schema's own type.
	if at.Derived() != unknown {
		t.Fatalf("atomised value claims derived type %q, want %q",
			at.Derived(), unknown)
	}
	c, err := xpath.CastAtomic(at, xdm.TypeInteger)
	if err != nil {
		t.Fatalf("casting the typed value to xs:integer failed: %v", err)
	}
	if c.String() != "10" {
		t.Fatalf("cast produced %q, want %q", c.String(), "10")
	}
}

// TestAddAttributeWithTypingCarriesEveryProperty pins the whole field list
// rather than the one property the atomisation test happens to exercise.
// Every one of the eight has been dropped by some hand-written field list in
// this repository before, always silently, so the builder's own list is
// asserted in full and on all three of its construction paths: the parentless
// attribute, the ordinary one, and the duplicate that replaces an earlier.
func TestAddAttributeWithTypingCarriesEveryProperty(t *testing.T) {
	want := xdm.Typing{
		TypeAnnotation:   xdm.AnnotationName(unregisteredNS, "U"),
		UnionMember:      "integer",
		DerivedPrimitive: "anySimpleType",
		ListItem:         "decimal",
		IsID:             true,
		IsIDREFS:         true,
		IsNilled:         true,
		NoTypedValue:     true,
	}
	name := xdm.QName{Local: "a"}

	t.Run("on an element", func(t *testing.T) {
		a := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
			return b.AddAttributeWithTyping(name, "1", want)
		})
		if got := xdm.TypingOf(a); got != want {
			t.Fatalf("recorded %+v, want %+v", got, want)
		}
	})

	t.Run("parentless", func(t *testing.T) {
		// No open element, so the attribute goes into the item list instead.
		b := xdmbuild.New(xsltLike{})
		if err := b.AddAttributeWithTyping(name, "1", want); err != nil {
			t.Fatalf("adding a parentless attribute failed: %v", err)
		}
		seq := b.Sequence()
		if len(seq) != 1 {
			t.Fatalf("builder produced %d items, want 1", len(seq))
		}
		a, ok := seq[0].(*xdm.Node)
		if !ok || a.Kind != xdm.KindAttribute {
			t.Fatalf("builder produced %T, want an attribute node", seq[0])
		}
		if got := xdm.TypingOf(a); got != want {
			t.Fatalf("recorded %+v, want %+v", got, want)
		}
	})

	t.Run("replacing a duplicate", func(t *testing.T) {
		// The XSLT policy lets the later attribute silently replace the
		// earlier, which is a fourth place the typing is written. The first
		// add deliberately carries DIFFERENT typing, so that a replacement
		// that updated only the value would be caught.
		a := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
			if err := b.AddAttributeTyped(name, "0", "string"); err != nil {
				return err
			}
			return b.AddAttributeWithTyping(name, "1", want)
		})
		if a.Value != "1" {
			t.Fatalf("replacement kept value %q, want %q", a.Value, "1")
		}
		if got := xdm.TypingOf(a); got != want {
			t.Fatalf("recorded %+v, want %+v", got, want)
		}
	})
}

// TestAddAttributeTypedDoesNotDeriveIsID pins the one behaviour of the
// name-only call that a careless wrapper would have changed. It assigns the
// annotation rather than going through Node.SetTypeAnnotation, so it does NOT
// turn on is-id for an xs:ID-annotated attribute, and callers that want that
// property set say so themselves. Routing the old call through a new typed one
// that derived is-id would have been a silent behaviour change in a public API
// promised to be additive over 1.1.
func TestAddAttributeTypedDoesNotDeriveIsID(t *testing.T) {
	a := attrBuiltWith(t, func(b *xdmbuild.Builder) error {
		return b.AddAttributeTyped(xdm.QName{Local: "a"}, "x1", "ID")
	})
	if a.IsID {
		t.Fatal("AddAttributeTyped derived is-id from the annotation; it " +
			"never did before, and the typed path must not have changed that")
	}
	if a.TypeAnnotation != "ID" {
		t.Fatalf("annotation is %q, want %q", a.TypeAnnotation, "ID")
	}
}
