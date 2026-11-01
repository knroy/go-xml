package xsd

import "testing"

// A set of up to smallSet states dedups by scanning order and builds no map;
// a larger one switches to a map and must still dedup.
func TestStateSetSmallHasNoMap(t *testing.T) {
	s := newStateSet()
	for i := 0; i < 2*smallSet; i++ {
		s.add(matchState{pos: i % smallSet})
	}
	if s.seen != nil {
		t.Fatal("a set of smallSet states built a map")
	}
	if s.len() != smallSet {
		t.Fatalf("len = %d, want %d", s.len(), smallSet)
	}
	for i := 0; i < 3*smallSet; i++ {
		s.add(matchState{pos: i})
		s.add(matchState{pos: i})
	}
	if s.len() != 3*smallSet {
		t.Fatalf("len = %d, want %d", s.len(), 3*smallSet)
	}
}
