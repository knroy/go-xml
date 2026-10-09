package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Comparisons, logical operators, instance of and the boolean built-ins
// return the two shared xs:boolean values instead of allocating one per
// evaluation, and the values are still the right ones.
func TestSharedBooleans(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for src, want := range map[string]bool{
		"1 = 1": true, "1 = 2": false, "1 eq 1": true, "1 lt 0": false,
		"true() and false()": false, "1 instance of xs:integer": true, "not(1 = 1)": false,
		"(1, 2) = 2": true,
	} {
		e := MustCompile(src, testNS{})
		a, err := e.Eval(ctx)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		b, _ := e.Eval(ctx)
		x := a[0].(*xdm.Atomic)
		if x.Type != xdm.TypeBoolean || x.Bool() != want {
			t.Fatalf("%s = %v, want %v", src, x, want)
		}
		if a[0] != b[0] {
			t.Errorf("%s: two evaluations returned distinct boolean values", src)
		}
	}
}
