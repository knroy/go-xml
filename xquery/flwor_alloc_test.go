package xquery

import (
	"runtime"
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

// flworBytes is the bytes one evaluation of src allocates, the least of three
// runs, after checking that it returns want items.
func flworBytes(t *testing.T, src string, want int) uint64 {
	t.Helper()
	q, err := Compile(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	least := ^uint64(0)
	for range 3 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		got, err := q.Eval(ctx)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Fatalf("%d items, want %d", len(got), want)
		}
		least = min(least, after.TotalAlloc-before.TotalAlloc)
	}
	t.Logf("%d bytes", least)
	return least
}

// TestFLWORLetValueNotRebuilt pins that a FLWOR bound by "let" is its own
// value rather than appended item by item through a builder.
// Measured: 29.4 MB before, 25.1 MB after.
func TestFLWORLetValueNotRebuilt(t *testing.T) {
	b := flworBytes(t, `let $l := for $a in 1 to 50000 return $a return $l`, 50000)
	if b > 27_000_000 {
		t.Errorf("%d bytes, want at most 27,000,000", b)
	}
}

// TestEvalInstallsQueryVersion pins that evaluating a query under a context
// of another XPath version installs the query's once, rather than each
// expression copying the context to install it. Measured: 36,526
// allocations before, 34,527 after.
func TestEvalInstallsQueryVersion(t *testing.T) {
	if a := flworAllocs(t, `for $a in 1 to 2000 return $a + 1`, 2000); a > 35500 {
		t.Errorf("%.0f allocations, want at most 35,500", a)
	}
}

// TestFLWORResultReserved pins that a FLWOR's result is sized from its tuple
// stream rather than grown by doubling. Measured: 24.3 MB before, 20.7 MB
// after.
func TestFLWORResultReserved(t *testing.T) {
	if b := flworBytes(t, `for $a in 1 to 50000 return $a`, 50000); b > 22_500_000 {
		t.Errorf("%d bytes, want at most 22,500,000", b)
	}
}
