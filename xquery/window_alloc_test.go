package xquery_test

import (
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
)

// TestWindowBindAllocs pins the cost of binding a window's boundary
// variables: they are added to the tuple in one pass, so 2000 sliding windows
// of 10 allocate a bounded number of objects.
func TestWindowBindAllocs(t *testing.T) {
	q, err := xquery.Compile(`count(for sliding window $w in 1 to 2009 `+
		`start $s at $i previous $p next $n when true() `+
		`end at $e when $e - $i eq 9 return $w)`, xquery.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	allocs := testing.AllocsPerRun(3, func() {
		if _, err := q.Eval(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocs=%v", allocs)
	if allocs > 450000 {
		t.Fatalf("window binding allocated %v objects, want <= 450000", allocs)
	}
}
