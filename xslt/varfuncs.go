package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// varFuncRef is one function named in a variable's select expression, recorded
// with enough context to resolve and report it once compilation has finished.
type varFuncRef struct {
	name  xdm.QName
	arity int
	ref   bool
	where string
}

// noteVariableFuncs records the functions a variable's select expression names,
// so that checkVariableFuncs can resolve them later.
//
// XPST0017 is a static error, but this engine resolves function names at
// evaluation time -- which means a name that does not exist is only reported
// when the expression is actually evaluated, and a variable nothing references
// never is. The map tests turn on precisely this: maps-901 declares a variable
// whose select calls map:new(), a spelling removed before the Recommendation,
// and asserts XPST0017 "even though the variable is not referenced".
//
// A variable is the whole of the fix rather than every expression in the
// stylesheet because a variable is the one construct whose expression the
// specification guarantees is analysed: an unmatched template or an untaken
// branch may hold a call this processor has no library for and still be legal
// under fallback, whereas a variable's select is always in scope.
func (c *compiler) noteVariableFuncs(comp *xpath.Compiled, where string) {
	for _, call := range comp.StaticCalls() {
		c.varFuncs = append(c.varFuncs, varFuncRef{
			name: call.Name, arity: call.Arity, ref: call.Ref, where: where,
		})
	}
}

// checkVariableFuncs reports XPST0017 for a function named in a variable's
// select expression that no function library and no xsl:function declares.
//
// It runs after every module has compiled, for the same reason
// checkPatternFuncs does: the xsl:function may be declared below the use, or
// in a module imported afterwards.
func (c *compiler) checkVariableFuncs() error {
	for _, r := range c.varFuncs {
		if r.name.URI == xdm.NSFN && lateBoundFuncNames[r.name.Local] {
			// Bound per transform rather than at compile time; see
			// lateBoundFuncNames.
			continue
		}
		if _, ok := c.sheet.funcs.Lookup(r.name, r.arity); ok {
			continue
		}
		// An extension function in a foreign namespace is not resolvable
		// statically: XSLT 18.1 lets a stylesheet call one it only expects to
		// be there, guarded by function-available(), and reports the failure
		// as the dynamic XTDE1425 if it is actually called. Only the
		// namespaces whose contents this specification fixes can be judged
		// here.
		if !staticallyClosedNamespace(r.name.URI) {
			continue
		}
		kind := "calls"
		if r.ref {
			kind = "references"
		}
		return fmt.Errorf(
			"XPST0017: %s %s %s with %d argument(s), but no function is "+
				"declared with that name and arity",
			r.where, kind, r.name.Lexical(), r.arity)
	}
	return nil
}

// staticallyClosedNamespace reports whether the set of functions in a
// namespace is fixed by a specification, so that a name absent from it cannot
// be supplied by anything else.
//
// Only these may be reported as XPST0017 without evaluating the call. A name
// in any other namespace is an extension function, which a processor is free
// to have and this one may simply lack.
func staticallyClosedNamespace(uri string) bool {
	switch uri {
	case xdm.NSFN, xdm.NSMap, xdm.NSArray, xdm.NSXS,
		"http://www.w3.org/2005/xpath-functions/math",
		// The namespace map functions were given in the drafts before the
		// Recommendation moved them. Nothing may be declared in it either,
		// so a call into it names a function that no longer exists.
		"http://www.w3.org/2011/xpath-functions/map":
		return true
	}
	return false
}

// lateBoundFuncNames are the fn: functions that exist but are absent from the
// library a static check can see, because they are registered per transform.
//
// It is the union of runtimeFuncNames and xsltOnlyFunctions rather than either
// of them. runtimeFuncNames omits the XSLT-defined functions that need no
// transform state -- unparsed-entity-uri, current-output-uri -- which are
// nonetheless registered per transform and so invisible here; and
// xsltOnlyFunctions is appendix G's list, which answers a different question
// and omits the accumulator and merge accessors. Consulting either alone
// reported XPST0017 for a function the stylesheet may perfectly well call.
var lateBoundFuncNames = func() map[string]bool {
	m := map[string]bool{
		"current-output-uri":          true,
		"available-system-properties": true,
		// Registered per transform by registerFormatNumber, which needs the
		// stylesheet's decimal formats. It is not in xsltOnlyFunctions --
		// 10.4.1 does not exclude it from xsl:evaluate, a decimal format
		// being a static declaration rather than dynamic context -- so it has
		// to be named here or a static check sees no fn:format-number at all.
		"format-number": true,
	}
	for n := range runtimeFuncNames {
		m[n] = true
	}
	for n := range xsltOnlyFunctions {
		m[n] = true
	}
	return m
}()

// foldFunctionRefsIntoGlobals adds to each global the variables named by the
// bodies of the functions its initialiser calls.
//
// A dependency reaches a global by three routes, and a lexical scan of the
// declaration sees only the first: its select expression, its sequence
// constructor, and the body of a function it calls. DocBook xslTNG needs all
// three, and the third is the one no amount of scanning the declaration
// itself can find -- $v:nominal-page-width selects f:parse-length(...), whose
// body matches against a $vp:relative-regex declared in another module.
//
// The walk is transitive and bounded by the set of functions, so a recursive
// or mutually recursive pair terminates: a name already visited is not
// followed again.
//
// This runs after every module has compiled, because the xsl:function may be
// declared below the global that calls it or in a module imported afterwards
// -- the same reason checkVariableFuncs runs here.
func (c *compiler) foldFunctionRefsIntoGlobals() {
	if len(c.funcRefs) == 0 {
		return
	}
	for _, g := range c.sheet.globals {
		if g.Select == nil {
			continue
		}
		seen := map[string]bool{}
		for _, ref := range g.bodyRefs {
			seen[ref] = true
		}
		// A function that names this very global is not a dependency of it.
		// param-0301 is the case: $x selects my:func(1), and my:func declares
		// a local $b whose select is $x. Section 9.4 makes that a circularity
		// only if it is evaluated, and here it never is -- the function
		// returns $a + 2 and never reads $b. Recording the self-reference
		// turned a stylesheet the specification requires to work into
		// XPST0008.
		seen[g.Name.Clark()] = true
		// The queue makes the walk transitive: a function whose body calls
		// another contributes that one's references too. visited bounds it,
		// so recursion -- direct or mutual -- terminates.
		visited := map[string]bool{}
		var queue []string
		for _, call := range g.Select.StaticCalls() {
			queue = append(queue, call.Name.Clark())
		}
		for len(queue) > 0 {
			fn := queue[0]
			queue = queue[1:]
			if visited[fn] {
				continue
			}
			visited[fn] = true
			for _, ref := range c.funcRefs[fn] {
				if !seen[ref] {
					seen[ref] = true
					g.bodyRefs = append(g.bodyRefs, ref)
				}
			}
			queue = append(queue, c.funcCalls[fn]...)
		}
	}
}
