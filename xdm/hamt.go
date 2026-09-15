package xdm

import (
	"hash/maphash"
	"math/bits"
)

// A hash array mapped trie, the persistent structure behind MapItem.
//
// Each node holds up to 32 slots selected by five bits of the key's hash;
// bitmap records which slots are occupied and slots holds only those, so a
// sparse node costs a few words rather than 32. A slot is a *mapEntry (a leaf),
// a *hamtNode (a subtree for keys that share this prefix), or a *hamtCollision
// (keys whose 64-bit hashes are entirely equal, which the arithmetic makes
// possible and the tests must therefore survive).
//
// Every operation copies the nodes on one root-to-leaf path and shares the
// rest, so a map:put over 421,875 entries allocates four or five small nodes
// rather than a fresh 421,875-entry slice. The old map is untouched, which is
// what the data model's immutable maps require and what an O(n) copy was
// paying for on every call.

const (
	hamtBits  = 5
	hamtMask  = 1<<hamtBits - 1
	hamtDepth = 64 / hamtBits * hamtBits // the shift at which the hash is spent
)

var hamtSeed = maphash.MakeSeed()

func hamtHash(ckey string) uint64 { return maphash.String(hamtSeed, ckey) }

type hamtNode struct {
	bitmap uint32
	slots  []any
	// edit is the builder that may still mutate this node in place, or nil
	// once it has been published in a MapItem. A builder only writes through
	// nodes it created itself; anything older is copied first.
	edit *hamtEdit
}

type hamtEdit struct{}

type hamtCollision struct {
	hash    uint64
	entries []*mapEntry
}

func hamtIndex(bitmap uint32, bit uint32) int {
	return bits.OnesCount32(bitmap & (bit - 1))
}

// get returns the entry stored under ckey, or nil.
func (n *hamtNode) get(h uint64, shift uint, ckey string) *mapEntry {
	for n != nil {
		bit := uint32(1) << ((h >> shift) & hamtMask)
		if n.bitmap&bit == 0 {
			return nil
		}
		switch s := n.slots[hamtIndex(n.bitmap, bit)].(type) {
		case *mapEntry:
			if s.ckey == ckey {
				return s
			}
			return nil
		case *hamtCollision:
			return s.get(ckey)
		case *hamtNode:
			n = s
			shift += hamtBits
		}
	}
	return nil
}

func (c *hamtCollision) get(ckey string) *mapEntry {
	for _, e := range c.entries {
		if e.ckey == ckey {
			return e
		}
	}
	return nil
}

// mutable returns n itself when edit owns it and a copy otherwise.
func (n *hamtNode) mutable(edit *hamtEdit) *hamtNode {
	if edit != nil && n.edit == edit {
		return n
	}
	out := &hamtNode{bitmap: n.bitmap, slots: make([]any, len(n.slots), len(n.slots)+1), edit: edit}
	copy(out.slots, n.slots)
	return out
}

// put stores e, replacing an entry with the same canonical key, and reports
// whether the key was new. On a replacement e takes the replaced entry's
// sequence number, so that a map:put over an existing key leaves it where it
// was in iteration order, as replacing a slice element did.
func (n *hamtNode) put(e *mapEntry, shift uint, edit *hamtEdit) (*hamtNode, bool) {
	bit := uint32(1) << ((e.hash >> shift) & hamtMask)
	if n == nil {
		return &hamtNode{bitmap: bit, slots: []any{e}, edit: edit}, true
	}
	if n.bitmap&bit == 0 {
		i := hamtIndex(n.bitmap, bit)
		out := n.mutable(edit)
		out.slots = append(out.slots, nil)
		copy(out.slots[i+1:], out.slots[i:])
		out.slots[i] = e
		out.bitmap |= bit
		return out, true
	}
	i := hamtIndex(n.bitmap, bit)
	var slot any
	added := false
	switch s := n.slots[i].(type) {
	case *mapEntry:
		if s.ckey == e.ckey {
			e.seq = s.seq
			slot = e
		} else {
			slot = hamtMerge(s, e, shift+hamtBits, edit)
			added = true
		}
	case *hamtCollision:
		if s.hash == e.hash {
			slot, added = s.put(e)
		} else {
			slot = hamtMerge(s, e, shift+hamtBits, edit)
			added = true
		}
	case *hamtNode:
		slot, added = s.put(e, shift+hamtBits, edit)
	}
	out := n.mutable(edit)
	out.slots[i] = slot
	return out, added
}

// hamtMerge builds the subtree that holds an existing slot and a new entry
// whose hashes agree up to shift. It walks down while they keep agreeing,
// and when the whole hash agrees makes a collision node.
func hamtMerge(old any, e *mapEntry, shift uint, edit *hamtEdit) any {
	var oldHash uint64
	switch s := old.(type) {
	case *mapEntry:
		oldHash = s.hash
	case *hamtCollision:
		oldHash = s.hash
	}
	if shift >= hamtDepth {
		var entries []*mapEntry
		if c, ok := old.(*hamtCollision); ok {
			entries = append(entries, c.entries...)
		} else {
			entries = append(entries, old.(*mapEntry))
		}
		return &hamtCollision{hash: e.hash, entries: append(entries, e)}
	}
	ob := uint32(1) << ((oldHash >> shift) & hamtMask)
	nb := uint32(1) << ((e.hash >> shift) & hamtMask)
	if ob == nb {
		return &hamtNode{bitmap: ob, slots: []any{hamtMerge(old, e, shift+hamtBits, edit)}, edit: edit}
	}
	if ob < nb {
		return &hamtNode{bitmap: ob | nb, slots: []any{old, e}, edit: edit}
	}
	return &hamtNode{bitmap: ob | nb, slots: []any{e, old}, edit: edit}
}

func (c *hamtCollision) put(e *mapEntry) (*hamtCollision, bool) {
	out := &hamtCollision{hash: c.hash, entries: make([]*mapEntry, len(c.entries), len(c.entries)+1)}
	copy(out.entries, c.entries)
	for i, x := range out.entries {
		if x.ckey == e.ckey {
			e.seq = x.seq
			out.entries[i] = e
			return out, false
		}
	}
	out.entries = append(out.entries, e)
	return out, true
}

// remove drops the entry under ckey and reports whether one was there. A node
// left holding a single leaf is replaced by that leaf, and one left empty by
// nil, so that a long run of removals does not leave a trie of empty paths.
func (n *hamtNode) remove(h uint64, shift uint, ckey string) (any, bool) {
	bit := uint32(1) << ((h >> shift) & hamtMask)
	if n.bitmap&bit == 0 {
		return n, false
	}
	i := hamtIndex(n.bitmap, bit)
	var slot any
	switch s := n.slots[i].(type) {
	case *mapEntry:
		if s.ckey != ckey {
			return n, false
		}
	case *hamtCollision:
		c, ok := s.remove(ckey)
		if !ok {
			return n, false
		}
		if len(c.entries) == 1 {
			slot = c.entries[0]
		} else {
			slot = c
		}
	case *hamtNode:
		sub, ok := s.remove(h, shift+hamtBits, ckey)
		if !ok {
			return n, false
		}
		slot = sub
	}
	if slot == nil {
		if len(n.slots) == 1 {
			return nil, true
		}
		out := &hamtNode{bitmap: n.bitmap &^ bit, slots: make([]any, 0, len(n.slots)-1)}
		out.slots = append(out.slots, n.slots[:i]...)
		out.slots = append(out.slots, n.slots[i+1:]...)
		if len(out.slots) == 1 {
			if e, ok := out.slots[0].(*mapEntry); ok && shift > 0 {
				return e, true
			}
		}
		return out, true
	}
	out := n.mutable(nil)
	out.slots[i] = slot
	return out, true
}

func (c *hamtCollision) remove(ckey string) (*hamtCollision, bool) {
	for i, x := range c.entries {
		if x.ckey == ckey {
			out := &hamtCollision{hash: c.hash, entries: make([]*mapEntry, 0, len(c.entries)-1)}
			out.entries = append(out.entries, c.entries[:i]...)
			out.entries = append(out.entries, c.entries[i+1:]...)
			return out, true
		}
	}
	return nil, false
}

// walk calls f for every entry, in hash order.
func (n *hamtNode) walk(f func(*mapEntry)) {
	for _, s := range n.slots {
		switch s := s.(type) {
		case *mapEntry:
			f(s)
		case *hamtCollision:
			for _, e := range s.entries {
				f(e)
			}
		case *hamtNode:
			s.walk(f)
		}
	}
}
