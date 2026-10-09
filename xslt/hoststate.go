package xslt

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// XSLT's dynamic state rides on the XPath context as an xpathleaf.Host: the
// transform runtime, what fn:current() returns, the dynamic-call marker that
// makes current() XTDE1360, and the context components (grouping, merge,
// regex captures) known to be cleared. It used to be a set of reserved
// variable bindings, and every current(), key() or stylesheet-function call
// walked the whole binding chain to find them. The host state follows the
// bindings' scoping exactly: it is copied with the context, comes from the
// call across a named function reference and from the closure inside an
// inline function, and a dynamic call clears current() and marks it absent
// (see xpath's hostOnCall).

func hostOf(ctx *xpath.Context) *xpathleaf.Host { return xpathleaf.GetHost(ctx) }

// runtimeOf is the transform runtime ctx carries, or nil.
func runtimeOf(ctx *xpath.Context) *runtime {
	if h := hostOf(ctx); h != nil {
		rt, _ := h.Runtime.(*runtime)
		return rt
	}
	return nil
}

// hostWithCurrent returns h with item bound as fn:current().
func hostWithCurrent(h *xpathleaf.Host, item xdm.Item) *xpathleaf.Host {
	// The host and the one-item sequence in one allocation.
	n := &struct {
		h    xpathleaf.Host
		item [1]xdm.Item
	}{}
	n.item[0] = item
	n.h.Current, n.h.CurrentSet = n.item[:], true
	if h != nil {
		n.h.Runtime, n.h.Absent, n.h.Unbound = h.Runtime, h.Absent, h.Unbound
	}
	return &n.h
}

// withCurrentItem binds item as fn:current() on a copy of ctx. When ctx
// already binds that node, ctx is returned: the copy would equal it, and
// template dispatch and pattern predicates almost always find it so.
func withCurrentItem(ctx *xpath.Context, item xdm.Item) *xpath.Context {
	if n, ok := item.(*xdm.Node); ok {
		if h := hostOf(ctx); h != nil && h.CurrentSet && len(h.Current) == 1 {
			if c, ok := h.Current[0].(*xdm.Node); ok && c == n {
				return ctx
			}
		}
	}
	return xpathleaf.WithHost(ctx, hostWithCurrent(hostOf(ctx), item)).(*xpath.Context)
}

// withFocusCurrent sets the focus and fn:current() in one copy of ctx.
func withFocusCurrent(ctx *xpath.Context, item xdm.Item, pos, size int) *xpath.Context {
	return xpathleaf.WithFocusHost(ctx, item, pos, size,
		hostWithCurrent(hostOf(ctx), item)).(*xpath.Context)
}

// bindRuntime returns a copy of ctx carrying rt as its transform runtime.
func bindRuntime(ctx *xpath.Context, rt *runtime) *xpath.Context {
	n := xpathleaf.Host{Runtime: rt}
	if h := hostOf(ctx); h != nil {
		n = *h
		n.Runtime = rt
	}
	return xpathleaf.WithHost(ctx, &n).(*xpath.Context)
}

// setUnbound records on ctx, which the caller has just made, which context
// components it is known to have cleared; see runtime.absent.
func setUnbound(ctx *xpath.Context, bits uint8) {
	h := hostOf(ctx)
	if h == nil || h.Unbound == bits {
		return
	}
	n := *h
	n.Unbound = bits
	xpathleaf.SetHost(ctx, &n)
}
