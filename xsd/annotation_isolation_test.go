package xsd

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
)

// The bug these tests pin: the data model's derivation registries
// (derivedPrimitives, listItems in xdm) are process-global and keyed by QName
// alone, so a node's typed value was decided by whichever schema loaded LAST
// rather than by the schema that validated it.
//
// It is not a fallback to something vague. Two schemas may each legitimately
// define {urn:probe}T -- one deriving from xs:decimal, one from xs:string --
// and "10" then atomises as whichever the registry happens to hold: a
// confident, silent, wrong answer. The consequence is an ordinary comparison
// changing its result, since "10" lt "9" is true under string ordering and
// false under numeric.
//
// The fix records the answer ON THE NODE at validation time, which is what
// UnionMember and IsID already do -- and is exactly why unions and IDs were
// immune to this while the other two registries were not.

const isolationNS = "urn:probe"

// probeSchema builds a schema defining {urn:probe}T as a restriction of the
// named built-in, with an element and an attribute carrying it.
func probeSchema(base string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    xmlns:p="%s" targetNamespace="%s"
	    elementFormDefault="qualified">
	  <xs:simpleType name="T">
	    <xs:restriction base="xs:%s"/>
	  </xs:simpleType>
	  <xs:element name="e" type="p:T"/>
	</xs:schema>`, isolationNS, isolationNS, base)
}

// probeListSchema defines {urn:probe}L as a list whose item type is the named
// built-in, which is the listItems half of the same bug.
func probeListSchema(item string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    xmlns:p="%s" targetNamespace="%s"
	    elementFormDefault="qualified">
	  <xs:simpleType name="L">
	    <xs:list itemType="xs:%s"/>
	  </xs:simpleType>
	  <xs:element name="e" type="p:L"/>
	</xs:schema>`, isolationNS, isolationNS, item)
}

// loadProbe assembles a schema through the public entry point, which is what
// registers the derivation into the process-global tables.
func loadProbe(t *testing.T, src string) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	s, err := Load(tree.Root, "probe.xsd", Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// validateProbe parses an instance, validates it with annotation on, and
// returns the annotated element.
func validateProbe(t *testing.T, s *Schema, value string) *xdm.Node {
	t.Helper()
	src := fmt.Sprintf(`<p:e xmlns:p="%s">%s</p:e>`, isolationNS, value)
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	el := tree.Root.Children[0]
	if el.TypeAnnotation == "" {
		t.Fatal("validation did not annotate the element")
	}
	return el
}

// TestDerivedPrimitiveIsolatedFromLaterSchema is the isolation property for
// derivedPrimitives, stated the way the bug was found: validate against a
// schema that says decimal, then load a schema that says string, and the
// already-validated node must still atomise as a decimal.
//
// The assertion is on the TYPE CODE rather than on the lexical form, because
// the two atomisations spell themselves identically -- "10" either way. The
// type is the whole difference, and it is what every comparison downstream
// reads.
func TestDerivedPrimitiveIsolatedFromLaterSchema(t *testing.T) {
	decimalSchema := loadProbe(t, probeSchema("decimal"))
	el := validateProbe(t, decimalSchema, "10")

	before := el.Atomize()
	if before.Type != xdm.TypeDecimal {
		t.Fatalf("precondition: node validated against the decimal schema "+
			"atomised as %v, want %v", before.Type, xdm.TypeDecimal)
	}

	// A second schema redefines the SAME QName as a restriction of xs:string.
	// Nothing about the already-validated node has changed, so nothing about
	// its typed value may change either.
	loadProbe(t, probeSchema("string"))

	after := el.Atomize()
	if after.Type != xdm.TypeDecimal {
		t.Errorf("a later schema redefining %s changed an already-validated "+
			"node's type: atomised as %v, want %v (the node was validated "+
			"against the schema deriving from xs:decimal)",
			xdm.AnnotationName(isolationNS, "T"), after.Type, xdm.TypeDecimal)
	}
}

// TestListItemIsolatedFromLaterSchema is the same property for listItems.
//
// Arity is preserved by the token split whatever the item type says, so
// counting the items cannot detect this bug; the ITEM TYPE is what corrupts,
// and that is what is asserted.
func TestListItemIsolatedFromLaterSchema(t *testing.T) {
	decimalList := loadProbe(t, probeListSchema("decimal"))
	el := validateProbe(t, decimalList, "10 20")

	items, ok := el.AtomizeList()
	if !ok {
		t.Fatal("precondition: a list-typed node did not atomise as a list")
	}
	if len(items) != 2 {
		t.Fatalf("precondition: got %d items, want 2", len(items))
	}
	if got := items[0].(*xdm.Atomic).Type; got != xdm.TypeDecimal {
		t.Fatalf("precondition: item atomised as %v, want %v",
			got, xdm.TypeDecimal)
	}

	loadProbe(t, probeListSchema("string"))

	items, ok = el.AtomizeList()
	if !ok {
		t.Fatal("a later schema made an already-validated list stop being one")
	}
	if len(items) != 2 {
		t.Errorf("item count changed to %d, want 2", len(items))
	}
	if got := items[0].(*xdm.Atomic).Type; got != xdm.TypeDecimal {
		t.Errorf("a later schema redefining %s changed an already-validated "+
			"list's item type: item atomised as %v, want %v",
			xdm.AnnotationName(isolationNS, "L"), got, xdm.TypeDecimal)
	}
}

// TestIsolationSurvivesDeepCopy is the copy-propagation half, and it is the
// one that would be a NEW bug of the same shape rather than the old one.
//
// xdmbuild.DeepCopy is what xsl:copy-of, fn:snapshot and xsl:merge build
// result trees with. A copy that kept the annotation but lost the resolved
// meaning would have to ask the global registry what the name means, which is
// precisely the question that has no stable answer -- so a transform's output
// would silently change type when an unrelated schema loaded.
func TestIsolationSurvivesDeepCopy(t *testing.T) {
	decimalSchema := loadProbe(t, probeSchema("decimal"))
	el := validateProbe(t, decimalSchema, "10")

	copied := xdmbuild.DeepCopy(el)
	if copied.TypeAnnotation != el.TypeAnnotation {
		t.Fatalf("precondition: DeepCopy lost the annotation: %q vs %q",
			copied.TypeAnnotation, el.TypeAnnotation)
	}

	loadProbe(t, probeSchema("string"))

	if got := copied.Atomize().Type; got != xdm.TypeDecimal {
		t.Errorf("a copied node lost its typing to a later schema: "+
			"atomised as %v, want %v", got, xdm.TypeDecimal)
	}
	// The copy must carry the resolved field itself, not merely agree by
	// accident with a registry that happens to hold the right answer.
	if copied.DerivedPrimitive != el.DerivedPrimitive {
		t.Errorf("DeepCopy did not propagate DerivedPrimitive: %q, want %q",
			copied.DerivedPrimitive, el.DerivedPrimitive)
	}
}

// TestListIsolationSurvivesDeepCopy is the copy half for the list registry.
func TestListIsolationSurvivesDeepCopy(t *testing.T) {
	decimalList := loadProbe(t, probeListSchema("decimal"))
	el := validateProbe(t, decimalList, "10 20")

	copied := xdmbuild.DeepCopy(el)
	if copied.ListItem != el.ListItem {
		t.Errorf("DeepCopy did not propagate ListItem: %q, want %q",
			copied.ListItem, el.ListItem)
	}

	loadProbe(t, probeListSchema("string"))

	items, ok := copied.AtomizeList()
	if !ok || len(items) != 2 {
		t.Fatalf("copied list stopped atomising as a list of 2: ok=%v n=%d",
			ok, len(items))
	}
	if got := items[0].(*xdm.Atomic).Type; got != xdm.TypeDecimal {
		t.Errorf("a copied list lost its item type to a later schema: "+
			"item atomised as %v, want %v", got, xdm.TypeDecimal)
	}
}

// TestRegistryFallbackStillWorks pins the other side of the contract: the
// node's own field is an OVERRIDE, never a precondition.
//
// A node annotated by something other than schema assessment -- a DTD
// attribute type, an XSLT validation instruction, a plain struct literal in a
// test -- carries no resolved field, and must keep atomising through the
// registry exactly as it did before this change. Requiring the field would
// have made every such node untyped.
func TestRegistryFallbackStillWorks(t *testing.T) {
	name := xdm.AnnotationName(isolationNS, "Fallback")
	xdm.RegisterDerivedType(name, "decimal")

	// Built by hand, the way a non-validating producer builds one: the
	// annotation is set, the resolved fields are not.
	n := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	n.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "10"})
	n.SetTypeAnnotation(name)
	if n.DerivedPrimitive != "" {
		t.Fatalf("precondition: SetTypeAnnotation should leave the resolved "+
			"field empty, got %q", n.DerivedPrimitive)
	}

	if got := n.Atomize().Type; got != xdm.TypeDecimal {
		t.Errorf("a node with no resolved field stopped atomising through "+
			"the registry: got %v, want %v", got, xdm.TypeDecimal)
	}

	listName := xdm.AnnotationName(isolationNS, "FallbackList")
	xdm.RegisterListType(listName, "decimal")
	l := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "l"}}
	l.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "10 20"})
	l.SetTypeAnnotation(listName)
	items, ok := l.AtomizeList()
	if !ok || len(items) != 2 {
		t.Fatalf("registry list fallback broke: ok=%v n=%d", ok, len(items))
	}
	if got := items[0].(*xdm.Atomic).Type; got != xdm.TypeDecimal {
		t.Errorf("registry list fallback lost the item type: %v, want %v",
			got, xdm.TypeDecimal)
	}
}

// TestReloadingSameSchemaIsStillFine guards the thing the xsd tests do
// constantly: registration is idempotent for identical definitions, so
// reloading a schema must not disturb a node validated against the earlier
// load.
func TestReloadingSameSchemaIsStillFine(t *testing.T) {
	s := loadProbe(t, probeSchema("decimal"))
	el := validateProbe(t, s, "10")

	for i := 0; i < 3; i++ {
		loadProbe(t, probeSchema("decimal"))
		if got := el.Atomize().Type; got != xdm.TypeDecimal {
			t.Fatalf("reload %d changed the node's type: %v, want %v",
				i, got, xdm.TypeDecimal)
		}
	}
}

// TestFreshNodeHasNoStaleResolvedFields pins that the resolved fields are
// ASSIGNED rather than or-ed when a node is re-annotated.
//
// A stale value from a previous assessment is worse than a missing one: a
// missing one falls back to the registry and gets the ordinary answer, while a
// stale one is a confident wrong answer of exactly the kind this whole change
// exists to remove.
func TestFreshNodeHasNoStaleResolvedFields(t *testing.T) {
	n := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	n.SetTypeAnnotationResolved("t1", "decimal", "decimal")
	if n.DerivedPrimitive != "decimal" || n.ListItem != "decimal" {
		t.Fatalf("precondition: fields not set: %q %q",
			n.DerivedPrimitive, n.ListItem)
	}

	// Re-annotated by a producer that has no answer: the old answer must go,
	// not linger.
	n.SetTypeAnnotationResolved("t2", "", "")
	if n.DerivedPrimitive != "" || n.ListItem != "" {
		t.Errorf("re-annotation left stale resolved fields: %q %q, want empty",
			n.DerivedPrimitive, n.ListItem)
	}
}

// TestStripLeavesNoResolvedFields records how the new state interacts with
// XSLT's input-type-annotations="strip".
//
// Stripping is implemented by building fresh nodes carrying only the
// properties section 3.5 preserves (is-id, is-idrefs), so the resolved fields
// are dropped along with the annotation they describe -- which is right: with
// no annotation there is nothing for them to be the meaning OF, and a node
// carrying an erasure for a type it no longer claims would be able to atomise
// as that type again. This asserts the shape of the node the strip path
// produces, without reaching into xslt.
func TestStripLeavesNoResolvedFields(t *testing.T) {
	s := loadProbe(t, probeSchema("decimal"))
	el := validateProbe(t, s, "10")
	if el.DerivedPrimitive == "" {
		t.Fatal("precondition: validation did not record the erasure")
	}

	// The strip path's construction: annotation cleared, is-id and is-idrefs
	// carried, everything else left at the zero value.
	stripped := &xdm.Node{
		Kind: xdm.KindElement, Name: el.Name,
		IsID: el.IsID, IsIDREFS: el.IsIDREFS,
	}
	stripped.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: el.StringValue()})

	if stripped.DerivedPrimitive != "" || stripped.ListItem != "" {
		t.Errorf("a stripped node carries resolved fields: %q %q",
			stripped.DerivedPrimitive, stripped.ListItem)
	}
	if got := stripped.Atomize().Type; got != xdm.TypeUntypedAtomic {
		t.Errorf("a stripped node atomised as %v, want %v",
			got, xdm.TypeUntypedAtomic)
	}
}
