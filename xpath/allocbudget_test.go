package xpath

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The budgets in this file are the library's OWN advertised invariant, not
// anything W3C requires: MaxBytes is documented as bounding the string content
// one evaluation builds, and MaxItems as bounding the items it materialises.
//
// What these tests pin is that the charge happens where the ALLOCATION does,
// rather than at an enclosing evaluator boundary. The difference is visible
// from a host: a caller that resolves a built-in through the function library
// and invokes it directly -- which is what xslt and xquery do, and what any
// embedder may do -- never passes through LetExpr, evalFor or the range
// operator, so an enclosing charge is not reached at all. A built-in that
// allocates without charging hands such a caller an unbounded allowance.
//
// Each test drives the budget to one byte (or one item) of headroom and then
// asks the built-in for more than that, through the host-facing path. A
// correct refusal is an error: XPDY0130 carrying xdm.ErrResourceLimit. A
// truncated result or a silent success would be the failure this guards, and
// the asserted message is what distinguishes the two.

// nearlyExhausted returns a context holding a budget with room bytes of string
// content and items items left.
//
// The hold matters. Compiled.Eval resets an unheld counter per expression, so
// a pre-charge on an unheld context would be wiped before the built-in ran --
// see the reasoning at Context.heldBytes. Holding first, then charging, is
// what makes the pre-charge survive to the call under test.
func nearlyExhausted(t *testing.T, room, items int) *Context {
	t.Helper()
	ctx := NewContext(nil, Builtins()).HoldByteBudget().HoldItemBudget()
	if err := ctx.ChargeBytes(MaxBytes - room); err != nil {
		t.Fatalf("arming the byte budget: %v", err)
	}
	if err := ctx.ChargeItems(MaxItems - items); err != nil {
		t.Fatalf("arming the item budget: %v", err)
	}
	return ctx
}

// callFn resolves a built-in the way a host language does and invokes it
// directly, bypassing every enclosing evaluator charge.
func callFn(t *testing.T, ctx *Context, local string, args ...xdm.Sequence) (xdm.Sequence, error) {
	t.Helper()
	f, ok := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: local}, len(args))
	if !ok {
		t.Fatalf("fn:%s#%d is not registered", local, len(args))
	}
	return f.Call(ctx, args)
}

// wantRefused asserts that err is the byte or item budget declining, with its
// code and sentinel intact. A nil error means the allocation escaped its
// budget, which is the defect these tests exist to catch.
func wantRefused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the allocation was not charged: want XPDY0130 %q, got a "+
			"successful result", want)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("error does not carry xdm.ErrResourceLimit: %v", err)
	}
	if !strings.Contains(err.Error(), "XPDY0130") {
		t.Fatalf("error lost its XPDY0130 code: %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error message = %q, want it to contain %q", err.Error(), want)
	}
}

const wantBytes = "evaluation built more than"
const wantItems = "evaluation materialised more than"

// TestStringProducersChargeTheBytesTheyBuild covers the byte budget: each
// built-in here materialises a NEW string, and each is reached here through
// the host-facing path where no enclosing expression charges it.
func TestStringProducersChargeTheBytesTheyBuild(t *testing.T) {
	big := strings.Repeat("a ", 4096) // 8192 bytes in, 8191 out normalized.

	tests := []struct {
		name string
		call func(ctx *Context) (xdm.Sequence, error)
	}{
		{"normalize-space", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "normalize-space", strSeq(big))
		}},
		{"upper-case", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "upper-case", strSeq(big))
		}},
		{"lower-case", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "lower-case", strSeq(strings.ToUpper(big)))
		}},
		{"serialize", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "serialize", strSeq(big))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := nearlyExhausted(t, 16, MaxItems)
			_, err := tc.call(ctx)
			wantRefused(t, err, wantBytes)
		})
	}
}

// TestSequenceProducersChargeTheItemsTheyBuild is the item-budget twin.
func TestSequenceProducersChargeTheItemsTheyBuild(t *testing.T) {
	big := strings.Repeat("a ", 4096)

	tests := []struct {
		name string
		call func(ctx *Context) (xdm.Sequence, error)
	}{
		{"string-to-codepoints", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "string-to-codepoints", strSeq(big))
		}},
		{"tokenize#1", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "tokenize", strSeq(big))
		}},
		{"tokenize#2", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "tokenize", strSeq(big), strSeq(" "))
		}},
		// These three copy their input into a second backing array. The result
		// is bounded by the argument rather than amplified from it, so the
		// exposure is a duplicate and not the open-ended growth the two above
		// are -- but the duplicate is unbounded from a host that invokes the
		// built-in directly, which is the path callFn takes.
		{"reverse", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "reverse", manyItems(64))
		}},
		{"insert-before", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "insert-before",
				manyItems(64), intSeq(1), manyItems(8))
		}},
		{"remove", func(ctx *Context) (xdm.Sequence, error) {
			return callFn(t, ctx, "remove", manyItems(64), intSeq(1))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := nearlyExhausted(t, MaxBytes, 16)
			_, err := tc.call(ctx)
			wantRefused(t, err, wantItems)
		})
	}
}

// TestChargedProducersStillReturnTheirResult is the control the fix plan asks
// for: with room to spare, every one of these must return its ordinary answer.
// A budget that refuses correct work is a conformance bug, and a charge added
// in the wrong place -- charging the input twice, say -- shows up here rather
// than in the refusal tests.
func TestChargedProducersStillReturnTheirResult(t *testing.T) {
	ctx := NewContext(nil, Builtins())

	if got, err := callFn(t, ctx, "normalize-space", strSeq("  a  b  ")); err != nil {
		t.Fatalf("normalize-space: %v", err)
	} else if s := got[0].(*xdm.Atomic).String(); s != "a b" {
		t.Errorf("normalize-space = %q, want %q", s, "a b")
	}
	if got, err := callFn(t, ctx, "upper-case", strSeq("straße")); err != nil {
		t.Fatalf("upper-case: %v", err)
	} else if s := got[0].(*xdm.Atomic).String(); s != "STRASSE" {
		t.Errorf("upper-case = %q, want %q", s, "STRASSE")
	}
	if got, err := callFn(t, ctx, "string-to-codepoints", strSeq("abc")); err != nil {
		t.Fatalf("string-to-codepoints: %v", err)
	} else if len(got) != 3 {
		t.Errorf("string-to-codepoints returned %d items, want 3", len(got))
	}
	if got, err := callFn(t, ctx, "tokenize", strSeq("a b c")); err != nil {
		t.Fatalf("tokenize: %v", err)
	} else if len(got) != 3 {
		t.Errorf("tokenize returned %d items, want 3", len(got))
	}
	if got, err := callFn(t, ctx, "tokenize", strSeq("a,b,c"), strSeq(",")); err != nil {
		t.Fatalf("tokenize#2: %v", err)
	} else if len(got) != 3 {
		t.Errorf("tokenize#2 returned %d items, want 3", len(got))
	}
	// fn:substring allocates through string(runes[...]) and is charged;
	// substring-before and substring-after slice and are not. All three must
	// still answer correctly.
	if got, err := callFn(t, ctx, "substring", strSeq("abcde"), intSeq(2), intSeq(3)); err != nil {
		t.Fatalf("substring: %v", err)
	} else if s := got[0].(*xdm.Atomic).String(); s != "bcd" {
		t.Errorf("substring = %q, want %q", s, "bcd")
	}
	if got, err := callFn(t, ctx, "substring-before", strSeq("a/b"), strSeq("/")); err != nil {
		t.Fatalf("substring-before: %v", err)
	} else if s := got[0].(*xdm.Atomic).String(); s != "a" {
		t.Errorf("substring-before = %q, want %q", s, "a")
	}
	if got, err := callFn(t, ctx, "serialize", strSeq("x")); err != nil {
		t.Fatalf("serialize: %v", err)
	} else if s := got[0].(*xdm.Atomic).String(); s != "x" {
		t.Errorf("serialize = %q, want %q", s, "x")
	}
}

// TestSerializeChargesOnlyTheBytesItWrote pins the exactness of the sink's
// block reservation.
//
// The sink draws 64 KiB at a time so the shared atomic stays off the per-write
// path, which would turn the bound into an approximation if the block were a
// discount. It is not: the block is charged in full when drawn and the unspent
// remainder is handed back, so a serialization that writes 12 bytes must leave
// the evaluation charged 12 bytes and not 65,536. Without the refund this test
// reports the block size, which is what makes it worth having: a 64 KiB
// over-charge per serialization would refuse legitimate work at a fraction of
// the documented limit.
func TestSerializeChargesOnlyTheBytesItWrote(t *testing.T) {
	ctx := NewContext(nil, Builtins()).HoldByteBudget()

	const want = "hello, world"
	got, err := callFn(t, ctx, "serialize", strSeq(want))
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if s := got[0].(*xdm.Atomic).String(); s != want {
		t.Fatalf("serialize = %q, want %q", s, want)
	}
	if n := atomic.LoadInt64(ctx.bytes); n != int64(len(want)) {
		t.Errorf("serialize charged %d bytes for a %d-byte result; "+
			"the reservation block was not released", n, len(want))
	}
}

// TestSerializeRefusesWithinOneBlock pins the other half: a budget whose
// remaining headroom is SMALLER than the reservation block must still be
// spendable up to what is left, rather than refusing everything because a full
// block cannot be drawn.
//
// This is the case a block reservation gets wrong if it draws unconditionally.
// The control is that the same call succeeds when the headroom is ample.
func TestSerializeRefusesWithinOneBlock(t *testing.T) {
	// Headroom far below one block, and below the result size.
	ctx := NewContext(nil, Builtins()).HoldByteBudget()
	if err := ctx.ChargeBytes(MaxBytes - 4); err != nil {
		t.Fatalf("arming: %v", err)
	}
	_, err := callFn(t, ctx, "serialize", strSeq(strings.Repeat("x", 4096)))
	wantRefused(t, err, wantBytes)
}

// manyItems builds a sequence of n integers, which is the item-budget twin of
// the long strings the byte tests use.
func manyItems(n int) xdm.Sequence {
	out := make(xdm.Sequence, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, xdm.NewInteger(int64(i)))
	}
	return out
}

// TestRemoveOutOfRangeIsNotCharged pins the one case among the three that does
// NOT allocate: fn:remove with a position outside the sequence returns its
// argument unchanged, sharing the backing array. Charging there would bill a
// caller for a copy that never happened, which is the "paid for twice" error
// the reservation discipline exists to avoid.
func TestRemoveOutOfRangeIsNotCharged(t *testing.T) {
	ctx := NewContext(nil, Builtins()).HoldItemBudget()
	in := manyItems(64)
	before := atomic.LoadInt64(ctx.items)
	got, err := callFn(t, ctx, "remove", in, intSeq(9999))
	if err != nil {
		t.Fatalf("remove past the end: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("remove past the end returned %d items, want %d", len(got), len(in))
	}
	if after := atomic.LoadInt64(ctx.items); after != before {
		t.Errorf("remove past the end charged %d items for a result that "+
			"shares its argument's backing array", after-before)
	}
}

// TestPathChargesTheStringItBuilds covers the one string producer whose output
// is bounded by neither its argument nor a constant: fn:path emits one step
// per ancestor, so its length grows with the depth of the node. A document 500
// elements deep produced 4,000 bytes against a budget with 16 left, and none
// of them were charged.
//
// fn:generate-id is charged in the same pass for the ownership rule rather
// than for its size -- "N" plus a decimal integer is a dozen bytes -- so it is
// asserted here to RETURN, not to refuse. A test that demanded a refusal from
// it would be asserting something false.
func TestPathChargesTheStringItBuilds(t *testing.T) {
	const depth = 500
	var sb strings.Builder
	for i := 0; i < depth; i++ {
		sb.WriteString("<e>")
	}
	for i := 0; i < depth; i++ {
		sb.WriteString("</e>")
	}
	doc, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deepest := doc.Root
	for {
		var kid *xdm.Node
		for _, k := range deepest.Children {
			if k.Kind == xdm.KindElement {
				kid = k
				break
			}
		}
		if kid == nil {
			break
		}
		deepest = kid
	}

	ctx := nearlyExhausted(t, 16, MaxItems)
	_, err = callFn(t, ctx, "path", xdm.One(deepest))
	wantRefused(t, err, wantBytes)

	// The control: with room to spare the same call returns its ordinary
	// answer, so the charge refuses oversize work rather than all work.
	got, err := callFn(t, NewContext(nil, Builtins()), "path", xdm.One(deepest))
	if err != nil {
		t.Fatalf("fn:path with an ample budget: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("fn:path returned %d items, want 1", len(got))
	}

	// fn:generate-id is bounded and must still succeed on a tight budget.
	if _, err := callFn(t, nearlyExhausted(t, 64, MaxItems),
		"generate-id", xdm.One(deepest)); err != nil {
		t.Errorf("fn:generate-id with 64 bytes of headroom: %v; its result is "+
			"a dozen bytes, so charging it must not refuse it", err)
	}
}
