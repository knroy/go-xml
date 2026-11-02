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
}

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
	c := &Cursor{}
	c.next, c.stop = iter.Pull(func(yield func(Event) bool) {
		err := xdmburst.Drive(r, opts, &client{c, yield})
		switch {
		case err == nil:
			yield(Event{Kind: EndDocument})
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
	c.act = act
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

func (k *client) Start(el any, d int) (xdmburst.Action, error) {
	if !k.yield(Event{StartElement, d, el.(*xdm.Node)}) {
		return 0, errStop
	}
	return k.c.act, nil
}

func (k *client) Leaf(n any, d int) error {
	x := n.(*xdm.Node)
	kind := Text
	switch x.Kind() {
	case xdm.KindComment:
		kind = Comment
	case xdm.KindPI:
		kind = PI
	}
	if !k.yield(Event{kind, d, x}) {
		return errStop
	}
	return nil
}

func (k *client) End(el any, d int, grounded any) error {
	if grounded != nil {
		k.c.grounded = grounded.(*xdm.Node)
	}
	if !k.yield(Event{EndElement, d, el.(*xdm.Node)}) {
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
