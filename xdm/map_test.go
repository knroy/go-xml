package xdm

import (
	"strconv"
	"testing"
)

// The map's behaviour as a persistent, insertion-ordered structure. These
// exercise the trie in hamt.go through MapItem's own API, across sizes large
// enough that nodes split several levels deep and removals collapse them
// again, and they pin the two properties callers depend on: an older map is
// untouched by a put or remove on it, and iteration walks entries in the order
// they first went in.

func mapKeysOf(t *testing.T, m *MapItem) []string {
	t.Helper()
	var out []string
	err := m.Entries(func(k *Atomic, v Sequence) error {
		out = append(out, k.String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != m.Len() {
		t.Fatalf("Entries walked %d entries, Len says %d", len(out), m.Len())
	}
	keys := m.Keys()
	if len(keys) != len(out) {
		t.Fatalf("Keys returned %d keys, Entries walked %d", len(keys), len(out))
	}
	for i := range keys {
		if keys[i].String() != out[i] {
			t.Fatalf("Keys and Entries disagree at %d: %q vs %q", i, keys[i].String(), out[i])
		}
	}
	return out
}

func TestMapPutLookupOrder(t *testing.T) {
	const n = 5000
	m := NewMap()
	var want []string
	for i := 0; i < n; i++ {
		k := "k" + strconv.Itoa(i)
		var err error
		m, err = m.Put(NewString(k), One(NewInteger(int64(i))))
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, k)
		if m.Len() != i+1 {
			t.Fatalf("after %d puts Len = %d", i+1, m.Len())
		}
	}
	for i := 0; i < n; i++ {
		v, ok, err := m.Get(NewString("k" + strconv.Itoa(i)))
		if err != nil || !ok {
			t.Fatalf("k%d missing", i)
		}
		if v[0].(*Atomic).Int64() != int64(i) {
			t.Fatalf("k%d = %v", i, v)
		}
	}
	if _, ok, _ := m.Get(NewString("absent")); ok {
		t.Fatal("absent key found")
	}
	got := mapKeysOf(t, m)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order broken at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestMapReplaceKeepsPosition(t *testing.T) {
	m := NewMap()
	for _, k := range []string{"a", "b", "c"} {
		m, _ = m.Put(NewString(k), One(NewString(k)))
	}
	m2, err := m.Put(NewString("a"), One(NewString("A")))
	if err != nil {
		t.Fatal(err)
	}
	if got := mapKeysOf(t, m2); got[0] != "a" || got[1] != "b" || got[2] != "c" || m2.Len() != 3 {
		t.Fatalf("replacing a moved it: %v", got)
	}
	v, _, _ := m2.Get(NewString("a"))
	if v[0].(*Atomic).String() != "A" {
		t.Fatalf("replacement not stored: %v", v)
	}
	// 1 and 1.0 are the same key; the numeric family shares an entry.
	m3, _ := m2.Put(NewInteger(1), One(NewString("int")))
	m4, _ := m3.Put(NewDouble(1.0), One(NewString("dbl")))
	if m4.Len() != 4 {
		t.Fatalf("1 and 1.0 made separate entries: Len = %d", m4.Len())
	}
	v, _, _ = m4.Get(NewInteger(1))
	if v[0].(*Atomic).String() != "dbl" {
		t.Fatalf("put of 1.0 did not replace 1: %v", v)
	}
}

func TestMapRemoveOrderAndCollapse(t *testing.T) {
	const n = 3000
	m := NewMap()
	for i := 0; i < n; i++ {
		m, _ = m.Put(NewString("k"+strconv.Itoa(i)), One(NewInteger(int64(i))))
	}
	// Remove every third key in one call, and the rest one at a time until
	// the trie is empty, checking order and membership as it shrinks.
	var third []*Atomic
	for i := 0; i < n; i += 3 {
		third = append(third, NewString("k"+strconv.Itoa(i)))
	}
	third = append(third, NewString("nosuch"))
	m2, err := m.RemoveAll(third)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Len() != n-len(third)+1 {
		t.Fatalf("Len after RemoveAll = %d", m2.Len())
	}
	got := mapKeysOf(t, m2)
	j := 0
	for i := 0; i < n; i++ {
		if i%3 == 0 {
			if _, ok, _ := m2.Get(NewString("k" + strconv.Itoa(i))); ok {
				t.Fatalf("k%d survived removal", i)
			}
			continue
		}
		if got[j] != "k"+strconv.Itoa(i) {
			t.Fatalf("order after removal broken at %d: got %q", j, got[j])
		}
		j++
	}
	cur := m2
	for i := n - 1; i >= 0; i-- {
		if i%3 == 0 {
			continue
		}
		next, err := cur.Remove(NewString("k" + strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		if next.Len() != cur.Len()-1 {
			t.Fatalf("Remove k%d: Len %d -> %d", i, cur.Len(), next.Len())
		}
		cur = next
	}
	if cur.Len() != 0 || cur.root != nil || len(mapKeysOf(t, cur)) != 0 {
		t.Fatalf("map not empty after removing everything: Len %d root %v", cur.Len(), cur.root)
	}
	// Removing nothing returns the receiver itself.
	same, _ := m.RemoveAll([]*Atomic{NewString("nosuch")})
	if same != m {
		t.Fatal("RemoveAll of absent keys did not return the receiver")
	}
	// A key put after removals goes to the end.
	back, _ := cur.Put(NewString("z"), Empty())
	back, _ = back.Put(NewString("k5"), Empty())
	if got := mapKeysOf(t, back); got[0] != "z" || got[1] != "k5" {
		t.Fatalf("re-added keys out of order: %v", got)
	}
}

func TestMapStructuralSharing(t *testing.T) {
	const n = 2000
	old := NewMap()
	for i := 0; i < n; i++ {
		old, _ = old.Put(NewString("k"+strconv.Itoa(i)), One(NewInteger(int64(i))))
	}
	before := mapKeysOf(t, old)
	added, _ := old.Put(NewString("new"), Empty())
	replaced, _ := old.Put(NewString("k7"), One(NewString("seven")))
	removed, _ := old.RemoveAll([]*Atomic{NewString("k0"), NewString("k1999"), NewString("k1000")})
	after := mapKeysOf(t, old)
	if old.Len() != n || len(after) != n {
		t.Fatalf("old map changed size: %d", old.Len())
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("old map's order changed at %d", i)
		}
	}
	if v, _, _ := old.Get(NewString("k7")); v[0].(*Atomic).Int64() != 7 {
		t.Fatalf("old map saw the replacement: %v", v)
	}
	if _, ok, _ := old.Get(NewString("new")); ok {
		t.Fatal("old map saw the new key")
	}
	for _, k := range []string{"k0", "k1999", "k1000"} {
		if _, ok, _ := old.Get(NewString(k)); !ok {
			t.Fatalf("old map lost %s", k)
		}
	}
	if added.Len() != n+1 || replaced.Len() != n || removed.Len() != n-3 {
		t.Fatalf("derived sizes: %d %d %d", added.Len(), replaced.Len(), removed.Len())
	}
	// The derived maps share the untouched subtrees rather than copying
	// them: the roots differ, but most root slots are the same nodes.
	shared := 0
	for i, s := range old.root.slots {
		if added.root.slots[i] == s {
			shared++
		}
	}
	if shared < len(old.root.slots)-1 {
		t.Fatalf("put copied %d of %d root slots", len(old.root.slots)-shared, len(old.root.slots))
	}
}

func TestMapBuilderOwnsOnlyItsNodes(t *testing.T) {
	base := NewMap()
	for i := 0; i < 100; i++ {
		base, _ = base.Put(NewString("k"+strconv.Itoa(i)), One(NewInteger(int64(i))))
	}
	b := NewMapBuilderFrom(base)
	if err := b.Set(NewString("k3"), One(NewString("three"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(NewString("extra"), Empty()); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := b.Lookup(NewString("k3")); !ok || v[0].(*Atomic).String() != "three" {
		t.Fatalf("builder Lookup: %v %v", v, ok)
	}
	built := b.Build()
	if v, _, _ := base.Get(NewString("k3")); v[0].(*Atomic).Int64() != 3 {
		t.Fatalf("builder wrote through to the base map: %v", v)
	}
	if base.Len() != 100 || built.Len() != 101 {
		t.Fatalf("sizes: base %d built %d", base.Len(), built.Len())
	}
	got := mapKeysOf(t, built)
	if got[3] != "k3" || got[100] != "extra" {
		t.Fatalf("built order: %v", got)
	}
	// A large build, in insertion order, through the transient path.
	bb := NewMapBuilder()
	for i := 0; i < 20000; i++ {
		_ = bb.Set(NewString("b"+strconv.Itoa(i)), Empty())
	}
	big := bb.Build()
	keys := mapKeysOf(t, big)
	for i := range keys {
		if keys[i] != "b"+strconv.Itoa(i) {
			t.Fatalf("builder order broken at %d: %q", i, keys[i])
		}
	}
}

// Two keys with the same 64-bit hash land in a collision node. maphash will
// not produce such a pair on request, so the trie is driven directly with
// entries whose hash field is forced equal.
func TestHAMTCollision(t *testing.T) {
	mk := func(k string, seq uint64) *mapEntry {
		return &mapEntry{key: NewString(k), value: Empty(), ckey: "str:" + k, hash: 0xdeadbeef, seq: seq}
	}
	var root *hamtNode
	var added bool
	for i, k := range []string{"a", "b", "c"} {
		root, added = root.put(mk(k, uint64(i)), 0, nil)
		if !added {
			t.Fatalf("%s not added", k)
		}
	}
	root, added = root.put(mk("b", 9), 0, nil)
	if added {
		t.Fatal("replacing b counted as an addition")
	}
	for _, k := range []string{"a", "b", "c"} {
		e := root.get(0xdeadbeef, 0, "str:"+k)
		if e == nil {
			t.Fatalf("%s not found under collision", k)
		}
		if k == "b" && e.seq != 1 {
			t.Fatalf("replaced b lost its sequence: %d", e.seq)
		}
	}
	if root.get(0xdeadbeef, 0, "str:d") != nil {
		t.Fatal("absent key found in collision node")
	}
	// A different hash that agrees with the collision on its first bits
	// pushes the collision node down rather than replacing it.
	other := &mapEntry{key: NewString("z"), ckey: "str:z", hash: 0xdeadbeef ^ (1 << 40), seq: 10}
	root, added = root.put(other, 0, nil)
	if !added || root.get(other.hash, 0, "str:z") == nil || root.get(0xdeadbeef, 0, "str:a") == nil {
		t.Fatal("collision node lost when a near-hash key was added")
	}
	next, ok := root.remove(0xdeadbeef, 0, "str:a")
	if !ok {
		t.Fatal("a not removed")
	}
	root = next.(*hamtNode)
	next, ok = root.remove(0xdeadbeef, 0, "str:c")
	if !ok {
		t.Fatal("c not removed")
	}
	root = next.(*hamtNode)
	if root.get(0xdeadbeef, 0, "str:b") == nil || root.get(0xdeadbeef, 0, "str:a") != nil {
		t.Fatal("collision removal wrong")
	}
	if _, ok := root.remove(0xdeadbeef, 0, "str:nosuch"); ok {
		t.Fatal("removing an absent key reported success")
	}
	count := 0
	root.walk(func(*mapEntry) { count++ })
	if count != 2 {
		t.Fatalf("walk saw %d entries, want 2", count)
	}
}
