package xdmstream

import (
	"strings"
	"testing"
)

// TestSM03TeeNestedBound: the consumers live at once across nested tees (a
// tee of a consumer's cursor) stay within maxTeeReaders; a tee past it
// starts nothing and fails with errTeeReaders, while one within it reads.
func TestSM03TeeNestedBound(t *testing.T) {
	src := doc(20)
	drain := func(c *Cursor) error {
		for {
			ev, err := c.Next()
			if err != nil || ev.Kind == EndDocument {
				return err
			}
		}
	}
	nested := func(outer, inner int) []error {
		var innerErrs []error
		errs := Open(strings.NewReader(src), opts).Tee(outer, func(k int, sub *Cursor) error {
			if k > 0 {
				return drain(sub)
			}
			innerErrs = sub.Tee(inner, func(_ int, c *Cursor) error { return drain(c) }, func(int, bool) {})
			return nil
		}, func(int, bool) {})
		for k, err := range errs {
			if err != nil {
				t.Fatalf("outer %d, inner %d: outer consumer %d: %v", outer, inner, k, err)
			}
		}
		return innerErrs
	}
	for _, err := range nested(2, maxTeeReaders-2) {
		if err != nil {
			t.Fatalf("within the bound: %v", err)
		}
	}
	for _, err := range nested(maxTeeReaders, 1) {
		if err != errTeeReaders {
			t.Fatalf("64 outer, 1 inner: %v, want errTeeReaders", err)
		}
	}
	for _, err := range nested(2, maxTeeReaders-1) {
		if err != errTeeReaders {
			t.Fatalf("2 outer, 63 inner: %v, want errTeeReaders", err)
		}
	}
}
