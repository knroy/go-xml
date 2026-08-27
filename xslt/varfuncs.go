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
	}
	for n := range runtimeFuncNames {
		m[n] = true
	}
	for n := range xsltOnlyFunctions {
		m[n] = true
	}
	return m
}()
