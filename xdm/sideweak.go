package xdm

import (
	"runtime"
	"weak"
)

// sideTable is a tree's namespace nodes. They are made on demand and held
// weakly: a node lives while a caller holds it, so pointer identity, Is and
// IdentityKey (which holds the pointer) stay true for every holder, and a
// node nobody holds is freed instead of staying for the life of the tree, and
// its entry is swept after the next collection. seq numbers the nodes in the order they were made.
type sideTable struct {
	m     map[sideKey]weak.Pointer[Node]
	seq   uint32
	peak  int  // most entries held since the map was last rebuilt
	armed bool // a sweep is waiting for the next collection
}

// sideLive returns the live node for key, or nil. Caller holds sideMu.
func (t *Tree) sideLive(key sideKey) *Node {
	if t.side == nil {
		return nil
	}
	if w, ok := t.side.m[key]; ok {
		return w.Value()
	}
	return nil
}

// sideAdd records s under key. Caller holds sideMu.
func (t *Tree) sideAdd(key sideKey, s *Node) {
	if t.side == nil {
		t.side = &sideTable{m: map[sideKey]weak.Pointer[Node]{}}
	}
	st := t.side
	st.seq++
	st.m[key] = weak.Make(s)
	if len(st.m) > st.peak {
		st.peak = len(st.m)
	}
	if !st.armed {
		st.armed = true
		// A cleanup per node costs a second runtime special each; one per
		// garbage collection, while the table is not empty, sweeps the
		// entries the collection has cleared.
		runtime.AddCleanup(&sideTick{}, sideSweep, t)
	}
}

// sideTick is what the sweep cleanup is attached to: nothing refers to it,
// so its cleanup runs after the next collection. It holds a pointer so the
// allocator does not batch it into a tiny block that outlives the cycle.
type sideTick struct{ _ *int }

// sideSweep drops the entries whose nodes a collection has freed, and gives
// the memory of a table that has shrunk a lot back (a Go map never does).
func sideSweep(t *Tree) {
	t.sideMu.Lock()
	defer t.sideMu.Unlock()
	st := t.side
	if st == nil {
		return
	}
	st.armed = false
	for k, w := range st.m {
		if w.Value() == nil {
			delete(st.m, k)
		}
	}
	switch {
	case len(st.m) == 0:
		t.side = nil
		return
	case len(st.m) < st.peak/4:
		m := make(map[sideKey]weak.Pointer[Node], len(st.m))
		for k, w := range st.m {
			m[k] = w
		}
		st.m, st.peak = m, len(m)
	}
	st.armed = true
	runtime.AddCleanup(&sideTick{}, sideSweep, t)
}

// sideSeq is the next sequence number. Caller holds sideMu.
func (t *Tree) sideSeq() uint32 {
	if t.side == nil {
		return 0
	}
	return t.side.seq
}

// sideTruncate forgets the namespace nodes of elements from index i on.
func (t *Tree) sideTruncate(i uint32) {
	t.sideMu.Lock()
	defer t.sideMu.Unlock()
	if t.side == nil {
		return
	}
	for k := range t.side.m {
		if k.owner >= i {
			delete(t.side.m, k)
		}
	}
	if len(t.side.m) == 0 {
		t.side = nil
	}
}
