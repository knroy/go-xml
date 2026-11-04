package xdm

import "github.com/knroy/go-xml/v2/internal/xdmburst"

// A validator on the stream adds default attributes to the start tag being
// told (xdmburst.AppendAttr), as the parser adds an element's own.
func init() {
	xdmburst.AppendAttr = func(el any, prefix, uri, local, value string) any {
		n := el.(*Node)
		t := n.tree
		if n.Kind() != KindElement || t.frozen || n.v1 != noIdx || t.n != n.self+1+n.attrCount() {
			return nil
		}
		a := n.appendAttrRaw()
		a.name = t.intern(QName{Prefix: prefix, URI: uri, Local: local})
		if value != "" {
			a.v0, a.v1 = t.text.add(value)
		}
		return a
	}
	xdmburst.AddNamespace = func(el any, prefix, uri string) bool {
		n := el.(*Node)
		t := n.tree
		if n.Kind() != KindElement || t.frozen || n.v1 != noIdx || t.n != n.self+1+n.attrCount() {
			return false
		}
		n.AddNamespace(prefix, uri)
		s := t.ownSource()
		if s.nsAdded == nil {
			s.nsAdded = map[uint32]int{}
		}
		s.nsAdded[n.self]++
		return true
	}
	xdmburst.RemoveAttr = func(el, a any) bool {
		n, x := el.(*Node), a.(*Node)
		t := n.tree
		if n.Kind() != KindElement || t.frozen || n.v1 != noIdx || n.flags&fManyAttrs != 0 ||
			t.n != n.self+1+n.attrCount() || x.tree != t || x.parent != n.self || x.Kind() != KindAttribute {
			return false
		}
		// The attributes are the tree's last records: each one past x moves
		// down a slot, its typing and base URI with it.
		for i := x.self; i+1 < t.n; i++ {
			d, s := t.rec(i), t.rec(i+1)
			*d = *s
			d.self, d.end = i, i+1
			if s.flags&fTyped != 0 {
				*d.ownTyping() = *s.typ()
			}
			if s.flags&fBase != 0 {
				t.bases[i] = t.bases[i+1]
				delete(t.bases, i+1)
			}
		}
		t.n--
		n.nattr--
		return true
	}
}

// takeAddedNS is how many of el's namespace declarations AddNamespace added,
// which the parse's scope does not hold, forgetting them: el is closing.
func (t *Tree) takeAddedNS(el *Node) int {
	if t.source == nil || t.source.nsAdded == nil {
		return 0
	}
	k := t.source.nsAdded[el.self]
	delete(t.source.nsAdded, el.self)
	return k
}
