package xpath_test

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// A range too large to hold and a range merely too large to count are the
// same refusal, and were reported two ways: the second went through
// ctx.countItems and carried XPDY0130 with the sentinel, the first returned a
// bare string. Which one a caller got depended on how far past the bound the
// expression happened to be.
func TestOversizeRangeCarriesTheResourceSentinel(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	_, err := xpath.Eval(`(1 to 99999999999999)[1]`, ctx, nil)
	if err == nil {
		t.Fatal("a range of 10^14 items returned no error")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false", err)
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 -- the same code the counted "+
			"path raises for the same refusal", code)
	}
}
