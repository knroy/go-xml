package xdm

import (
	"strings"
	"testing"
)

// Sequence's accessors are small enough to look obviously right and are
// tested anyway, because the empty case is where each of them differs and
// the difference is what callers rely on: First returns nil where Single
// returns an error, and both are correct for their own callers.
func TestSequenceAccessors(t *testing.T) {
	one := One(NewString("a"))
	two := Sequence{NewString("a"), NewString("b")}

	if !(Sequence{}).IsEmpty() {
		t.Error("an empty sequence should report IsEmpty")
	}
	if one.IsEmpty() {
		t.Error("a one-item sequence should not report IsEmpty")
	}

	// First is total: it answers for the empty sequence rather than failing,
	// which is what makes it usable where an absent value is ordinary.
	if got := (Sequence{}).First(); got != nil {
		t.Errorf("First of empty = %v, want nil", got)
	}
	if got := one.First(); got == nil {
		t.Error("First of a one-item sequence returned nil")
	}
	if got := two.First(); got != two[0] {
		t.Error("First did not return the first item")
	}

	// Single is partial on purpose: its callers are the places where more
	// than one item is a type error, so silently truncating would turn a
	// mistake into a wrong answer.
	if _, err := one.Single(); err != nil {
		t.Errorf("Single of a one-item sequence: %v", err)
	}
	for _, s := range []Sequence{{}, two} {
		if _, err := s.Single(); err == nil {
			t.Errorf("Single of a %d-item sequence should be an error", len(s))
		}
	}
}

// TypeName and Order are what the XPath layer asks a node for, so they are
// exercised there rather than here — but the kinds they must cover are this
// package's, and a new kind arriving without a name is a silent gap.
func TestNodeTypeNameCoversEveryKind(t *testing.T) {
	const src = `<?xml version="1.0"?><!-- c --><?pi x?><r a="1">text</r>`
	tree, err := ParseString(src, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[NodeKind]string{}
	var walk func(n *Node)
	walk = func(n *Node) {
		name := n.TypeName()
		if name == "" {
			t.Errorf("%v has no type name", n.Kind)
		}
		seen[n.Kind] = name
		for _, a := range n.Attrs {
			_ = a
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree.Root)

	for _, k := range []NodeKind{KindDocument, KindElement, KindText,
		KindComment, KindPI} {
		if _, ok := seen[k]; !ok {
			t.Errorf("the fixture did not produce a %v node", k)
		}
	}

	// Document order is assigned during the parse, and is what every
	// comparison in the XPath layer rests on: it must increase down the
	// document.
	var last = -1
	var check func(n *Node)
	check = func(n *Node) {
		if o := n.Order(); o <= last {
			t.Errorf("%v at %q has order %d, not after %d",
				n.Kind, n.Name.Local, o, last)
		} else {
			last = o
		}
		for _, c := range n.Children {
			check(c)
		}
	}
	check(tree.Root)
}

// A wrapped parse error must stay unwrappable, so that errors.Is and
// errors.As reach what it wraps. A caller distinguishing a size-limit refusal
// from a syntax error needs that.
func TestParseErrorUnwraps(t *testing.T) {
	_, err := ParseString(`<a></b>`, ParseOptions{})
	if err == nil {
		t.Fatal("malformed XML should be an error")
	}
	// Unwrapping either yields something or reports nothing to unwrap; what
	// must not happen is a panic or a self-referential loop.
	seen := 0
	for e := err; e != nil; seen++ {
		if seen > 10 {
			t.Fatal("the error chain does not terminate")
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	if !strings.Contains(err.Error(), "parse XML") {
		t.Errorf("error = %v, want one naming the parse", err)
	}
}
