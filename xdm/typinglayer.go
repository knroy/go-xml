package xdm

// typingLayer holds the typing a schema assessment gives the nodes of one
// subtree without writing it to their tree, which the caller may share. The
// assessment reads the original, and the typed copy is made once, afterwards,
// with the layer's typing in place of the original's (xdmclone.Options.Layer).
//
// The slot of record lo+i is entry i of a private tree's typing table, so the
// Node setters write it unchanged through a stand-in record. A slot starts as
// a copy of the record's own typing when it is first written; until then the
// record's own typing stands.
//
// A layer belongs to one assessment: it is not safe for concurrent use.
type typingLayer struct {
	src    *Tree
	lo, hi uint32
	slots  Tree
	state  []uint8 // per record: layerTouched, and fTyped once the slot has an entry
	p      Node    // the stand-in
}

const layerTouched = 1 << 7

func newTypingLayer(top *Node) *typingLayer {
	t := top.tree
	return &typingLayer{src: t, lo: top.self, hi: t.endOf(top), state: make([]uint8, t.endOf(top)-top.self)}
}

// slot reports n's index in the layer.
func (l *typingLayer) slot(n *Node) (uint32, bool) {
	if n.tree != l.src || n.flags&fSide != 0 || n.self < l.lo || n.self >= l.hi {
		return 0, false
	}
	return n.self - l.lo, true
}

// open returns the stand-in for n's slot, to write through and then close,
// or nil when n is outside the layer and is written to itself.
func (l *typingLayer) open(n *Node) *Node {
	i, ok := l.slot(n)
	if !ok {
		return nil
	}
	l.p = Node{tree: &l.slots, self: i}
	if s := l.state[i]; s&layerTouched != 0 {
		l.p.flags = s & fTyped
	} else if n.flags&fTyped != 0 {
		*l.p.ownTyping() = *n.typ()
	}
	return &l.p
}

func (l *typingLayer) close() { l.state[l.p.self] = layerTouched | l.p.flags&fTyped }

// view returns what answers for n's typing: its slot's stand-in once
// written, otherwise n.
func (l *typingLayer) view(n *Node) *Node {
	if i, ok := l.slot(n); ok && l.state[i]&layerTouched != 0 {
		l.p = Node{tree: &l.slots, self: i, flags: l.state[i] & fTyped}
		return &l.p
	}
	return n
}

// typingAt returns the typing the layer holds for record i of src, and
// whether it holds one; typed reports whether that typing is an entry.
func (l *typingLayer) typingAt(i uint32) (t *nodeTyping, typed, ok bool) {
	if i < l.lo || i >= l.hi {
		return nil, false, false
	}
	s := l.state[i-l.lo]
	if s&layerTouched == 0 {
		return nil, false, false
	}
	if s&fTyped == 0 {
		return nil, false, true
	}
	k, off := chunkOf(i - l.lo)
	return &l.slots.typing[k][off], true, true
}

// The methods below are Node's typing setters and readers, writing to n's
// slot when n is in the layer and to n otherwise. Package xsd reaches them
// through xdmclone.NewLayer.

func (l *typingLayer) SetAssessedTyping(n *Node, annotation, derivedPrimitive, listItem string,
	env *TypeEnvironment, noTypedValue, mixedContent bool) {
	if p := l.open(n); p != nil {
		p.SetAssessedTyping(annotation, derivedPrimitive, listItem, env, noTypedValue, mixedContent)
		l.close()
		return
	}
	n.SetAssessedTyping(annotation, derivedPrimitive, listItem, env, noTypedValue, mixedContent)
}

func (l *typingLayer) SetTypeAnnotationResolved(n *Node, annotation, derivedPrimitive, listItem string) {
	if p := l.open(n); p != nil {
		p.SetTypeAnnotationResolved(annotation, derivedPrimitive, listItem)
		l.close()
		return
	}
	n.SetTypeAnnotationResolved(annotation, derivedPrimitive, listItem)
}

func (l *typingLayer) SetTypeEnv(n *Node, e *TypeEnvironment) {
	if p := l.open(n); p != nil {
		p.SetTypeEnv(e)
		l.close()
		return
	}
	n.SetTypeEnv(e)
}

func (l *typingLayer) ApplyTyping(n *Node, t Typing) {
	if p := l.open(n); p != nil {
		p.ApplyTyping(t)
		l.close()
		return
	}
	n.ApplyTyping(t)
}

func (l *typingLayer) TypingOf(n *Node) Typing { return TypingOf(l.view(n)) }

// CopyTyping gives dst src's typing and type environment, as the layer has them.
func (l *typingLayer) CopyTyping(dst, src *Node) {
	s := l.view(src)
	dst.CopyTypingFrom(s)
	dst.SetTypeEnv(s.TypeEnv())
}
