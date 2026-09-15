package xdmbuild

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestAppendTextAssertionFires proves the invariant check in appendTextTo is a
// live assertion rather than dead syntax.
//
// It lives in-package and reaches past the exported API on purpose. The whole
// point of the assertion is that no sequence of exported calls can trip it --
// that was established by construction and by probing 308,423 calls across the
// conformance suites for zero mismatches -- so a test that only used exported
// calls could never execute the line, which is exactly the hole this closes.
// The first draft of this change recovered silently here instead, and a
// sabotage of the correctness-critical part of that recovery passed the entire
// suite green because nothing reached it.
//
// So the misuse is staged directly: Node.Value is an exported field, and
// assigning to a text node the builder still owns is precisely the breakage
// the check names. Reaching in is the only way to demonstrate the guard works,
// and a guard that cannot be demonstrated is indistinguishable from one that
// does not.
func TestAppendTextAssertionFires(t *testing.T) {
	b := New(assertPolicy{})
	el := b.StartElement(xdm.QName{Local: "out"})
	el.AppendText("a")

	// Desynchronise the accumulator from the node, as an outside writer of
	// the exported Value field would.
	el.open.Children[0].Value = "tampered"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("appending to a node whose Value was replaced did not " +
				"panic: the invariant check is dead syntax, and the unsafe " +
				"aliasing it guards now rests on nothing")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "text accumulator does not match") {
			t.Fatalf("panicked with %v, which does not name the invariant", r)
		}
	}()
	el.AppendText("b")
}

// assertPolicy is the minimum Policy this file needs; the exported tests use
// their own in the external test package.
type assertPolicy struct{}

func (assertPolicy) Err(Fault, string) error  { return nil }
func (assertPolicy) InheritNamespaces() bool  { return true }
func (assertPolicy) PreserveNamespaces() bool { return true }
func (assertPolicy) PreserveTypes() bool      { return true }
func (assertPolicy) DropEmptyText() bool      { return false }
func (assertPolicy) CountNodes(int) error     { return nil }
