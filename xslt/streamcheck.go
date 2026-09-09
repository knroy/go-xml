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
// category, a union expression, an instruction needing type-determined usage
// -- it abandons the whole enclosing construct and reports nothing.
//
// Two checks run here. checkStreamability assesses the body of every
// streamable container as one sequence constructor under the §19.8.4
// instruction rules (streaminstructions.go), which is the general case;
// checkStreamableBody is the older per-expression walk, still useful where an
// unmodelled instruction withholds the whole-body verdict. Separately,
// checkDeclaredStreamableAttributeSets enforces the §19.8.6 requirement that a
// set declaring streamable="yes" really is streamable.

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
// worse than none. A streamable mode's rules are still reached for their MATCH
// PATTERNS, which need no such following: see checkStreamableModePatterns in
// streamcontext.go.
func checkStreamability(root *xdm.Node) error {
	var err error
	// §19.8.5: the stylesheet's own xsl:function declarations, so that a call
	// on one can be assessed instead of abandoning the enclosing construct,
	// and so that a declared-streamable function's own body can be checked.
	funcs := collectStreamFuncs(root)
	if err := checkStreamableFunctions(root, funcs); err != nil {
		return err
	}
	sets := attributeSetDeclarations(root)
	// §19.8.6: "a streaming processor is required to check that an
	// attribute set containing such a declaration does in fact satisfy the
	// streamability rules". This is checked against the declaration itself,
	// not against each use of it, so it runs whether or not the set is
	// referenced from a streamable construct.
	if e := checkDeclaredStreamableAttributeSets(root, sets); e != nil {
		return e
	}
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
		// §19.8.4 assesses the whole body as one sequence constructor. When
		// every instruction in it is modelled, its verdict subsumes the
		// per-expression scan below, and it catches what that scan cannot:
		// an instruction that is streamable expression by expression but
		// whose operands together read the stream twice.
		if p, known := analyzeSequenceConstructor(el, postureStriding, sets); known && !p.streamable() {
			err = fmt.Errorf(
				"the body of a streamable %s is %v and %v, "+
					"so it is not guaranteed-streamable (XTSE3430)",
				el.Name.Local, p.posture, p.sweep)
			return false
		}
		if e := checkStreamableBody(el, funcs); e != nil {
			err = e
			return false
		}
		return true
	})
	return err
}

// checkDeclaredStreamableAttributeSets raises XTSE3430 for an
// xsl:attribute-set that declares streamable="yes" but whose content is not
// guaranteed-streamable (§19.8.6).
//
// The declaration is what is checked, so an unused set is checked too: the
// point of the declaration is that other packages may rely on it.
func checkDeclaredStreamableAttributeSets(root *xdm.Node, sets map[xdm.QName][]*xdm.Node) error {
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		if !isXSL(el, "attribute-set") || !isYes(el.AttrValue("streamable")) {
			return true
		}
		// A streamable set that uses a set which is not streamable breaks
		// XTSE0730, the more specific error, and the compiler raises it
		// later. Reporting XTSE3430 here instead would mask it, so this
		// check stands aside.
		if usesNonStreamableSet(el, sets) {
			return true
		}
		p, known := analyzeAttributeSet(el, sets)
		if known && !p.streamable() {
			err = fmt.Errorf(
				"xsl:attribute-set %q declares streamable=\"yes\" but is %v and %v, "+
					"so it is not guaranteed-streamable (XTSE3430)",
				el.AttrValue("name"), p.posture, p.sweep)
			return false
		}
		return true
	})
	return err
}

// usesNonStreamableSet reports whether a declared-streamable attribute set
// names, in its own use-attribute-sets attribute, a set that is not itself
// declared streamable. That is XTSE0730, which the compiler raises with a
// message naming both sets.
func usesNonStreamableSet(decl *xdm.Node, sets map[xdm.QName][]*xdm.Node) bool {
	for _, n := range attributeSetNames(decl) {
		used, ok := sets[n]
		if !ok {
			continue
		}
		for _, u := range used {
			if !isYes(u.AttrValue("streamable")) {
				return true
			}
		}
	}
	return false
}

// attributeSetDeclarations collects the xsl:attribute-set declarations of the
// stylesheet by name, for the §19.8.6 operand of use-attribute-sets. Several
// declarations may share a name, and §19.8.6 treats the attribute set as the
// union of all of them.
func attributeSetDeclarations(root *xdm.Node) map[xdm.QName][]*xdm.Node {
	sets := map[xdm.QName][]*xdm.Node{}
	walkElements(root, func(el *xdm.Node) bool {
		if !isXSL(el, "attribute-set") {
			return true
		}
		name := el.AttrValue("name")
		if name == "" {
			return true
		}
		qn, e := resolveQNameAttr(el, name)
		if e != nil {
			return true
		}
		sets[qn] = append(sets[qn], el)
		return true
	})
	return sets
}

// checkStreamableBody examines the sequence constructor inside a streamable
// container, whose context posture is striding.
func checkStreamableBody(container *xdm.Node, funcs map[funcKey]*streamFunc) error {
	var err error
	walkElements(container, func(el *xdm.Node) bool {
		if err != nil || el == container {
			return err == nil
		}
		// Only the instructions whose select expression is evaluated with
		// the container's own focus are examined. An instruction that
		// changes the focus -- xsl:for-each, xsl:iterate, xsl:for-each-group,
		// xsl:apply-templates -- gives its body a context posture derived
		// from its own select, and §19.8.4 has separate rules for each.
		//
		// This walk predates those rules and is now the fallback: the
		// whole-body check above assesses the same constructs correctly,
		// with the right context posture, whenever every instruction in the
		// body is modelled. What is left for this walk is the body that
		// contains an unmodelled instruction somewhere -- there the
		// whole-body verdict is withheld, but a select expression evaluated
		// with the container's own focus can still be judged on its own.
		// Descending past a focus-changing instruction here would assess an
		// inner expression against the wrong context posture.
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
		p, known := analyzeExprFuncs(expr, postureStriding, funcs)
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
// The list is confined to the instructions whose §19.8.4 operand roles are
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
