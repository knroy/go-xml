package xdmbuild

// The buffer an xsl:try writes its body into when the builder the try
// writes for streams (internal/xdmsink.Try; TransformOptions.Output).
//
// Tree mode builds the body in a sequence builder of its own and, when the
// body succeeds, appends that sequence to out; a caught error discards it.
// Appending a sequence is appending its items one at a time, and nothing
// else touches out while the body runs. So this buffer hands each item on
// to out (forward) once it is complete -- when the next one starts -- with
// the calls the append would make, and keeps only the item being built: out
// sees the same calls in the same order, only earlier. Everything the body
// itself does (an attribute after content, the merge of text, a document
// node's children) still happens in the buffer, as in tree mode.
//
// With rollback-output="yes", what out writes from the first item handed on
// is provisional: a checkpoint holds out's builder state, its sink's, and
// the Writer's (xdmsink.Checkpointer), and a caught error puts them all back;
// success commits. Before the checkpoint, out's sink writes everything that
// is complete (settle), so a rollback takes back only what the buffer handed
// on. Out's newest child is written then but stays in its tree (the sink's
// anchor), as the sink always keeps a frame's newest child: a later attribute
// still meets XTDE0410, and the text merge sees what it saw. Settling is not
// done, and the try's output is held to its end as before, when out's newest
// child is text (the first item handed on could merge with it), when out is
// not open and cannot be opened by the first item, when the sink cannot
// start yet (the result so far does not decide the output method) or never
// will, and when the first item is an attribute, a
// namespace or a function item (appended at the end in tree mode, where it
// joins out's element or fails).
//
// An element the body starts at its top, with everything before it handed
// on, is built in place in out (start), so a try around one large element
// writes its content as the sink writes any element's, provisionally: tree
// mode appends the finished element to out with nothing between. A
// checkpoint's anchor then stays in out's tree across the tries that follow
// (everything appended after it was under a checkpoint): the sink keeps a
// set of them, one a frame at most.
//
// A buffer whose out is another try's buffer hands its items to that one,
// and its checkpoint nests: it covers the outer buffer's state as well.
//
// With rollback-output="no" (given only when the try's catches take every
// error) items are handed on without a checkpoint: written through.
//
// An item that cannot go early (an attribute, a namespace node or a function
// item, after the first) stops the handing on: it and the rest of the body's
// output are appended when the try ends, as in tree mode.

import (
	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/xdm"
)

type tryMode uint8

const (
	tryPending tryMode = iota // rollback: nothing handed on yet
	tryLive                   // rollback: handing on, under cp
	tryThrough                // no rollback: handing on
	tryHeld                   // the body's output is held to the try's end
)

// tryFwd makes a Builder a try's buffer.
type tryFwd struct {
	to      *Builder // the builder the try appends to
	mode    tryMode
	stopped bool // an item came that cannot go early: the rest is held
	// produced counts the items the body appended, handed on or not (tree
	// mode's len(Sequence()), for XTDE3530).
	produced int
	// charged counts the result nodes out charged for the items handed on
	// (a copy of another tree's node, a new text node): tree mode charges
	// them only when the body succeeds, so a rollback hands them back.
	charged int64
	cp      *tryMark
}

// tryMark is a checkpoint: the state rollback puts back.
type tryMark struct {
	s       *sink
	frames  int
	stamp   int64
	items   int
	opened  int64
	faults  int
	pending *Builder
	w       any
	base    levelMark
	fwds    []fwdMark // the buffers between this one and base, outermost last
}

type levelMark struct {
	b          *Builder
	items      int
	lastAtomic bool
	refused    error
}

type fwdMark struct {
	levelMark
	f tryFwd
}

// countPolicy counts what a policy is asked to charge, and is charged.
type countPolicy struct {
	Policy
	n *int64
}

func (p countPolicy) CountNodes(n int) error {
	err := p.Policy.CountNodes(n)
	if err == nil {
		*p.n += int64(n)
	}
	return err
}

func init() {
	xdmsink.Try = func(out any, p any, rollback bool) any {
		o := out.(*Builder)
		if g := o.fwd; g != nil && (g.mode == tryHeld || g.stopped) ||
			o.fwd == nil && (o.sink == nil || o.sink.off) {
			return nil
		}
		b := New(p.(Policy))
		b.fwd = &tryFwd{to: o, mode: tryPending}
		if !rollback {
			b.fwd.mode = tryThrough
		}
		return b
	}
	xdmsink.TryEnd = func(b any, commit bool) int64 {
		f := b.(*Builder).fwd
		if f == nil || f.cp == nil {
			return 0
		}
		m := f.cp
		f.cp = nil
		if commit {
			m.s.w.(xdmsink.Checkpointer).Commit(m.w)
			m.s.cps--
			return 0
		}
		return f.charged + m.rollback()
	}
}

// before is told that b, a try's buffer, is about to append an item.
func (f *tryFwd) before(b *Builder) {
	f.produced++
	if len(b.items) == 0 || f.stopped || f.mode == tryHeld {
		return
	}
	if f.mode == tryPending && !f.live(b) {
		f.mode = tryHeld
		return
	}
	f.forward(b)
}

// early reports whether it can be handed on before the try ends.
func early(it xdm.Item) bool {
	switch v := it.(type) {
	case *xdm.Node:
		return v.Kind() != xdm.KindAttribute && v.Kind() != xdm.KindNamespace
	case *xdm.Atomic:
		return true
	}
	return false
}

// live takes the checkpoint of b, a pending buffer, before its first item
// is handed on.
func (f *tryFwd) live(b *Builder) bool {
	if len(b.items) > 0 && !early(b.items[0]) {
		return false
	}
	m := &tryMark{}
	if !settle(f.to, m) {
		return false
	}
	f.cp, f.mode = m, tryLive
	return true
}

// forward hands b's items, all complete, on to f.to, as far as they can go.
func (f *tryFwd) forward(b *Builder) {
	to := f.to
	p := to.policy
	to.policy = countPolicy{p, &f.charged}
	n := 0
	for _, it := range b.items {
		if !early(it) {
			f.stopped = true
			break
		}
		if v, ok := it.(*xdm.Node); ok {
			to.AppendNode(v)
		} else {
			to.AppendValue(it.(*xdm.Atomic))
		}
		n++
	}
	xdmsink.Forwarded.Add(int64(n))
	to.policy = p
	rest := copy(b.items, b.items[n:])
	clear(b.items[rest:])
	b.items = b.items[:rest]
	if rest == 0 {
		b.frag = nil
	}
}

// start is told that b, a try's buffer, starts an element named name at the
// top of the body, with everything before it handed on: when b may hand on,
// the element is built in place in f.to instead, under the checkpoint, so
// the sink writes its content as it is made rather than b holding it as a
// tree to its end. Tree mode appends the finished element to f.to with
// nothing between, which is building it there: the same calls on f.to, in
// the same order, only earlier. The element is charged and latched as b's
// (refused, p). nil when the element stays in b.
func (f *tryFwd) start(b *Builder, name xdm.QName, refused *error, p Policy) *Builder {
	if f.stopped || f.mode == tryHeld || len(b.items) > 0 {
		return nil
	}
	if f.mode == tryPending && !f.live(b) {
		f.mode = tryHeld
		return nil
	}
	xdmsink.Forwarded.Add(1)
	t := f.to
	t.lastAtomic = false
	t.note(xdm.KindElement, "")
	if t.fwd != nil {
		if eb := t.fwd.start(t, name, refused, p); eb != nil {
			return eb
		}
	}
	var el *xdm.Node
	if t.open != nil {
		el = t.open.AppendElementInheriting(name)
	} else {
		el = t.newItem(xdm.KindElement, name, "")
		t.items = append(t.items, el)
	}
	return &Builder{open: el, parent: t, policy: p, refused: refused, sink: t.sink}
}

// settle readies t, the builder a try's buffer hands its items to, for a
// checkpoint, and records its state in m: a buffer hands on what it holds
// and its own builder is settled in turn; a streaming builder's sink writes
// what is complete. It reports false when no checkpoint can be taken.
func settle(t *Builder, m *tryMark) bool {
	if g := t.fwd; g != nil {
		if g.mode == tryPending && !g.live(t) {
			g.mode = tryHeld
		}
		if g.mode == tryHeld || g.stopped {
			return false
		}
		g.forward(t)
		if g.stopped {
			return false
		}
		m.fwds = append(m.fwds, fwdMark{levelMark{t, 0, t.lastAtomic, *t.refused}, *g})
		return settle(g.to, m)
	}
	// A sink that has not started begins now if the result so far decides
	// it, as the next append would: that decision does not change.
	s := t.sink
	if s == nil || s.off || !s.started && !s.begin() {
		return false
	}
	cw, ok := s.w.(xdmsink.Checkpointer)
	if !ok || !s.settle(t) {
		return false
	}
	*m = tryMark{s: s, frames: len(s.frames), stamp: s.stamp, items: s.items, opened: s.opened,
		faults: len(s.faults), pending: s.pending, fwds: m.fwds,
		base: levelMark{t, len(t.items), t.lastAtomic, *t.refused}}
	m.w = cw.Mark()
	s.cps++
	return true
}

// settle writes what is complete on the way to t, so that nothing the next
// append would write is left for it but what is appended after now; t's
// newest child is written and kept in its tree. false when t is not open
// and the next append could not open it, or its newest child is text.
func (s *sink) settle(t *Builder) bool {
	if t.hold {
		return false
	}
	if t.fr == 0 {
		// An element with nothing in it yet, the newest child of the
		// innermost frame: the first item opens it.
		p := s.up(t)
		if t.doc != 0 || t.open == nil || t.open.FirstChild() != nil || p == nil || p.fr == 0 ||
			s.frames[len(s.frames)-1] != p {
			return false
		}
		if p.open == nil {
			s.items_(p, t.open)
		}
		return true
	}
	// Finish the frames inside t, which have closed; t's child among them
	// stays in t's tree.
	var done *xdm.Node
	for {
		k := len(s.frames) - 1
		top := s.frames[k]
		if top == t {
			break
		}
		s.close(top, done)
		p := s.frames[k-1]
		done = nil
		switch {
		case p == t && p.open != nil:
			s.kept = top.open
		case p.open != nil:
			done = top.open
		case top.open != nil:
			s.streamed[top.open] = true
		}
	}
	if t.open == nil {
		s.items_(t, nil)
		return true
	}
	if c := t.open.LastChild(); c != nil && c != s.kept && !s.anchors[c] {
		if c.Kind() == xdm.KindText {
			return false
		}
		s.w.Child(c, s.depths[len(s.depths)-1]+1)
		s.kept = c
	}
	if s.cps == 0 && s.kept != nil {
		if a := t.open.FirstChild(); s.anchors[a] && a.NextSibling() == s.kept {
			// An earlier checkpoint's anchor, never taken back (everything
			// appended since was under that checkpoint): it stays this
			// frame's anchor, and the newest child, written, goes.
			t.open.RemoveLastChild()
		} else {
			s.anchors[s.kept] = true
		}
		s.kept = nil
	}
	return true
}

// rollback puts back what m recorded, and returns the result nodes the
// buffers between charged since.
func (m *tryMark) rollback() int64 {
	s := m.s
	s.w.(xdmsink.Checkpointer).Rollback(m.w)
	for len(s.frames) > m.frames {
		k := len(s.frames) - 1
		s.frames[k].fr = 0
		s.frames[k] = nil
		s.frames, s.depths = s.frames[:k], s.depths[:k]
	}
	t := m.base.b
	if t.open != nil {
		for c := t.open.LastChild(); c != nil && !s.anchors[c]; c = t.open.LastChild() {
			t.open.RemoveLastChild()
		}
		s.kept = nil
	} else {
		for _, it := range t.items[m.base.items:] {
			if nd, ok := it.(*xdm.Node); ok {
				delete(s.streamed, nd) // an element started in place (start) and closed
			}
		}
		clear(t.items[m.base.items:])
		t.items = t.items[:m.base.items]
	}
	m.base.restore()
	var charged int64
	for _, fm := range m.fwds {
		b := fm.b
		charged += b.fwd.charged - fm.f.charged
		clear(b.items)
		b.items = b.items[:0]
		*b.fwd = fm.f
		fm.restore()
	}
	s.stamp, s.items, s.pending = m.stamp, m.items, m.pending
	clear(s.faults[m.faults:])
	s.faults = s.faults[:m.faults]
	xdmsink.Opened.Add(m.opened - s.opened)
	s.opened = m.opened
	s.cps--
	return charged
}

func (l levelMark) restore() {
	l.b.lastAtomic = l.lastAtomic
	*l.b.refused = l.refused
}
