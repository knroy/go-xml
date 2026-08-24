// Package xdm implements the XQuery/XPath Data Model (XDM) that XPath 2.0 and
// XSLT 2.0 are defined over.
//
// The central difference from XPath 1.0 is that every value is a *sequence* of
// items, and an item is either a node or a typed atomic value. XPath 1.0 had
// four types (node-set, string, number, boolean) with implicit coercion
// everywhere; 2.0 has the full XML Schema datatype hierarchy with explicit
// promotion rules. Modelling that faithfully here is what lets the rest of the
// engine avoid the 1.0-style "just call ToString" shortcuts that make 2.0
// stylesheets silently produce wrong answers.
package xdm

import "fmt"

// Item is a single member of a sequence: either a Node or an atomic value.
//
// The interface is closed to outside implementations (unexported marker
// method). XDM defines exactly these two kinds of item in XPath 2.0; function
// items arrive in 3.0 and would be added here.
type Item interface {
	isItem()
	// TypeName returns the QName of the item's type, for error messages and
	// instance-of tests.
	TypeName() string
}

// Opaque wraps an arbitrary Go value as an Item.
//
// It exists so that layers above this package can thread their own state
// through an evaluation context, which binds sequences rather than typed
// fields. The XSLT engine uses it for the transform runtime and grouping
// state, which the xpath package cannot name without an import cycle.
//
// An Opaque is not a legal XDM value: it has no string value, does not
// atomise, and must never reach a stylesheet. Every producer binds it under a
// reserved namespace that no stylesheet can spell.
type Opaque struct {
	// Label names the kind of value, for error messages.
	Label string
	// Value is the wrapped payload.
	Value any
}

func (o *Opaque) isItem() {}

// TypeName implements Item.
func (o *Opaque) TypeName() string { return "opaque(" + o.Label + ")" }

// Sequence is an ordered list of items. The empty sequence is a nil or
// zero-length slice; both are treated identically by every operation, so
// callers never have to normalise before comparing.
//
// A sequence is flat: XDM has no nested sequences, and every constructor in
// this package maintains that invariant.
type Sequence []Item

// Empty is the canonical empty sequence.
//
// A function rather than a variable. An exported package-level var of slice
// type is writable by anyone who imports the package, and a single stray
// assignment would corrupt the value every other caller reads -- a
// process-wide fault with no owner and no way to detect it. The value is
// nil, so this compiles to nothing.
func Empty() Sequence { return nil }

// One wraps a single item as a sequence. Named for how often it is needed:
// most XPath operations produce exactly one item and must still return a
// sequence.
func One(it Item) Sequence { return Sequence{it} }

// IsEmpty reports whether s has no items.
func (s Sequence) IsEmpty() bool { return len(s) == 0 }

// First returns the first item, or nil if the sequence is empty. Callers that
// require exactly one item should use Single instead so that a length > 1 is
// reported rather than silently truncated.
func (s Sequence) First() Item {
	if len(s) == 0 {
		return nil
	}
	return s[0]
}

// Single returns the sole item of a one-item sequence. It reports an error for
// any other length, because the places that call it (operands of arithmetic,
// the argument of a function declared to take exactly one item) are precisely
// the places where XPath 2.0 raises XPTY0004 rather than coercing.
func (s Sequence) Single() (Item, error) {
	switch len(s) {
	case 1:
		return s[0], nil
	case 0:
		return nil, fmt.Errorf("XPTY0004: expected exactly one item, got empty sequence")
	default:
		return nil, fmt.Errorf("XPTY0004: expected exactly one item, got %d", len(s))
	}
}

// Concat joins sequences, preserving order and flatness.
func Concat(seqs ...Sequence) Sequence {
	n := 0
	for _, s := range seqs {
		n += len(s)
	}
	if n == 0 {
		return Empty()
	}
	out := make(Sequence, 0, n)
	for _, s := range seqs {
		out = append(out, s...)
	}
	return out
}
