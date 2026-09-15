package xpath_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

func parseJSONNested(t *testing.T, n int) error {
	t.Helper()
	q := `parse-json('` + strings.Repeat("[", n) + strings.Repeat("]", n) + `')`
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version, ctx.LibraryVersion = xpath.XPath31, xpath.XPath31
	_, err := xpath.Eval(q, ctx, nil)
	return err
}

// The JSON scanner is recursive descent, so nesting in the text is nesting on
// the Go stack. The XML parser has bounded that since it was written; its
// JSON spelling did not, and JSON arrives from the same untrusted places --
// fn:parse-json over a string the document supplied, most directly.
func TestDeeplyNestedJSONIsRefused(t *testing.T) {
	if err := parseJSONNested(t, 5000); err == nil {
		t.Fatal("5,000 levels of JSON nesting were accepted; the scanner " +
			"recurses once per level with nothing counting them")
	}
}

// The boundary, from both sides, so that a bound set to the wrong number or
// charged in the wrong place is caught rather than merely "some limit".
func TestJSONNestingBoundary(t *testing.T) {
	if err := parseJSONNested(t, 999); err != nil {
		t.Errorf("999 levels is inside the bound and must parse: %v", err)
	}
	if err := parseJSONNested(t, 1001); err == nil {
		t.Error("1,001 levels is outside the bound and must be refused")
	}
}

// Ordinary JSON must be unaffected: a bound that refused real documents would
// pass both tests above and break the function.
func TestOrdinaryJSONStillParses(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version, ctx.LibraryVersion = xpath.XPath31, xpath.XPath31
	const q = `parse-json('{"a":[1,2,{"b":[3,{"c":4}]}],"d":{"e":[[[5]]]}}')?d?e?1?1?1`
	seq, err := xpath.Eval(q, ctx, nil)
	if err != nil {
		t.Fatalf("ordinary nested JSON must parse: %v", err)
	}
	if len(seq) != 1 {
		t.Fatalf("got %d items, want 1", len(seq))
	}
}
