package xdm

import (
	"maps"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
)

func init() {
	xdmclone.Clone = func(top any, o xdmclone.Options) func(any) any {
		m := cloneSubtree(top.(*Node), o)
		if m == nil {
			return nil
		}
		return func(n any) any { return m(n.(*Node)) }
	}
	xdmclone.NewLayer = func(top any) any {
		if n := top.(*Node); n.flags&fSide == 0 {
			return newTypingLayer(n)
		}
		return nil
	}
}

// cloneSubtree is xdmclone.Clone (see there). The records of top's subtree
// are copied in order with their indices renumbered; the stores the records
// point into are append-only, so the copy shares them, with capacities
// clipped so that whatever the copy appends lands in storage of its own.
func cloneSubtree(top *Node, o xdmclone.Options) func(*Node) *Node {
	if top.flags&fSide != 0 {
		return nil
	}
	t := top.tree
	lo, hi := top.self, t.endOf(top)
	n := hi - lo

	// With edits, newIdx[k] is the copy's index for source record lo+k, or
	// for a dropped one the index the next record gets. Attributes added to
	// an element go in front of the record just past its own attributes, so
	// that record and every later one move up by their count.
	var newIdx []uint32
	var dropped []bool
	type addition struct {
		at    uint32 // the source index they are inserted before
		owner uint32 // the element's source index
		attrs []any
	}
	// adds is in increasing order of at, as the elements are met, so both
	// walks below consume it with a cursor rather than a lookup per record.
	var adds []addition
	if o.Drop != nil || o.Add != nil {
		newIdx = make([]uint32, n+1)
		if o.Drop != nil {
			dropped = make([]bool, n)
		}
		delta := int64(0)
		next := 0 // the first addition not yet counted into delta
		for i := lo; i < hi; i++ {
			r := t.rec(i)
			if next < len(adds) && adds[next].at == i {
				delta += int64(len(adds[next].attrs))
				next++
			}
			newIdx[i-lo] = uint32(int64(i-lo) + delta)
			switch {
			case r.kind == uint8(KindElement) && o.Add != nil:
				if as := o.Add(r); len(as) > 0 {
					if r.flags&fManyAttrs != 0 || int(r.nattr)+len(as) >= 0xFFFF {
						return nil
					}
					adds = append(adds, addition{i + 1 + uint32(r.nattr), i, as})
				}
			case r.isLeaf() && r.kind != uint8(KindAttribute) && o.Drop != nil && i != lo:
				if o.Drop(r) {
					dropped[i-lo] = true
					delta--
				}
			}
		}
		if next < len(adds) && adds[next].at == hi {
			delta += int64(len(adds[next].attrs))
		}
		newIdx[n] = uint32(int64(n) + delta)
	}
	mapIdx := func(i uint32) uint32 {
		if newIdx == nil {
			return i - lo
		}
		return newIdx[i-lo]
	}
	// livePrev is the nearest sibling at or before i that was kept.
	livePrev := func(i uint32) uint32 {
		for dropped != nil && i != noIdx && dropped[i-lo] {
			i = t.rec(i).prev
		}
		return i
	}

	document := !o.Detached && !t.fragment && t.Root == top
	// The copy is not frozen, so it neither inherits nor builds an element
	// index (elemIndex): it may still be appended to.
	c := &Tree{fragment: !document}
	if document {
		c.id.Store(int64(nextTreeID()))
		c.DocType, c.XMLVersion = t.DocType, t.XMLVersion
		// The source part is the copy's own. Only its text fields are shared.
		if s := t.source; s != nil {
			c.source = &treeSource{externalSubset: s.externalSubset}
		}
	}
	layer, _ := o.Layer.(*typingLayer)
	if layer != nil && layer.src != t {
		layer = nil
	}
	c.names = t.names[:len(t.names):len(t.names)]
	c.nameIx = maps.Clone(t.nameIx)
	c.text.blocks = make([][]byte, len(t.text.blocks))
	for i, b := range t.text.blocks {
		c.text.blocks[i] = b[:len(b):len(b)]
	}
	c.text.size = t.text.size
	c.frames = t.frames[:len(t.frames):len(t.frames)]
	c.frameData = t.frameData[:len(t.frameData):len(t.frameData)]
	c.frameOne = maps.Clone(t.frameOne)
	c.frameMany = maps.Clone(t.frameMany)

	total := n
	if newIdx != nil {
		total = newIdx[n]
	}
	last, _ := chunkOf(total - 1)
	c.chunks = make([][]Node, last+1)
	for k := range c.chunks {
		c.chunks[k] = make([]Node, chunkLen(k))
	}
	c.n = total

	// place writes r as record j of the copy and returns it.
	place := func(j uint32, r Node) *Node {
		k, off := chunkOf(j)
		d := &c.chunks[k][off]
		*d = r
		return d
	}
	ai := 0 // the next addition to place
	addAttrs := func(at uint32) {
		if ai == len(adds) || adds[ai].at != at {
			return
		}
		a := adds[ai]
		ai++
		owner := mapIdx(a.owner)
		j := mapIdx(at) - uint32(len(a.attrs))
		for _, x := range a.attrs {
			src := x.(*Node)
			d := place(j, Node{tree: c, kind: uint8(KindAttribute), self: j, parent: owner,
				prev: noIdx, end: j + 1, name: c.intern(src.Name())})
			if v := src.Value(); v != "" {
				d.v0, d.v1 = c.text.add(v)
			}
			d.CopyTypingFrom(src)
			d.SetTypeEnv(src.TypeEnv())
			d.SetBaseURI(src.BaseURI())
			j++
		}
	}

	for i := lo; i < hi; i++ {
		if adds != nil {
			addAttrs(i)
		}
		if dropped != nil && dropped[i-lo] {
			continue
		}
		s := t.rec(i)
		j := mapIdx(i)
		r := *s
		r.tree, r.self = c, j
		if i == lo {
			r.parent, r.prev = noIdx, noIdx
		} else {
			r.parent = mapIdx(r.parent)
			if r.prev = livePrev(r.prev); r.prev != noIdx {
				r.prev = mapIdx(r.prev)
			}
		}
		if r.isLeaf() {
			r.end = j + 1
		} else {
			r.end = mapIdx(t.endOf(s))
			// addAttrs(i) has placed everything before i, so an
			// addition of i's own is the next.
			if ai < len(adds) && adds[ai].owner == i {
				r.nattr += uint16(len(adds[ai].attrs))
			}
			if r.v1 != noIdx {
				if r.v1 = livePrev(r.v1); r.v1 != noIdx {
					r.v1 = mapIdx(r.v1)
				}
			}
		}
		d := place(j, r)
		from, typed := s.typ(), s.flags&fTyped != 0
		if layer != nil {
			if lt, ltyped, ok := layer.typingAt(i); ok {
				from, typed = lt, ltyped
			}
		}
		if typed {
			dt := d.ownTyping()
			*dt = *from // the names are shared, not copied
			if o.Detached && dt.get().env != nil {
				v := *dt.names
				v.env = nil
				setNames(dt, v)
			}
		}
		if s.flags&fBase != 0 {
			if o.Detached && s.kind == uint8(KindAttribute) {
				d.flags &^= fBase
			} else {
				if c.bases == nil {
					c.bases = map[uint32]string{}
				}
				c.bases[j] = t.bases[i]
			}
		}
		if s.flags&fManyAttrs != 0 {
			if src := c.ownSource(); src.attrCounts == nil {
				src.attrCounts = map[uint32]uint32{}
			}
			c.source.attrCounts[j] = t.source.attrCounts[i]
		}
		if s.kind == uint8(KindDocument) && !o.Detached {
			if u := t.docURIs[i]; u != "" {
				if c.docURIs == nil {
					c.docURIs = map[uint32]string{}
				}
				c.docURIs[j] = u
			}
		}
	}
	if adds != nil {
		addAttrs(hi)
	}

	root := c.rec(0)
	if document {
		c.Root = root
	} else {
		root.SetBaseURI(top.BaseURI())
	}
	return func(x *Node) *Node {
		if x == nil || x.tree != t || x.flags&fSide != 0 || x.self < lo || x.self >= hi ||
			(dropped != nil && dropped[x.self-lo]) {
			return nil
		}
		return c.rec(mapIdx(x.self))
	}
}
