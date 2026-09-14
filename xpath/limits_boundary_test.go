package xpath

import (
	"math"
	"strings"
	"testing"
)

// Context.MaxDepth at its edges. See xdm/limits_boundary_test.go for why this
// class of test exists.
//
// Nested for-expressions are the vehicle: ForExpr.Eval calls Context.Descend
// exactly once, so five nested "for"s cost exactly five levels and the
// boundary is a number the test chooses rather than one it has to discover.
// (An inline function calling itself does NOT descend -- only named function
// calls, for, let and quantified expressions do -- which is a sharp edge worth
// knowing when reading a depth failure.)
func TestContextMaxDepthBoundaries(t *testing.T) {
	const depth = 5
	src := strings.Repeat("for $x in 1 to 1 return ", depth) + "0"

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means the package-level MaxDepth (500). The bound
		// is a DoS guard for a caller evaluating an expression it did not
		// write, not a conformance limit -- the conformance harnesses raise it.
		{"zero is the default", 0, ""},
		// Deliberate: depthLimit() tests `c.MaxDepth > 0`, so a negative value
		// falls through to the package default rather than meaning unlimited.
		// Same reasoning as xdm.ParseOptions.MaxDepth: a depth bound of zero
		// or below would refuse every expression, so there is no useful
		// reading of a negative value other than "the caller set nothing".
		{"negative is also the default, not unlimited", -1, ""},
		{"the smallest limit refuses", 1, "recursion exceeded 1 levels"},
		{"one under refuses", depth - 1, "recursion exceeded 4 levels"},
		{"exactly at the limit is accepted", depth, ""},
		{"one over is accepted", depth + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := CompileXQuery(src, nil, XPath31)
			if err != nil {
				t.Fatalf("compiling: %v", err)
			}
			ctx := NewContext(nil, nil)
			ctx.MaxDepth = tt.max
			_, err = c.Eval(ctx)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("accepted expression was refused: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("expression evaluated; want an error matching %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error %q does not name the limit; want it to contain %q",
					err, tt.wantErr)
			}
		})
	}
}
