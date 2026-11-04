package xdm

import "github.com/knroy/go-xml/v2/internal/xdmburst"

// has-children of a view (lane L6). A view has no content, so fn:has-children
// of it is a posture fault -- unless the stream driver, at the view's start
// tag, peeked at the next event and recorded the answer here (two flag bits
// of the view's own record). Only a shallow node's bits are read.

func init() {
	xdmburst.ShallowKids = func(n any) (bool, bool) {
		x := n.(*Node)
		if x.flags&fShallow == 0 || x.flags&fKidsKnown == 0 {
			return false, false
		}
		return x.flags&fKids != 0, true
	}
	xdmburst.SetShallowKids = func(n any, has bool) {
		x := n.(*Node)
		if x.flags&fShallow == 0 {
			return
		}
		x.flags |= fKidsKnown
		if has {
			x.flags |= fKids
		}
	}
}
