// Package xdmburst lets streamed XSLT cut bursts at a pattern with wildcards
// and descendant steps, and internal/xdmstream drive xdm's stream parse,
// without adding exported API to xdm. Package xdm sets Parse and Drive in its
// init. The arguments typed any are xdm types: this package cannot import
// xdm, which imports it.
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

// MaxSteps is the longest pattern Parse takes.
const MaxSteps = 63

// Parse is xdm.ParseBursts over a pattern of 1 to MaxSteps steps: opts is an
// xdm.ParseOptions, and fn is called with the *xdm.Node of each outermost
// element the pattern matches -- an element matched inside it is delivered
// within it, not again.
var Parse func(r io.Reader, opts any, steps []Step, fn func(any) error) error

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
)

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
	// when grounded is its copy, or skipped, when el is still whole in the
	// parse's tree.
	End(el any, d int, grounded any) error
	// Watch is told of every element's start (end false) and end, those
	// inside a grounded or skipped element too, before Start or End.
	Watch(el any, d int, end bool)
}

// Drive parses r as xdm.Parse does, with opts an xdm.ParseOptions, telling
// c what it meets. An error from c stops the parse and is returned.
var Drive func(r io.Reader, opts any, c Client) error
