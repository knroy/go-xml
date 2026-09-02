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
	// inspect, not eval: this reads the calls the compiler recorded and never
	// runs the expression, so the ops/typed/check machinery an evaluation
	// would have to apply is beside the point here. It answers nil for an
	// expression xpath never compiled — a constructor or a nested FLWOR —
	// whose calls the node walker below finds instead.
	add := func(e *compiledExpr) {
		c := e.inspect()
		if c == nil {
			return
		}
		calls = append(calls, c.StaticCalls()...)
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
					collectStaticCalls(op.n, add)
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

// checkBodyVars reports XPST0008 for a variable a declared function's body
// names and nothing binds.
//
// §4.15 gives a function body a static context that holds its own parameters
// and the module's global variables, and nothing else. In particular it does
// not hold another function's parameters — K-FunctionProlog-37 declares
// local:MyFunction($myArg) and then names $myArg inside local:MyFunction2,
// which is an error even though the name is a parameter of a function three
// lines up. A global variable *is* in scope throughout the module in either
// textual direction, so a body may name one declared below it.
//
// Variables resolve at evaluation time here, so the reference was previously
// found only when the function was actually called, and both of those cases
// declare a function nothing calls. The names come from
// xpath.Compiled.FreeVariables, which subtracts what the expression itself
// binds; a lexical scan cannot, and would report the $b of "let $b := $a
// return $b" as unbound.
//
// It runs per evaluation rather than at compile time, and takes the context
// the query will run in. XQuery says a name a function body reads must be a
// parameter or a global the prolog declares, but a host may also bind a
// variable straight onto the evaluation context — which is how the suite's
// environments supply theirs, and how UseCaseSTRING's q4 names $company-data
// that no prolog declares. Those bindings are as real as a declaration and
// only the context knows them, so the check waits for it. Nothing has been
// evaluated by then, which is what the cases need: they declare a function
// that is never called.
//
// Only a body that compiled wholly to an XPath expression is checked. A body
// this package had to parse holds XQuery's own binding forms — every FLWOR
// clause, a typeswitch case, a catch clause's error variables, a window
// clause's four — and the walker beneath cannot see any of them, so a name
// they bind would be reported as free. The cost of missing those bodies is a
// case not caught; the cost of guessing at them is a valid query refused, and
// only the first is acceptable.
func (q *Query) checkBodyVars(ctx *xpath.Context) error {
	global := map[string]bool{}
	for _, d := range q.vars {
		global[d.name.Clark()] = true
	}
	for _, d := range q.funcs {
		// inspect, not eval: the free variables are read off the compiled
		// form, and nothing is run. An expression with lifted operands is
		// skipped rather than inspected, because the lifter's own invented
		// variables would be reported as free.
		body := d.expr.inspect()
		if body == nil || len(d.expr.ops) > 0 {
			continue
		}
		params := map[string]bool{}
		for _, pm := range d.params {
			params[pm.name.Clark()] = true
		}
		for _, name := range body.FreeVariables() {
			k := name.Clark()
			if params[k] || global[k] {
				continue
			}
			if ctx != nil {
				if _, ok := ctx.LookupVar(name); ok {
					continue
				}
			}
			return fmt.Errorf(
				"XPST0008: $%s is not in scope in the body of %s#%d",
				name.Lexical(), d.name.Lexical(), len(d.params))
		}
	}
	return nil
}
