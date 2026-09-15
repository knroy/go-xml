package dtd

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// nestedModel builds a DOCTYPE whose one element declaration has a content
// model nested n parentheses deep.
//
// The full "name [...]" wrapper is deliberate: Parse skips declarations it
// does not recognise, so a bare "<!ELEMENT ...>" never reaches parseModel and
// every assertion below would pass without testing anything. The control case
// checks len(d.Elements) for exactly that reason.
func nestedModel(n int) string {
	return "r [<!ELEMENT r " + strings.Repeat("(", n) + "a" +
		strings.Repeat(")", n) + ">]"
}

// A content model past maxModelDepth is refused with a clear error rather than
// crashing the process.
//
// parseCP and parseGroup are mutually recursive with no bound before this, and
// a Go stack overflow is a "fatal error" that recover() cannot catch: a server
// embedding this library dies outright, taking every in-flight request with
// it. The input here is just past the bound so the refusal is clean and cannot
// take the test binary down; TestModelDepthSurvivesExtremeNesting covers the
// input that used to crash.
func TestModelDepthRefusesDeepNesting(t *testing.T) {
	_, err := Parse(nestedModel(maxModelDepth + 1))
	if err == nil {
		t.Fatal("a content model past the depth bound parsed clean")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error does not wrap ErrResourceLimit: %v", err)
	}
	if !strings.Contains(err.Error(), "content model nests more than") {
		t.Errorf("error does not name the limit it hit: %v", err)
	}
}

// A legitimately nested model still parses, and produces the element it
// declares.
//
// Six levels is the deepest content model in testdata (the TEI Lite DTD), so
// the depths here bracket every real DTD the suites carry. Exactly at the
// bound must also be accepted: the refusal is for models past it.
func TestModelDepthAcceptsRealisticNesting(t *testing.T) {
	for _, n := range []int{1, 6, 64, maxModelDepth} {
		d, err := Parse(nestedModel(n))
		if err != nil {
			t.Fatalf("nesting %d: %v", n, err)
		}
		// Proof the declaration reached parseModel at all, rather than being
		// skipped as unrecognised.
		if len(d.Elements) != 1 {
			t.Fatalf("nesting %d: got %d elements, want 1",
				n, len(d.Elements))
		}
		if _, ok := d.Elements["r"]; !ok {
			t.Errorf("nesting %d: element r was not declared", n)
		}
	}
}

// The input that crashed the process before the bound existed now returns an
// error, in-process and without a stack overflow.
//
// Five million bytes is the reproduction: at Go's default 1 GB stack ceiling
// this printed "fatal error: stack overflow" and killed the process. The bound
// refuses it after 1000 frames, so it is now safe to run here directly — if
// the bound were removed this test would not fail, it would take the whole
// package binary down with it, which is itself the signal.
func TestModelDepthSurvivesExtremeNesting(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a 5 MB model")
	}
	_, err := Parse(nestedModel(2_500_000))
	if err == nil {
		t.Fatal("a model nested 2,500,000 deep parsed clean")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error does not wrap ErrResourceLimit: %v", err)
	}
}
