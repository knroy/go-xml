package xslt

// Lane K1: calls on stylesheet functions that the split reads as calls,
// where streamcall.go cannot inline them (a body that is not the call
// exactly, a recursive function, a call in a predicate).
//
// A call is marked, as the split's expression is parsed (inlineCalls), when
// the function and every stylesheet function its body reaches have the
// inliner's shape -- xsl:param elements and one xsl:sequence select, no
// cache, collation or base of their own -- so that the call is an XPath
// expression with no side effect, and one of:
//
//   - values: every parameter is declared with an atomic type, or there is
//     none (§10.3.3 atomizes each argument): the body is given values only,
//     never a node, so the call reads of the stream what the atomization of
//     its arguments reads, and no more (§19.8.5: a zero-arity call is
//     grounded and motionless);
//   - burst: no parameter has a schema type, and every body reached reads
//     of the nodes it is given nothing a grounded copy lacks (burstSafe:
//     downward, attributes, names of ancestors; no identity, root, key or
//     position among all the items). Given a grounded copy of a match, the
//     call returns what it returns given the match.
//
// The marks are read by burstSafe (callSafe), atomicValued (a declared
// atomic result type), viewCheck (values calls over atomized operands) and
// the split's walk (a call holding the consuming path). They are kept per
// call node, weakly: a node's mark goes when its parse does.

import (
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// sheetCall is what the split knows of a call on a stylesheet function.
type sheetCall struct {
	values bool // every parameter takes atomic values
	burst  bool // every body reached is burstSafe over the nodes it is given
	atomic bool // the declared result type is atomic
	// climb says a body reached climbs; up that the result may hold
	// ancestors' copies or their attributes, of kind res (streamfn_climb.go).
	climb, up bool
	res       ckind
	// keep says the call is read as a call, not inlined: its closure was
	// judged with climbing allowed and recurses, which an inlined body,
	// its inner calls unmarked, would not let the split read.
	keep bool
	// atomicArg says, per parameter, that its type is atomic: the call
	// atomizes that argument (streamfn_inline.go).
	atomicArg []bool
}

var sheetCalls sync.Map // weak.Pointer[xpath.FuncCall] -> sheetCall

func noteSheetCall(x *xpath.FuncCall, sc sheetCall) {
	k := weak.Make(x)
	if _, loaded := sheetCalls.Swap(k, sc); !loaded {
		goruntime.AddCleanup(x, func(k weak.Pointer[xpath.FuncCall]) { sheetCalls.Delete(k) }, k)
	}
}

// sheetCallOf is the mark of x, when it has one.
func sheetCallOf(x *xpath.FuncCall) (sheetCall, bool) {
	if !stylesheetFn(x.Name) {
		return sheetCall{}, false
	}
	v, ok := sheetCalls.Load(weak.Make(x))
	if !ok {
		return sheetCall{}, false
	}
	return v.(sheetCall), true
}

// markSheetCalls marks the calls in e on stylesheet functions of fns.
func markSheetCalls(e xpath.Expr, fns map[funcKey]*streamFunc) {
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if v.Kind() != reflect.Interface {
			return
		}
		x, ok := v.Interface().(*xpath.FuncCall)
		if !ok || !stylesheetFn(x.Name) {
			return
		}
		if sc, ok := judgeSheetCall(x, fns); ok {
			noteSheetCall(x, sc)
		}
	})
}

// judgeSheetCall decides x's mark.
func judgeSheetCall(x *xpath.FuncCall, fns map[funcKey]*streamFunc) (sheetCall, bool) {
	f := fns[keyOf(x.Name, len(x.Args))]
	if f == nil {
		return sheetCall{}, false
	}
	bodies := map[*streamFunc]xpath.Expr{}
	if !sheetClosure(f, fns, bodies) {
		return sheetCall{}, false
	}
	sc := sheetCall{values: atomicParams(f.body), atomic: atomicAs(f.body, f.asType), atomicArg: paramAtomics(f.body)}
	if !sc.values {
		sc.burst = true
		for g, body := range bodies {
			if !plainParams(g.body) || !burstSafe(body, false) {
				sc.burst = false
				break
			}
		}
		climbMark(&sc, f, fns, bodies) // streamfn_climb.go
	}
	return sc, sc.values || sc.burst
}

// sheetClosure parses the body of f and of every stylesheet function it
// reaches into bodies, marking the calls in them for the burstSafe check
// that follows (judgeSheetCall: the marks of a closure that fails are on
// parses nothing else reads). It fails on a body of another shape, a call
// on a function it does not know, and a function item or dynamic call,
// through which a body could reach one.
func sheetClosure(f *streamFunc, fns map[funcKey]*streamFunc, bodies map[*streamFunc]xpath.Expr) bool {
	body := sheetBody(f.body)
	if body == nil {
		return false
	}
	bodies[f] = body
	ok := true
	exprWalk(reflect.ValueOf(&body).Elem(), func(v reflect.Value) {
		if !ok || v.Kind() != reflect.Interface {
			return
		}
		switch x := v.Interface().(type) {
		case *xpath.DynamicCall:
			ok = false
		case *xpath.NamedFunctionRef:
			ok = !stylesheetFn(x.Name) && !(x.Name.URI == fnNS && burstFnBanned[x.Name.Local])
		case *xpath.FuncCall:
			if x.Name.URI == fnNS && burstFnBanned[x.Name.Local] {
				ok = false
				return
			}
			if !stylesheetFn(x.Name) {
				return
			}
			g := fns[keyOf(x.Name, len(x.Args))]
			if g == nil {
				ok = false
				return
			}
			noteSheetCall(x, sheetCall{values: atomicParams(g.body), burst: true, atomic: atomicAs(g.body, g.asType)})
			if _, seen := bodies[g]; !seen && !sheetClosure(g, fns, bodies) {
				ok = false
			}
		}
	})
	return ok
}

// sheetBody is the select of fn, an xsl:function of xsl:param elements and
// one xsl:sequence, parsed; nil for any other shape (streamcall.go's
// inlineCall takes the same).
func sheetBody(fn *xdm.Node) xpath.Expr {
	for a := range fn.Attrs() {
		switch n := a.Name(); {
		case n.URI == xdm.NSXML:
			return nil
		case n.URI != "":
		case n.Local == "name", n.Local == "as", n.Local == "streamability", n.Local == "visibility",
			n.Local == "override-extension-function", n.Local == "override", n.Local == "use-when",
			n.Local == "exclude-result-prefixes":
		default:
			return nil // new-each-time, cache, default-collation, version, ...
		}
	}
	var seq *xdm.Node
	for ch := range fn.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "", ch.Kind() == xdm.KindComment:
		case isXSL(ch, "param") && seq == nil:
			if ch.Attr("", "select") != nil || ch.FirstChild() != nil {
				return nil
			}
		case isXSL(ch, "sequence") && seq == nil && ch.FirstChild() == nil:
			seq = ch
		default:
			return nil
		}
	}
	if seq == nil {
		return nil
	}
	for a := range seq.Attrs() {
		if n := a.Name(); n.URI != "" || n.Local != "select" && n.Local != "use-when" {
			return nil
		}
	}
	return parseAt(seq, seq.AttrValue("select"))
}

// atomicParams reports whether every xsl:param of fn is declared with an
// atomic type (none: true).
func atomicParams(fn *xdm.Node) bool {
	for _, p := range fn.ChildElements() {
		if isXSL(p, "param") && !atomicAs(p, p.AttrValue("as")) {
			return false
		}
	}
	return true
}

// atomicAs reports whether as, a sequence type written on el, is an atomic
// type of the XML Schema namespace with any occurrence.
func atomicAs(el *xdm.Node, as string) bool {
	t := strings.TrimRight(strings.Join(strings.Fields(as), ""), "?*+")
	if t == "" || strings.ContainsAny(t, "()") {
		return false
	}
	q, err := resolveQNameAttr(el, t)
	return err == nil && q.URI == xdm.NSXS
}

// plainParams reports whether no xsl:param of fn has a schema type: a copy
// carries what the parse gave it, which a type naming a schema type or
// declaration could tell apart from the original.
func plainParams(fn *xdm.Node) bool {
	for _, p := range fn.ChildElements() {
		if !isXSL(p, "param") {
			continue
		}
		t := strings.Join(strings.Fields(p.AttrValue("as")), "")
		if strings.Contains(t, "schema-") || strings.Contains(t, ",") || strings.Contains(t, "document-node(") {
			return false
		}
	}
	return true
}

// sheetCallSafe is callSafe (streamburst.go) for a call on a stylesheet
// function: marked, and its arguments burstSafe.
func sheetCallSafe(x *xpath.FuncCall, noPos bool) bool {
	sc, ok := sheetCallOf(x)
	return ok && (sc.values || sc.burst) && !sc.up && allBurstSafe(x.Args, noPos)
}

// sheetCallAtomic is atomicValued for a call outside the fn namespace.
func sheetCallAtomic(x *xpath.FuncCall) bool {
	sc, ok := sheetCallOf(x)
	return ok && sc.atomic
}

// sheetCallFresh is mapFresh (streamshapes.go) for a call on a stylesheet
// function: it makes no node of the match's copy when it is given only
// atomic values, or returns only values.
func sheetCallFresh(x *xpath.FuncCall) bool {
	if sheetCallAtomic(x) {
		return true
	}
	for _, a := range x.Args {
		if !mapAtomic(a) {
			return false
		}
	}
	return true
}

// sheetCall is viewCheck.call for a call on a stylesheet function: a values
// call over operands the view answers, atomized.
func (vc viewCheck) sheetCall(x *xpath.FuncCall, f vfocus) bool {
	if foreignArgs(x) { // streamfn_inline.go
		return true
	}
	sc, ok := sheetCallOf(x)
	if !ok || !sc.values {
		return false
	}
	for _, a := range x.Args {
		if !vc.safe(a, f, false) {
			return false
		}
	}
	return true
}

// sheetCall is walk's case for a call on a stylesheet function. A values
// call takes its arguments atomized. A burst call takes them as they are,
// and the consuming path in one of them only as the argument itself (or
// copied: copy-of(P), snapshot(P)), a plain path whose matches cannot
// nest: the outer then calls the function on the matches' grounded copies,
// which do not overlap as nested matches' copies would.
func (w *splitter) sheetCall(x *xpath.FuncCall) (done, ok bool) {
	sc, marked := sheetCallOf(x)
	if !marked {
		return false, false
	}
	use := useKeep
	if sc.values {
		use = useAtomize
	}
	for k := range x.Args {
		orig, before := x.Args[k], w.pe
		use := use
		if k < len(sc.atomicArg) && sc.atomicArg[k] {
			use = useAtomize // §10.3.3: the parameter's type atomizes it
		}
		var inner xpath.Expr
		if c, ok := orig.(*xpath.FuncCall); ok && c.Name.URI == fnNS && len(c.Args) == 1 &&
			(c.Name.Local == "copy-of" || c.Name.Local == "snapshot") {
			inner = c.Args[0]
		}
		if !w.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }, use) {
			return true, false
		}
		if use == useAtomize && !sc.values {
			continue // values reach the body: nothing of a node
		}
		if sc.climb && !sc.values && before == nil && w.pe != nil {
			// The outer would call it once on every match's copy: their
			// ancestors' copies are not one node each, as the tree's are.
			return true, w.refuse("function " + x.Name.Local + "() climbing from the matches")
		}
		if sc.values || before != nil || w.pe == nil || atomicValued(orig) || inner != nil && w.pe == inner {
			continue
		}
		if w.pe != orig || !plainSplitPath(w.path) {
			return true, w.refuse("function " + x.Name.Local + "() of a path whose matches may nest")
		}
	}
	return true, true
}

// plainSplitPath reports whether pp is the context item, or child steps
// with no tail, set operator, map or skip.
func plainSplitPath(pp splitPathParts) bool {
	if pp.ctxItem {
		return true
	}
	if len(pp.tail) > 0 || pp.br != nil || pp.mapR != nil || pp.skip > 0 || pp.oi != "" || pp.group {
		return false
	}
	for _, st := range pp.steps {
		if st.Axis != xpath.AxisChild {
			return false
		}
	}
	return len(pp.steps) > 0
}
