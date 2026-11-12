package xslt

// A dynamic call of a global accumulator-before#1 or accumulator-after#1 in
// a streamed instruction's motionless part (lane L1; accumulator-061).
//
// "../$aa('f:figNr')" with <xsl:param name="aa" select="accumulator-after#1"/>:
// the function item is made once, when the global is evaluated, with the
// global's focus -- the global context item, never a node of a streamed
// document (§2.3.2: absent under TransformReader's streamable initial mode,
// streamglobal_l7.go), and a focus that is one saves an absent focus anyway
// (§10.3.6, accumdyn_l4.go). So the call reads nothing of the focus it is
// made from: it raises XTDE3350 (no context item), or reads another
// document's accumulator, the same in the streamed run and the tree's. For
// the view check it is its arguments (mapCalls rewrites it to their sequence,
// as it rewrites a map's $m(k) to map:get: a check-only parse).
//
// The value is the declaration's only while no caller supplies the
// parameter: a supplied function item, a host's above all, may read the
// dynamic focus it is called with, which would be a view here. So an
// xsl:param relied on is recorded per stylesheet document (dynParams), and a
// transformation supplying it does not stream (dynParamSupplied).

import (
	goruntime "runtime"
	"strings"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// dynParams maps a stylesheet document to the Clark names of the global
// xsl:params whose accumulator function items the view check relied on. The
// key is weak and the value holds no node, so an entry dies with its
// document: each compile has a fresh copy, and a strong key would pin every
// compiled stylesheet for the life of the process.
var dynParams sync.Map // weak.Pointer[xdm.Node] -> *sync.Map (string -> bool)

// accumFnGlobal reports whether q, named at el, is a global variable or
// parameter bound to accumulator-before#1 or accumulator-after#1, with no
// local declaration shadowing it; a parameter is recorded in dynParams.
func accumFnGlobal(el *xdm.Node, q xdm.QName) bool {
	d := globalDeclOf(el, q)
	if d == nil || onlyElementChild(d) != nil || strings.TrimSpace(d.StringValue()) != "" {
		return false
	}
	s := d.AttrValue("select")
	if s == "" {
		return false
	}
	e, err := xpath.ParseVersion(s, newNSResolver(d, xpathDefaultNamespace(d)), xpathVersionAt(d))
	if err != nil {
		return false
	}
	r, ok := e.(*xpath.NamedFunctionRef)
	if !ok || r.Arity != 1 || r.Name.URI != fnNS || r.Name.Local != "accumulator-before" && r.Name.Local != "accumulator-after" {
		return false
	}
	if isXSL(d, "param") {
		root := d.Root()
		key := weak.Make(root)
		m, loaded := dynParams.LoadOrStore(key, &sync.Map{})
		if !loaded {
			goruntime.AddCleanup(root, func(k weak.Pointer[xdm.Node]) { dynParams.Delete(k) }, key)
		}
		m.(*sync.Map).Store(q.Clark(), true)
	}
	return true
}

// globalDeclOf is the top-level xsl:variable or xsl:param named q that el
// sees, or nil when there is none or a local declaration comes first (the
// scoping of mapVarKind).
func globalDeclOf(el *xdm.Node, q xdm.QName) *xdm.Node {
	for n := el; n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		p := n.Parent()
		if p == nil || p.Kind() != xdm.KindElement {
			return nil
		}
		top := isXSL(p, "stylesheet") || isXSL(p, "transform") || isXSL(p, "package")
		for c := range p.Children() {
			if c == n && !top {
				break
			}
			if c.Kind() == xdm.KindElement && (isXSL(c, "variable") || isXSL(c, "param")) && c != n {
				if name, err := resolveQNameAttr(c, c.AttrValue("name")); err == nil && name == q {
					if top {
						return c
					}
					return nil // a local one
				}
			}
		}
		if top {
			return nil
		}
	}
	return nil
}

// accumFnCall is what mapCalls puts for dc, a call "$v(args)" of such a
// global: the sequence of its arguments; nil when dc is not one.
func accumFnCall(dc *xpath.DynamicCall, el *xdm.Node, bound map[xdm.QName]bool) xpath.Expr {
	vr, ok := dc.Target.(*xpath.VarRef)
	if !ok || bound[vr.Name] || !accumFnGlobal(el, vr.Name) {
		return nil
	}
	return &xpath.SequenceExpr{Items: dc.Args}
}

// dynParamSupplied reports whether opts supplies a parameter the stream's
// checks took for its declared accumulator function item.
func (s *Stylesheet) dynParamSupplied(opts *TransformOptions) bool {
	if opts == nil || len(opts.Params) == 0 || s.source == nil {
		return false
	}
	m, ok := dynParams.Load(weak.Make(s.source))
	if !ok {
		return false
	}
	found := false
	m.(*sync.Map).Range(func(k, _ any) bool {
		if _, ok := opts.Params[k.(string)]; ok {
			found = true
		}
		return !found
	})
	return found
}
