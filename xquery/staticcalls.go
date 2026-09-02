package xquery

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// checkStaticCalls reports XPST0017 for a function a declaration names and
// nothing declares.
//
// XPST0017 is a static error, and this engine resolves function names at
// evaluation time — which is what lets a query's own "declare function" be
// visible to a call written above it, with no separate binding pass. The cost
// is that a name that exists nowhere is only reported when the call is
// actually evaluated, and a declared function nobody calls never is. So
// K2-FunctionProlog-38 declares local:foo, whose body calls a function that
// does not exist, and asserts XPST0017 for a query whose body is the literal
// 1: the declaration is never invoked, and the error is still the answer.
//
// Only the *declarations* are walked, not the query body. The body is
// evaluated in full by every run that gets that far, so a bad call in it is
// already reported — with the same code, from the same library — and walking
// it here would only move where the message came from. The declarations are
// the part a run may legitimately never touch.
func (q *Query) checkStaticCalls() error {
	seen := map[string]bool{}
	var calls []xpath.StaticCall
	add := func(e *compiledExpr) {
		if e == nil || e.compiled == nil {
			return
		}
		calls = append(calls, e.compiled.StaticCalls()...)
	}
	for _, d := range q.funcs {
		add(d.expr)
		for _, n := range d.body {
			collectStaticCalls(n, add)
		}
	}
	for _, d := range q.vars {
		add(d.init)
		for _, n := range d.body {
			collectStaticCalls(n, add)
		}
	}
	for _, c := range calls {
		key := fmt.Sprintf("%s#%d", c.Name.Clark(), c.Arity)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := q.lib.Lookup(c.Name, c.Arity); ok {
			continue
		}
		// A name outside the namespaces whose contents the specification
		// fixes cannot be judged here: it may be an extension function the
		// host registers on the evaluation context, which this has not seen.
		// Resolution stays at evaluation time for those, where the library
		// actually in force is the one consulted.
		if !closedFunctionNamespace(c.Name.URI) {
			continue
		}
		kind := "calls"
		if c.Ref {
			kind = "references"
		}
		return fmt.Errorf("XPST0017: a declaration %s %s#%d, which is not declared",
			kind, c.Name.Lexical(), c.Arity)
	}
	return nil
}

// closedFunctionNamespace reports whether the specification fixes what a
// namespace contains, so that a name it does not hold is certainly an error.
//
// The local-functions namespace is one of them, and is the reason this check
// earns its place: everything in it is declared by the query itself, so a
// local: call the prolog does not answer can never be answered by anything
// else. XQST0045 already forbids the query declaring anything in the four
// specification namespaces, so those are fixed for the opposite reason.
func closedFunctionNamespace(uri string) bool {
	switch uri {
	case xdm.NSFN, xdm.NSMap, xdm.NSArray, xdm.NSXS,
		"http://www.w3.org/2005/xpath-functions/math",
		"http://www.w3.org/2005/xquery-local-functions":
		return true
	}
	return false
}

// collectStaticCalls hands every compiled expression beneath a constructor to
// add, so that a call written inside one is seen.
//
// It mirrors collectNodeCalls, which answers the same question lexically for
// the dependency graph. Lexical is right there — an over-approximation only
// makes the graph larger than it needs to be — and wrong here, where a name
// that is not really a call would be reported as an error.
func collectStaticCalls(n node, add func(*compiledExpr)) {
	each := func(exprs []*compiledExpr, kids ...[]node) {
		for _, e := range exprs {
			add(e)
			if e != nil {
				for _, op := range e.ops {
					collectStaticCalls(op, add)
				}
			}
		}
		for _, ks := range kids {
			for _, k := range ks {
				collectStaticCalls(k, add)
			}
		}
	}
	switch v := n.(type) {
	case *enclosed:
		each([]*compiledExpr{v.expr}, v.items)
	case *element:
		exprs := []*compiledExpr{v.nameExpr}
		kids := [][]node{v.content}
		for i := range v.attrs {
			exprs = append(exprs, v.attrs[i].nameExpr)
			kids = append(kids, v.attrs[i].value)
		}
		each(exprs, kids...)
	case *comment:
		each(nil, v.content)
	case *pi:
		each([]*compiledExpr{v.targetExpr}, v.content)
	case *textNode:
		each(nil, v.content)
	case *document:
		each(nil, v.content)
	case *inlineFunc:
		each([]*compiledExpr{v.expr}, v.body)
	}
}
