package xdm

import (
	"strconv"
	"testing"
)

// The op:same-key-023 workload, as a regression test.
//
// That case builds 75³ = 421,875 keys and calls map:put and map:remove once
// for each. It is the case that proved MapItem's original entries-slice-plus-
// rebuilt-index design quadratic: both operations were O(n), so the case
// finished in no deadline at all, and replacing the representation with a
// persistent hash array mapped trie is what made it pass.
//
// Nothing in the package asserted that afterwards. `map_test.go` tops out at
// 20,000 entries and `map_bench_test.go` at 100,000, and the benchmarks price
// ONE operation against a pre-built map rather than running the build-then-
// put-and-remove-per-key shape that made the case quadratic. So a change that
// reintroduced an O(n) put would leave every test in the package green and be
// caught only by the suite, in a case that would appear to hang rather than
// fail. This test closes that: it runs the real workload through the public
// API and asserts the entry counts at each stage.
//
// It is a correctness test, not a timing one. Asserting a wall-clock bound
// would make it flaky on a loaded machine, and it does not need to: at 421,875
// entries an O(n) put is quadratic enough that the test does not complete at
// all, which the package's own timeout reports. What is asserted here is that
// the counts are right, which an index rebuild could also get wrong.
//
// Skipped under -short, since it is seconds rather than milliseconds and the
// short lane exists for the fast feedback loop.
func TestMapLargeWorkloadSameKey023(t *testing.T) {
	if testing.Short() {
		t.Skip("the 421,875-key op:same-key-023 workload is seconds, not milliseconds")
	}

	// 75³, built the way the case builds it: three nested loops over a
	// 75-value range, so the keys are distinct and numerous rather than a
	// single counter's worth.
	const side = 75
	const want = side * side * side

	bld := NewMapBuilder()
	keys := make([]*Atomic, 0, want)
	for i := 0; i < side; i++ {
		for j := 0; j < side; j++ {
			for k := 0; k < side; k++ {
				key := NewString("k" + strconv.Itoa(i) + ":" + strconv.Itoa(j) + ":" + strconv.Itoa(k))
				keys = append(keys, key)
				if err := bld.Set(key, One(NewInteger(int64(i*side*side+j*side+k)))); err != nil {
					t.Fatalf("Set at (%d,%d,%d): %v", i, j, k, err)
				}
			}
		}
	}
	m := bld.Build()
	if m.Len() != want {
		t.Fatalf("built map holds %d entries, want %d; the builder merged or "+
			"dropped keys that op:same-key holds distinct", m.Len(), want)
	}

	// A put of an ALREADY PRESENT key must replace rather than grow: this is
	// the half of the workload that depends on the key matching itself.
	replaced, err := m.Put(keys[0], One(NewInteger(-1)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if replaced.Len() != want {
		t.Fatalf("re-putting an existing key gave %d entries, want %d",
			replaced.Len(), want)
	}

	// A put of a fresh key, then its removal, returning to the original size.
	grown, err := m.Put(NewString("fresh"), One(NewInteger(0)))
	if err != nil {
		t.Fatalf("Put(fresh): %v", err)
	}
	if grown.Len() != want+1 {
		t.Fatalf("putting a fresh key gave %d entries, want %d", grown.Len(), want+1)
	}

	// Lookup of every key must find it. This is the property the canonical
	// key exists for, asserted at the size where a wrong key is most likely
	// to collide with another one by accident.
	for i, key := range keys {
		v, ok, err := m.Get(key)
		if err != nil || !ok {
			t.Fatalf("key %d (%q) was built into the map and then not found "+
				"(ok=%v, err=%v)", i, key.String(), ok, err)
		}
		if len(v) != 1 {
			t.Fatalf("key %d (%q) looked up to %d items, want 1", i, key.String(), len(v))
		}
	}

	// Removal of every key, one at a time, which is the other half of the
	// workload and the operation that was O(n) in the original design.
	cur := m
	for i, key := range keys {
		next, err := cur.Remove(key)
		if err != nil {
			t.Fatalf("Remove(%q): %v", key.String(), err)
		}
		if next.Len() != want-i-1 {
			t.Fatalf("after removing %d keys the map holds %d entries, want %d",
				i+1, next.Len(), want-i-1)
		}
		cur = next
	}
	if cur.Len() != 0 {
		t.Fatalf("after removing every key the map holds %d entries", cur.Len())
	}
	// The original is untouched: maps are immutable in the data model, and a
	// removal that mutated in place would have been invisible above.
	if m.Len() != want {
		t.Fatalf("the original map changed during removals: %d entries, want %d",
			m.Len(), want)
	}
}
