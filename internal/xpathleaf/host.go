package xpathleaf

import "github.com/knroy/go-xml/v2/xdm"

// StableLibrary is implemented by a host's xpath.FunctionLibrary wrapper whose
// answers depend only on its own comparable fields, the library it wraps and
// Context.StaticHost. Package xpath caches a call site's resolution through
// such a wrapper, keyed on the wrapper's value; a wrapper that does not
// implement it is looked up on every call.
type StableLibrary interface {
	// WrappedLibrary returns the xpath.FunctionLibrary the wrapper consults.
	WrappedLibrary() any
}

// BindHost, set by the host, is consulted when package xpath turns the library
// function name into a function item (a named function reference,
// fn:function-lookup, or a partial application) at ctx, a *xpath.Context. A
// non-nil result maps the context of each later call of the item to the one
// the function runs under, putting back the host state it read at ctx: an XSLT
// function such as key() keeps answering for the transformation the item was
// made in, as it did when each transformation built its own library.
var BindHost func(ctx any, name xdm.QName) func(callCtx any) any

// Host is a host language's dynamic state, carried on an xpath.Context in one
// unexported field and copied with it, as the focus is. XSLT keeps its
// transform runtime and fn:current() here rather than in variable bindings,
// whose every lookup walked the whole binding chain.
//
// A Host on a context is never written: a change makes a new one.
type Host struct {
	// Runtime is the host's own state (XSLT: the transform runtime).
	Runtime any
	// Current is what fn:current() returns, a single item or, cleared, nil.
	// CurrentSet says it is bound at all. A sequence rather than the item, so
	// that current() returns it without allocating.
	Current    xdm.Sequence
	CurrentSet bool
	// Absent is set across a dynamic function call, where fn:current()
	// behaves as if the context item were absent (XSLT XTDE1360).
	Absent bool
	// Unbound holds host-defined bits of context components this context is
	// known to have cleared (XSLT: grouping, merge, regex captures).
	Unbound uint8
}

// Package xpath sets these in its init. ctx is a *xpath.Context; WithHost and
// WithFocusHost return a copy of it, SetHost writes in place and is only for
// a context the caller has just made and not yet shared.
var (
	GetHost       func(ctx any) *Host
	WithHost      func(ctx any, h *Host) any
	SetHost       func(ctx any, h *Host)
	WithFocusHost func(ctx any, item xdm.Item, pos, size int, h *Host) any
)

// StepMemoHost is implemented by a Host.Runtime that gives package xpath
// somewhere to remember step walks over parsed trees for as long as the
// runtime lives (see xpath's stepMemo). StepMemo returns what NewStepMemo
// made for it. A host that implements it promises not to change a parsed
// tree while that runtime evaluates.
type StepMemoHost interface{ StepMemo() any }

// NewStepMemo returns an empty memo for a StepMemoHost. Package xpath sets it
// in its init.
var NewStepMemo func() any
