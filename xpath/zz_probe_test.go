package xpath

import "testing"

func TestVCProbe2(t *testing.T) {
	root := mustParse(t, `<r/>`)
	for _, e := range []string{
		`map{1:1} eq 1`, `(map{1:1}) eq 1`, `map{} eq ()`,
		`map{1:1}[1]`, `map{1:1} , 2`, `array{1} eq 1`,
	} {
		ctx := NewContext(root, Builtins())
		ctx.Version = XPath31
		seq, err := Eval(e, ctx, testNS{})
		t.Logf("%-18s => %v | err=%v", e, renderSeq(seq), err)
	}
}
