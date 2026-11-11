package xpath

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// sharedArray is 0 doubled k times, "[$a, $a]" in a fold: k charged items
// that describe 2^k leaves.
func sharedArray(k int) *xdm.ArrayItem {
	a := xdm.NewArray(xdm.Sequence{xdm.NewInteger(0)})
	for i := 1; i < k; i++ {
		a = xdm.NewArray(xdm.Sequence{a}, xdm.Sequence{a})
	}
	return a
}

// within runs f and fails the test if it has not returned in a few seconds.
// The walks below are linear when bounded and 2^40 when not, so the watchdog
// tells the two apart without measuring anything finer.
func within(t *testing.T, name string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return: the walk visits every shared leaf", name)
	}
}

// fn:deep-equal remembers the member pairs it has found equal, so comparing a
// shared-member array is linear in its distinct structure rather than in the
// 2^40 leaves it describes; a pair that differs, or that holds a function
// item, still answers as before.
func TestDeepEqualSharedMembersIsLinear(t *testing.T) {
	a, b := sharedArray(40), sharedArray(40)
	one := xdm.NewArray(xdm.Sequence{xdm.NewInteger(1)})
	for i := 1; i < 40; i++ {
		one = xdm.NewArray(xdm.Sequence{one}, xdm.Sequence{one})
	}
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct {
		name string
		x, y *xdm.ArrayItem
		want bool
	}{
		{"self", a, a, true},
		{"separately built", a, b, true},
		{"different leaf", a, one, false},
	} {
		within(t, c.name, func() {
			got, err := deepEqual(ctx, xdm.Sequence{c.x}, xdm.Sequence{c.y})
			if err != nil || got != c.want {
				t.Errorf("%s: got %v, %v, want %v", c.name, got, err, c.want)
			}
		})
	}
	// The walk polls the evaluation's context.
	gctx, cancel := context.WithCancel(context.Background())
	cancel()
	members := make([]xdm.Sequence, 2048)
	for i := range members {
		members[i] = xdm.Sequence{xdm.NewInteger(int64(i))}
	}
	wide := xdm.NewArray(members...)
	cctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.Ctx = gctx })
	if _, err := deepEqual(cctx, xdm.Sequence{wide}, xdm.Sequence{wide}); !errors.Is(err, context.Canceled) {
		t.Errorf("deep-equal under a cancelled Env.Ctx: err=%v, want context.Canceled", err)
	}
}

// The adaptive and JSON writers stop at a byte refusal or a cancelled
// context. Before, the sink latched the refusal and dropped the writes, and
// the writers went on visiting all 2^40 leaves.
func TestSerializeSharedMembersStopsOnRefusal(t *testing.T) {
	a := sharedArray(40)
	ser, ok := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: "serialize"}, 2)
	if !ok {
		t.Fatal("fn:serialize#2 is not registered")
	}
	params := func(method string) xdm.Sequence {
		m, err := Eval(`map{'method': '`+method+`'}`, NewContext(nil, Builtins()).WithVersion(XPath31), nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, method := range []string{"adaptive", "json"} {
		ctx := NewContext(nil, Builtins()).WithVersion(XPath31).HoldByteBudget()
		if err := ctx.ChargeBytes(MaxBytes - 1<<16); err != nil {
			t.Fatal(err)
		}
		within(t, method+" under a byte bound", func() {
			_, err := ser.Call(ctx, []xdm.Sequence{{a}, params(method)})
			if xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("%s: got %v, want XPDY0130", method, err)
			}
		})
		gctx, cancel := context.WithCancel(context.Background())
		cancel()
		cctx := NewContext(nil, Builtins()).WithVersion(XPath31).WithEnv(func(e *Env) { e.Ctx = gctx })
		within(t, method+" under a cancelled context", func() {
			if _, err := ser.Call(cctx, []xdm.Sequence{{a}, params(method)}); !errors.Is(err, context.Canceled) {
				t.Errorf("%s under a cancelled Env.Ctx: err=%v, want context.Canceled", method, err)
			}
		})
	}
}
