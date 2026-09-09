package xslt

// The XTSE3430 check driven by the §19.8 streamability analysis.
//
// §19.1 says a processor that does not stream "is not required to assess
// whether constructs are guaranteed-streamable", so nothing here is forced.
// What forces it is the streaming feature: a processor claiming that feature
// must reject a stylesheet whose streamable construct is not
// guaranteed-streamable, and the W3C suite tests exactly that.
//
// The check is deliberately narrow, because the cost of the two possible
// mistakes is wildly asymmetric. Missing an XTSE3430 leaves a test failing.
// Raising a spurious one rejects a stylesheet that is perfectly valid, and
// does so at compile time, where the user has no way around it. So the rule
// followed throughout is: report an error only when the analysis positively
// derived roaming from constructs it fully models. The moment it meets
// anything it does not model -- a stylesheet function with a streamability
// category, a union expression, a map constructor, an XSLT instruction whose
// §19.8.6 rules are not written -- it abandons the whole enclosing construct
// and reports nothing.

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// checkStreamability raises XTSE3430 where the §19.8 analysis shows that a
// construct inside a streamable context is not guaranteed-streamable.
//
// Only xsl:source-document and xsl:stream with streamable="yes" are examined.
// A streamable mode declared with xsl:mode is not: the constructs reached
// through it are the bodies of template rules, whose context posture is
// striding by §19.6, but reaching them means following apply-templates
// through modes, and an error reported against the wrong template would be
// worse than none.
func checkStreamability(root *xdm.Node) error {
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		if !isXSL(el, "source-document") && !isXSL(el, "stream") {
			return true
		}
		// xsl:stream is always streamable; xsl:source-document only when it
		// says so. §19.6 gives both a striding context posture.
		if isXSL(el, "source-document") && !isYes(el.AttrValue("streamable")) {
			return true
		}
		if e := checkStreamableBody(el); e != nil {
			err = e
			return false
		}
		return true
	})
	return err
}

// checkStreamableBody examines the sequence constructor inside a streamable
// container, whose context posture is striding.
func checkStreamableBody(container *xdm.Node) error {
	var err error
	walkElements(container, func(el *xdm.Node) bool {
		if err != nil || el == container {
			return err == nil
		}
		// Only the instructions whose select expression is evaluated with
		// the container's own focus are examined. An instruction that
		// changes the focus -- xsl:for-each, xsl:iterate, xsl:for-each-group,
		// xsl:apply-templates -- gives its body a context posture derived
		// from its own select, and §19.8.6 has separate rules for each. None
		// of those rules is implemented, so descending past one of these
		// would assess an inner expression against the wrong context
		// posture, and could report an error that the real rules do not.
		if changesFocus(el) {
			return false
		}
		src, ok := streamableSelect(el)
		if !ok {
			return true
		}
		ns := newNSResolver(el, xpathDefaultNamespace(el))
		expr, perr := xpath.ParseVersion(src, ns, xpathVersionAt(el))
		if perr != nil {
			// A malformed expression is some other check's error to report.
			return true
		}
		// §19.6: the context posture inside xsl:stream or a streamable
		// xsl:source-document is striding.
		p, known := analyzeExpr(expr, postureStriding)
		if known && !p.streamable() {
			err = fmt.Errorf(
				"select expression %q in a streamable %s is %v and %v, "+
					"so it is not guaranteed-streamable (XTSE3430)",
				src, container.Name.Local, p.posture, p.sweep)
			return false
		}
		return true
	})
	return err
}

// streamableSelect returns the select expression of an instruction whose
// select is evaluated with the enclosing focus, and whether el is such an
// instruction.
//
// The list is confined to the instructions whose §19.8.6 operand roles are
// unambiguous and whose select operand is assessed against the container's own
// context posture: xsl:sequence and xsl:copy-of transmit or absorb their
// select, and xsl:value-of absorbs it. Each of the three is roaming exactly
// when its select expression is, so no instruction-level rule beyond that is
// needed for them.
func streamableSelect(el *xdm.Node) (string, bool) {
	if el.Name.URI != xdm.NSXSL {
		return "", false
	}
	switch el.Name.Local {
	case "sequence", "copy-of", "value-of":
		a := el.Attr("", "select")
		if a == nil {
			return "", false
		}
		return a.Value, true
	}
	return "", false
}

// changesFocus reports whether the instruction gives its body a context item
// other than its parent's, so that the analysis must not descend into it.
func changesFocus(el *xdm.Node) bool {
	if el.Name.URI != xdm.NSXSL {
		return false
	}
	switch el.Name.Local {
	case "for-each", "for-each-group", "iterate", "apply-templates",
		"analyze-string", "merge", "fork", "stream", "source-document",
		"evaluate", "function", "template", "on-completion", "try",
		"perform-sort", "next-iteration", "catch":
		return true
	}
	return false
}

// walkElements calls visit for el and each element below it, depth first.
// visit returns false to stop the analysis descending past that element.
func walkElements(el *xdm.Node, visit func(*xdm.Node) bool) {
	if !visit(el) {
		return
	}
	for _, c := range el.ChildElements() {
		walkElements(c, visit)
	}
}
