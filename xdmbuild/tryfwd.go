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
// still meets XTDE0410, and the text merge sees what it saw. A newest child
// that is text is not written: the first item handed on may merge with it,
// as at the end of the try, and it is written when the next append comes,
// provisionally; a rollback puts its value back, unwritten, for the catch's
// text to merge with. Settling is not done, and the try's output is held to
// its end as before, when out is not open and cannot be opened by the first
// item, when the sink never will start, and when the first item is a
// namespace or a function item, or an attribute that cannot go on to out's
// element yet (appended at the end in tree mode, where it joins out's
// element or fails). Attributes first go on to out's element when it has
// nothing in it (attrsOK): its start is written when its first child
// comes, so only that is held; the mark keeps a copy of the element, put
// back in its place on a rollback.
//
// An element the body starts at its top, with everything before it handed
// on, is built in place in out (start), so a try around one large element
// writes its content as the sink writes any element's, provisionally: tree
// mode appends the finished element to out with nothing between. A
// checkpoint's anchor then stays in out's tree across the tries that follow
// (everything appended after it was under a checkpoint): the sink keeps a
// set of them, one a frame at most.
//
// A sink that cannot start yet (the output method waits for the result's
// first element) is marked before it begins, when the try writes at the
// top of the result: its Begin, and the method it chooses from what the try
// hands on, are provisional too, and a rollback leaves it undecided.
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
	"strings"

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
	// attrs: the body's first items are attributes that may go on to to's
	// element, not opened yet (attrsOK), until another item comes.
	attrs bool
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
	started bool // the sink had begun: a mark taken before can un-begin it
	// text is the value of base's newest child when it is text, left
	// unwritten (sink.settle): what is handed on may merge into it, and a
	// rollback puts it back as it was, for the catch to merge into.
	text    string
	hasText bool
	// elem is a copy of base's element, taken when attributes go on to it:
	// a rollback puts it back in its place (xdm has no taking an attribute
	// off), before anything is in it.
	elem    *xdm.Node
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
			b.fwd.mode, b.fwd.attrs = tryThrough, attrsOK(o)
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
	attr := len(b.items) > 0 && isAttr(b.items[0])
	if len(b.items) > 0 && !early(b.items[0]) && (!attr || !attrsOK(f.to)) {
		return false
	}
	m := &tryMark{}
	if !settle(f.to, m) {
		return false
	}
	if attr {
		m.elem = xdm.Copy(f.to.open)
		xdm.CopyTypeEnvs(m.elem, f.to.open)
		f.attrs = true
	}
	f.cp, f.mode = m, tryLive
	return true
}

func isAttr(it xdm.Item) bool {
	n, ok := it.(*xdm.Node)
	return ok && n.Kind() == xdm.KindAttribute
}

// attrsOK reports whether attributes at the top of a try's body may be
// handed on to t ahead of the try's end: t streams, and its element has
// nothing in it yet, so the sink has not written its start (it does when
// the first child comes), and the attributes join it as the end-of-try
// append would. t's mark, when there is one, keeps a copy to put back.
func attrsOK(t *Builder) bool {
	return t.fwd == nil && t.sink != nil && t.fr == 0 && t.doc == 0 &&
		t.open != nil && t.open.Kind() == xdm.KindElement && t.open.FirstChild() == nil
}

// forward hands b's items, all complete, on to f.to, as far as they can go.
func (f *tryFwd) forward(b *Builder) {
	to := f.to
	p := to.policy
	to.policy = countPolicy{p, &f.charged}
	n := 0
	for _, it := range b.items {
		if !early(it) && (!f.attrs || !isAttr(it)) {
			f.stopped = true
			break
		}
		f.attrs = f.attrs && isAttr(it)
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
	f.attrs = false
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
	// One that cannot decide yet (the output method waits for the first
	// element) is marked before it begins, when t is the top of the result:
	// whatever the try hands on then decides it provisionally, and a
	// rollback leaves it undecided again for what comes next.
	s := t.sink
	if s == nil || s.off {
		return false
	}
	cw, ok := s.w.(xdmsink.Checkpointer)
	if !ok {
		return false
	}
	if !s.started && !s.begin() {
		if s.off || t != s.root {
			return false
		}
	} else if !s.settle(t) {
		return false
	}
	var text string
	hasText := false
	if t.open != nil {
		if c := t.open.LastChild(); c != nil && c.Kind() == xdm.KindText && c != s.kept && !s.anchors[c] {
			text, hasText = strings.Clone(c.Value()), true // the store may compact
		}
	}
	*m = tryMark{s: s, started: s.started, text: text, hasText: hasText, frames: len(s.frames), stamp: s.stamp, items: s.items, opened: s.opened,
		faults: len(s.faults), pending: s.pending, fwds: m.fwds,
		base: levelMark{t, len(t.items), t.lastAtomic, *t.refused}}
	m.w = cw.Mark()
	s.cps++
	return true
}

// settle writes what is complete on the way to t, so that nothing the next
// append would write is left for it but what is appended after now; t's
// newest child is written and kept in its tree, unless it is text, which
// stays unwritten (what comes next may merge into it; tryMark.text). false
// when t is not open and the next append could not open it.
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
			return true
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
		if m.hasText {
			t.open.AppendText(m.text) // written since, merged into, or neither
		}
		if m.elem != nil {
			t.ReplaceOpen(m.elem) // the attributes the try handed on taken off
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
	s.started, s.off = m.started, false // off: decided by a Begin since (settle needs !off)
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
