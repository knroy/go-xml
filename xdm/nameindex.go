package xdm

import (
	"sort"

	"github.com/knroy/go-xml/v2/internal/xdmindex"
)

func init() {
	xdmindex.Named = func(n any, uri, local string, add func(any)) bool {
		return n.(*Node).namedDescendants(uri, local, add)
	}
}

// elementIndex lists a parsed document's elements by expanded name: the
// record indices of the elements with name id k are, in document order,
// elems[start[k]:start[k+1]], and ids maps an expanded name to its id.
//
// It holds 4 bytes per element and a few per distinct name, which is about
// 1.6% of what a parsed XMark document retains. Building it reads every
// record once and takes 8 bytes per element of scratch.
type elementIndex struct {
	ids   map[[2]string]uint32
	start []uint32
	elems []uint32
}

// namedDescendants is xdmindex.Named (see there).
func (n *Node) namedDescendants(uri, local string, add func(any)) bool {
	t := n.tree
	// A frozen tree is a parsed document: complete, shared read-only, and
	// refusing appends, so an index built once stays valid. Any other tree
	// may still be growing.
	if t == nil || !t.frozen || n.flags&fSide != 0 {
		return false
	}
	if n.isLeaf() || n.v1 == noIdx {
		return true
	}
	ix := t.elemIndex.Load()
	if ix == nil {
		// Goroutines that miss together each build the same index from the
		// same records, so whichever store lands is correct.
		ix = t.buildElementIndex()
		t.elemIndex.Store(ix)
	}
	k, ok := ix.ids[[2]string{uri, local}]
	if !ok {
		return true
	}
	list := ix.elems[ix.start[k]:ix.start[k+1]]
	end := t.endOf(n)
	i := sort.Search(len(list), func(i int) bool { return list[i] > n.self })
	for ; i < len(list) && list[i] < end; i++ {
		add(t.rec(list[i]))
	}
	return true
}

// buildElementIndex indexes every element record of t by expanded name.
func (t *Tree) buildElementIndex() *elementIndex {
	// The name table holds prefixed names, so several entries can share an
	// expanded name; id maps each entry to its expanded name's id.
	ix := &elementIndex{ids: make(map[[2]string]uint32)}
	id := make([]uint32, len(t.names))
	for i, q := range t.names {
		k := [2]string{q.URI, q.Local}
		v, ok := ix.ids[k]
		if !ok {
			v = uint32(len(ix.ids))
			ix.ids[k] = v
		}
		id[i] = v
	}
	// One pass over the records collects each element with its name id
	// and counts them per name; a second, over that shorter list, places
	// them. Reading the 40-byte records twice cost twice as much.
	type el struct{ self, id uint32 }
	els := make([]el, 0, t.n/2)
	count := make([]uint32, len(ix.ids)+1)
	left := t.n
	for _, c := range t.chunks {
		if left == 0 {
			break
		}
		if uint32(len(c)) > left {
			c = c[:left]
		}
		for i := range c {
			if c[i].kind == uint8(KindElement) {
				k := id[c[i].name]
				els = append(els, el{c[i].self, k})
				count[k+1]++
			}
		}
		left -= uint32(len(c))
	}
	for k := 1; k < len(count); k++ {
		count[k] += count[k-1]
	}
	ix.start = count
	ix.elems = make([]uint32, len(els))
	next := make([]uint32, len(ix.ids))
	copy(next, count)
	for _, e := range els {
		ix.elems[next[e.id]] = e.self
		next[e.id]++
	}
	return ix
}
