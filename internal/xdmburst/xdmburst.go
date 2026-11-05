// Package xdmburst lets internal/xdmstream drive xdm's stream parse without
// adding exported API to xdm, and holds the pattern xdm.ParseBursts cuts
// bursts at. Package xdm sets Drive in its init. The arguments typed any are
// xdm types: this package cannot import xdm, which imports it.
package xdmburst

import "io"

// Step is one step of a burst pattern: an element name test, reached from
// the step before it (or the document node) as a child or, with Descendant,
// as any descendant.
type Step struct {
	URI, Local       string
	AnyURI, AnyLocal bool
	Descendant       bool
}

// MaxSteps is the longest pattern xdm.ParseBursts takes.
const MaxSteps = 63

// Action is what a stream parse does with an element at its start tag.
type Action uint8

const (
	// Keep keeps the element open: its content is met piece by piece.
	Keep Action = iota
	// Ground builds the element whole; End is handed a copy of it, in a
	// tree of its own under shallow copies of its ancestors (a burst).
	Ground
	// Skip builds the element, checks it, and drops it at its end.
	Skip
	// GroundLater grounds the element, as Ground, but hands its copy to
	// Later, perhaps after more of the document: small elements grounded so
	// are copied into one tree, sharing the copies of their ancestors.
	GroundLater
	// Discard checks the element as Skip does, but drops its content as it
	// is parsed, each child once it closes, so that the parse holds no more
	// of it than its open path: End sees the element without its content.
	Discard
	// Pause, from Start under Steps, stops the parse at the element's start
	// tag: the step returns, and the next is handed what to do with it.
	Pause
)

// ErrPause, from Leaf or End under Steps, stops the parse after the event:
// the step returns, and the next goes on from there. Its type is a pointer,
// not error, so that the parse's err == ErrPause at every event compares two
// words in line rather than calling the runtime's interface equality.
var ErrPause = &pauseError{"xdmburst: pause"}

type pauseError struct{ s string }

func (e *pauseError) Error() string { return e.s }

// Client is told what a stream parse (Drive) meets. Its nodes are
// *xdm.Node in the parse's own tree, whose records are reused once the
// node is dropped: a node is valid until the client next returns control
// to the parse, and must not be kept. Strings read from it stay valid.
type Client interface {
	// Start decides what becomes of el, an element just opened at depth d
	// (1: the document element) under an element kept.
	Start(el any, d int) (Action, error)
	// Leaf is told of n, a text node, comment or processing instruction at
	// depth d directly inside a kept element or the document node, once it
	// is complete.
	Leaf(n any, d int) error
	// End is told that el, at depth d, closed: one kept, or one grounded,
	// when grounded is its copy (nil for GroundLater), or skipped, when el
	// is still whole in the parse's tree.
	End(el any, d int, grounded any) error
	// Later is handed the copies of the elements grounded by GroundLater,
	// in document order: before the copy of any later Ground, and at the
	// latest when the document element closes. A copy is not handed over
	// when the parse fails first.
	Later(grounded any) error
	// Watch is told of every element's start (end false) and end, those
	// inside a grounded or skipped element too, before Start or End; and
	// (end false) of every text, comment and processing instruction once it
	// is complete, inside such an element too, before the next start or end.
	// ord is the node's ordinal: its index in a tree-mode parse.
	Watch(n any, d int, end bool, ord uint64)
}

// Drive parses r as xdm.Parse does, with opts an xdm.ParseOptions, telling
// c what it meets. An error from c stops the parse and is returned.
var Drive func(r io.Reader, opts any, c Client) error

// Steps is Drive in steps, on the caller's goroutine, for a client that
// pauses (Pause, ErrPause): each call of step parses until c pauses, and
// returns nil, or until the parse ends: io.EOF once the document is read,
// its error otherwise (step is then not called again). act is what to do
// with the element whose Start paused the step before; the first call's is
// ignored. Nothing is read before the first call. done releases the parse,
// once, whether it ended or not.
var Steps func(r io.Reader, opts any, c Client) (step func(act Action) error, done func())

// FromStream reports whether n, a *xdm.Node, was copied out of a stream: a
// view, or a node of a grounded copy (its order and identity are its
// original's). Set by xdm's init.
var FromStream func(n any) bool

// ShallowKids reports, for n, a *xdm.Node, a view whose has-children the
// stream has answered (SetShallowKids), that answer and true; otherwise
// false, false. SetShallowKids records it: the cursor's Peek at the view's
// start tag, before anything reads its content. Set by xdm's init.
var (
	ShallowKids    func(n any) (has, known bool)
	SetShallowKids func(n any, has bool)
)

// Viewing is a Client that builds views: Drive hands it, before the first
// event, the function that returns the view of the element Start is asking
// about (a *xdm.Node), valid only during Start: a node in a small tree of
// its own holding shallow copies of the element, its ancestors and the
// document node, without content, that may be kept.
type Viewing interface {
	Viewer(view func() any)
}

// LeafViewing is a Client that copies leaves out of the stream: Drive hands
// it, before the first event, the function that returns a copy of n (a
// *xdm.Node), a text, comment or processing instruction at depth d that
// Leaf is telling it of, valid only during Leaf: complete, under shallow
// copies of its ancestors as a view has them, the original to order and
// identity, that may be kept.
type LeafViewing interface {
	LeafViewer(view func(n any, d int) any)
}

// Stripping is a Client that strips whitespace as XSLT's xsl:strip-space
// does: Strips reports whether whitespace-only text directly inside el (an
// *xdm.Node open in the parse's tree, its ancestors with it) is dropped. The
// parse asks it of each such text run beside its own rules, and a run it
// drops never joins the tree.
type Stripping interface {
	Strips(el any) bool
}

// Flushing is a Client that can have the copies GroundLater still holds
// handed over before the document element closes: Drive hands it, before
// the first event, the function that calls Later with each of them now. It
// may be called only while End is telling the client of an element's close.
type Flushing interface {
	Flusher(flush func() error)
}

// NewTreeID reserves the next tree id, the order a tree made now takes among
// the others. Set by xdm's init.
var NewTreeID func() int64

// Identified is a Client whose stream has an id reserved when the stream
// started (NewTreeID): what is copied out of it orders among other trees
// where a tree-mode parse made at that moment would, not where the first
// copy happens to be made.
type Identified interface {
	TreeID() int64
}

// Originating is a Client that can tell its own copies: Drive hands it,
// before the first event, the function that returns the ordinal of n (a
// *xdm.Node) when n is an element copied out of this very stream -- a view,
// or a node of a grounded copy -- and false for anything else.
type Originating interface {
	Originator(ord func(n any) (uint64, bool))
}
