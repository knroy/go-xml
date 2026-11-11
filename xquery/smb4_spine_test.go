package xquery

import (
	"errors"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSMB4DeclaredChainChargesDepth: a declared function recursing from the
// bottom of a 10,000-term chain is refused at the depth bound rather than
// overflowing the stack (50 levels of it overflowed 64 MB uncharged), and a
// shallow call of it still answers.
func TestSMB4DeclaredChainChargesDepth(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	q := "declare function local:f($n) { if ($n le 0) then 0 else " +
		"local:f($n - 1)" + strings.Repeat(" + 1", 9999) + " }; "
	seq, err := evalQ(t, q+"local:f(2)")
	if err != nil || len(seq) != 1 || seq[0].(*xdm.Atomic).Int64() != 19998 {
		t.Fatalf("local:f(2) = %v, %v; want 19998", seq, err)
	}
	_, err = evalQ(t, q+"local:f(50)")
	if !errors.Is(err, xdm.ErrResourceLimit) || xdm.ErrorCode(err) != "XPDY0001" {
		t.Fatalf("local:f(50): err = %v, want XPDY0001 ErrResourceLimit", err)
	}
}
