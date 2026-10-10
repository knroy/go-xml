package xslt

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Local variables and parameters are bound on one stack per transform
// (xpathleaf.Locals), the frame design Saxon uses, rather than by a
// Context.WithVar per binding, which copied the runtime and the context and
// allocated a scope node every time. A sequence constructor truncates the
// stack to where it found it when it returns; a template, a stylesheet
// function and a global variable each enter a frame, which hides the locals
// of whatever invoked them. The stack sits above the globals in the context
// chain, so a local shadows a global of its name; XPath's own range variables
// (for, let, quantifiers, inline function parameters) are bound by WithVar
// above it and shadow it in turn.
//
// The stack is written in place. What outlives the scope it was made in
// captures a copy: an inline function does (xpath's frozenLocals), and so
// does a deferred xsl:on-non-empty (execConditionalSequence). Once the
// transform has returned, a function item that escaped it may be called from
// several goroutines, so from then on bindings go back to WithVar.

// localStack is the stack this runtime binds on, or nil once the transform
// has returned.
func (rt *runtime) localStack() *xpathleaf.Locals {
	if rt.transformState == nil || rt.locals == nil || rt.localsDone.Load() {
		return nil
	}
	return rt.locals
}

// bindLocal binds a local variable or parameter for the rest of the current
// sequence constructor.
func (rt *runtime) bindLocal(name xdm.QName, val xdm.Sequence) *runtime {
	st := rt.localStack()
	if st == nil {
		return rt.withVar(name, val)
	}
	st.Push(name, val)
	return rt
}

// localsMark and localsPop bracket a scope: what is bound in between goes out
// of scope at the pop.
func (rt *runtime) localsMark() int {
	if st := rt.localStack(); st != nil {
		return st.Len()
	}
	return 0
}

func (rt *runtime) localsPop(n int) {
	if st := rt.localStack(); st != nil {
		st.Truncate(n)
	}
}

// enterFrame starts the frame of a template, function or global body: the
// locals already bound are hidden from it. leaveFrame(enterFrame()) ends it.
func (rt *runtime) enterFrame() (n, base int) {
	if st := rt.localStack(); st != nil {
		n = st.Len()
		return n, st.SetBase(n)
	}
	return 0, 0
}

func (rt *runtime) leaveFrame(n, base int) {
	if st := rt.localStack(); st != nil {
		st.Truncate(n)
		st.SetBase(base)
	}
}

// globalBindings returns a copy of rt whose context sees only the globals
// and the locals bound from here on: a stylesheet function's body, which the
// bindings at its call site (XPath range variables included) must not reach.
func (rt *runtime) globalBindings() *runtime {
	if rt.globalCtx == nil {
		return rt
	}
	n := *rt
	n.ctx = xpathleaf.WithBindingsOf(rt.ctx, rt.globalCtx).(*xpath.Context)
	return &n
}

// finishLocals ends the stack's use: the transform has returned.
func (rt *runtime) finishLocals() {
	if st := rt.localStack(); st != nil {
		st.Truncate(0)
		st.SetBase(0)
		rt.localsDone.Store(true)
	}
}
