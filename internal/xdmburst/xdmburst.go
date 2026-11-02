// Package xdmburst lets streamed XSLT cut bursts at a pattern with wildcards
// and descendant steps without adding exported API to xdm. Package xdm sets
// Parse in its init. The arguments typed any are xdm types: this package
// cannot import xdm, which imports it.
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
