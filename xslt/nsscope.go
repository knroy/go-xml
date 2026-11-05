package xslt

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
)

// A deep result read its namespaces by walking up its ancestors: each
// shallow copy asked its source element for its in-scope bindings
// (scopeBindings), and asked of each one whether its result parent already
// supplies it (suppliedAt), a walk up the result that asked, of each
// ancestor, what its own name's prefix is bound to (scopeURI), another walk.
// An identity transform of a deep document was quadratic in depth, and cubic
// under a default namespace. Past nsFrameDepth levels of apply-templates,
// each result element whose body runs has a frame that keeps what was found
// for it and, for a shallow copy, its source's bindings, so that its
// children read their parent's answers. Shallower results walk as before:
// their walks are short, and a frame per element would cost each of them.

// nsFrameDepth is the apply-templates depth from which result elements get
// frames (a variable for the tests, which give every element one).
var nsFrameDepth = 32

// nsFrame is el, a result element whose body is running, and the answers
// found from it up. They hold while el's declarations stay as they were:
// fixup adds some once its body is done. src, for a shallow copy, is the
// source element copied and all its in-scope bindings.
type nsFrame struct {
	el    *xdm.Node
	up    *nsFrame
	src   *xdm.Node
	all   []nsBinding
	memo  []nsMemo
	decls int
}

const (
	lookScope  = iota // scopeURI: the innermost binding of the prefix
	lookSupply        // suppliedAt: the binding a child would inherit
)

type nsMemo struct {
	kind   uint8
	prefix string
	r      nsLook
}

// nsLook is what a lookup found: found says an element binds the prefix
// (to uri), blocked that it runs inherit-namespaces="no", fix that an
// element on the way will bind the prefix in fixup.
type nsLook struct {
	found, blocked, fix bool
	uri                 string
}

func (f *nsFrame) get(kind uint8, prefix string) (nsLook, bool) {
	if n := f.el.NumNamespaceDecls(); n != f.decls {
		f.memo, f.decls = f.memo[:0], n
		return nsLook{}, false
	}
	for _, m := range f.memo {
		if m.kind == kind && m.prefix == prefix {
			return m.r, true
		}
	}
	return nsLook{}, false
}

func (f *nsFrame) put(kind uint8, prefix string, r nsLook) {
	f.memo = append(f.memo, nsMemo{kind, prefix, r})
}

// enterNS makes el, a result element whose body is about to run, the
// innermost, in a deep result; src and its bindings, for a shallow copy,
// are shared (on the heap) or local (copied if kept). It returns what
// leaveNS restores.
func enterNS(rt *runtime, el, src *xdm.Node, shared, local []nsBinding) *nsFrame {
	prev := rt.ns
	if el == nil || rt.depth < nsFrameDepth {
		return prev
	}
	f := &nsFrame{el: el, up: prev, decls: el.NumNamespaceDecls()}
	if src != nil {
		f.src, f.all = src, shared
		if shared == nil {
			f.all = slices.Clone(local)
		}
	}
	rt.ns = f
	return prev
}

func leaveNS(rt *runtime, prev *nsFrame) { rt.ns = prev }

// srcBindings is scopeBindings(node): from its parent's when the innermost
// frame is a copy of the parent (shared, nil when not), else read up the
// ancestors into buf (local).
func srcBindings(rt *runtime, node *xdm.Node, buf []nsBinding) (shared, local []nsBinding) {
	if f := rt.ns; f != nil && f.src != nil && f.src == node.Parent() && node.Kind() == xdm.KindElement {
		if node.NumNamespaceDecls() == 0 {
			return f.all, nil
		}
		return overlayScope(f.all, node), nil
	}
	return nil, scopeBindings(node, buf)
}

// copyShallowScope copies to sub the in-scope namespaces of node, a source element
// whose shallow copy sub is building (copyNamespacesTo), unless copyNS is
// false, and returns them, shared or local as srcBindings has them.
func copyShallowScope(rt *runtime, sub *outputBuilder, node *xdm.Node, copyNS bool, buf []nsBinding) (shared, local []nsBinding) {
	shared, local = srcBindings(rt, node, buf)
	if copyNS {
		bs := local
		if shared != nil {
			bs = shared
		}
		addBindings(rt, sub, bs)
	}
	return shared, local
}

// lookUp answers a lookup of prefix from n up: step decides at one element
// (passed the frame its own lookups start from), and an element with a
// frame answers from its memo. The frames passed without an answer keep the
// one found.
func lookUp(f *nsFrame, n *xdm.Node, kind uint8, prefix string, elementsOnly bool,
	step func(cur *xdm.Node, f *nsFrame) (nsLook, bool)) nsLook {
	var buf [4]*nsFrame
	pending := buf[:0]
	var r nsLook
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.Kind() != xdm.KindElement {
			if elementsOnly {
				break
			}
			continue
		}
		if f != nil && cur != f.el && f.up != nil && cur == f.up.el {
			f = f.up // n was the parent of the innermost frame's element
		}
		at := f
		if f != nil && cur == f.el {
			if m, ok := f.get(kind, prefix); ok {
				r = m
				break
			}
			pending = append(pending, f)
			f = f.up
		}
		if m, ok := step(cur, at); ok {
			r = m
			break
		}
	}
	for _, p := range pending {
		p.put(kind, prefix, r)
	}
	return r
}

// scopeIn is scopeURI, reading the frames from f.
func scopeIn(f *nsFrame, n *xdm.Node, prefix string) string {
	if f == nil {
		return scopeURI(n, prefix)
	}
	r := lookUp(f, n, lookScope, prefix, false, func(cur *xdm.Node, _ *nsFrame) (nsLook, bool) {
		found, last := false, ""
		for p, uri := range cur.DeclaredNamespaces() {
			if p == prefix {
				found, last = true, uri
			}
		}
		return nsLook{found: found, uri: last}, found
	})
	switch {
	case r.found:
		return r.uri
	case prefix == "xml":
		return xdm.NSXML
	}
	return ""
}
