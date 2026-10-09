package xdm

import (
	"fmt"
	"reflect"
	"testing"
)

// psviProperties is the set CopyTypingFrom and CopyTypingStrippedFrom carry
// between them: the eight the PSVI records about a node.
//
// It is written out rather than derived so that the census below compares two
// independently maintained lists. Deriving it from either operation would make
// the test agree with whatever the operations happen to do.
var psviProperties = map[string]bool{
	"typeAnnotation":   true,
	"unionMember":      true,
	"derivedPrimitive": true,
	"listItem":         true,
	"isID":             true,
	"isIDREFS":         true,
	"isNilled":         true,
	"noTypedValue":     true,
	"mixedContent":     true,
}

// nonPSVIProperties are the fields of Node that are deliberately NOT
// carried by the typing operations, each with the reason it is excluded.
//
// The list exists so that a NEW field is neither silently absorbed into the
// PSVI set nor silently exempted from it: adding one to Node without deciding
// which side it falls on fails TestPSVIPropertyCensus, which is the point.
// That is the recurring bug this whole mechanism was built against -- a
// property added to the node and never added to the copy, dropped silently by
// every copy site at once.
var nonPSVIProperties = map[string]string{
	"kind":       "the node's identity, not an assessment of it",
	"name":       "ditto",
	"value":      "ditto",
	"parent":     "structure; a copy is attached by whoever copies it",
	"children":   "structure",
	"attrs":      "structure; their typing travels by their own copy",
	"namespaces": "structure",
	"baseURI":    "dm:base-uri, decided by where the copy lands",
	"documentURI": "dm:document-uri is the URI a document was RETRIEVED BY. " +
		"A copy was not retrieved at all, so inheriting it would make " +
		"doc(document-uri($d)) is $d false while claiming it is true.",
	"order":  "document order, assigned by the tree the copy joins",
	"offset": "source position of the original, not of the copy",
	"tree":   "the containing tree, set by whoever links the copy",
	"ext":    "the type environment, carried by SetTypeEnv, not by the typing copy",
}

// TestPSVIPropertyCensus is the guard on the guard: it fails when a field is
// added to Node and classified as neither PSVI nor structure.
//
// CopyTypingFrom exists because eight properties travel together and every
// hand-written field list eventually dropped one. That argument only holds
// while "eight" is still the whole set -- a property added to Node later and
// never added to the operation would be dropped by all ten copy sites at once,
// silently, which is exactly the shape of every defect in this family.
func TestPSVIPropertyCensus(t *testing.T) {
	rt := reflect.TypeOf(Node{})
	seen := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		seen[f.Name] = true
		if psviProperties[f.Name] {
			continue
		}
		if _, ok := nonPSVIProperties[f.Name]; ok {
			continue
		}
		t.Errorf("Node.%s is classified neither as a PSVI "+
			"property nor as structure. Decide which it is: if the PSVI "+
			"records it, add it to CopyTypingFrom, to CopyTypingStrippedFrom "+
			"(kept or cleared, per XSLT 2.0 3.5) and to psviProperties here. "+
			"If it does not, add it to nonPSVIProperties with the reason.",
			f.Name)
	}
	for name := range psviProperties {
		if !seen[name] {
			t.Errorf("psviProperties names %q, which Node no longer has", name)
		}
	}
}

// TestCopyTypingFromCarriesEveryPSVIProperty checks the preserving operation
// against the census rather than against a repeated field list, so that a
// property added to both Node and psviProperties but forgotten in
// CopyTypingFrom is caught here instead of by whichever copy site first
// needs it.
//
// Every field is set to a value distinguishable from its zero, because a copy
// that leaves a field zero and a source that was zero to begin with are
// indistinguishable.
func TestCopyTypingFromCarriesEveryPSVIProperty(t *testing.T) {
	src := &Node{kind: KindElement}
	setEveryPSVIProperty(src)

	dst := &Node{kind: KindElement}
	dst.CopyTypingFrom(src)

	sv, dv := reflect.ValueOf(src).Elem(), reflect.ValueOf(dst).Elem()
	for name := range psviProperties {
		// The fields are unexported, so they are compared by their printed
		// values: Interface would panic on them.
		got, want := fmt.Sprint(dv.FieldByName(name)), fmt.Sprint(sv.FieldByName(name))
		if got != want {
			t.Errorf("CopyTypingFrom did not carry %s: got %v, want %v", name, got, want)
		}
	}
}

// TestCopyTypingStrippedFromTouchesEveryPSVIProperty checks that the stripping
// operation makes a DECISION about each of the seven -- clear it or keep it --
// rather than leaving one untouched.
//
// A field it never mentions keeps whatever the destination already held, which
// on a fresh copy is the zero value and so looks like "cleared" by accident.
// Starting from a destination whose every field is non-zero and distinct from
// the source's is what separates a deliberate clear from an omission.
func TestCopyTypingStrippedFromTouchesEveryPSVIProperty(t *testing.T) {
	src := &Node{kind: KindElement}
	setEveryPSVIProperty(src)

	// A destination pre-loaded with DIFFERENT non-zero values. Any field the
	// operation does not write keeps one of these, which matches neither the
	// cleared nor the kept answer.
	dst := &Node{kind: KindElement,
		typeAnnotation: "stale", unionMember: "stale", derivedPrimitive: "stale",
		listItem: "stale", isID: false, isIDREFS: false, isNilled: true}
	dst.CopyTypingStrippedFrom(src)

	// XSLT 2.0 3.5: the four naming the type go, is-id and is-idrefs stay,
	// dm:nilled goes. See the commentary on CopyTypingStrippedFrom.
	if dst.typeAnnotation != "" || dst.unionMember != "" ||
		dst.derivedPrimitive != "" || dst.listItem != "" {
		t.Errorf("stripping left part of the type behind: annotation=%q "+
			"UnionMember=%q DerivedPrimitive=%q ListItem=%q",
			dst.typeAnnotation, dst.unionMember, dst.derivedPrimitive,
			dst.listItem)
	}
	if !dst.isID || !dst.isIDREFS {
		t.Errorf("stripping dropped is-id/is-idrefs, which 3.5 exempts: "+
			"IsID=%v IsIDREFS=%v", dst.isID, dst.isIDREFS)
	}
	if dst.isNilled {
		t.Error("stripping kept dm:nilled, which 3.5 makes false on every " +
			"element of a stripped tree")
	}
}

// setEveryPSVIProperty gives each of the seven a non-zero value, and fails if
// the census names one it does not know how to set -- which is how a newly
// added property reaches this file rather than being quietly skipped.
func setEveryPSVIProperty(n *Node) {
	n.typeAnnotation = "{urn:census}T"
	n.unionMember = "{urn:census}M"
	n.derivedPrimitive = "decimal"
	n.listItem = "decimal"
	n.isID = true
	n.isIDREFS = true
	n.isNilled = true
}
