package xquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
)

// A "for" over N items after W "let"s binds onto N tuples from one parent;
// each copied all W bindings, N x W held while the item budget saw N. The
// children now share the parent's bindings, and every form still sees them.
func TestSM07ForAfterWideLetsSharesBindings(t *testing.T) {
	const w, n = 200, 2000
	var lets strings.Builder
	for i := 0; i < w; i++ {
		fmt.Fprintf(&lets, "let $l%d := %d ", i, i)
	}
	run := func(tail string) string {
		t.Helper()
		q, err := Compile(lets.String()+tail, Options{})
		if err != nil {
			t.Fatal(err)
		}
		seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
		if err != nil {
			t.Fatalf("%s: %v", tail, err)
		}
		var parts []string
		for _, it := range seq {
			parts = append(parts, fmt.Sprint(it))
		}
		return strings.Join(parts, " ")
	}
	for tail, want := range map[string]string{
		fmt.Sprintf("for $i at $p in 1 to %d let $x := $i + $l199 where $p = 3 return ($x, $l0)", n): "202 0",
		"for $i in 1 to 4 group by $k := $i mod 2 order by $k return ($k, sum($i), $l5)":             "0 6 5 5 1 4 5 5",
		"for $l7 in (70, 71) return $l7": "70 71",
		"for tumbling window $win in 1 to 6 start at $s when true() end at $e when $e - $s = 2 return sum($win) + $l3": "9 18",
	} {
		if got := run(tail); got != want {
			t.Errorf("%s:\n got %s\nwant %s", tail, got, want)
		}
	}
	q, err := Compile("count("+lets.String()+fmt.Sprintf("for $i in 1 to %d return $i)", n), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	// Copied, the stream holds n x w x 24 bytes of bindings: about 9.6 MB.
	if a := allocated(func() { _, err = q.Eval(ctx) }); a > 4<<20 || err != nil {
		t.Fatalf("%d tuples under %d lets allocated %d bytes (err %v); the "+
			"bindings are copied per tuple again", n, w, a, err)
	}
}
