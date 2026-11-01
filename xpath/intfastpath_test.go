package xpath

import (
	"math"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// mod, idiv, unary minus and integer comparison take an int64 path, with the
// results the exact path gives, including at the int64 edges.
func TestIntegerFastPaths(t *testing.T) {
	min, max := int64(math.MinInt64), int64(math.MaxInt64)
	for _, tc := range []struct {
		a, b int64
		op   string
		want string
	}{
		{7, 3, "mod", "1"}, {-7, 3, "mod", "-1"}, {7, -3, "mod", "1"}, {-7, -3, "mod", "-1"},
		{min, -1, "mod", "0"}, {7, 3, "idiv", "2"}, {-7, 3, "idiv", "-2"}, {7, -3, "idiv", "-2"},
		{min, -1, "idiv", "9223372036854775808"}, {max, 1, "idiv", "9223372036854775807"},
	} {
		got, err := arithmetic(xdm.NewInteger(tc.a), xdm.NewInteger(tc.b), tc.op)
		if err != nil || got.String() != tc.want || got.Type != xdm.TypeInteger {
			t.Errorf("%d %s %d = %v, %v; want %s", tc.a, tc.op, tc.b, got, err, tc.want)
		}
	}
	for _, op := range []string{"mod", "idiv"} {
		if _, err := arithmetic(xdm.NewInteger(1), xdm.NewInteger(0), op); err == nil {
			t.Errorf("1 %s 0: want FOAR0001", op)
		}
	}
	if got := negate(xdm.NewInteger(min)); got.String() != "9223372036854775808" {
		t.Errorf("-MinInt64 = %v", got)
	}
	if got := negate(xdm.NewInteger(5)); got.String() != "-5" {
		t.Errorf("-5 = %v", got)
	}
	if compareNumeric(xdm.NewInteger(min), xdm.NewInteger(max)) != -1 ||
		compareNumeric(xdm.NewInteger(3), xdm.NewInteger(3)) != 0 ||
		compareNumeric(xdm.NewInteger(4), xdm.NewInteger(3)) != 1 {
		t.Error("integer comparison")
	}

	// The saving is the point: these were 25, 13, 6 and 2 allocations.
	a, b := xdm.NewInteger(1000003), xdm.NewInteger(1009)
	for _, tc := range []struct {
		name string
		max  float64
		f    func()
	}{
		{"mod", 4, func() { _, _ = arithmetic(a, b, "mod") }},
		{"idiv", 4, func() { _, _ = arithmetic(a, b, "idiv") }},
		{"negate", 4, func() { negate(a) }},
		{"compare", 0, func() { compareNumeric(a, b) }},
	} {
		if n := testing.AllocsPerRun(100, tc.f); n > tc.max {
			t.Errorf("%s: %v allocs, want <= %v (NewInteger itself is 3)", tc.name, n, tc.max)
		}
	}
}
