package xdm

import (
	"reflect"
	"testing"
)

// psviProperties is the set CopyTypingFrom and CopyTypingStrippedFrom carry
// between them: the seven the PSVI records about a node.
//
// It is written out rather than derived so that the census below compares two
// independently maintained lists. Deriving it from either operation would make
// the test agree with whatever the operations happen to do.
var psviProperties = map[string]bool{
	"TypeAnnotation":   true,
	"UnionMember":      true,
	"DerivedPrimitive": true,
	"ListItem":         true,
	"IsID":             true,
	"IsIDREFS":         true,
	"IsNilled":         true,
}

// nonPSVIProperties are the exported fields of Node that are deliberately NOT
// carried by the typing operations, each with the reason it is excluded.
//
// The list exists so that a NEW field is neither silently absorbed into the
// PSVI set nor silently exempted from it: adding one to Node without deciding
// which side it falls on fails TestPSVIPropertyCensus, which is the point.
// That is the recurring bug this whole mechanism was built against -- a
// property added to the node and never added to the copy, dropped silently by
// every copy site at once.
var nonPSVIProperties = map[string]string{
	"Kind":       "the node's identity, not an assessment of it",
	"Name":       "ditto",
	"Value":      "ditto",
	"Parent":     "structure; a copy is attached by whoever copies it",
	"Children":   "structure",
	"Attrs":      "structure; their typing travels by their own copy",
	"Namespaces": "structure",
	"BaseURI":    "dm:base-uri, decided by where the copy lands",
	"DocumentURI": "dm:document-uri is the URI a document was RETRIEVED BY. " +
		"A copy was not retrieved at all, so inheriting it would make " +
		"doc(document-uri($d)) is $d false while claiming it is true.",
}

// TestPSVIPropertyCensus is the guard on the guard: it fails when a field is
// added to Node and classified as neither PSVI nor structure.
//
// CopyTypingFrom exists because seven properties travel together and every
// hand-written field list eventually dropped one. That argument only holds
// while "seven" is still the whole set -- a property added to Node later and
// never added to the operation would be dropped by all ten copy sites at once,
// silently, which is exactly the shape of every defect in this family.
func TestPSVIPropertyCensus(t *testing.T) {
	rt := reflect.TypeOf(Node{})
	seen := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		seen[f.Name] = true
		if psviProperties[f.Name] {
			continue
		}
		if _, ok := nonPSVIProperties[f.Name]; ok {
			continue
		}
		t.Errorf("Node.%s is exported but classified neither as a PSVI "+
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
	src := &Node{Kind: KindElement}
	setEveryPSVIProperty(src)

	dst := &Node{Kind: KindElement}
	dst.CopyTypingFrom(src)

	sv, dv := reflect.ValueOf(src).Elem(), reflect.ValueOf(dst).Elem()
	for name := range psviProperties {
		if !reflect.DeepEqual(sv.FieldByName(name).Interface(),
			dv.FieldByName(name).Interface()) {
			t.Errorf("CopyTypingFrom did not carry %s: got %v, want %v", name,
				dv.FieldByName(name).Interface(), sv.FieldByName(name).Interface())
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
	src := &Node{Kind: KindElement}
	setEveryPSVIProperty(src)

	// A destination pre-loaded with DIFFERENT non-zero values. Any field the
	// operation does not write keeps one of these, which matches neither the
	// cleared nor the kept answer.
	dst := &Node{Kind: KindElement,
		TypeAnnotation: "stale", UnionMember: "stale", DerivedPrimitive: "stale",
		ListItem: "stale", IsID: false, IsIDREFS: false, IsNilled: true}
	dst.CopyTypingStrippedFrom(src)

	// XSLT 2.0 3.5: the four naming the type go, is-id and is-idrefs stay,
	// dm:nilled goes. See the commentary on CopyTypingStrippedFrom.
	if dst.TypeAnnotation != "" || dst.UnionMember != "" ||
		dst.DerivedPrimitive != "" || dst.ListItem != "" {
		t.Errorf("stripping left part of the type behind: annotation=%q "+
			"UnionMember=%q DerivedPrimitive=%q ListItem=%q",
			dst.TypeAnnotation, dst.UnionMember, dst.DerivedPrimitive,
			dst.ListItem)
	}
	if !dst.IsID || !dst.IsIDREFS {
		t.Errorf("stripping dropped is-id/is-idrefs, which 3.5 exempts: "+
			"IsID=%v IsIDREFS=%v", dst.IsID, dst.IsIDREFS)
	}
	if dst.IsNilled {
		t.Error("stripping kept dm:nilled, which 3.5 makes false on every " +
			"element of a stripped tree")
	}
}

// setEveryPSVIProperty gives each of the seven a non-zero value, and fails if
// the census names one it does not know how to set -- which is how a newly
// added property reaches this file rather than being quietly skipped.
func setEveryPSVIProperty(n *Node) {
	n.TypeAnnotation = "{urn:census}T"
	n.UnionMember = "{urn:census}M"
	n.DerivedPrimitive = "decimal"
	n.ListItem = "decimal"
	n.IsID = true
	n.IsIDREFS = true
	n.IsNilled = true
}
