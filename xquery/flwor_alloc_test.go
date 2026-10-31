package xquery

import (
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
)

// flworAllocs is the allocations one evaluation of src makes, after checking
// that it returns want items.
func flworAllocs(t *testing.T, src string, want int) float64 {
	t.Helper()
	q, err := Compile(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	var n int
	allocs := testing.AllocsPerRun(3, func() {
		got, err := q.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n = len(got)
	})
	if n != want {
		t.Fatalf("%d items, want %d", n, want)
	}
	t.Logf("%.0f allocations", allocs)
	return allocs
}

// TestFLWORTupleNamesShared pins that the tuples a clause produces share one
// names slice and that "for" binds each item without wrapping it. Measured:
// 35,300 allocations before, 25,290 after.
func TestFLWORTupleNamesShared(t *testing.T) {
	if a := flworAllocs(t, `for $a in 1 to 2000 let $b := $a return $b`, 2000); a > 30000 {
		t.Errorf("%.0f allocations, want at most 30,000", a)
	}
}
