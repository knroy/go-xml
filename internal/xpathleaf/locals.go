package xpathleaf

import "github.com/knroy/go-xml/v2/xdm"

// Locals is a host's stack of local variable bindings, for a host whose
// local scopes nest strictly: XSLT's variables and parameters, each in scope
// from its declaration to the end of its sequence constructor. Binding one is
// an append and leaving its scope a truncation, where an xpath.Context.WithVar
// per binding copies the scope and allocates a node every time.
//
// One context scope (WithLocals) makes the whole stack visible to lookups,
// which search it from the top down to Base. A host raises Base on entering a
// template or function body, which sees its own locals and the globals but
// not its caller's. The stack is written in place, so a value that outlives
// the scopes it reads, such as an inline function, captures a copy (Frozen).
// It is used by the one goroutine running the evaluation, and by no other:
// while that goroutine is in host code, or once the evaluation has ended, it
// is off (Suspend, Close), and whatever else runs against the host binds its
// locals elsewhere.
type Locals struct {
	names []localName
	vals  []xdm.Sequence
	base  int
	off   bool
}

// LocalsHost is implemented by a Host.Runtime that binds on a Locals.
type LocalsHost interface{ Locals() *Locals }

// Off reports whether the stack is suspended or closed: the goroutine
// running now must not bind on it.
func (l *Locals) Off() bool { return l.off }

// Suspend turns the stack off and hides every binding on it from lookups,
// until the returned resume. It does nothing to a nil or an off stack.
func (l *Locals) Suspend() (resume func()) {
	if l == nil || l.off {
		return func() {}
	}
	base := l.base
	l.base, l.off = len(l.names), true
	return func() { l.base, l.off = base, false }
}

// Close empties the stack and turns it off for good: the evaluation using it
// has ended.
func (l *Locals) Close() {
	l.Truncate(0)
	l.base, l.off = 0, true
}

type localName struct{ uri, local string }

// Push binds name to val above every binding already on the stack.
func (l *Locals) Push(name xdm.QName, val xdm.Sequence) {
	l.names = append(l.names, localName{name.URI, name.Local})
	l.vals = append(l.vals, val)
}

// Len is the number of bindings on the stack, visible or not.
func (l *Locals) Len() int { return len(l.names) }

// Truncate drops the bindings above the first n.
func (l *Locals) Truncate(n int) {
	if n >= len(l.names) {
		return
	}
	clear(l.vals[n:]) // let the collector have the values
	l.names, l.vals = l.names[:n], l.vals[:n]
}

// SetBase hides the bindings below n from lookups and returns the old base.
func (l *Locals) SetBase(n int) int {
	old := l.base
	l.base = n
	return old
}

// Detach takes the bindings above the first n off the stack, for Reattach to
// put back.
func (l *Locals) Detach(n int) *Locals {
	d := &Locals{names: append([]localName(nil), l.names[n:]...),
		vals: append([]xdm.Sequence(nil), l.vals[n:]...)}
	l.Truncate(n)
	return d
}

// Reattach pushes back what Detach took off.
func (l *Locals) Reattach(d *Locals) {
	l.names = append(l.names, d.names...)
	l.vals = append(l.vals, d.vals...)
}

// Lookup finds the topmost visible binding of the name.
func (l *Locals) Lookup(uri, local string) (xdm.Sequence, bool) {
	for i := len(l.names) - 1; i >= l.base; i-- {
		if n := l.names[i]; n.local == local && n.uri == uri {
			return l.vals[i], true
		}
	}
	return nil, false
}

// Frozen is a copy of the visible bindings that later pushes and truncations
// do not change.
func (l *Locals) Frozen() *Locals {
	return &Locals{names: append([]localName(nil), l.names[l.base:]...),
		vals: append([]xdm.Sequence(nil), l.vals[l.base:]...)}
}

// Package xpath sets these in its init; ctx and base are *xpath.Context.
// WithLocals returns a child scope of ctx resolving names through l.
// WithBindingsOf returns a copy of ctx whose variable bindings are base's.
var (
	WithLocals     func(ctx any, l *Locals) any
	WithBindingsOf func(ctx, base any) any
)
