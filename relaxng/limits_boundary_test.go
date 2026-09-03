package relaxng

import (
	"math"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// ValidateOptions at its edges. See xdm/limits_boundary_test.go for why this
// class of test exists.
//
// This package carries the `+1` shape the MaxBytes overflow came from --
// validate.go computes patternSize(p, v.maxPattern+1), which wraps to
// math.MinInt when MaxPatternSize is math.MaxInt. It is harmless here, and the
// MaxInt case below is what proves it: patternSize returns 0 for any limit
// <= 0, and 0 never exceeds a large maxPattern, so the bound simply never
// fires -- which is the right answer at a limit no pattern can reach. The test
// pins that, so a future change to patternSize's guard cannot quietly turn the
// largest limit into a refusal the way it did in xdm.

func compileBoundarySchema(t *testing.T, src string) *Schema {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	s, err := Compile(doc.Root)
	if err != nil {
		t.Fatalf("compiling schema: %v", err)
	}
	return s
}

// recurseSchema admits <r> nested to any depth.
const recurseSchema = `<grammar xmlns="http://relaxng.org/ns/structure/1.0">
  <start><ref name="r"/></start>
  <define name="r"><element name="r"><optional><ref name="r"/></optional></element></define>
</grammar>`

func TestRelaxNGMaxDepthBoundaries(t *testing.T) {
	s := compileBoundarySchema(t, recurseSchema)

	const depth = 5
	doc, err := xdm.ParseString(
		strings.Repeat("<r>", depth)+strings.Repeat("</r>", depth), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxDepth (1000), matching
		// xdm.DefaultMaxDepth.
		{"zero is the default", 0, ""},
		// Deliberate: the field documents "a negative value means no limit",
		// and validate.go implements it with a `v.maxDepth >= 0` guard.
		{"negative is unlimited", -1, ""},
		{"the smallest limit refuses", 1, "nesting exceeds 1 levels"},
		{"one under refuses", depth - 1, "nesting exceeds 4 levels"},
		{"exactly at the limit is accepted", depth, ""},
		{"one over is accepted", depth + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.ValidateWithOptions(doc.Root, ValidateOptions{MaxDepth: tt.max})
			checkBoundaryErr(t, err, tt.wantErr)
		})
	}
}

func TestRelaxNGMaxPatternSizeBoundaries(t *testing.T) {
	s := compileBoundarySchema(t, recurseSchema)

	doc, err := xdm.ParseString(
		strings.Repeat("<r>", 5)+strings.Repeat("</r>", 5), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxPatternSize.
		{"zero is the default", 0, ""},
		// Deliberate: the field documents "a negative value means no limit",
		// implemented by the `v.maxPattern >= 0` guard.
		{"negative is unlimited", -1, ""},
		{"a tiny limit refuses", 1, "derivative pattern exceeds 1 nodes"},
		{"a tiny limit still refuses at two", 2, "derivative pattern exceeds 2 nodes"},
		// The `maxPattern+1` overflow case: it must not turn into a refusal.
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.ValidateWithOptions(doc.Root, ValidateOptions{MaxPatternSize: tt.max})
			checkBoundaryErr(t, err, tt.wantErr)
		})
	}
}

// checkBoundaryErr requires a refusal to name the limit that fired.
func checkBoundaryErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("accepted document was refused: %v", err)
	case want != "" && err == nil:
		t.Errorf("document was accepted; want an error matching %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error %q does not name the limit; want it to contain %q", err, want)
	}
}
