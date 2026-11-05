package xslt

// Lane MM3: a call on a stylesheet function whose body is a sequence
// constructor of instructions, given only grounded copies of the consuming
// path and atomic values (sf-unparsed-entity-03/04: f:f(snapshot(.)) at the
// document element).
//
// A function body has no focus (§10.3.2), so what it reads of a document
// it reads through its arguments. Given only copies -- copy-of(P) or
// snapshot(P), which the split makes of P's matches as the tree makes them
// of its nodes -- and atomic values, the body reaches no node of the
// stream: the outer calls the function, the tree's own code, on the copies,
// as the burst route does for a one-xsl:sequence body (streamsplit_calls.go).
// The body must hold nothing whose answer depends on more than the copies'
// content: no impure instruction or call (pureBody), no call on or reference
// to another stylesheet function (whose body this does not read), no
// attribute set, no function reading node identity or another document of
// the copy (burstFnBanned, but for the unparsed-entity functions, which the
// copy's DTD answers: xdm CopyDTDFrom), and no text value template.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// copiesOnlyBody reports whether fn, an xsl:function whose body is not one
// xsl:sequence, may be called on copies by the outer.
func copiesOnlyBody(fn *xdm.Node) bool {
	if fn == nil || !pureBody(fn) {
		return false
	}
	ok := true
	check := func(e xpath.Expr) {
		walkExprNodes(e, func(v any) {
			switch x := v.(type) {
			case *xpath.FuncCall:
				if stylesheetFn(x.Name) || x.Name.URI == fnNS && burstFnBanned[x.Name.Local] &&
					x.Name.Local != "unparsed-entity-uri" && x.Name.Local != "unparsed-entity-public-id" {
					ok = false
				}
			case *xpath.NamedFunctionRef:
				if stylesheetFn(x.Name) || x.Name.URI == fnNS && burstFnBanned[x.Name.Local] {
					ok = false
				}
			case *xpath.DynamicCall:
				ok = false
			}
		})
	}
	walkElements(fn, func(el *xdm.Node) bool {
		for ch := range el.Children() {
			if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
				ok = false
			}
		}
		for a := range el.Attrs() {
			n, v := a.Name(), a.Value()
			if n.URI != "" || n.Local == "use-attribute-sets" {
				ok = false
				continue
			}
			if e := parseAt(el, v); e != nil {
				check(e)
			}
			if srcs, isAVT := avtExpressions(v); isAVT {
				for _, s := range srcs {
					if e := parseAt(el, s); e != nil {
						check(e)
					} else {
						ok = false
					}
				}
			}
		}
		return ok
	})
	return ok
}

// copiesCall is sheetCall's case for a call marked copies: each argument
// is atomized by its parameter's type, motionless and atomic, or copy-of(P)
// or snapshot(P) of the consuming path P itself.
func (w *splitter) copiesCall(x *xpath.FuncCall, sc sheetCall) (done, ok bool) {
	for k := range x.Args {
		orig, before := x.Args[k], w.pe
		use := useKeep
		if k < len(sc.atomicArg) && sc.atomicArg[k] {
			use = useAtomize // §10.3.3: the parameter's type atomizes it
		}
		var inner xpath.Expr
		if c, isCall := orig.(*xpath.FuncCall); isCall && c.Name.URI == fnNS && len(c.Args) == 1 &&
			(c.Name.Local == "copy-of" || c.Name.Local == "snapshot") {
			inner = c.Args[0]
		}
		if !w.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }, use) {
			return true, false
		}
		switch {
		case use == useAtomize:
		case w.pe == before && atomicValued(orig):
		case before == nil && inner != nil && w.pe == inner:
		default:
			return true, w.refuse("function " + x.Name.Local + "() given more than copies and values")
		}
	}
	return true, true
}

// copiedArgs reports whether each of args is copy-of(X) or atomic: on a
// match's grounded copy, a copy-of is the tree's copy of the node (a
// snapshot is not: its ancestors are the copy's). sheetCallSafe takes a
// copies call so (function-5011's tail sf:unclassified(copy-of(.))).
func copiedArgs(args []xpath.Expr) bool {
	for _, a := range args {
		c, ok := a.(*xpath.FuncCall)
		if ok && c.Name.URI == fnNS && c.Name.Local == "copy-of" && len(c.Args) == 1 || atomicValued(a) {
			continue
		}
		return false
	}
	return true
}
