package relaxng

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// startTagCloseDeriv runs once per element. When the pattern holds no
// attributePat outside element content there is nothing to discard, and it
// used to rebuild the whole pattern anyway, sending every rebuilt choice back
// through choice()'s deduplication: 18% of DocBook validation time. It now
// hands back its input, allocating nothing, and remembers on each ref that
// the ref's expansion has no attributes. A pattern that does hold one is
// still rewritten.
func TestStartTagCloseDerivKeepsAttributeFreePattern(t *testing.T) {
	a := &elementPat{Name: qnamePat{xdm.QName{Local: "a"}}, Pattern: emptyPat{}}
	b := &elementPat{Name: qnamePat{xdm.QName{Local: "b"}}, Pattern: textPat{}}
	free := newGroupPat(newChoicePat(a, b), newOneOrMorePat(newInterleavePat(a, textPat{})))
	ref := &refPat{name: "free", resolve: func() (pattern, error) { return free, nil }}
	p := pattern(newAfterPat(newGroupPat(ref, free), emptyPat{}))

	if got := startTagCloseDeriv(p); !patEq(got, p) {
		t.Fatalf("startTagCloseDeriv changed an attribute-free pattern: %#v", got)
	}
	if !ref.attrFree.Load() {
		t.Error("the ref's attribute-free expansion was not remembered")
	}
	if n := testing.AllocsPerRun(100, func() { startTagCloseDeriv(p) }); n != 0 {
		t.Errorf("startTagCloseDeriv allocated %v times on an attribute-free pattern, want 0", n)
	}

	att := &attributePat{Name: qnamePat{xdm.QName{Local: "id"}}, Pattern: textPat{}}
	withAtt := &refPat{name: "att", resolve: func() (pattern, error) { return newGroupPat(att, a), nil }}
	got := startTagCloseDeriv(newAfterPat(newChoicePat(withAtt, b), emptyPat{}))
	want := after(b, emptyPat{})
	if !patEq(got, want) {
		t.Errorf("unused attribute not discarded: got %#v, want %#v", got, want)
	}
	if withAtt.attrFree.Load() {
		t.Error("a ref whose expansion holds an attribute was marked attribute-free")
	}
}
