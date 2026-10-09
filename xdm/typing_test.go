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
	"annotation":       true,
	"unionMember":      true,
	"derivedPrimitive": true,
	"listItem":         true,
	"isID":             true,
	"isIDREFS":         true,
	"isNilled":         true,
	"noTypedValue":     true,
	"mixedContent":     true,
}

var nonPSVIProperties = map[string]string{
	"env": "the type environment, carried by SetTypeEnv, not by the typing copy",
}

// The typing a node carries lives in nodeTyping; every field of it is either
// a PSVI property the typing copies carry or named here as one they do not.
func TestPSVIPropertyCensus(t *testing.T) {
	rt := reflect.TypeOf(nodeTyping{})
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
		t.Errorf("nodeTyping.%s is classified neither as a PSVI "+
			"property nor as something else. Decide which it is: if the PSVI "+
			"records it, add it to Typing, ApplyTyping, TypingOf, "+
			"CopyTypingStrippedFrom (kept or cleared, per XSLT 2.0 3.5) and to "+
			"psviProperties here. If it does not, add it to nonPSVIProperties "+
			"with the reason.", f.Name)
	}
	for name := range psviProperties {
		if !seen[name] {
			t.Errorf("psviProperties names %q, which nodeTyping no longer has", name)
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
	src := NewNode(KindElement, QName{}, "")
	setEveryPSVIProperty(src)

	dst := NewNode(KindElement, QName{}, "")
	dst.CopyTypingFrom(src)

	sv, dv := reflect.ValueOf(src.typ()).Elem(), reflect.ValueOf(dst.typ()).Elem()
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
	src := NewNode(KindElement, QName{}, "")
	setEveryPSVIProperty(src)

	// A destination pre-loaded with DIFFERENT non-zero values. Any field the
	// operation does not write keeps one of these, which matches neither the
	// cleared nor the kept answer.
	dst := NewNode(KindElement, QName{}, "")
	dst.ApplyTyping(Typing{TypeAnnotation: "stale", UnionMember: "stale",
		DerivedPrimitive: "stale", ListItem: "stale", IsNilled: true})
	dst.CopyTypingStrippedFrom(src)

	// XSLT 2.0 3.5: the four naming the type go, is-id and is-idrefs stay,
	// dm:nilled goes. See the commentary on CopyTypingStrippedFrom.
	if dst.TypeAnnotation() != "" || dst.UnionMember() != "" ||
		dst.DerivedPrimitive() != "" || dst.ListItem() != "" {
		t.Errorf("stripping left part of the type behind: annotation=%q "+
			"UnionMember=%q DerivedPrimitive=%q ListItem=%q",
			dst.TypeAnnotation(), dst.UnionMember(), dst.DerivedPrimitive(),
			dst.ListItem())
	}
	if !dst.IsID() || !dst.IsIDREFS() {
		t.Errorf("stripping dropped is-id/is-idrefs, which 3.5 exempts: "+
			"IsID=%v IsIDREFS=%v", dst.IsID(), dst.IsIDREFS())
	}
	if dst.IsNilled() {
		t.Error("stripping kept dm:nilled, which 3.5 makes false on every " +
			"element of a stripped tree")
	}
}

// setEveryPSVIProperty gives each of the seven a non-zero value, and fails if
// the census names one it does not know how to set -- which is how a newly
// added property reaches this file rather than being quietly skipped.
func setEveryPSVIProperty(n *Node) {
	n.ApplyTyping(Typing{TypeAnnotation: "{urn:census}T", UnionMember: "{urn:census}M",
		DerivedPrimitive: "decimal", ListItem: "decimal", IsID: true, IsIDREFS: true,
		IsNilled: true, NoTypedValue: true, MixedContent: true})
}
