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
	xdmburst.NewTreeID = func() int64 { return int64(nextTreeID()) }
	xdmburst.FromStream = func(n any) bool { return n.(*Node).flags&fOrigin != 0 }
}

// driver is the burster that serves c.
func driver(c xdmburst.Client) *burster {
	b := &burster{
		c:      &driveClient{c},
		fn:     func(n *Node) error { return c.Later(n) },
		leaves: true,
		gens:   ordsOf(0),
		ords:   &ordStack{},
	}
	b.ords.push(0)
	// An element's ordinal is taken at its start: records under it may be
	// taken back before its end, which shifts what ordOf would say then.
	var open ordStack
	watch := func(n *Node, d int, end bool) {
		switch {
		case end:
			c.Watch(n, d, true, open.at(d))
		case n.Kind() == KindElement:
			open.truncate(d)
			for open.len() < d {
				open.push(0)
			}
			open.push(b.ordOf(n))
			c.Watch(n, d, false, open.at(d))
		default:
			c.Watch(n, d, false, b.ordOf(n))
		}
	}
	if w, ok := c.(xdmburst.Watching); ok {
		// Off until the client asks, at a pause or before the first event:
		// the elements open then are the spine (ords) and, at a start tag,
		// the element paused at, whose ordinals their ends are told with.
		w.Watcher(func() {
			if b.watch != nil {
				return
			}
			open = ordStack{runs: append([]ordRun(nil), b.ords.runs...)}
			if b.paused == pausedStart {
				open.truncate(b.pd)
				for open.len() < b.pd {
					open.push(0)
				}
				open.push(b.ordOf(b.pel))
			}
			b.watch = watch
		})
	} else {
		b.watch = watch
	}
	if i, ok := c.(xdmburst.Identified); ok {
		b.origin = &streamOrigin{id: i.TreeID()}
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
	if v, ok := c.(xdmburst.LeafViewing); ok {
		v.LeafViewer(func(n any, d int) any { return b.leafView(n.(*Node), d) })
	}
	if e, ok := c.(xdmburst.EndAhead); ok {
		b.ahead = e
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
