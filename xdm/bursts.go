package xdm

import (
	"fmt"
	"io"
	"sort"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmclone"
)

// Burst-mode parsing: the incremental half of streamed XSLT execution.
//
// ParseBursts reads a document exactly as Parse does -- the same tokeniser,
// the same well-formedness checks, the same DOCTYPE handling, and the same
// MaxBytes, MaxNodes and MaxDepth, counted over the whole document -- but it
// never holds the whole tree. Only the chain of open ancestors (the "spine")
// is kept. Each element at the end of path is handed to fn once it is
// complete, as the document element's descendant in a small tree of its own,
// and is then dropped, records and text both. Everything else -- elements off
// the path, and the text, comments and processing instructions directly
// inside a spine element -- is built, checked, and dropped the same way.
//
// The tree fn receives holds the document node (with the document's base and
// document URIs), a shallow copy of each ancestor (name, namespace
// declarations, attributes, base URI) and a deep copy of the element. So the
// element's base URI, in-scope namespaces, attributes, parent and ancestors
// read as they would in the whole tree; its siblings, and anything else
// outside its ancestry -- the comments and processing instructions outside
// the document element too -- do not exist there. Each burst is a new tree, so
// nodes from different bursts are distinct and order by creation, which is
// document order; a node fn keeps stays valid after fn returns.

// ParseBursts parses r and calls fn with each element whose ancestry of
// element names, from the document element down, is path, of at most
// xdmburst.MaxSteps names. An error from fn stops the parse and is returned.
func ParseBursts(r io.Reader, opts ParseOptions, path []QName, fn func(*Node) error) error {
	steps := make([]xdmburst.Step, len(path))
	for i, q := range path {
		steps[i] = xdmburst.Step{URI: q.URI, Local: q.Local}
	}
	return parseBursts(r, opts, steps, fn)
}

// parseBursts is ParseBursts over a pattern that may have wildcards and
// descendant steps. An element whose ancestry matches it is a burst; one
// whose ancestry could still lead to a match below it is kept on the spine;
// any other is dropped. A match inside a burst is delivered with it, so the
// bursts are the outermost matches, in document order.
func parseBursts(r io.Reader, opts ParseOptions, steps []xdmburst.Step, fn func(*Node) error) error {
	if len(steps) == 0 || len(steps) > xdmburst.MaxSteps {
		panic("xdm: ParseBursts needs a path of 1 to 63 names")
	}
	opts.bursts = &burster{c: &patternClient{steps: steps, states: []uint64{1}}, fn: fn, gens: ordsOf(0)}
	_, err := parse(r, "", opts)
	return err
}

// burster is the stream driver: the state a streamed parse keeps inside
// parse. Its client decides, at each start tag outside an element already
// decided, whether the element is kept on the spine (its content then met
// piece by piece), built whole and copied out at its end (grounded: handed
// to the client's end at once, or, for GroundLater, to fn in a batch), or
// built, checked and dropped. ParseBursts' client is patternClient; the
// pull cursor (internal/xdmstream) is the other, through xdmburst.Drive.
type burster struct {
	c  streamClient
	fn func(*Node) error
	// leaves hands c the text, comments and processing instructions of the
	// spine, the document node's own too, before they are dropped.
	leaves bool
	// strip, when set, says whitespace-only text directly inside an open
	// element is dropped as it is parsed (xdmburst.Stripping).
	strip func(el *Node) bool
	// watch, when set, is told of every element's start and end, those
	// inside a burst or a dropped element too, and (end false) of every
	// text, comment and processing instruction, once complete, before the
	// next start or end it is told of. Only the stream driver sets it.
	watch func(el *Node, d int, end bool)
	// watched is the record of the last leaf watch was told of inside the
	// element being built whole (drop).
	watched uint32
	// spine is how many levels of the open chain could lead to a match and
	// are kept.
	spine int
	// drop is the depth of the element being built only to be dropped (a
	// burst, or an element off the path), or 0; burst says it is a burst.
	drop  int
	burst bool
	// quiet is the depth of the element being discarded (Discard), or 0:
	// it and its descendants are kept on the spine, so that each closed
	// child is taken back as a spine element's is, but the client is told
	// nothing of them.
	quiet int
	// later says the burst is GroundLater's; grounded is the copy of the
	// one just closed that is not.
	later    bool
	grounded *Node
	// marks[d] is the tree's state once the spine element at depth d (0:
	// the document node) had stored its attributes. Everything stored after
	// it belongs to content that is dropped.
	marks markStack
	// gens[d] tells the spine element at depth d from the others that have
	// been there: gen counts the elements the client was asked about.
	gens ordStack
	gen  uint64
	// batch holds the small bursts not yet delivered (burstBatch), or is nil.
	batch *burstBatch
	// spineCopies counts the records copied as ancestors of a burst, for
	// the test that keeps it in proportion to the elements read.
	spineCopies int
	// spineAttrs counts the attributes copied with those ancestors, and
	// burstRecs the records of the bursts themselves (spineAttrsPerRecord).
	spineAttrs, burstRecs int
	// moved is the first of the parse's chunks the last burst's tree took
	// over, or 0.
	moved int
	v11   bool

	// For the stream driver only (ords non-nil), what copies out of the
	// stream are given to carry their originals' order and identity
	// (streamview.go). ords[d] is the ordinal of the spine element at depth
	// d, beside gens; discarded counts the records the parse has taken back,
	// so that record i of its tree is the original numbered i + discarded.
	ords      *ordStack
	discarded uint64
	origin    *streamOrigin
	// cur is the element start is asking the client about, at depth curD,
	// whose gen is gen; views is the batch its view goes into.
	cur   *Node
	curD  int
	views *viewBatch
	// paused is where a stream client paused the parse (errPause), for
	// resume: at pel, an element's start or end or a leaf, at depth pd; the
	// leaves were told by trimLeaves (pcur, pspaces, pnames) or by prolog
	// (pcur the document node, prologPause).
	paused      pauseAt
	pel, pcur   *Node
	pd          int
	pspaces     *spaceTable
	pnames      *nameCache
	prologPause bool
	// viewCopies counts the records views copied for ancestors, and
	// viewScans the copies they looked past, for the test that keeps them
	// in proportion to the elements viewed.
	viewCopies, viewScans int
	// leafDrops counts the leaves dropLeaf took back, for the test that
	// keeps them off RemoveLastChild's general path.
	leafDrops int
	// elemDrops counts the elements dropElement took back, for the test
	// that keeps them off it too.
	elemDrops int
	// closing says the token after the leaves trimLeaves is told of is the
	// end tag of the element they are in, as written (the parse loop sets
	// it); ahead is the client as an xdmburst.EndAhead, or nil.
	closing bool
	ahead   xdmburst.EndAhead
	// trims counts the calls of trimLeaves, and leafWalks the leaves told
	// by its general walk (tellFrom), for the test that keeps the common
	// case off both.
	trims, leafWalks int
}

// burstMark is how much of the text store, the name table and the frame
// table a spine element and its ancestors use.
type burstMark struct {
	text                     textMark
	names, frames, frameData int
}

func markOf(t *Tree) burstMark {
	return burstMark{t.text.mark(), len(t.names), len(t.frames), len(t.frameData)}
}

// The per-depth state of the spine is held in runs, so that a deep document
// does not cost a word or more per open element beside its record: the
// marks of nested elements are mostly equal, and their gens and ordinals
// step evenly.

// markStack is a stack of marks, one per depth, held as runs of equal ones.
type markStack struct {
	runs []markRun
	n    int
}

// markRun is the mark of the depths from at to the next run's.
type markRun struct {
	at int
	m  burstMark
}

func (s *markStack) len() int { return s.n }

func (s *markStack) push(m burstMark) {
	if k := len(s.runs); k == 0 || s.runs[k-1].m != m {
		s.runs = append(s.runs, markRun{s.n, m})
	}
	s.n++
}

func (s *markStack) pop() {
	s.n--
	if k := len(s.runs); s.runs[k-1].at == s.n {
		s.runs = s.runs[:k-1]
	}
}

func (s *markStack) top() burstMark { return s.runs[len(s.runs)-1].m }

// reset leaves m alone on the stack.
func (s *markStack) reset(m burstMark) {
	s.runs, s.n = s.runs[:0], 0
	s.push(m)
}

// ordStack is a stack of numbers, one per depth, held as runs in
// arithmetic progression.
type ordStack struct {
	runs []ordRun
}

// ordRun holds the cnt depths from at: v, v+step, ...
type ordRun struct {
	at, cnt int32
	v, step uint64
}

// ordsOf is a stack holding vs.
func ordsOf(vs ...uint64) ordStack {
	var s ordStack
	for _, v := range vs {
		s.push(v)
	}
	return s
}

func (s *ordStack) len() int {
	k := len(s.runs)
	if k == 0 {
		return 0
	}
	r := &s.runs[k-1]
	return int(r.at + r.cnt)
}

func (s *ordStack) push(v uint64) {
	if k := len(s.runs); k > 0 {
		r := &s.runs[k-1]
		switch {
		case r.cnt == 1:
			r.step = v - r.v
			r.cnt++
			return
		case v == r.v+uint64(r.cnt)*r.step:
			r.cnt++
			return
		}
	}
	s.runs = append(s.runs, ordRun{at: int32(s.len()), cnt: 1, v: v})
}

// at is the number at depth i.
func (s *ordStack) at(i int) uint64 {
	k := len(s.runs) - 1
	if int(s.runs[k].at) > i {
		k = sort.Search(k, func(j int) bool { return int(s.runs[j+1].at) > i })
	}
	r := s.runs[k]
	return r.v + uint64(i-int(r.at))*r.step
}

// truncate keeps the numbers below depth n.
func (s *ordStack) truncate(n int) {
	if n >= s.len() {
		return
	}
	k := len(s.runs)
	for k > 0 && int(s.runs[k-1].at) >= n {
		k--
	}
	s.runs = s.runs[:k]
	if k > 0 {
		s.runs[k-1].cnt = int32(n) - s.runs[k-1].at
	}
}

// streamClient is what a burster asks and tells (see burster). Its nodes
// are the parse's own: valid until the call returns, and not to be kept.
type streamClient interface {
	// start decides what becomes of el, just opened at depth d under a
	// spine element.
	start(el *Node, d int) (xdmburst.Action, error)
	// leaf is told of n, a leaf at depth d inside a spine element (or the
	// document node), once it is complete; only with burster.leaves.
	leaf(n *Node, d int) error
	// end is told that el, at depth d, has closed: an element start kept
	// (kept); or one it grounded, when g is its copy (nil for GroundLater,
	// whose copy goes to fn); or dropped, still whole in the parse's tree.
	end(el *Node, d int, kept bool, g *Node) error
}

// patternClient is ParseBursts' client: an element whose ancestry matches
// the pattern is a burst; one whose ancestry could still lead to a match
// below it is kept on the spine; any other is dropped.
type patternClient struct {
	steps []xdmburst.Step
	// states[d] is the set of pattern steps the spine element at depth d
	// (0: the document node) has matched, as bits: bit k means the first k
	// steps are matched by it and its ancestors.
	states []uint64
}

func (p *patternClient) start(el *Node, d int) (xdmburst.Action, error) {
	n := p.advance(p.states[len(p.states)-1], el.Name())
	switch {
	case n&(1<<len(p.steps)) != 0:
		return xdmburst.GroundLater, nil
	case n != 0:
		p.states = append(p.states, n)
		return xdmburst.Keep, nil
	}
	// Off the path: checked, and dropped as it is parsed.
	return xdmburst.Discard, nil
}

func (p *patternClient) leaf(*Node, int) error { return nil }

func (p *patternClient) end(_ *Node, _ int, kept bool, _ *Node) error {
	if kept {
		p.states = p.states[:len(p.states)-1]
	}
	return nil
}

// start classifies the element just built at depth d and opened as el.
func (b *burster) start(el *Node, d int) error {
	if b.watch != nil {
		b.watch(el, d, false)
	}
	if b.drop != 0 {
		return nil
	}
	if b.quiet != 0 {
		b.keep(el, d)
		return nil
	}
	// Every element asked about has a gen of its own, so that its view can
	// serve as the copy of it its descendants' views share once it is kept.
	b.gen++
	b.cur, b.curD = el, d
	act, err := b.c.start(el, d)
	if err != nil {
		b.cur = nil
		return err
	}
	if act == xdmburst.Pause {
		b.paused, b.pel, b.pd = pausedStart, el, d
		return errPause
	}
	b.started(el, d, act)
	return nil
}

// errPause is a stream client's pause (xdmburst.Steps): the parse returns
// it, and goes on once resume has finished what the pause interrupted.
var errPause = xdmburst.ErrPause

// pauseAt is what a pause interrupted.
type pauseAt uint8

const (
	notPaused pauseAt = iota
	pausedStart
	pausedEnd
	pausedLeaf
	// pausedLeafOne: at the one leaf trimLeaves met (pel, inside pcur),
	// which nothing follows: resume only takes it back.
	pausedLeafOne
)

// resume finishes what the pause of the parse interrupted, act deciding the
// element whose start it paused at; it may pause again (errPause).
func (b *burster) resume(act xdmburst.Action) error {
	p := b.paused
	b.paused = notPaused
	switch p {
	case pausedStart:
		b.started(b.pel, b.pd, act)
	case pausedEnd:
		return b.ended(b.pel, b.pd, b.pspaces, b.pnames)
	case pausedLeafOne:
		return b.trimRest(b.pcur, b.pspaces, b.pnames)
	case pausedLeaf:
		if err := b.tellFrom(b.pel.tree, b.pel.self+1, b.pd); err != nil {
			return err
		}
		if b.prologPause {
			b.prologDone(b.pcur)
			return nil
		}
		return b.trimRest(b.pcur, b.pspaces, b.pnames)
	}
	return nil
}

// started is start's, once the client has said what becomes of el.
func (b *burster) started(el *Node, d int, act xdmburst.Action) {
	b.cur = nil
	switch act {
	case xdmburst.Ground, xdmburst.GroundLater:
		b.drop, b.burst, b.later = d, true, act == xdmburst.GroundLater
		b.watched = el.self
	case xdmburst.Keep:
		b.keep(el, d)
	case xdmburst.Discard:
		b.quiet = d
		b.keep(el, d)
	default:
		b.drop = d
		b.watched = el.self
	}
}

// keep puts el, just opened at depth d, on the spine.
func (b *burster) keep(el *Node, d int) {
	b.spine = d
	b.marks.push(markOf(el.tree))
	b.gens.push(b.gen)
	if b.ords != nil {
		b.ords.push(b.ordOf(el))
	}
}

// advance is the set of steps matched by an element named q whose parent
// matched the set s.
func (p *patternClient) advance(s uint64, q QName) uint64 {
	var n uint64
	for k, st := range p.steps {
		if s&(1<<k) == 0 {
			continue
		}
		if st.Descendant {
			n |= 1 << k // q may be on the way to step k
		}
		if (st.AnyURI || st.URI == q.URI) && (st.AnyLocal || st.Local == q.Local) {
			n |= 1 << (k + 1)
		}
	}
	return n
}

// end handles the close of el, at depth d, whose parent is now current. It
// delivers a burst and drops whatever is not spine.
func (b *burster) end(el *Node, d int, spaces *spaceTable, names *nameCache) error {
	if b.watch != nil {
		b.watch(el, d, true)
	}
	kept := false
	switch {
	case b.drop == d:
		b.drop = 0
		if b.burst {
			b.burst = false
			if err := b.take(el); err != nil {
				return err
			}
		}
	case b.drop == 0 && d == b.spine && d > 0:
		kept = true
		b.spine--
		b.marks.pop()
		b.gens.truncate(b.gens.len() - 1)
		if b.ords != nil {
			b.ords.truncate(b.ords.len() - 1)
		}
		if b.quiet != 0 {
			if d > b.quiet {
				// Inside a discarded element: taken back now, told nothing.
				b.discardLast(el.Parent(), spaces, names)
				return nil
			}
			b.quiet, kept = 0, false // the discarded element: told as skipped
		}
	default:
		return nil
	}
	g := b.grounded
	b.grounded = nil
	if err := b.c.end(el, d, kept, g); err != nil {
		if err == errPause {
			b.paused, b.pel, b.pd, b.pspaces, b.pnames = pausedEnd, el, d, spaces, names
		}
		return err
	}
	return b.ended(el, d, spaces, names)
}

// ended is end's, once the client has been told of el's close.
func (b *burster) ended(el *Node, d int, spaces *spaceTable, names *nameCache) error {
	if d == 1 {
		// The document element is done: so is the last batch.
		if err := b.flush(); err != nil {
			return err
		}
	}
	b.discardLast(el.Parent(), spaces, names)
	return nil
}

// trimLeaves drops the leaves completed directly inside the spine element
// cur, at depth d: text, comments and processing instructions no path step
// selects. With leaves, the client is told of them first, and the document
// node's own (d 0) are trimmed too once the document element has opened
// (see prolog).
//
// It is also where a batch is delivered early: what the parse stored since
// cur's mark is all garbage now, and once a child of cur opens it cannot be
// taken back until cur closes, so it is not let grow past burstFrozen.
func (b *burster) trimLeaves(cur *Node, d int, spaces *spaceTable, names *nameCache) error {
	b.trims++
	if b.watch != nil && (b.drop != 0 || b.quiet != 0 && d == b.spine) {
		// Inside an element built whole or discarded the client is told
		// nothing, the watch is: of the leaves completed at the end of
		// cur's children since it was last told, which in a discarded
		// element are all of them (they are taken back below), and in one
		// built whole those past the last told (watched: records there are
		// not reused before it closes).
		first := cur.LastChild()
		if first == nil || !first.isLeaf() || b.drop != 0 && first.self <= b.watched {
			first = nil
		}
		for first != nil {
			p := first.PrevSibling()
			if p == nil || !p.isLeaf() || b.drop != 0 && p.self <= b.watched {
				break
			}
			first = p
		}
		for n := first; n != nil; n = n.NextSibling() {
			b.watch(n, d+1, false)
			b.watched = n.self
		}
	}
	if b.drop != 0 || d != b.spine || d == 0 && (!b.leaves || b.marks.len() == 0) {
		return nil
	}
	if b.leaves && b.quiet == 0 {
		// The common case first: one leaf, the tree's last record, with no
		// leaf before it, told with no watch to tell, and on the client's
		// pause taken back with nothing more to tell (pausedLeafOne).
		if t, i := cur.tree, cur.v1; i != noIdx && i+1 == t.n && b.watch == nil {
			if n := t.rec(i); n.isLeaf() && (n.prev == noIdx || !t.rec(n.prev).isLeaf()) {
				var err error
				if b.closing && b.ahead != nil && b.batch == nil && d > 0 {
					err = b.ahead.LeafEnd(n, d+1) // its parent's End comes next, and nothing between fails
				} else {
					err = b.c.leaf(n, d+1)
				}
				if err != nil {
					if err == errPause {
						b.paused, b.pel, b.pd = pausedLeafOne, n, d
						b.pcur, b.pspaces, b.pnames = cur, spaces, names
					}
					return err
				}
				return b.trimRest(cur, spaces, names)
			}
		}
		if err := b.tellLeaves(cur, d); err != nil {
			if err == errPause {
				b.prologPause, b.pcur, b.pspaces, b.pnames = false, cur, spaces, names
			}
			return err
		}
	}
	return b.trimRest(cur, spaces, names)
}

// trimRest is trimLeaves', once the leaves have been told.
func (b *burster) trimRest(cur *Node, spaces *spaceTable, names *nameCache) error {
	for t := cur.tree; cur.v1 != noIdx && t.rec(cur.v1).isLeaf(); {
		b.discardLast(cur, spaces, names)
	}
	if b.batch != nil && textSince(cur.tree, b.marks.top().text.blocks) >= burstFrozen {
		if err := b.flush(); err != nil {
			return err
		}
		b.rewind(cur.tree, spaces, names)
	}
	return nil
}

// tellLeaves tells the client of the leaves at the end of cur's children,
// at depth d, in document order.
func (b *burster) tellLeaves(cur *Node, d int) error {
	// cur is a node of the parse being built, so its children are its v1
	// and prev links (what LastChild and PrevSibling read, without their
	// checks for copied nodes).
	t := cur.tree
	first := cur.v1
	if first == noIdx || !t.rec(first).isLeaf() {
		return nil
	}
	for p := t.rec(first).prev; p != noIdx && t.rec(p).isLeaf(); p = t.rec(p).prev {
		first = p
	}
	return b.tellFrom(t, first, d)
}

// tellFrom tells the client of the leaves from record i of t on, the last
// children of a node at depth d that is being built: a leaf is one record,
// and nothing is stored after the last child of the deepest open node, so
// they are the records from i to the end.
func (b *burster) tellFrom(t *Tree, i uint32, d int) error {
	for ; i < t.n; i++ {
		n := t.rec(i)
		b.leafWalks++
		if b.watch != nil {
			b.watch(n, d+1, false)
		}
		if err := b.c.leaf(n, d+1); err != nil {
			if err == errPause {
				b.paused, b.pel, b.pd = pausedLeaf, n, d
			}
			return err
		}
	}
	return nil
}

// prolog tells the client of the comments and processing instructions
// before the document element, at its start tag, and drops them. They wait
// until then because a parse that meets an entity with markup in the DOCTYPE
// starts again (parseExpandedSteps) and would meet them twice.
func (b *burster) prolog(root *Node) error {
	if !b.leaves {
		return nil
	}
	if err := b.tellLeaves(root, 0); err != nil {
		if err == errPause {
			b.prologPause, b.pcur = true, root
		}
		return err
	}
	b.prologDone(root)
	return nil
}

// prologDone is prolog's, once the leaves have been told.
func (b *burster) prologDone(root *Node) {
	n := root.tree.n
	for root.LastChild() != nil {
		root.RemoveLastChild()
	}
	b.discarded += uint64(n - root.tree.n)
}

// textSince is how many bytes t's text store holds in blocks from the
// block numbered from on.
func textSince(t *Tree, from int) int {
	n := 0
	for _, blk := range t.text.blocks[from:] {
		n += len(blk)
	}
	return n
}

// discardLast removes p's last child and the text, names and namespace
// frames stored since p's spine mark; names is the parse's name cache.
func (b *burster) discardLast(p *Node, spaces *spaceTable, names *nameCache) {
	n := p.tree.n
	if dropLeaf(p) {
		b.leafDrops++
	} else if dropElement(p) {
		b.elemDrops++
	} else {
		p.RemoveLastChild()
	}
	b.discarded += uint64(n - p.tree.n)
	if t := p.tree; b.moved > 0 {
		// Those chunks are the burst's now; the parse allocates afresh.
		clear(t.chunks[b.moved:])
		t.chunks = t.chunks[:b.moved]
		b.moved = 0
	}
	if b.batch != nil {
		// The batch's records point into the stores: they are taken back
		// once it has been delivered.
		return
	}
	b.rewind(p.tree, spaces, names)
}

// dropLeaf takes back p's last child, as RemoveLastChild does, when that is
// a text, comment or processing instruction that is the tree's last record,
// p is the deepest node being built, and the tree keeps no source tables:
// such a record has nothing outside the array to clear (no base URI, typing,
// position or namespace nodes, which only elements own). It reports whether
// it did; a stream parse drops half its records here, every leaf it tells.
func dropLeaf(p *Node) bool {
	t := p.tree
	if t.source != nil || t.frozen || p.flags&fSide != 0 || p.v1 == noIdx || p.v1+1 != t.n ||
		t.open.len() == 0 || t.open.at(t.open.len()-1) != uint64(p.self) {
		return false
	}
	c := t.rec(p.v1)
	switch NodeKind(c.kind) {
	case KindText, KindComment, KindPI:
	default:
		return false
	}
	if c.flags&(fBase|fTyped|fManyAttrs) != 0 {
		return false
	}
	p.v1 = c.prev
	t.n--
	return true
}

// dropElement takes back p's last child, as RemoveLastChild does, when that
// is an element closed with all its records the tree's last, p is the deepest
// node being built, and nothing outside the array holds any of those records:
// no source tables, no base URI, typing or many-attribute count, and no
// namespace node made for the tree. It reports whether it did; a stream parse
// drops every element it is told the end of here.
func dropElement(p *Node) bool {
	t := p.tree
	if t.source != nil || t.frozen || p.flags&fSide != 0 || p.v1 == noIdx ||
		t.open.len() == 0 || t.open.at(t.open.len()-1) != uint64(p.self) {
		return false
	}
	c := t.rec(p.v1)
	if c.kind != uint8(KindElement) || c.end != t.n {
		return false
	}
	for i := c.self; i < t.n; i++ {
		r := t.rec(i)
		if r.flags&(fBase|fTyped|fManyAttrs) != 0 || r.kind == uint8(KindDocument) {
			return false
		}
	}
	t.sideMu.Lock() // the sweep cleanup writes t.side
	side := t.side != nil
	t.sideMu.Unlock()
	if side {
		return false
	}
	p.v1 = c.prev
	t.n = c.self
	return true
}

// rewind takes back what the parse's tree t stored since the deepest
// spine element's mark.
func (b *burster) rewind(t *Tree, spaces *spaceTable, names *nameCache) {
	m := b.marks.top()
	// Each call is made only when it has something to do (their own first
	// tests, without the calls): after most leaves none has.
	if s := &t.text; len(s.blocks) > m.text.blocks+1 ||
		len(s.blocks) == m.text.blocks+1 && int(s.cur) != len(s.blocks) || s.aux != nil {
		s.rewind(m.text)
	}
	if len(t.names) > m.names+burstSlack {
		rewindNames(t, m.names, names)
	}
	if len(t.frames) > m.frames+burstSlack {
		t.rewindFrames(m.frames, m.frameData)
	}
	// The whitespace table remembers ranges in the store, some of which may
	// have just been released.
	if int(spaces.hi) > m.text.blocks {
		spaces.forget(m.text.blocks)
	}
}

// take delivers the burst el: a large one, or one grounded for now, at
// once, in a tree of its own (deliver), and a small one with the bursts
// before it (burstBatch).
func (b *burster) take(el *Node) error {
	t := el.tree
	b.burstRecs += int(t.endOf(el) - el.self)
	if !b.later || t.endOf(el)-el.self > burstSmall {
		if err := b.flush(); err != nil {
			return err
		}
		g := b.deliver(el)
		if err := b.spineAttrsCheck(); err != nil {
			return err
		}
		if !b.later {
			b.grounded = g
			return nil
		}
		return b.fn(g)
	}
	b.add(el)
	if err := b.spineAttrsCheck(); err != nil {
		return err
	}
	if bt := b.batch; int(bt.t.n) >= max(burstBatchRecords, 4*bt.spine) ||
		textSince(t, bt.blocks) >= burstBatchText {
		return b.flush()
	}
	return nil
}

// Each burst's tree holds copies of its ancestors with their attributes, so
// bursts under an element of many attributes cost bursts x attributes where
// the tree costs bursts + attributes. Past spineAttrsFloor attributes so
// copied, a parse that has copied more than spineAttrsPerRecord for each
// record of its bursts is refused.
//
// ponytail: a refusal, not a cure; sharing the ancestors' copies between
// trees handed out one by one (Ground) would need trees that grow after.
const spineAttrsPerRecord = 64

var spineAttrsFloor = 1 << 24 // a var for the test

func (b *burster) spineAttrsCheck() error {
	if b.spineAttrs > spineAttrsFloor && b.spineAttrs > spineAttrsPerRecord*b.burstRecs {
		return fmt.Errorf(
			"parse XML: streaming copied %d ancestor attributes for %d records, more than %d per record: %w",
			b.spineAttrs, b.burstRecs, spineAttrsPerRecord, ErrResourceLimit)
	}
	return nil
}

// A burst of more than burstSmall records is delivered on its own; smaller
// ones are batched until the batch holds burstBatchRecords records (or four
// times the records its ancestors' copies take, attributes included), or the parse has stored burstBatchText bytes of
// text since the batch began. The text a spine element's content stored
// before a child of it opened waits for the element to close: burstFrozen
// bounds it.
//
// ponytail: per level, so a batch open under a deep spine can leave up to
// depth x burstFrozen (64 MB at MaxDepth 1000) waiting; a total would need
// the levels' shares tracked.
const (
	burstSmall        = chunkMax
	burstBatchRecords = 4096
	burstBatchText    = 1 << 20
	burstFrozen       = 64 << 10
)

// burstBatch is a tree that small bursts are copied into, each under copies
// of its ancestors, until it is delivered: its bursts are handed to fn one
// after another, all in the one tree. A burst under the same ancestors as
// the one before it shares their copies, so the ancestors are copied once
// for each that is opened, and once more for each batch, rather than once
// for every burst: a burst under a deep spine cost the spine's depth.
//
// Its records are copied verbatim, sharing the parse's names, frames and
// text, which the parse therefore does not take back (discardLast) until
// the batch has been delivered. Bursts in one tree are still disjoint
// subtrees in document order, each with its whole ancestry, and nothing a
// burst may read tells it from a tree of its own (see the xslt gate).
type burstBatch struct {
	t *Tree
	// path[d] is the copy of the spine element at depth d (0: the document
	// node), and gens[d] that element's gens entry.
	path []uint32
	gens []uint64
	// els are the bursts, in order.
	els []uint32
	// spine is how many records the copies of ancestors take.
	spine int
	// blocks is how many text blocks the parse had when the batch began.
	blocks int
	src    *Tree
	// o, for the stream driver, is the batch's origin (streamview.go).
	o *treeOrigin
}

// add copies el, a small burst, into the batch, under copies of the spine
// elements it does not share with the burst before it.
func (b *burster) add(el *Node) {
	t := el.tree
	bt := b.batch
	if bt == nil {
		bt = &burstBatch{t: &Tree{}, blocks: len(t.text.blocks), src: t}
		b.batch = bt
		if b.ords != nil {
			bt.o = bt.t.setOrigin(b.stream(t))
			bt.o.add(0, 0)
		}
		r := bt.t.rec(bt.copyRange(t, 0, 1, noIdx, noIdx))
		r.v1 = noIdx
		if bt.o != nil {
			r.flags |= fShallow
		}
		bt.path, bt.gens = append(bt.path, 0), append(bt.gens, 0)
		bt.spine = 1
	}
	d := b.gens.len() - 1 // el's parent
	k := 1
	for k < len(bt.path) && k <= d && bt.gens[k] == b.gens.at(k) {
		k++
	}
	bt.closeTo(k)
	anc := make([]*Node, 0, d-k+1)
	for p := el.Parent(); len(anc) < d-k+1; p = p.Parent() {
		anc = append(anc, p)
	}
	for i := len(anc) - 1; i >= 0; i-- {
		s := anc[i]
		j := bt.appendUnder(t, s.self, s.self+1+s.attrCount())
		e := bt.t.rec(j)
		if bt.o != nil {
			bt.o.add(j, b.ords.at(k))
			e.flags |= fShallow // its content is not all here
		}
		e.end, e.v1 = openEnd, noIdx
		bt.path, bt.gens = append(bt.path, j), append(bt.gens, b.gens.at(k))
		b.spineCopies += 1 + int(s.attrCount())
		b.spineAttrs += int(s.attrCount())
		bt.spine += 1 + int(s.attrCount())
		k++
	}
	j := bt.appendUnder(t, el.self, t.endOf(el))
	if bt.o != nil {
		bt.o.add(j, b.ordOf(el))
	}
	bt.els = append(bt.els, j)
}

// appendUnder copies the records [lo, hi) of t, a node's own and those after
// it, as the last child of the batch's deepest copy, and returns its index.
func (bt *burstBatch) appendUnder(t *Tree, lo, hi uint32) uint32 {
	p := bt.t.rec(bt.path[len(bt.path)-1])
	j := bt.copyRange(t, lo, hi, p.self, p.v1)
	p.v1 = j
	return j
}

// closeTo ends the copies at depths k and deeper.
func (bt *burstBatch) closeTo(k int) {
	for i := len(bt.path) - 1; i >= k; i-- {
		bt.t.rec(bt.path[i]).end = bt.t.n
	}
	bt.path, bt.gens = bt.path[:k], bt.gens[:k]
}

// copyRange appends copies of the records [lo, hi) of t, whose links within
// the range are renumbered; the first's parent and previous sibling are
// parent and prev. It returns the first's index.
func (bt *burstBatch) copyRange(t *Tree, lo, hi, parent, prev uint32) uint32 {
	c := bt.t
	base := c.n
	remap := func(x uint32) uint32 {
		if x >= lo && x <= hi {
			return x - lo + base
		}
		return x
	}
	for i := lo; i < hi; i++ {
		s := t.rec(i)
		r := *s
		d := c.alloc()
		j := d.self
		r.tree, r.self = c, j
		if bt.o != nil {
			r.flags |= fOrigin
		}
		if i == lo {
			r.parent, r.prev = parent, prev
		} else {
			r.parent = remap(r.parent)
			if r.prev != noIdx {
				r.prev = remap(r.prev)
			}
		}
		if r.isLeaf() {
			r.end = j + 1
		} else {
			r.end = remap(t.endOf(s))
			if r.v1 != noIdx {
				r.v1 = remap(r.v1)
			}
		}
		*d = r
		if s.flags&fTyped != 0 {
			*d.ownTyping() = *s.typ() // the names are shared, not copied
		}
		if s.flags&fBase != 0 {
			if c.bases == nil {
				c.bases = map[uint32]string{}
			}
			c.bases[j] = t.bases[i]
		}
		if s.flags&fManyAttrs != 0 {
			if src := c.ownSource(); src.attrCounts == nil {
				src.attrCounts = map[uint32]uint32{}
			}
			c.source.attrCounts[j] = t.source.attrCounts[i]
		}
		if s.kind == uint8(KindDocument) {
			if u := t.docURIs[i]; u != "" {
				if c.docURIs == nil {
					c.docURIs = map[uint32]string{}
				}
				c.docURIs[j] = u
			}
		}
	}
	return base
}

// flush delivers the batch, if there is one: it becomes a document as a
// burst's tree is (deliver), sharing the parse's stores as they now are,
// and fn is called with each of its bursts in order.
func (b *burster) flush() error {
	bt := b.batch
	if bt == nil {
		return nil
	}
	b.batch = nil
	bt.closeTo(0)
	c, t := bt.t, bt.src
	c.rec(0).end = c.n
	c.Root = c.rec(0)
	if bt.o == nil {
		c.id.Store(int64(nextTreeID()))
	}
	c.DocType, c.XMLVersion = t.DocType, "1.0"
	if b.v11 {
		c.XMLVersion = "1.1"
	}
	if s := t.source; s != nil {
		c.ownSource().externalSubset = s.externalSubset
	}
	c.names = t.names[:len(t.names):len(t.names)]
	c.text.blocks = make([][]byte, len(t.text.blocks))
	for i, blk := range t.text.blocks {
		c.text.blocks[i] = blk[:len(blk):len(blk)]
	}
	c.text.size = t.text.size
	c.frames = t.frames[:len(t.frames):len(t.frames)]
	c.frameData = t.frameData[:len(t.frameData):len(t.frameData)]
	for _, i := range bt.els {
		if err := b.fn(c.rec(i)); err != nil {
			return err
		}
	}
	return nil
}

// deliver hands el to fn in a tree of its own. By now the parse's tree holds
// nothing but the document node, the spine and el (a burst parse does not
// keep the prolog's comments and processing instructions, which every burst
// would copy), which is exactly the burst's tree: it is
// copied in bulk, sharing the names, frames and text the records point into.
// The chunks wholly past el's first are not copied but taken over, so that
// one large burst is not held twice; discardLast drops them from the parse.
func (b *burster) deliver(el *Node) *Node {
	t := el.tree
	m := cloneMoving(t.Root, xdmclone.Options{}, el.self, b.ords != nil)
	k, _ := chunkOf(el.self)
	if last, _ := chunkOf(t.n - 1); last > k {
		b.moved = k + 1
	}
	c := m(el)
	if b.ords != nil {
		// The copy's records are the parse's, at the same indices: the
		// document node, the spine (each with its attributes), then el.
		o := c.tree.setOrigin(b.stream(t))
		o.add(0, 0)
		for d := 1; d < t.open.len(); d++ {
			o.add(uint32(t.open.at(d)), b.ords.at(d))
		}
		o.add(el.self, b.ordOf(el))
		for p := c.Parent(); p != nil; p = p.Parent() {
			p.flags |= fShallow // its content is not all here
		}
	}
	b.spineCopies += int(el.self)
	b.spineAttrs += int(el.self) - b.gens.len() // less the document node and the elements
	c.tree.XMLVersion = "1.0"
	if b.v11 {
		c.tree.XMLVersion = "1.1"
	}
	return c
}

// textMark is a point in a textStore that rewind returns it to.
type textMark struct{ blocks int }

func (s *textStore) mark() textMark { return textMark{len(s.blocks)} }

// rewind forgets every value stored since m. A burst's tree, and any string
// read from the store, shares the bytes those values are in, so they are
// released rather than reused: the blocks made since m are dropped from the
// store -- the burst trees that read them keep them alive as long as they
// need them -- except the current one, which takes the number after m's and
// goes on taking values past its length. Bytes are only ever appended, so
// nothing already read changes, and block sizes go on growing as they would.
func (s *textStore) rewind(m textMark) {
	// Nothing to drop when no block was made since m but the current one,
	// which would keep its number: the rewind after most leaves.
	if n := len(s.blocks); n > m.blocks+1 || n == m.blocks+1 && int(s.cur) != n {
		var cur []byte
		if int(s.cur) > m.blocks {
			cur = s.blocks[s.cur-1]
		}
		for i := m.blocks; i < len(s.blocks); i++ {
			s.blocks[i] = nil
		}
		s.blocks = s.blocks[:m.blocks]
		if cur != nil {
			s.newBlock(cur)
			s.cur = int32(len(s.blocks))
		}
	}
	// The shared whitespace values and any borrowed blocks may lie in the
	// blocks just dropped. The values share registers come from the DTD,
	// stored before any mark.
	if s.aux != nil {
		s.aux.ws.forget(m.blocks)
		s.aux.lent, s.aux.last = nil, nil
	}
}

// burstSlack is how many names, or frames, the content of the spine may add
// before they are taken back. Most documents repeat a few names, which are
// then interned once rather than again for every burst; distinct ones are
// held to this many.
const burstSlack = 1024

// rewindNames forgets the names interned since the table held n, and their
// indices in the tree's cache and in cache, once there are more than
// burstSlack. A burst's
// tree shares the table, so the table is clipped rather than overwritten:
// the next name appended copies it.
func rewindNames(t *Tree, n int, cache *nameCache) {
	if len(t.names) <= n+burstSlack {
		return
	}
	for _, q := range t.names[n:] {
		if i, ok := t.nameIx[q]; ok && int(i) >= n {
			delete(t.nameIx, q)
		}
	}
	t.names = t.names[:n:n]
	if n == 0 {
		t.names = nil // intern starts a table with the empty name at 0
	}
	if h := t.nameHot; h != nil {
		for i, x := range h {
			if int(x) >= n {
				h[i] = 0
			}
		}
	}
	for i := range cache {
		if int(cache[i].idx) >= n {
			cache[i].q, cache[i].idx = QName{}, 0
		}
	}
}

// rewindFrames forgets the namespace frames interned since the table held n
// frames and nd bindings, as rewindNames does names.
func (t *Tree) rewindFrames(n, nd int) {
	if len(t.frames) <= n+burstSlack {
		return
	}
	for i := n; i < len(t.frames); i++ {
		f := t.frameAt(uint32(i))
		if len(f) == 1 {
			if j, ok := t.frameOne[f[0]]; ok && int(j) >= n {
				delete(t.frameOne, f[0])
			}
		} else if t.frameMany != nil {
			k := frameKey(f)
			if j, ok := t.frameMany[k]; ok && int(j) >= n {
				delete(t.frameMany, k)
			}
		}
	}
	t.frames = t.frames[:n:n]
	if n == 0 {
		t.frames = nil
	}
	t.frameData = t.frameData[:nd:nd]
}
