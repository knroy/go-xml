package xpath

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

func init() {
	xpathleaf.Mark = func(fn any) { fn.(*Function).leaf = true }
	xpathleaf.GetHost = func(c any) *xpathleaf.Host { return c.(*Context).host }
	xpathleaf.WithHost = func(c any, h *xpathleaf.Host) any {
		n := *c.(*Context)
		n.host = h
		return &n
	}
	xpathleaf.SetHost = func(c any, h *xpathleaf.Host) { c.(*Context).host = h }
	xpathleaf.WithLocals = func(c any, l *xpathleaf.Locals) any {
		n := *c.(*Context)
		n.Vars, n.bind, n.Parent = nil, &varBinding{locals: l}, c.(*Context)
		return &n
	}
	xpathleaf.WithBindingsOf = func(c, base any) any {
		n, b := *c.(*Context), base.(*Context)
		n.Vars, n.bind, n.Parent = b.Vars, b.bind, b.Parent
		return &n
	}
	xpathleaf.WithFocusHost = func(c any, item xdm.Item, pos, size int, h *xpathleaf.Host) any {
		n := *c.(*Context)
		n.Item, n.Position, n.Size, n.host = item, int32(pos), int32(size), h
		return &n
	}
	xpathleaf.ResolveDocumentIn = func(r, c any, uri, base string) (*xdm.Tree, error) {
		return resolveDocumentIn(r.(ContextDocumentResolver), c.(*Context), uri, base)
	}
	xpathleaf.CallItem = func(c any, fn *xdm.FunctionItem, args []xdm.Sequence) (xdm.Sequence, error) {
		return callItem(c.(*Context), fn, args)
	}
}

// The host state with no runtime, cleared and marked: shared, since a Host is
// never written once it is on a context.
var (
	hostCurrentCleared = &xpathleaf.Host{CurrentSet: true}
	hostCallMarked     = &xpathleaf.Host{CurrentSet: true, Absent: true}
)

// hostOnCall is h across a dynamic function call: the current item cleared,
// as ClearedOnDynamicCall clears variables, and with mark, flagged absent, as
// MarkedOnDynamicCall marks them.
func hostOnCall(h *xpathleaf.Host, mark bool) *xpathleaf.Host {
	if h == nil || h.Runtime == nil && h.Unbound == 0 {
		if mark || h != nil && h.Absent {
			return hostCallMarked
		}
		return hostCurrentCleared
	}
	if h.CurrentSet && h.Current == nil && (h.Absent || !mark) {
		return h
	}
	n := *h
	n.Current, n.CurrentSet = nil, true
	n.Absent = n.Absent || mark
	return &n
}
