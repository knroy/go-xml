package xpath

import (
	"strings"
	"testing"
)

// The pre-charge for fn:replace estimates the result from the matched text,
// not the whole input: a result that is empty, or the input unchanged, must
// not be refused for the length of the input.
func TestReplaceChargeFollowsTheResult(t *testing.T) {
	in := strings.Repeat("abcdefgh", 100) // 800 bytes
	run := func(pat, repl string) error {
		ctx := nearlyExhausted(t, 100, 16)
		_, err := callFn(t, ctx, "replace", strSeq(in), strSeq(pat), strSeq(repl))
		return err
	}
	if err := run("abcdefgh", ""); err != nil {
		t.Errorf("an empty result was refused: %v", err)
	}
	if err := run("zzz", "x"); err != nil {
		t.Errorf("an unchanged input was refused: %v", err)
	}
	// A group reference is bounded by the matched text, which over-estimates
	// here (1,200 against a 100-byte result); the difference is handed back,
	// so the same call can be made again on the same budget.
	ctx := nearlyExhausted(t, 1500, 16)
	for i := 0; i < 3; i++ {
		if _, err := callFn(t, ctx, "replace", strSeq(in), strSeq("(a)bcdefgh"), strSeq("$1")); err != nil {
			t.Fatalf("call %d: the over-estimate was not refunded: %v", i, err)
		}
	}
	_, err := callFn(t, nearlyExhausted(t, 100, 16), "replace", strSeq(in), strSeq("a"), strSeq("xyz"))
	wantRefused(t, err, "")
}
