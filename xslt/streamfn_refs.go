package xslt

// Lane L2: function items and copies the split reads beside its calls.
//
//   - A named function reference in a burst expression (filter(P, f#1),
//     for-each(tokenize(.), f:test#1)): it captures no focus when its
//     function is not focus-dependent, so burstSafe takes it as it takes a
//     call -- an fn function burstSafe would admit called, or a stylesheet
//     function judged as a call would be (streamsplit_calls.go): values
//     (every parameter atomic: what it is handed is atomized), or burst
//     with no step up (its bodies read of the nodes they are handed only
//     what one match's grounded copy has, which is what a burst
//     expression hands it).
//   - outermost(copy-of(P)) and innermost(copy-of(P)), P child steps: each
//     copy is a tree of its own (§F&O copy-of), so no copy is an ancestor
//     of another and both keep them all, in the order the trees were made
//     (xdm record.go), which is the sequence's: they are copy-of(P).

import (
	goruntime "runtime"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

var sheetRefs sync.Map // weak.Pointer[xpath.NamedFunctionRef] -> sheetCall

// markSheetRefs marks the references in e to stylesheet functions of fns a
// call on which judgeSheetCall marks values, or burst without climbing.
func markSheetRefs(e xpath.Expr, j *sheetJudge) {
	walkExprNodes(e, func(v any) {
		x, ok := v.(*xpath.NamedFunctionRef)
		if !ok || !stylesheetFn(x.Name) || x.Cast != nil {
			return
		}
		probe := &xpath.FuncCall{Name: x.Name, Args: make([]xpath.Expr, x.Arity)}
		if sc, ok := j.judge(probe); ok && (sc.values || sc.burst && !sc.climb && !sc.up) {
			k := weak.Make(x)
			if _, loaded := sheetRefs.Swap(k, sc); !loaded {
				goruntime.AddCleanup(x, func(k weak.Pointer[xpath.NamedFunctionRef]) { sheetRefs.Delete(k) }, k)
			}
		}
	})
}

// namedRefSafe is burstSafe for a named function reference.
func namedRefSafe(x *xpath.NamedFunctionRef) bool {
	switch x.Name.URI {
	case fnNS:
		n := x.Name.Local
		return !burstFnBanned[n] && !focusDependent[keyOf(x.Name, x.Arity)] &&
			n != "position" && n != "copy-of" && n != "snapshot" && n != "current" && n != "has-children"
	case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
		return x.Cast == nil
	}
	if !stylesheetFn(x.Name) || x.Cast != nil {
		return false
	}
	_, ok := sheetRefs.Load(weak.Make(x))
	return ok
}

// sheetRefOf is the mark of x, a reference to a stylesheet function, when
// it has one: what a call on the function would be marked.
func sheetRefOf(x *xpath.NamedFunctionRef) (sheetCall, bool) {
	v, ok := sheetRefs.Load(weak.Make(x))
	if !ok {
		return sheetCall{}, false
	}
	return v.(sheetCall), true
}

// copiedOuterInner is outermost(copy-of(P)) or innermost(copy-of(P)), P a
// path of child steps, as copy-of(P); nil for any other call.
func copiedOuterInner(x *xpath.FuncCall) xpath.Expr {
	if !outerInner(x) {
		return nil
	}
	c, ok := x.Args[0].(*xpath.FuncCall)
	if !ok || c.Name.URI != fnNS || c.Name.Local != "copy-of" || len(c.Args) != 1 {
		return nil
	}
	steps := []xpath.Expr{c.Args[0]}
	if pe, ok := c.Args[0].(*xpath.PathExpr); ok {
		steps = pe.Steps
	}
	if len(steps) == 0 {
		return nil
	}
	for _, s := range steps {
		st, ok := s.(*xpath.Step)
		if !ok || st.Axis != xpath.AxisChild {
			return nil
		}
	}
	return c
}
