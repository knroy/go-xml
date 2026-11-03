package xdm

import (
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// The stream driver (burster) serves an outside client through
// xdmburst.Drive, or, in steps the client pauses, xdmburst.Steps: every element it is asked about is kept, grounded or
// skipped as the client says, and every leaf of a kept element is reported.
func init() {
	xdmburst.Drive = func(r io.Reader, opts any, c xdmburst.Client) error {
		o := opts.(ParseOptions)
		o.bursts = driver(c)
		_, err := parse(r, "", o)
		return err
	}
	xdmburst.Steps = func(r io.Reader, opts any, c xdmburst.Client) (func(xdmburst.Action) error, func()) {
		var b *burster
		var parsed func() (*Tree, error)
		done := func() {}
		step := func(act xdmburst.Action) error {
			if parsed == nil {
				// Nothing is read before the first step.
				o := opts.(ParseOptions)
				b = driver(c)
				o.bursts = b
				parsed, done = parseSteps(r, "", o)
			} else if err := b.resume(act); err != nil {
				return stepEnd(err)
			}
			_, err := parsed()
			return stepEnd(err)
		}
		release := func() {
			d := done
			done = func() {}
			d()
		}
		return step, release
	}
	xdmburst.FromStream = func(n any) bool { return n.(*Node).flags&fOrigin != 0 }
}

// driver is the burster that serves c.
func driver(c xdmburst.Client) *burster {
	b := &burster{
		c:      &driveClient{c},
		fn:     func(n *Node) error { return c.Later(n) },
		leaves: true,
		gens:   []uint64{0},
		ords:   []uint64{0},
	}
	// An element's ordinal is taken at its start: records under it may be
	// taken back before its end, which shifts what ordOf would say then.
	var open []uint64
	b.watch = func(n *Node, d int, end bool) {
		switch {
		case end:
			c.Watch(n, d, true, open[d])
		case n.Kind() == KindElement:
			open = append(open[:min(d, len(open))], make([]uint64, d+1-min(d, len(open)))...)
			open[d] = b.ordOf(n)
			c.Watch(n, d, false, open[d])
		default:
			c.Watch(n, d, false, b.ordOf(n))
		}
	}
	if o, ok := c.(xdmburst.Originating); ok {
		o.Originator(func(n any) (uint64, bool) {
			x := n.(*Node)
			if x.flags&fOrigin == 0 || x.flags&fSide != 0 || x.Kind() != KindElement ||
				b.origin == nil || x.origin().s != b.origin {
				return 0, false
			}
			return x.originKey() >> 16, true
		})
	}
	if v, ok := c.(xdmburst.Viewing); ok {
		v.Viewer(func() any {
			if n := b.view(); n != nil {
				return n
			}
			return nil // not a typed nil
		})
	}
	if f, ok := c.(xdmburst.Flushing); ok {
		f.Flusher(b.flush)
	}
	if s, ok := c.(xdmburst.Stripping); ok {
		b.strip = func(el *Node) bool { return s.Strips(el) }
	}
	return b
}

// driveClient is an xdmburst.Client as a streamClient.
type driveClient struct{ c xdmburst.Client }

func (a *driveClient) start(el *Node, d int) (xdmburst.Action, error) { return a.c.Start(el, d) }

func (a *driveClient) leaf(n *Node, d int) error { return a.c.Leaf(n, d) }

func (a *driveClient) end(el *Node, d int, _ bool, g *Node) error {
	if g == nil {
		return a.c.End(el, d, nil) // not a typed nil
	}
	return a.c.End(el, d, g)
}

// stepEnd is a step's outcome as xdmburst.Steps reports it: nil at a pause,
// io.EOF at the parse's end, or its error.
func stepEnd(err error) error {
	switch err {
	case errPause:
		return nil
	case nil:
		return io.EOF
	}
	return err
}
