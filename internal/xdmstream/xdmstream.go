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
// must not be kept (a kept one would read another node). Strings read from
// it stay valid. Ground copies an element out for keeping.
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
	err      error
	obs      []Observer
}

// errStop ends the parse when the cursor is closed.
var errStop = errors.New("xdmstream: closed")

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
		c.ev = *p
		return c.ev, nil
	}
	ev, err := c.resume(xdmburst.Keep)
	c.ev = ev
	return ev, err
}

// Peek returns the event Next will return, without moving to it. At a
// start tag it decides to descend: Skip, Ground and Text are no longer
// possible there. The current event's node stays valid only if it is a
// start tag.
func (c *Cursor) Peek() (Event, error) {
	if c.peeked == nil && c.peekErr == nil {
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
// Skip's.
func (c *Cursor) Skip() error {
	_, err := c.decide(xdmburst.Skip)
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

// Text moves past the current element, as Skip, and returns its string
// value, read from the parse's tree before the element is dropped: nothing
// is copied out.
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

func (k *client) Watch(el any, d int, end bool) {
	for _, o := range k.c.obs {
		o(el.(*xdm.Node), d, end)
	}
}
