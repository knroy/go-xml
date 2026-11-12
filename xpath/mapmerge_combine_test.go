package xpath

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// map:merge with duplicates=combine materialises the joined value, so what it
// adds is charged to MaxItems. Before, each duplicate copied the whole value
// so far into a fresh slice and charged nothing: 200 maps sharing one key with
// a 20-item value built a 4000-item value under a 1000-item bound. The join
// now appends in place to a slice the call owns, which must leave the input
// maps' own values untouched.
func TestMapMergeCombineChargesItemsAndKeepsInputs(t *testing.T) {
	eval := func(limit int, src string) (xdm.Sequence, error) {
		c, err := CompileWith(src, CompileOptions{Version: XPath31,
			Namespaces: testResolver{"map": xdm.NSMap}})
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return c.Eval(NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.MaxItems = limit }))
	}
	big := `let $s := 1 to 20 return count(map:merge((1 to 200) ! map { 1: $s }, map { 'duplicates': 'combine' })?1)`
	if _, err := eval(1000, big); xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("4000-item combine under a 1000-item bound: err=%v, want the XPDY0130 refusal", err)
	}
	if got, err := eval(0, big); err != nil || len(got) != 1 || got[0].(*xdm.Atomic).String() != "4000" {
		t.Errorf("4000-item combine under the default bound: got %v, %v; want 4000", got, err)
	}

	got, err := eval(0, `let $m := map { 1: (1, 2) } return
		(map:merge(($m, map { 1: 3 }, map { 1: (4, 5) }), map { 'duplicates': 'combine' })?1, $m?1)`)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	for i, it := range got {
		if i > 0 {
			s += " "
		}
		s += it.(*xdm.Atomic).String()
	}
	if want := "1 2 3 4 5 1 2"; s != want {
		t.Errorf("combine: got %q, want %q", s, want)
	}
}
