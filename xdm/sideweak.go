package xdm

import (
	"runtime"
	"weak"
)

// sideChunkLen is how many namespace nodes share one allocation, and so one
// weak handle: the runtime keeps a list per heap span of every object with a
// weak handle, and a handle per node made a namespace-heavy query several
// times slower.
const sideChunkLen = 16

type sideChunk [sideChunkLen]Node

// sideEnt locates a namespace node: slot i of a chunk, held weakly.
type sideEnt struct {
	w weak.Pointer[sideChunk]
	i uint8
}

// sideTable is a tree's namespace nodes. They are made on demand and held
// weakly, a chunk at a time: a node lives while a caller holds it or any
// node of its chunk, so pointer identity, Is and IdentityKey (which holds the
// pointer) stay true for every holder, and nodes nobody holds are freed
// instead of staying for the life of the tree. Their entries are swept after
// the next collection. seq numbers the nodes in the order they were made.
type sideTable struct {
	m     map[sideKey]sideEnt
	seq   uint32
	peak  int  // most entries held since the map was last rebuilt
	armed bool // a sweep is waiting for the next collection

	cur  *sideChunk // being filled; held strongly until the next sweep
	used int
	curW weak.Pointer[sideChunk]
}

// sideLive returns the live node for key, or nil. Caller holds sideMu.
func (t *Tree) sideLive(key sideKey) *Node {
	if t.side == nil {
		return nil
	}
	if e, ok := t.side.m[key]; ok {
		if c := e.w.Value(); c != nil {
			return &c[e.i]
		}
	}
	return nil
}

// sideAdd makes the node proto describes and records it under key. Caller
// holds sideMu.
func (t *Tree) sideAdd(key sideKey, proto Node) *Node {
	if t.side == nil {
		t.side = &sideTable{m: map[sideKey]sideEnt{}}
	}
	st := t.side
	if st.cur == nil || st.used == sideChunkLen {
		st.cur, st.used = new(sideChunk), 0
		st.curW = weak.Make(st.cur)
	}
	s := &st.cur[st.used]
	*s = proto
	st.m[key] = sideEnt{st.curW, uint8(st.used)}
	st.used++
	st.seq++
	if len(st.m) > st.peak {
		st.peak = len(st.m)
	}
	if !st.armed {
		st.armed = true
		// One cleanup per garbage collection, while the table is not
		// empty, sweeps the entries the collection has cleared.
		runtime.AddCleanup(&sideTick{}, sideSweep, t)
	}
	return s
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
	st.cur = nil // else its entries would never clear
	for k, e := range st.m {
		if e.w.Value() == nil {
			delete(st.m, k)
		}
	}
	switch {
	case len(st.m) == 0:
		t.side = nil
		return
	case len(st.m) < st.peak/4:
		m := make(map[sideKey]sideEnt, len(st.m))
		for k, e := range st.m {
			m[k] = e
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
