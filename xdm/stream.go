package xdm

import (
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// The stream driver (burster) serves an outside client through
// xdmburst.Drive: every element it is asked about is kept, grounded or
// skipped as the client says, and every leaf of a kept element is reported.
func init() {
	xdmburst.Drive = func(r io.Reader, opts any, c xdmburst.Client) error {
		o := opts.(ParseOptions)
		o.bursts = driver(c)
		_, err := parse(r, "", o)
		return err
	}
}

// driver is the burster that serves c.
func driver(c xdmburst.Client) *burster {
	b := &burster{
		c:      &driveClient{c},
		fn:     func(n *Node) error { return c.Later(n) },
		leaves: true,
		watch:  func(el *Node, d int, end bool) { c.Watch(el, d, end) },
		gens:   []uint64{0},
		ords:   []uint64{0},
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
