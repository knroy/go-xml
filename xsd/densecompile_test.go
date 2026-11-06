package xsd

import (
	"errors"
	"runtime"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A dense content model must be refused before it is built in full. A
// sequence of 2048 optional elements fits the position budget, but its follow
// sets are as wide as the sequence, and checkUPA refuses any state wider than
// maxUPAStateWidth. The compile used to build every edge first: 229 MB and
// about half a second, for a refusal the width gate then made in
// microseconds. Bounding the build at the same width makes the refusal cost
// what the gate allows.
func TestDenseModelRefusedBeforeFullCompile(t *testing.T) {
	doc, err := xdm.ParseString(genOptionalSequence(2048), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, lerr := Load(doc.Root, "", Options{})
	runtime.ReadMemStats(&after)
	if !errors.Is(lerr, xdm.ErrResourceLimit) {
		t.Fatalf("Load = %v, want a refusal wrapping ErrResourceLimit", lerr)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 32<<20 {
		t.Errorf("refusing a 2048-wide dense model allocated %d MB, want under 32 MB: "+
			"the model was built in full before the width gate refused it", got>>20)
	}
	// The narrow end of the same shape still loads.
	doc, _ = xdm.ParseString(genOptionalSequence(200), xdm.ParseOptions{})
	if _, err := Load(doc.Root, "", Options{}); err != nil {
		t.Errorf("a 200-wide optional sequence is within maxUPAStateWidth and must load: %v", err)
	}
}
