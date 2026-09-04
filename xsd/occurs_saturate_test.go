package xsd

import (
	"math/big"
	"testing"
)

// mulExact and addExact are pinned directly, since what they promise is an
// arithmetic property rather than a property of any one schema.
//
// The contract has two halves and both are asserted here. The clamped int must
// saturate at occursHuge rather than wrap, which is what the runtime matcher
// reads. The exact value must be non-nil exactly when the clamp lost
// something, because every comparison downstream decides whether to reach for
// big.Int arithmetic by asking whether either operand carries one — a spurious
// exact value would cost allocations on ordinary schemas, and a missing one
// would silently restore the collapse these helpers exist to prevent.
func TestOccursExactHelpersSaturate(t *testing.T) {
	huge := big.NewInt(int64(occursHuge))
	// h2 and h3 are past the saturation point, so they must come back with
	// an exact value attached.
	h2 := new(big.Int).Mul(huge, big.NewInt(2))
	h3 := new(big.Int).Mul(huge, big.NewInt(3))
	hh := new(big.Int).Mul(huge, huge)

	for _, c := range []struct {
		a, b  int
		want  int
		exact *big.Int
	}{
		{0, occursHuge, 0, nil},
		{occursHuge, 0, 0, nil},
		{1, occursHuge, occursHuge, nil},
		{occursHuge, 2, occursHuge, h2},
		{occursHuge, 3, occursHuge, h3},
		{occursHuge, occursHuge, occursHuge, hh},
		{3, 4, 12, nil},
	} {
		got, exact := mulExact(c.a, nil, c.b, nil)
		if got != c.want {
			t.Errorf("mulExact(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
		checkExact(t, "mulExact", c.a, c.b, exact, c.exact)
	}

	for _, c := range []struct {
		a, b  int
		want  int
		exact *big.Int
	}{
		{0, 0, 0, nil},
		{3, 4, 7, nil},
		{occursHuge, 1, occursHuge, new(big.Int).Add(huge, big.NewInt(1))},
		{occursHuge, occursHuge, occursHuge, h2},
	} {
		got, exact := addExact(c.a, nil, c.b, nil)
		if got != c.want {
			t.Errorf("addExact(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
		checkExact(t, "addExact", c.a, c.b, exact, c.exact)
	}
}

func checkExact(t *testing.T, op string, a, b int, got, want *big.Int) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s(%d, %d) kept an exact value %s for a result that fits", op, a, b, got)
	case want != nil && got == nil:
		t.Errorf("%s(%d, %d) dropped the exact value %s", op, a, b, want)
	case want != nil && got.Cmp(want) != 0:
		t.Errorf("%s(%d, %d) exact = %s, want %s", op, a, b, got, want)
	}
}

// cmpExactOccurs is the comparison the whole exact layer exists to get right:
// two bounds that both clamped to occursHuge must not compare equal.
func TestCmpExactOccurs(t *testing.T) {
	n, _ := new(big.Int).SetString("1000000000000000000000000000000", 10)
	n3 := new(big.Int).Mul(n, big.NewInt(3))

	if cmpExactOccurs(occursHuge, n, occursHuge, n3) >= 0 {
		t.Error("1e30 did not compare below 3e30")
	}
	if cmpExactOccurs(occursHuge, n3, occursHuge, n) <= 0 {
		t.Error("3e30 did not compare above 1e30")
	}
	if cmpExactOccurs(occursHuge, n, occursHuge, n) != 0 {
		t.Error("1e30 did not compare equal to itself")
	}
	// A clamped bound against an ordinary one still orders correctly, and
	// the ordinary side is widened rather than the clamped side narrowed.
	if cmpExactOccurs(occursHuge, n, 5, nil) <= 0 {
		t.Error("1e30 did not compare above 5")
	}
	// Two ordinary bounds take the int path and must agree with it.
	if cmpExactOccurs(3, nil, 4, nil) >= 0 || cmpExactOccurs(4, nil, 3, nil) <= 0 {
		t.Error("the int fast path disagrees with the exact comparison")
	}
}
