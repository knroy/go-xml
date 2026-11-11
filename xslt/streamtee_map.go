package xslt

// A map constructor whose entries each read the document (lane L1;
// sx-MapExpr-002/004/005).
//
// <xsl:variable name="m" select="map{'a': P1, 'b': P2, ...}"/> at the
// document node of a container: each Pi consumes the document, so no one
// split takes the select ("a second consuming operand"). It is the XPath
// form of an xsl:map whose entries each consume (§19.8.4.23, streamfork.go),
// and it runs as one: each consuming value is split on its own and read on a
// cursor of its own over the one read of the document's bytes (Cursor.Tee);
// then the map is made of the keys and the values they read.
//
// What the tree does that this does the same: MapConstructor.Eval evaluates
// each key, then its value, entry by entry, and stops at the first error.
// The keys are string literals, all different (no XQDY0137 to order), and
// every value is a consuming one or a literal, so the first error in entry
// order is the first error of the parts in entry order, which Tee keeps (a
// part after a failed one is not read on). A part that gives up (a min or
// max over mixed types) gives the select back to the tree as any select at
// the document node does (evalOp). Only in a container's own sequence
// constructor, not inside a fork or map (whose cursors are a tee's already)
// nor a rule's body; the parts call no stylesheet function and no fn:trace,
// so that a part's turn binds no local variable and tells no message.
//
// Memory: each part is held to the memory rule as a select at the document
// node is (readySelect); the parse per part is the tee's (one block of bytes
// beside each parse).

import (
	"reflect"
	"strconv"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// streamTee is the plan of such a select: the splits of the consuming
// values, in entry order, and the map made of their values ($tee0, $tee1, ...).
type streamTee struct {
	parts []*streamSplit
	outer *xpath.Compiled
}

func teeVar(j int) xdm.QName {
	return xdm.QName{URI: "urn:x-go-xml:split", Local: "tee" + strconv.Itoa(j)}
}

// maxTeeParts bounds the consumers one tee runs, each a parse and a
// goroutine live for the whole document (xdmstream.Cursor.Tee). A map or fork
// with more consuming parts is not teed: it runs as it would unplanned, on
// the tree, with the same result.
const maxTeeParts = 64

// teeFor plans sel, the select of op, a variable at the document node whose
// split was refused, as a tee of its map's entries; nil when it is not one.
func (c *compiler) teeFor(op *streamOp, sel *xpath.Compiled, ns xpath.NamespaceResolver) *streamTee {
	if !op.doc || op.x.rule || op.inGroup || op.avt != "" || !isXSL(op.el, "variable") || sel.CompatMode() {
		return nil
	}
	for n := op.el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if isXSL(n, "fork") || isXSL(n, "map") || isXSL(n, "try") {
			return nil
		}
	}
	m, ok := reparse(sel, ns).(*xpath.MapConstructor)
	if !ok || len(m.Keys) != len(m.Values) {
		return nil
	}
	t := &streamTee{}
	seen := map[string]bool{}
	out := &xpath.MapConstructor{Keys: m.Keys, Values: make([]xpath.Expr, len(m.Values))}
	for i, k := range m.Keys {
		lit, ok := k.(*xpath.Literal)
		if !ok || lit.Val == nil || lit.Val.Type != xdm.TypeString || seen[lit.Val.String()] {
			return nil
		}
		seen[lit.Val.String()] = true
		v := m.Values[i]
		if _, ok := v.(*xpath.Literal); ok {
			out.Values[i] = v
			continue
		}
		if !teePartCalls(v) {
			return nil
		}
		s, _ := splitStreamIn(v, sel, true, false, false)
		if s == nil {
			return nil
		}
		out.Values[i] = &xpath.VarRef{Name: teeVar(len(t.parts))}
		t.parts = append(t.parts, s)
	}
	if len(t.parts) < 2 || len(t.parts) > maxTeeParts {
		return nil
	}
	t.outer = xpathleaf.WithExpr(sel, out).(*xpath.Compiled)
	return t
}

// teePartCalls reports whether e calls only built-in functions, fn:trace
// aside.
func teePartCalls(e xpath.Expr) bool {
	ok := true
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		f, is := v.Interface().(*xpath.FuncCall)
		if !is {
			if _, dyn := v.Interface().(*xpath.DynamicCall); dyn {
				ok = false
			}
			return
		}
		switch f.Name.URI {
		case fnNS:
			if f.Name.Local == "trace" {
				ok = false
			}
		case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
		default:
			ok = false
		}
	})
	return ok
}

// ready holds each part to the memory rule at the document node.
func (t *streamTee) ready(op *streamOp) string {
	for _, s := range t.parts {
		if why := s.memory(0); why != "" {
			return elName(op.el) + " " + why + ": " + op.sel.Source()
		}
	}
	return ""
}

// teeEval reads the parts of op's tee, each on a cursor of its own, and
// makes the map. The first error in entry order is the select's.
func (run *streamRun) teeEval(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	t := op.tee
	if run.d != 0 || !run.c.Fresh() {
		return nil, errStreamPlan
	}
	vals := make([]xdm.Sequence, len(t.parts))
	// The eval lock is let go as each turn ends, as the fork's prongs do
	// (streamfork.go).
	back := xpathleaf.LockYield(rt.evalLock)
	errs := run.c.Tee(len(t.parts), func(j int, c *xdmstream.Cursor) error {
		defer xpathleaf.LockYield(rt.evalLock)
		v, err := t.parts[j].evalAt(rt.ctx, c, 0, nil)
		vals[j] = v
		return err
	}, func(int, bool) {})
	back()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	ctx := rt.ctx
	for j, v := range vals {
		ctx = ctx.WithVar(teeVar(j), v)
	}
	return t.outer.Eval(ctx)
}
