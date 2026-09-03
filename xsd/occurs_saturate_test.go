package xsd

import "testing"

// TestOccursOverflowMulSaturates pins the helper directly, since the wrap it
// prevents is an arithmetic property and not a property of any one schema.
// Asserting on the saturated value is stronger than asserting the absence of a
// minus sign: it says what the answer must be, not merely what it must not be.
func TestOccursOverflowMulSaturates(t *testing.T) {
	for _, c := range []struct{ a, b, want int }{
		{0, occursHuge, 0},
		{occursHuge, 0, 0},
		{1, occursHuge, occursHuge},
		{occursHuge, 2, occursHuge},
		{occursHuge, 3, occursHuge},
		{occursHuge, occursHuge, occursHuge},
		{3, 4, 12},
	} {
		if got := mulOccurs(c.a, c.b); got != c.want {
			t.Errorf("mulOccurs(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, c := range []struct{ a, b, want int }{
		{0, 0, 0},
		{3, 4, 7},
		{occursHuge, 1, occursHuge},
		{occursHuge, occursHuge, occursHuge},
	} {
		if got := addOccurs(c.a, c.b); got != c.want {
			t.Errorf("addOccurs(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
