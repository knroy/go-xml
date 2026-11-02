package xpath

import (
	"math"
	"sync/atomic"

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
	xpathleaf.WithExpr = func(c, e any) any {
		n := *c.(*Compiled)
		n.expr = optimize(e.(Expr))
		n.nsNeeded = readsStaticNamespaces(n.expr)
		return n.withOwn()
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

func init() {
	xpathleaf.SetLimits = func(c any, l xpathleaf.Limits) {
		b := c.(*Context).ev().limits
		if b == nil {
			return
		}
		b.nodes, b.bytes = limitOr(l.Nodes, MaxNodes), limitOr(l.Bytes, MaxBytes)
		b.nodesBuilt, b.bytesBuilt = max(l.NodesBuilt, 0), max(l.BytesBuilt, 0)
	}
	xpathleaf.Charged = func(c any) (int64, int64) {
		e := c.(*Context).ev()
		if e.nodes == nil || e.bytes == nil {
			return 0, 0
		}
		return atomic.LoadInt64(e.nodes), atomic.LoadInt64(e.bytes)
	}
	xpathleaf.Refund = func(c any, nodes, bytes int64) {
		e := c.(*Context).ev()
		refund(e.nodes, nodes)
		refund(e.bytes, bytes)
	}
}

// limitOr is a caller's limit: def for zero, none for negative.
func limitOr(v, def int64) int64 {
	switch {
	case v == 0:
		return def
	case v < 0:
		return math.MaxInt64
	}
	return v
}

// refund lowers *p by n, never below zero.
func refund(p *int64, n int64) {
	if p == nil || n <= 0 {
		return
	}
	for {
		old := atomic.LoadInt64(p)
		if atomic.CompareAndSwapInt64(p, old, max(old-n, 0)) {
			return
		}
	}
}
