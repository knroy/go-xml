package xdm

import (
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// The stream driver (burster) serves an outside client through
// xdmburst.Drive: every element it is asked about is kept, grounded or
// skipped as the client says, every leaf of a kept element is reported, and
// each grounded element is copied out at once, in a tree of its own.
func init() {
	xdmburst.Drive = func(r io.Reader, opts any, c xdmburst.Client) error {
		a := &driveClient{c: c}
		o := opts.(ParseOptions)
		o.bursts = &burster{
			c:       a,
			fn:      func(n *Node) error { a.grounded = n; return nil },
			leaves:  true,
			noBatch: true,
			watch:   func(el *Node, d int, end bool) { c.Watch(el, d, end) },
			gens:    []uint64{0},
		}
		_, err := parse(r, "", o)
		return err
	}
}

// driveClient is an xdmburst.Client as a streamClient.
type driveClient struct {
	c        xdmburst.Client
	grounded *Node
}

func (a *driveClient) start(el *Node, d int) (xdmburst.Action, error) { return a.c.Start(el, d) }

func (a *driveClient) leaf(n *Node, d int) error { return a.c.Leaf(n, d) }

func (a *driveClient) end(el *Node, d int, _ bool) error {
	var g any
	if a.grounded != nil {
		g, a.grounded = a.grounded, nil
	}
	return a.c.End(el, d, g)
}
