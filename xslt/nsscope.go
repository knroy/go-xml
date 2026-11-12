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
//
// Eight, not 32: a frame costs an allocation (32 B) per element whose body
// runs, about 3% of a transform's time, but a walk costs a step per level
// for every element under the frameless levels. 100 000 leaves under 28
// levels of p:e literal result elements or xsl:copy took 14-18% longer with
// frames from 32 than from 8; from 8 the two agree to within noise for
// 1-7 levels, which have no frames, tie at 10 and are 3-11% faster from 13.

// nsFrameDepth is the apply-templates depth from which result elements get
// frames (a variable for the tests, which give every element one).
var nsFrameDepth = 8

// nsFrame is el, a result element whose body is running; src, for a shallow
// copy, is the source element copied. x, made only when there is something
// to keep (a frame per level costs every deep result), holds src's in-scope
// bindings and the answers found from el up.
type nsFrame struct {
	el, src *xdm.Node
	up      *nsFrame
	x       *nsKept
}

// nsKept is a frame's all (nil: no bindings) and memo, which holds while
// el's declarations number decls: fixup adds some once el's body is done.
type nsKept struct {
	all   []nsBinding
	memo  []nsMemo
	decls int
}

func (f *nsFrame) all() []nsBinding {
	if f.x == nil {
		return nil
	}
	return f.x.all
}

const (
	lookScope   = iota // scopeURI: the innermost binding of the prefix
	lookSupply         // suppliedAt: the binding a child would inherit
	lookDefault        // undeclaresDefault: the default once fixup is done
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
	x := f.x
	if x == nil {
		f.x = &nsKept{decls: f.el.NumNamespaceDecls()}
		return nsLook{}, false
	}
	if n := f.el.NumNamespaceDecls(); n != x.decls {
		x.memo, x.decls = x.memo[:0], n
		return nsLook{}, false
	}
	for _, m := range x.memo {
		if m.kind == kind && m.prefix == prefix {
			return m.r, true
		}
	}
	return nsLook{}, false
}

func (f *nsFrame) put(kind uint8, prefix string, r nsLook) {
	f.x.memo = append(f.x.memo, nsMemo{kind, prefix, r})
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
	f := &nsFrame{el: el, src: src, up: prev}
	if all := shared; len(all) > 0 || len(local) > 0 {
		if all == nil {
			all = slices.Clone(local)
		}
		f.x = &nsKept{all: all, decls: el.NumNamespaceDecls()}
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
			return f.all(), nil
		}
		return overlayScope(f.all(), node), nil
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

// undeclaresDefault reports whether el, a result element, needs xmlns="":
// its name is unprefixed and in no namespace, and the default namespace it
// would inherit once the elements around it are finished is not empty.
// Those elements' fixup is still to run (each runs once its body is done),
// so what they declare now is not what el will inherit: an unprefixed
// ancestor's default will be its own name's namespace, whatever it declares
// yet (§5.7.3), and only a prefixed one's declarations stand.
func undeclaresDefault(f *nsFrame, el *xdm.Node) bool {
	if el.Name().URI != "" || el.Name().Prefix != "" {
		return false
	}
	if r, ok := declaresDefault(el); ok {
		return r.uri != ""
	}
	// The parent decides, mostly: asked first, without the frames, whose
	// memo would cost each deep level one.
	p := el.Parent()
	if p == nil || p.Kind() != xdm.KindElement {
		return false
	}
	if r, ok := defaultAt(p, nil); ok {
		return r.uri != ""
	}
	if f == nil {
		for cur := p.Parent(); cur != nil && cur.Kind() == xdm.KindElement; cur = cur.Parent() {
			if r, ok := defaultAt(cur, nil); ok {
				return r.uri != ""
			}
		}
		return false
	}
	return lookUp(f, p, lookDefault, "", true, defaultAt).uri != ""
}

// defaultAt is undeclaresDefault's step: the default namespace of cur once
// its fixup is done, if cur decides it.
func defaultAt(cur *xdm.Node, _ *nsFrame) (nsLook, bool) {
	if cur.Name().Prefix == "" {
		return nsLook{found: true, uri: cur.Name().URI}, true
	}
	return declaresDefault(cur)
}

// declaresDefault is the last declaration of the default namespace on cur.
func declaresDefault(cur *xdm.Node) (nsLook, bool) {
	r := nsLook{}
	for p, uri := range cur.DeclaredNamespaces() {
		if p == "" {
			r = nsLook{found: true, uri: uri}
		}
	}
	return r, r.found
}
