// Package xdmstream is a pull cursor over a document as it is parsed: the
// events of xdm's one parse loop, with every well-formedness, DOCTYPE,
// entity and limit rule xdm.Parse applies, and MaxBytes, MaxNodes and
// MaxDepth counted over the whole document. Only the open elements are
// held; each element the consumer moves past is dropped.
//
// The parse runs as a coroutine (iter.Pull) over xdm's stream driver
// (xdmburst.Drive), resumed by each call that needs the next event.
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
	"iter"

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

// An Observer is told of every element's start and its end (end true), in
// document order, those inside an element skipped, grounded or read by Text
// too, before the cursor reports it. The node is valid only during the call.
type Observer func(el *xdm.Node, depth int, end bool)

// Cursor reads one document. It is not safe for concurrent use.
type Cursor struct {
	next func() (Event, bool)
	stop func()
	ev   Event // the last event Next returned
	// peeked holds the event Peek read ahead, and peekErr its error.
	peeked  *Event
	peekErr error
	// act is what the parse is to do with ev, when ev is a start tag, once
	// resumed.
	act      xdmburst.Action
	grounded *xdm.Node
	// later are GroundLater's functions waiting for their copies, in order.
	later []func(*xdm.Node) error
	err   error
	obs   []Observer
	// view builds the view of the start tag the parse is stopped at, and
	// viewed is the one built for ev.
	view   func() any
	viewed *xdm.Node
	// flush hands over the copies GroundLater holds (Flush).
	flush func() error
	// strip says whitespace-only text inside an element is stripped
	// (StripWhitespace).
	strip func(el *xdm.Node) bool
	// For NextTag and SkipTag: filt is the filter of the call resuming the
	// parse; quiet hides the end of the element SkipTag skips, and hide the
	// start tag a Peek read that the filter drops. open says, per depth,
	// what became of the open element there; closed is Event.Closed of the
	// next event.
	filt        *Filter
	quiet, hide bool
	open        []openState
	closed      int
	// resumes counts the switches to the parse, for the tests.
	resumes int
	// src and opts are what Open was given, for Tee; teed says Tee has
	// taken src over.
	src  io.Reader
	opts xdm.ParseOptions
	teed bool
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

// errStop ends the parse when the cursor is closed.
var errStop = errors.New("xdmstream: closed")

// errNotAtEnd is Flush's error away from an end tag.
var errNotAtEnd = errors.New("xdmstream: not at an end tag")

// errNotAtStart is the error of Skip, Ground and Text away from an
// undecided start tag.
var errNotAtStart = errors.New("xdmstream: not at a start tag")

// Open returns a cursor over r, parsed with opts. Nothing is read until the
// first call. Close releases it.
func Open(r io.Reader, opts xdm.ParseOptions) *Cursor {
	c := &Cursor{src: r, opts: opts}
	c.next, c.stop = iter.Pull(func(yield func(Event) bool) {
		err := xdmburst.Drive(r, opts, &client{c, yield})
		switch {
		case err == nil:
			yield(Event{Kind: EndDocument, Closed: c.closed})
		case err != errStop:
			c.err = err
		}
	})
	return c
}

// StripWhitespace has the parse drop whitespace-only text directly inside
// each element el for which strip reports true, as xsl:strip-space does: el
// is open in the parse's tree, with its ancestors. Such text is not an event,
// nor in a copy, nor in Text's value. It must be set before the first call.
func (c *Cursor) StripWhitespace(strip func(el *xdm.Node) bool) { c.strip = strip }

// Close stops the parse. The cursor is spent.
func (c *Cursor) Close() { c.stop() }

// Observe adds o to the cursor's observers, from the next element on.
func (c *Cursor) Observe(o Observer) { c.obs = append(c.obs, o) }

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
	ev, err := c.resume(xdmburst.Keep)
	c.ev, c.viewed = ev, nil
	return ev, err
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
		ev, err := c.resume(xdmburst.Keep)
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
	}
	c.filt = f
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
	c.filt, c.quiet = f, true
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
// that may be kept: the element, whole, in a tree of its own, under shallow
// copies of its ancestors (name, namespaces, attributes, base URI) and a
// document node with the document's URIs, as xdm.ParseBursts delivers one.
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
// copies of their ancestors (xdm's burst batches), where a Ground of each
// copies all its ancestors every time: under a deep spine that is the
// difference between linear and records x depth. Such a tree holds those
// elements under their common ancestors' copies, so the ancestors' content
// is not one element's alone. fn is called in document order, before any
// later Ground returns, and at the latest when the document element closes;
// its error stops the parse and is returned by the call that met it. A copy
// is not handed over when the parse fails first.
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
	return c.flush()
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
	c.act = act
	c.resumes++
	ev, ok := c.next()
	if !ok {
		if c.err != nil {
			return Event{}, c.err
		}
		return Event{}, io.EOF
	}
	return ev, nil
}

// client is the cursor's side of xdmburst.Drive: each call hands the parse's
// event to the consumer and waits for it to ask for the next.
type client struct {
	c     *Cursor
	yield func(Event) bool
}

// send hands ev to the consumer, with what was closed unseen before it.
func (k *client) send(ev Event) bool {
	ev.Closed, k.c.closed = k.c.closed, 0
	return k.yield(ev)
}

func (k *client) Start(el any, d int) (xdmburst.Action, error) {
	c, n := k.c, el.(*xdm.Node)
	for len(c.open) <= d {
		c.open = append(c.open, openKept)
	}
	if f := c.filt; f != nil && f.Accept != nil && !f.Accept(n) {
		c.open[d] = openHidden
		return xdmburst.Keep, nil
	}
	if !k.send(Event{Kind: StartElement, Depth: d, Node: n}) {
		return 0, errStop
	}
	switch {
	case c.hide:
		// NextTag dropped it after a Peek had read it.
		c.hide, c.open[d] = false, openHidden
	case c.act == xdmburst.Keep:
		c.open[d] = openKept
	default:
		c.open[d] = openDecided
	}
	return c.act, nil
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
	if !k.send(Event{Kind: kind, Depth: d, Node: x}) {
		return errStop
	}
	return nil
}

func (k *client) End(el any, d int, grounded any) error {
	c := k.c
	if grounded != nil {
		c.grounded = grounded.(*xdm.Node)
	}
	switch c.open[d] {
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
	if !k.send(Event{Kind: EndElement, Depth: d, Node: el.(*xdm.Node)}) {
		return errStop
	}
	return nil
}

func (k *client) Later(g any) error {
	fn := k.c.later[0]
	k.c.later = k.c.later[1:]
	return fn(g.(*xdm.Node))
}

func (k *client) Viewer(view func() any) { k.c.view = view }

func (k *client) Flusher(flush func() error) { k.c.flush = flush }

func (k *client) Strips(el any) bool { return k.c.strip != nil && k.c.strip(el.(*xdm.Node)) }

func (k *client) Watch(el any, d int, end bool) {
	for _, o := range k.c.obs {
		o(el.(*xdm.Node), d, end)
	}
}
