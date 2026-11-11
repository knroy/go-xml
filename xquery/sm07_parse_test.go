package xquery

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// wantDepthRefusal asserts the XPDY0130 nesting refusal.
func wantDepthRefusal(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted; the nesting bound did not apply", what)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) || xdm.ErrorCode(err) != "XPDY0130" {
		t.Fatalf("%s: %v, want XPDY0130 wrapping ErrResourceLimit", what, err)
	}
}

// Every form whose body a sub-parser reads back carries the nesting depth
// down, so nesting them past maxNestDepth is refused rather than recursing
// without bound, and shallow nesting still compiles.
func TestSM07BracketedBodiesCarryTheDepth(t *testing.T) {
	forms := map[string][2]string{
		"try":       {"try { ", " } catch * { 0 }"},
		"ordered":   {"ordered { ", " }"},
		"if":        {"if (", ") then 1 else 2"},
		"switch":    {"switch (", ") case 1 return 1 default return 2"},
		"element":   {"element e { ", " }"},
		"inline fn": {"function() { ", " }"},
		"string":    {"``[`{ ", " }`]``"},
		"mixed":     {"try { <a>{ ", " }</a> } catch * { 0 }"},
	}
	for name, f := range forms {
		for _, n := range []int{10, 1000} {
			src := strings.Repeat(f[0], n) + "1" + strings.Repeat(f[1], n)
			_, err := Compile(src, Options{})
			if n == 10 {
				if err != nil {
					t.Errorf("%s nested %d deep: %v", name, n, err)
				}
				continue
			}
			wantDepthRefusal(t, name, err)
		}
	}
}

// Every bracketed body is parsed once per offset, not once per read: try,
// computed constructors, ordered and inline-function bodies as the operand of
// an operator were re-read three times per level, 3^d. Measured on the base:
// try at d=12 took 4.3 s; with the body memo each form at d=12 allocates a
// few hundred KB.
func TestSM07NestedOperandBodiesAreParsedOnce(t *testing.T) {
	forms := map[string][2]string{
		"try":     {"try { ", " } catch * { 0 } = 1"},
		"element": {"element e { ", " } = 1"},
		"ordered": {"ordered { ", " } = 1"},
		"inline":  {"function() { <a/>, ", " }() = 1"},
	}
	for name, f := range forms {
		const d = 12
		src := strings.Repeat(f[0], d) + "1" + strings.Repeat(f[1], d)
		var err error
		if b := allocated(func() { _, err = Compile(src, Options{}) }); b > 4<<20 {
			t.Errorf("%s: %d bytes of query allocated %d bytes; a nested body "+
				"is re-parsed per read again", name, len(src), b)
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
