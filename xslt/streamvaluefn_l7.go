package xslt

// Calls given only values, whatever their body (lane L7; the j:escape,
// j:hex4, j:indent of use-package-150's package).
//
// A stylesheet function whose every parameter is declared with an atomic
// type (§10.3.3 atomizes each argument) is given values only, and its body
// has no focus (§10.3: the focus is absent in a function body), so nothing
// it does can read the stream, whatever instructions it holds: the call
// reads of the stream what the atomization of its arguments reads, as K1's
// "values" calls do for one-xsl:sequence bodies. Such a call is marked
// values and runs as itself, through its compiled code (never inlined:
// rewriteCalls inlines one-xsl:sequence bodies only), so its names bind in
// the package it is declared in.
//
// What the body may not hold, to keep the run's observable order and
// state the tree's: an instruction with an effect outside its value
// (xsl:message, xsl:result-document), one that reads a document or
// evaluates code (xsl:source-document, xsl:merge, xsl:evaluate), template
// dispatch (whose rules are not judged here), and in any attribute a call
// on accumulator-before/-after, current-group & co, or a document loader.

import (
	"regexp"

	"github.com/knroy/go-xml/v2/xdm"
)

var impureCallRE = regexp.MustCompile(`\b(accumulator-before|accumulator-after|current-group|current-grouping-key|current-merge-group|current-merge-key|doc|document|collection|unparsed-text|unparsed-text-lines|json-doc|transform|function-lookup|load-xquery-module|trace)\s*[(#]`)

// valuesOnlyCall is judgeSheetCall's mark for f when every parameter is
// atomic (there is at least one) and its body is pure.
func valuesOnlyCall(f *streamFunc) (sheetCall, bool) {
	if f == nil || f.arity() == 0 || !atomicParams(f.body) || !pureBody(f.body) {
		return sheetCall{}, false
	}
	return sheetCall{values: true, atomic: atomicAs(f.body, f.asType)}, true
}

// pureBody reports whether the xsl:function fn holds none of what
// valuesOnlyCall excludes.
func pureBody(fn *xdm.Node) bool {
	ok := true
	walkElements(fn, func(el *xdm.Node) bool {
		if el.Name().URI == xdm.NSXSL {
			switch el.Name().Local {
			case "message", "result-document", "source-document", "merge", "evaluate",
				"apply-templates", "apply-imports", "next-match", "call-template", "assert":
				ok = false
			}
		}
		for a := range el.Attrs() {
			if impureCallRE.MatchString(a.Value()) {
				ok = false
			}
		}
		return ok
	})
	return ok
}
