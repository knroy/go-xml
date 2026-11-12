// Package xdmstream is a pull cursor over a document as it is parsed: the
// events of xdm's one parse loop, with every well-formedness, DOCTYPE,
// entity and limit rule xdm.Parse applies, and MaxBytes, MaxNodes and
// MaxDepth counted over the whole document. Only the open elements are
// held; each element the consumer moves past is dropped.
//
// The parse runs in steps on the reader's goroutine (xdmburst.Steps): each
// call that needs the next event parses on to it, and the parse pauses there.
//
// What a consumer may read at each event, through Event.Node:
//
//   - StartElement: the element's name, attributes, in-scope namespaces and
//     base URI, and its ancestors' (all open). Not its content, which is
//     not read yet.
//   - EndElement: the same element; its content has been dropped.
//   - Text, Comment, PI: the node, complete: kind, name, string value.
//   - EndDocument: nothing (Node is nil).
//
// Event.Node is a node in the parse's own tree, whose records are reused
// once it is dropped: it is valid until the next call on the cursor and
// must not be kept (a kept one would read another node), nor compared with
// the nodes below. Strings read from it stay valid. View gives a start
// tag's element as a node that may be kept, without its content; Ground
// and GroundLater copy an element out whole. Those are the nodes of the
// document to identity and order: copies of one node are that node.
package xdmstream

import (
	"errors"
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/xdm"
)

// Kind is an event's kind.
type Kind uint8

const (
	StartElement Kind = iota + 1
	EndElement
	Text
	Comment
	PI
	EndDocument
)

// Event is one step of the document. Depth is the node's depth: 1 for the
// document element and the comments and processing instructions beside it.
type Event struct {
	Kind  Kind
	Depth int
	Node  *xdm.Node
	// Closed is the least depth at which an element NextTag passed over
	// closed since the event before, 0 when none did: what was open there
	// is not the element open there now.
	Closed int
}

// An Observer is told of every element's start and its end (end true), and
// of every text, comment and processing instruction (end false), in document
// order, those inside an element skipped, grounded or read by Text too,
// before the cursor reports it; last, before EndDocument, of the document's
// end (n nil, depth 0, end true). The node is valid only during the call,
// and must not be kept; its ancestors are open, its name, attributes and
// (for a leaf) value are complete. ord is the node's ordinal (Ordinal).
type Observer func(n *xdm.Node, depth int, end bool, ord uint64)

// Cursor reads one document. It is not safe for concurrent use.
type Cursor struct {
	// step parses on to the next event, out, which the client sets
	// (xdmburst.Steps); nil before the first call and once the parse is
	// over. done releases the parse; begun says it was started.
	step  func(xdmburst.Action) error
	done  func()
	begun bool
	out   Event
	// startD is the depth of the start tag the parse paused at, 0 when it
	// paused elsewhere: the next step decides it.
	startD int
	ev     Event // the last event Next returned
	// peeked holds the event Peek read ahead, and peekErr its error.
	peeked   *Event
	peekErr  error
	grounded *xdm.Node
	// later are GroundLater's functions waiting for their copies, in order.
	// lateErr is the first error one returned, held for the call that met
	// it (takeLate); lateFailed says one failed: those after it are not
	// called.
	later      []func(*xdm.Node) error
	lateErr    error
	lateFailed bool
	err        error
	obs        []Observer
	// watchOn turns the parse's watch on, which the first observer needs
	// (xdmburst.Watching); nil before the parse is started.
	watchOn func()
	// view builds the view of the start tag the parse is stopped at, and
	// viewed is the one built for ev.
	view   func() any
	viewed *xdm.Node
	// leafView copies the leaf the parse is stopped at (Leaf).
	leafView func(n any, d int) any
	// flush hands over the copies GroundLater holds (Flush).
	flush func() error
	// ordinal is Ordinal's answer, from the parse.
	ordinal func(n any) (uint64, bool)
	// strip says whitespace-only text inside an element is stripped
	// (StripWhitespace).
	strip func(el *xdm.Node) bool
	// For NextTag and SkipTag: filt is the filter of the call resuming the
	// parse; quiet hides the end of the element SkipTag skips, and hide the
	// start tag a Peek read that the filter drops. open says, per depth,
	// what became of the open element there; closed is Event.Closed of the
	// next event.
	filt        *Filter
	filtv       Filter // filt's copy: the caller's need not outlive the call
	quiet, hide bool
	open        openStates
	closed      int
	// endAhead says the parse paused at a leaf whose parent's end tag it
	// has read (xdmburst.EndAhead): Next may report that end without a step.
	// endTold says it did, and the parse's End for it is still to come;
	// settling, that the step running is only to bring the parse there.
	endAhead, endTold, settling bool
	// noAhead turns endAhead off, for the tests that compare the two.
	noAhead bool
	// resumes counts the steps of the parse, for the tests.
	resumes int
	// src and opts are what Open was given, for Tee; teed says Tee has
	// taken src over.
	src  io.Reader
	opts xdm.ParseOptions
	teed bool
	// handoff is Handoff's.
	handoff func() func()
	// id is the document's tree id, reserved by Open (xdmburst.Identified).
	id int64
	// cancel is Cancel's.
	cancel func() error
}

// A Filter says which events NextTag and SkipTag report. What they pass
// over is still parsed and checked, and an element whose start tag they
// pass over is descended into, so that what is inside it is met; its end
// tag is passed over too.
type Filter struct {
	// Accept reports whether the start tag of el is reported; every one is
	// when Accept is nil. It is called during the parse, with el open in
	// the parse's tree, and must not call the cursor.
	Accept func(el *xdm.Node) bool
	// Leaves says text, comments and processing instructions are reported.
	Leaves bool
	// The end tag of an element descended into is reported at Depth or
	// above, and deeper only with Ends.
	Depth int
	Ends  bool
}

// openState is what became of an element at its start tag.
type openState uint8

const (
	openKept    openState = iota // reported, and descended into
	openHidden                   // not reported (NextTag's filter), its end neither
	openDecided                  // reported, and skipped or grounded
)

// openStates holds an openState per depth in two bits, so that a deep
// document costs a quarter of a byte per open element; a depth not yet set
// is openKept.
type openStates []byte

func (o openStates) at(d int) openState { return openState(o[d/4]>>(d%4*2)) & 3 }

func (o *openStates) set(d int, s openState) {
	for len(*o) <= d/4 {
		*o = append(*o, 0)
	}
	sh := d % 4 * 2
	(*o)[d/4] = (*o)[d/4]&^(3<<sh) | byte(s)<<sh
}

// errNotAtEnd is Flush's error away from an end tag.
var errNotAtEnd = errors.New("xdmstream: not at an end tag")

// errNotAtLeaf is Leaf's error away from a leaf, or after Peek.
var errNotAtLeaf = errors.New("xdmstream: not at a leaf")

// errNotAtStart is the error of Skip, Ground and Text away from an
// undecided start tag.
var errNotAtStart = errors.New("xdmstream: not at a start tag")

// Open returns a cursor over r, parsed with opts. Nothing is read until the
// first call, but the document's place among other trees is fixed now, as a
// tree-mode parse made now would have it: copies out of it order before a
// document loaded after Open and before the first read. Close releases it.
func Open(r io.Reader, opts xdm.ParseOptions) *Cursor {
	return &Cursor{src: r, opts: opts, id: xdmburst.NewTreeID()}
}

// Handoff has f called where the cursor's parse waits for another
// goroutine: a Tee's cursors, which take turns, call it as a turn ends
// (what f returns is called when the turn goes on), so that state the
// reader's goroutine holds (xpath's EvalLock, once a host call has shared
// it) can be let go before the next reader needs it. The parse itself runs
// on the reader's goroutine, so a cursor read alone hands nothing off. A
// Tee's cursors have c's. It must be set before the first call.
func (c *Cursor) Handoff(f func() func()) { c.handoff = f }

// StripWhitespace has the parse drop whitespace-only text directly inside
// each element el for which strip reports true, as xsl:strip-space does: el
// is open in the parse's tree, with its ancestors. Such text is not an event,
// nor in a copy, nor in Text's value. It must be set before the first call.
func (c *Cursor) StripWhitespace(strip func(el *xdm.Node) bool) { c.strip = strip }

// Cancel has the parse stop once cancel returns an error (a context's Err):
// it is asked at each read of the input, so a parse that reads on inside
// one step -- through a skipped element or events a filter passes over --
// stops too, and the error the cursor then returns is cancel's. A Tee's
// cursors have c's. It must be set before the first call.
func (c *Cursor) Cancel(cancel func() error) {
	c.cancel = cancel
	c.src = cancelReader{c.src, cancel}
}

// cancelReader is Cancel's reader: r, failing with cancel's error once there
// is one.
type cancelReader struct {
	r      io.Reader
	cancel func() error
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.cancel(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Close stops the parse. The cursor is spent.
func (c *Cursor) Close() {
	c.begun, c.step = true, nil
	if c.done != nil {
		c.done()
	}
}

// At is the current event: the last one Next, NextTag, SkipTag, Skip,
// Ground, GroundLater or Text returned (a Peek does not move it).
func (c *Cursor) At() Event { return c.ev }

// Observe adds o to the cursor's observers, from the next element on.
func (c *Cursor) Observe(o Observer) {
	c.settle() // not to be told of an end tag already reported
	c.obs = append(c.obs, o)
	if c.watchOn != nil {
		c.watchOn()
	}
}

// Ordinal returns the ordinal of n when n is an element copied out of this
// cursor's document (a view, or a node of a copy Ground or GroundLater
// made): the index of its original in a tree-mode parse, which an Observer
// is told with it. For any other node it returns false.
func (c *Cursor) Ordinal(n *xdm.Node) (uint64, bool) {
	if c.ordinal == nil || n == nil {
		return 0, false
	}
	return c.ordinal(n)
}

// Next returns the next event, descending into the current element when it
// is a start tag. After EndDocument it returns io.EOF; after a parse error,
// that error again.
func (c *Cursor) Next() (Event, error) {
	if p := c.peeked; p != nil || c.peekErr != nil {
		c.peeked = nil
		if c.peekErr != nil {
			return Event{}, c.peekErr
		}
		c.ev, c.viewed = *p, nil
		return c.ev, nil
	}
	if c.endAhead {
		if ev, ok := c.ahead(nil); ok {
			c.ev, c.viewed = ev, nil
			return ev, nil
		}
	}
	ev, err := c.resume(xdmburst.Keep)
	c.ev, c.viewed = ev, nil
	return ev, err
}

// ahead returns the end tag the parse has read after the leaf it paused at
// (endAhead), when the consumer, reading with f, is to be told of it now: it
// is reported without a step, the parse's End for it then going on without
// a pause (endTold). Otherwise the parse goes on to tell it as usual.
func (c *Cursor) ahead(f *Filter) (Event, bool) {
	c.endAhead = false
	d := c.ev.Depth - 1
	if len(c.obs) > 0 || c.open.at(d) != openKept || f != nil && d > f.Depth && !f.Ends {
		return Event{}, false
	}
	c.endTold = true
	ev := Event{Kind: EndElement, Depth: d, Node: c.ev.Node.Parent(), Closed: c.closed}
	c.closed = 0
	return ev, true
}

// settle brings the parse to the end tag Next reported ahead of it, if any,
// for Observe: the observer is not to be told of it.
func (c *Cursor) settle() {
	if c.endTold {
		c.settling = true
		ev := c.ev
		if _, err := c.resume(xdmburst.Keep); err != nil && c.err == nil {
			c.lateErr = err // a GroundLater function's: the next call's
		}
		c.ev = ev
	}
}

// View returns the view of the current element, a start tag Next just
// returned: a node that may be kept, in a small tree of its own, with the
// element's name, attributes, namespaces, base URI, ancestors (copies of
// them, with theirs) and identity, as a full parse has them, and no content.
// Asking it for content -- children, descendants, siblings, its string or
// typed value -- panics with a *streamtrace.PostureError (counted by
// streamtrace.Guard) instead of answering. Views of one element, and the
// copies Ground and GroundLater make, are one node to Is, Identity,
// Compare and generate-id, and order as a full parse's nodes do.
//
// It may be asked again, and after Peek, until the cursor moves on; then it
// fails as Skip would.
func (c *Cursor) View() (*xdm.Node, error) {
	if c.ev.Kind != StartElement {
		return nil, errNotAtStart
	}
	if c.viewed == nil {
		if c.peeked != nil || c.peekErr != nil {
			return nil, errNotAtStart // Peek built it, or there was none to build
		}
		c.viewed = c.view().(*xdm.Node)
	}
	return c.viewed, nil
}

// Leaf returns the current event's text, comment or processing
// instruction as a node that may be kept: a copy, complete, under copies of
// its ancestors as a view has them (names, attributes, namespaces, base URI;
// no content), which is the original to Is, Identity, Compare and
// generate-id, as views and grounded copies are. It may be asked again
// until the cursor moves on, but not after Peek.
func (c *Cursor) Leaf() (*xdm.Node, error) {
	if c.ev.Kind < Text || c.ev.Kind > PI || c.peeked != nil || c.peekErr != nil || c.leafView == nil {
		return nil, errNotAtLeaf
	}
	if c.viewed == nil {
		c.viewed = c.leafView(c.ev.Node, c.ev.Depth).(*xdm.Node)
	}
	return c.viewed, nil
}

// Peek returns the event Next will return, without moving to it. At a
// start tag it decides to descend: Skip, Ground and Text are no longer
// possible there, and the view is made first, since it can be made only
// there. The current event's node stays valid only if it is a start tag.
func (c *Cursor) Peek() (Event, error) {
	if c.peeked == nil && c.peekErr == nil {
		if c.ev.Kind == StartElement && c.viewed == nil {
			// The view can be made only while the parse is at the tag.
			c.View()
		}
		c.endAhead = false // told by the parse, as usual
		ev, err := c.resume(xdmburst.Keep)
		if err != nil && c.err == nil {
			// A GroundLater function's: this call's, and ev is still next.
			c.peeked = &ev
			return Event{}, err
		}
		c.peeked, c.peekErr = &ev, err
	}
	if c.peekErr != nil {
		return Event{}, c.peekErr
	}
	return *c.peeked, nil
}

// NextTag is Next for a reader that needs only some of the document's
// events, those f reports (see Filter), and EndDocument. Passing over events
// costs no switch to the reader: it is what makes a reader that wants few of
// them cheap. Event.Closed says how far up the elements passed over closed.
func (c *Cursor) NextTag(f *Filter) (Event, error) {
	if p := c.peeked; p != nil || c.peekErr != nil {
		c.peeked = nil
		if c.peekErr != nil {
			return Event{}, c.peekErr
		}
		switch {
		case p.Kind == StartElement && f.Accept != nil && !f.Accept(p.Node):
			c.hide = true // the parse waits at its start tag
			c.closed = min0(c.closed, p.Closed)
		case p.Kind >= Text && p.Kind <= PI && !f.Leaves:
			c.closed = min0(c.closed, p.Closed)
		case p.Kind == EndElement && p.Depth > f.Depth && !f.Ends:
			c.closed = min0(min0(c.closed, p.Closed), p.Depth)
		default:
			c.ev, c.viewed = *p, nil
			return c.ev, nil
		}
		c.endAhead = false // ahead of the event passed over, not of c.ev
	}
	if c.endAhead {
		if ev, ok := c.ahead(f); ok {
			c.ev, c.viewed = ev, nil
			return ev, nil
		}
	}
	c.filtv = *f
	c.filt = &c.filtv
	ev, err := c.resume(xdmburst.Keep)
	c.filt, c.hide = nil, false
	c.ev, c.viewed = ev, nil
	return ev, err
}

// SkipTag is Skip, then NextTag: the end tag of the element skipped is not
// returned, and the skip and the next event cost one switch to the reader.
func (c *Cursor) SkipTag(f *Filter) (Event, error) {
	if c.ev.Kind != StartElement || c.peeked != nil || c.peekErr != nil {
		return Event{}, errNotAtStart
	}
	c.filtv = *f
	c.filt, c.quiet = &c.filtv, true
	ev, err := c.resume(xdmburst.Discard)
	c.filt, c.quiet = nil, false
	c.ev, c.viewed = ev, nil
	return ev, err
}

// min0 is the least of a and b that is not 0, or 0.
func min0(a, b int) int {
	if a == 0 || b != 0 && b < a {
		return b
	}
	return a
}

// Skip moves past the current element, a start tag Next just returned, to
// its end. Its content is still parsed and checked, and an error in it is
// Skip's, but it is dropped as it is parsed: the parse holds no more of it
// than the path to the node being read.
func (c *Cursor) Skip() error {
	_, err := c.decide(xdmburst.Discard)
	return err
}

// Ground moves past the current element, as Skip, and returns a copy of it
// that may be kept: the element, whole, under shallow copies of its
// ancestors (name, namespaces, attributes, base URI) and a document node
// with the document's URIs, as xdm.ParseBursts delivers one. A small
// element's copy shares its tree and its ancestors' copies with the small
// elements grounded before and after it (xdm's ground batch), so a Ground
// under a deep spine costs the element, not the spine; nothing the copy
// answers tells that tree from one of its own: it has no siblings there,
// and its order, identity and generate-id are its original's. A large
// element's copy is in a tree of its own.
func (c *Cursor) Ground() (*xdm.Node, error) {
	if _, err := c.decide(xdmburst.Ground); err != nil {
		return nil, err
	}
	g := c.grounded
	c.grounded = nil
	return g, nil
}

// GroundLater moves past the current element, as Skip, and has fn called
// with a copy of it, as Ground returns one, perhaps after more of the
// document has been read. Small elements grounded so share one tree and the
// copies of their ancestors (xdm's burst batches), as Ground's do, and the
// tree is handed over complete, with no later growth. Such a tree holds those
// elements under their common ancestors' copies, so the ancestors' content
// is not one element's alone. fn is called in document order, before any
// later Ground returns, and at the latest when the document element closes.
// A copy is not handed over when the parse fails first.
//
// fn's error does not stop the parse: it is returned by the call that met it
// (the one during which the copy was handed over, or Flush), and the cursor
// reads on from where that call left it, so the rest of the document can
// still be read and checked (a consumer that failed settles: is the rest
// well-formed, valid?). Once a function has failed, those after it are not
// called: their consumer has failed.
func (c *Cursor) GroundLater(fn func(*xdm.Node) error) error {
	if c.ev.Kind != StartElement || c.peeked != nil || c.peekErr != nil {
		return errNotAtStart
	}
	c.later = append(c.later, fn)
	_, err := c.decide(xdmburst.GroundLater)
	return err
}

// Flush calls now the functions GroundLater was given whose copies are
// still held, rather than when the document element closes. It may be
// called only at an end tag: when the last event Next, Skip, Ground,
// GroundLater or Text returned is an EndElement, and nothing is peeked. A
// consumer whose reading ends inside the document -- at the close of the
// element it was handed -- calls it there, so that every copy it asked for
// has arrived. The error is the first those functions returned.
func (c *Cursor) Flush() error {
	if c.ev.Kind != EndElement || c.peeked != nil || c.peekErr != nil {
		return errNotAtEnd
	}
	// At an end tag reported ahead (endTold) nothing is held: the parse
	// reports ends ahead only while no GroundLater copy waits.
	if err := c.flush(); err != nil {
		return err
	}
	return c.takeLate()
}

// takeLate returns the error a GroundLater function returned since the last
// call, once.
func (c *Cursor) takeLate() error {
	err := c.lateErr
	c.lateErr = nil
	return err
}

// Text moves past the current element, as Skip, and returns its string
// value, read from the parse's tree before the element is dropped: nothing
// is copied out, but the parse holds the element whole until its end.
func (c *Cursor) Text() (string, error) {
	ev, err := c.decide(xdmburst.Skip)
	if err != nil {
		return "", err
	}
	return ev.Node.StringValue(), nil
}

// decide resumes the parse with act for the current start tag, and returns
// its end event, which becomes the current event.
func (c *Cursor) decide(act xdmburst.Action) (Event, error) {
	if c.ev.Kind != StartElement || c.peeked != nil || c.peekErr != nil {
		return Event{}, errNotAtStart
	}
	ev, err := c.resume(act)
	c.ev = ev
	return ev, err
}

// resume runs the parse to its next event, act deciding the start tag it
// stopped at, if any.
func (c *Cursor) resume(act xdmburst.Action) (Event, error) {
	if c.teed {
		return Event{}, errTeed
	}
	c.resumes++
	c.endAhead = false
	if !c.begun {
		c.begun = true
		c.step, c.done = xdmburst.Steps(c.src, c.opts, &client{c})
	}
	if d := c.startD; d > 0 {
		// What the parse paused at the start tag of becomes of it.
		c.startD = 0
		switch {
		case c.hide:
			// NextTag dropped it after a Peek had read it.
			c.hide = false
			c.open.set(d, openHidden)
		case act == xdmburst.Keep:
			c.open.set(d, openKept)
		default:
			c.open.set(d, openDecided)
		}
	}
	if c.step == nil {
		if c.err != nil {
			return Event{}, c.err
		}
		return Event{}, io.EOF
	}
	step := c.step
	c.step = nil // until the step is back: a panic in it spends the cursor
	switch err := step(act); err {
	case nil:
		c.step = step
		return c.out, c.takeLate()
	case io.EOF:
		c.done()
		for _, o := range c.obs {
			o(nil, 0, true, 0)
		}
		return Event{Kind: EndDocument, Closed: c.closed}, c.takeLate()
	default:
		if c.cancel != nil {
			if cerr := c.cancel(); cerr != nil {
				err = cerr // not the parse error a cut-off read became
			}
		}
		c.done()
		c.err = err
		if late := c.takeLate(); late != nil {
			return Event{}, late // met first, before the parse failed
		}
		return Event{}, err
	}
}

// client is the cursor's side of xdmburst.Steps: each event it hands to the
// consumer pauses the parse.
type client struct {
	c *Cursor
}

// send hands ev to the consumer, with what was closed unseen before it.
func (k *client) send(ev Event) {
	ev.Closed, k.c.closed = k.c.closed, 0
	k.c.out = ev
}

func (k *client) Start(el any, d int) (xdmburst.Action, error) {
	c, n := k.c, el.(*xdm.Node)
	if len(c.open) <= d/4 {
		c.open.set(d, openKept) // as a depth never set reads
	}
	if f := c.filt; f != nil && f.Accept != nil && !f.Accept(n) {
		c.open.set(d, openHidden)
		return xdmburst.Keep, nil
	}
	k.send(Event{Kind: StartElement, Depth: d, Node: n})
	c.startD = d
	return xdmburst.Pause, nil
}

func (k *client) Leaf(n any, d int) error {
	if f := k.c.filt; f != nil && !f.Leaves {
		return nil
	}
	x := n.(*xdm.Node)
	kind := Text
	switch x.Kind() {
	case xdm.KindComment:
		kind = Comment
	case xdm.KindPI:
		kind = PI
	}
	k.send(Event{Kind: kind, Depth: d, Node: x})
	return xdmburst.ErrPause
}

func (k *client) End(el any, d int, grounded any) error {
	c := k.c
	if c.endTold {
		// Reported ahead (Cursor.ahead): on, unless the parse was only to
		// be brought here (settle).
		c.endTold = false
		if c.settling {
			c.settling = false
			return xdmburst.ErrPause
		}
		return nil
	}
	if grounded != nil {
		c.grounded = grounded.(*xdm.Node)
	}
	switch c.open.at(d) {
	case openHidden:
		c.closed = min0(c.closed, d)
		return nil
	case openDecided:
		if c.quiet {
			c.quiet = false // SkipTag's element: on to the next event
			return nil
		}
	case openKept:
		if f := c.filt; f != nil && d > f.Depth && !f.Ends {
			c.closed = min0(c.closed, d)
			return nil
		}
	}
	k.send(Event{Kind: EndElement, Depth: d, Node: el.(*xdm.Node)})
	return xdmburst.ErrPause
}

func (k *client) LeafEnd(n any, d int) error {
	err := k.Leaf(n, d)
	k.c.endAhead = err == xdmburst.ErrPause && !k.c.noAhead
	return err
}

// Later hands g to the first GroundLater function waiting. Its error is
// held (lateErr), not the parse's: the parse goes on.
func (k *client) Later(g any) error {
	c := k.c
	fn := c.later[0]
	c.later = c.later[1:]
	if c.lateFailed {
		return nil
	}
	if err := fn(g.(*xdm.Node)); err != nil {
		c.lateErr, c.lateFailed = err, true
	}
	return nil
}

func (k *client) Viewer(view func() any) { k.c.view = view }

func (k *client) LeafViewer(view func(n any, d int) any) { k.c.leafView = view }

func (k *client) Watcher(on func()) {
	k.c.watchOn = on
	if len(k.c.obs) > 0 {
		on()
	}
}

func (k *client) Flusher(flush func() error) { k.c.flush = flush }

func (k *client) Strips(el any) bool { return k.c.strip != nil && k.c.strip(el.(*xdm.Node)) }

func (k *client) Watch(n any, d int, end bool, ord uint64) {
	for _, o := range k.c.obs {
		o(n.(*xdm.Node), d, end, ord)
	}
}

func (k *client) TreeID() int64 { return k.c.id }

func (k *client) Originator(ord func(n any) (uint64, bool)) { k.c.ordinal = ord }
