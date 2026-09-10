package xslt

// The XSLT instruction rules of §19.8.4, the attribute-set rule of §19.8.6,
// and the value-template rule of §19.8.7.
//
// streamability.go classifies XPath expressions; this file classifies the
// XSLT constructs that contain them. Both are written against the same
// lattice (streamlattice.go), and for a good reason: §19.8.4 says of most
// instructions only that they "follow the general streamability rules", and
// then lists the operand roles and their usages. So the whole of the rule for
// xsl:value-of, xsl:element, xsl:comment and two dozen others is a table of
// usages handed to combine(). The instructions that need more than that --
// xsl:for-each, xsl:for-each-group, xsl:iterate, xsl:fork, xsl:map,
// xsl:merge, xsl:stream -- state their own ordered cascades, and each is
// written out below in the order the spec gives, because the order is
// load-bearing: the cascades are "the first of the following that applies".
//
// The same `known` discipline as streamability.go applies throughout, and for
// the same reason. An unmodelled construct sets known=false and yields
// roaming/free-ranging; the caller in streamcheck.go turns a verdict into an
// XTSE3430 only when known is true. That asymmetry is deliberate: a missed
// error leaves a test red, while a spurious one rejects a valid stylesheet at
// compile time, where the user has no way around it.

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// instrAnalyzer assesses XSLT instructions in a given context posture.
type instrAnalyzer struct {
	// ctxPosture is the posture of the context item where the instruction
	// sits (§19.6).
	ctxPosture posture

	// ctxAllowsChildren records whether the context item can have children,
	// which decides whether absorbing "." reads from the stream (§19.8.1).
	ctxAllowsChildren bool

	// known is cleared when anything below is not modelled. Its meaning is
	// exactly that of analyzer.known, and the two are merged wherever an
	// instruction operand is an XPath expression.
	known bool

	// attrSets resolves an attribute-set name to its declarations, for the
	// use-attribute-sets operand of §19.8.6. Nil when the enclosing
	// stylesheet was not scanned, in which case any use-attribute-sets
	// attribute makes the construct unknown rather than streamable.
	attrSets map[xdm.QName][]*xdm.Node

	// currentGroup and groupInScope carry to every expression assessed here
	// the properties §19.8.9.4 gives a call on fn:current-group: those of
	// the select expression of the innermost xsl:for-each-group that both
	// contains the call and is its focus-setting container. forEachGroup
	// sets them on the analyzer it builds for its own body, and they are
	// cleared wherever the focus moves to a different instruction, so that a
	// call reached through such a construct falls to the rule's "otherwise,
	// roaming and free-ranging".
	currentGroup props
	groupInScope bool

	// groupOutOfReach says this construct is nested inside an
	// xsl:for-each-group whose body the walk never assessed, so a call on
	// fn:current-group() within it is withheld rather than judged.
	groupOutOfReach bool

	// funcs indexes the stylesheet's own xsl:function declarations, so that
	// an expression inside an instruction body can assess a call on one
	// under §19.8.5 (callProps) instead of abandoning the whole body.
	//
	// Without it every stylesheet-function call in a streamable instruction
	// reached funcCall's "not modelled" branch, which cleared known for the
	// enclosing construct and suppressed the XTSE3430 that §19.8.5's call
	// rules are there to raise. The function table was already built by
	// checkStreamability for the body check; it simply never reached here.
	funcs map[funcKey]*streamFunc

	// accumAfter describes this instruction's position within its enclosing
	// sequence constructor, for §19.8.9.1's rule on fn:accumulator-after.
	// body() sets it as it walks the members in order; it is the only place
	// that does, because it is the only place that knows the order. The zero
	// value has known false, which leaves a call on fn:accumulator-after
	// unmodelled wherever the enclosing sequence constructor was not walked.
	accumAfter accumAfterState
}

// analyzeInstruction returns the posture and sweep of an XSLT instruction (or
// literal result element) evaluated in the given context posture, and whether
// every construct within it was modelled.
func analyzeInstruction(el *xdm.Node, ctx posture, attrSets map[xdm.QName][]*xdm.Node) (props, bool) {
	a := &instrAnalyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: true,
		known:             true,
		attrSets:          attrSets,
	}
	p := a.instruction(el)
	return p, a.known
}

// analyzeAttributeSet returns the posture and sweep of one xsl:attribute-set
// declaration under §19.8.6, for the check that a declared-streamable set is
// in fact streamable.
func analyzeAttributeSet(decl *xdm.Node, attrSets map[xdm.QName][]*xdm.Node) (props, bool) {
	a := &instrAnalyzer{
		ctxPosture:        postureStriding,
		ctxAllowsChildren: true,
		known:             true,
		attrSets:          attrSets,
	}
	p := a.attributeSetProps([]*xdm.Node{decl})
	return p, a.known
}

// analyzeSequenceConstructor returns the posture and sweep of the sequence
// constructor formed by the children of el.
// funcs indexes the stylesheet's xsl:function declarations so that a call on
// one inside the body is assessed under §19.8.5; a nil map leaves every such
// call unmodelled, which is right only where the declarations are not to hand.
func analyzeSequenceConstructor(
	el *xdm.Node, ctx posture, attrSets map[xdm.QName][]*xdm.Node,
	funcs map[funcKey]*streamFunc,
) (props, bool) {
	a := &instrAnalyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: true,
		known:             true,
		attrSets:          attrSets,
		funcs:             funcs,
		// §19.8.9.4 for a container nested inside an xsl:for-each-group: see
		// enclosingGroupSelect.
	}
	var outerStreamed bool
	a.currentGroup, outerStreamed = enclosingGroupSelect(el, attrSets, funcs)
	a.groupInScope = a.currentGroup != props{}
	// A nested container whose enclosing group is not grounded reads streamed
	// nodes a second time. §19.8.9.4 calls that roaming, but this walk never
	// assessed the outer instruction's body, so the rejection is withheld
	// rather than raised: si-group-051 must run, and si-group-052 -- the same
	// stylesheet with a striding select -- is rejected by the general rules
	// on the path itself, not by this clause.
	a.groupOutOfReach = !a.groupInScope && !outerStreamed && hasForEachGroupAncestor(el)
	p := a.body(el)
	return p, a.known
}

// enclosingGroupSelect gives the properties §19.8.9.4 assigns to a call on
// fn:current-group() inside a streamable container that is itself nested in an
// xsl:for-each-group. The walk starts at the container, so the outer
// instruction's select expression has to be found and assessed here.
//
// Only a grounded select yields an answer. The group of a grounded select is
// already materialised, so reading it inside the nested container costs
// nothing further and the call is grounded and motionless. That is
// si-group-051, whose select is "Order/copy-of()" and which the suite expects
// to run. A non-grounded select leaves the group as streamed nodes that the
// nested container would have to read a second time; §19.8.9.4 makes such a
// call roaming, and si-group-052 -- identical but for a select of "Order" --
// expects exactly that rejection. The zero props signals "no enclosing group",
// which leaves the caller's own roaming answer in place.
//
// The second return value separates the two ways of arriving at those zero
// props. It is true when an enclosing xsl:for-each-group was found AND its
// select was assessed all the way to a posture -- so "not grounded" is a fact
// about the stylesheet -- and false when there is no enclosing group at all,
// or when the outer select is itself unmodelled. Only the second case may
// withhold: the first is si-group-052, whose refusal §19.8.9.4 derives from
// the group being streamed, and which is otherwise silenced by
// groupOutOfReach.
func enclosingGroupSelect(
	el *xdm.Node, attrSets map[xdm.QName][]*xdm.Node, funcs map[funcKey]*streamFunc,
) (props, bool) {
	for n := el.Parent; n != nil; n = n.Parent {
		if !isXSL(n, "for-each-group") {
			continue
		}
		at := n.Attr("", "select")
		if at == nil {
			return props{}, false
		}
		outer := &instrAnalyzer{
			ctxPosture:        postureStriding,
			ctxAllowsChildren: true,
			known:             true,
			attrSets:          attrSets,
			funcs:             funcs,
		}
		sel := outer.exprPropsIn(at.Value, n, postureStriding, true)
		if !outer.known {
			return props{}, false
		}
		if sel.posture != postureGrounded {
			return props{}, true
		}
		return groundedMotionless, false
	}
	return props{}, false
}

// hasForEachGroupAncestor reports whether el is nested inside an
// xsl:for-each-group.
func hasForEachGroupAncestor(el *xdm.Node) bool {
	for n := el.Parent; n != nil; n = n.Parent {
		if isXSL(n, "for-each-group") {
			return true
		}
	}
	return false
}

// unknown records an unmodelled construct.
func (a *instrAnalyzer) unknown() props {
	a.known = false
	return roamingFreeRanging
}

// sub returns an analyzer for a nested construct assessed in a different
// context posture, sharing the known flag through mergeFrom.
func (a *instrAnalyzer) sub(ctx posture, allowsChildren bool) *instrAnalyzer {
	return &instrAnalyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: allowsChildren,
		known:             a.known,
		attrSets:          a.attrSets,
		currentGroup:      a.currentGroup,
		groupInScope:      a.groupInScope,
		groupOutOfReach:   a.groupOutOfReach,
		funcs:             a.funcs,
	}
}

func (a *instrAnalyzer) mergeFrom(b *instrAnalyzer) {
	a.known = a.known && b.known
}

// exprProps assesses an XPath expression in this analyzer's context posture,
// merging its known flag into this one.
func (a *instrAnalyzer) exprProps(src string, el *xdm.Node) props {
	return a.exprPropsIn(src, el, a.ctxPosture, a.ctxAllowsChildren)
}

// exprPropsIn assesses an XPath expression in an explicit context posture.
// §19.8.4 asks for this in several places -- the sort keys of xsl:for-each and
// the group-by of xsl:for-each-group are assessed with a grounded context
// posture regardless of where the instruction sits.
func (a *instrAnalyzer) exprPropsIn(src string, el *xdm.Node, ctx posture, allowsChildren bool) props {
	p, _ := a.exprOperandIn(src, el, ctx, allowsChildren)
	return p
}

// exprOperandIn is exprPropsIn plus the operand's allowsChildren, which
// §19.8.1 needs to decide whether an absorption usage is downgraded to
// inspection. Getting this wrong is not a rounding error: it is the difference
// between "@code" costing a read of the stream and costing nothing, and so
// between rejecting and accepting a large class of valid stylesheets.
func (a *instrAnalyzer) exprOperandIn(src string, el *xdm.Node, ctx posture, allowsChildren bool) (props, bool) {
	ns := newNSResolver(el, xpathDefaultNamespace(el))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(el))
	if err != nil {
		// A malformed expression is another check's error to report, and
		// this analysis must not turn it into an XTSE3430.
		return a.unknown(), true
	}
	an := &analyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: allowsChildren,
		known:             a.known,
		currentGroup:      a.currentGroup,
		groupInScope:      a.groupInScope,
		groupOutOfReach:   a.groupOutOfReach,
		funcs:             a.funcs,
		// expr is the whole of this attribute's XPath expression, so it is
		// the outermost one §19.8.9.3 asks about.
		currentPosture:        ctx,
		currentAllowsChildren: allowsChildren,
		currentInScope:        true,
		accumAfter:            a.accumAfter,
	}
	p := an.expr(expr)
	kids := an.allowsChildren(expr)
	a.known = a.known && an.known
	return p, kids
}

// avtProps applies §19.8.7 to an attribute value template: the construct's
// operands are the expressions inside the braces, each with usage absorption.
// A pure-literal value template has no operands and so is grounded and
// motionless.
func (a *instrAnalyzer) avtProps(el *xdm.Node, attr string) (props, bool) {
	at := el.Attr("", attr)
	if at == nil {
		return groundedMotionless, false
	}
	return a.avtPropsOf(at.Value, el), true
}

func (a *instrAnalyzer) avtPropsOf(src string, el *xdm.Node) props {
	exprs, ok := avtExpressions(src)
	if !ok {
		// Malformed braces: some other check's error.
		return a.unknown()
	}
	if len(exprs) == 0 {
		return groundedMotionless
	}
	var ops []operand
	for _, e := range exprs {
		// The required type of a value template operand is xs:string, so
		// the operand is atomized; whether that reads the stream depends on
		// whether the expression can deliver a node with children (§19.8.1).
		p, kids := a.exprOperandIn(e, el, a.ctxPosture, a.ctxAllowsChildren)
		ops = append(ops, operand{props: p, usage: usageAbsorption, allowsChildren: kids})
	}
	return combine(ops, false)
}

// avtExpressions extracts the source of each expression inside braces in an
// attribute value template, without compiling any of them. It reports false if
// the braces are malformed.
//
// This mirrors the scanning half of compileAVT, which cannot be reused because
// it compiles as it goes and fails the whole stylesheet on a bad expression.
func avtExpressions(src string) ([]string, bool) {
	var out []string
	for i := 0; i < len(src); {
		switch src[i] {
		case '{':
			if i+1 < len(src) && src[i+1] == '{' {
				i += 2
				continue
			}
			end, err := findAVTClose(src, i+1)
			if err != nil {
				return nil, false
			}
			if s := src[i+1 : end]; len(s) > 0 {
				out = append(out, s)
			}
			i = end + 1
		case '}':
			if i+1 < len(src) && src[i+1] == '}' {
				i += 2
				continue
			}
			return nil, false
		default:
			i++
		}
	}
	return out, true
}

// selectOperand builds an operand from the select attribute of el with the
// given usage, and reports whether the attribute is present.
func (a *instrAnalyzer) selectOperand(el *xdm.Node, u usage) (operand, bool) {
	at := el.Attr("", "select")
	if at == nil {
		return operand{}, false
	}
	p, kids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)
	return operand{props: p, usage: u, allowsChildren: kids}, true
}

// exprOperand builds an operand from an attribute holding an XPath expression,
// carrying the expression's own allowsChildren.
func (a *instrAnalyzer) exprOperand(el *xdm.Node, attr string, u usage) (operand, bool) {
	at := el.Attr("", attr)
	if at == nil {
		return operand{}, false
	}
	p, kids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)
	return operand{props: p, usage: u, allowsChildren: kids}, true
}

// bodyOperand builds an operand from the contained sequence constructor.
func (a *instrAnalyzer) bodyOperand(el *xdm.Node, u usage) operand {
	return operand{
		props:          a.body(el),
		usage:          u,
		allowsChildren: true,
	}
}

// avtOperand builds an absorption operand from an attribute value template,
// omitted when the attribute is absent.
func (a *instrAnalyzer) avtOperands(el *xdm.Node, attrs ...string) []operand {
	var ops []operand
	for _, name := range attrs {
		p, present := a.avtProps(el, name)
		if !present {
			continue
		}
		ops = append(ops, operand{props: p, usage: usageAbsorption, allowsChildren: true})
	}
	return ops
}

// body assesses the sequence constructor formed by the element children of el.
//
// §19.8.1 makes a sequence constructor a construct whose operands are its
// instructions, each with usage transmission: what an instruction returns is
// returned by the constructor, in order.
func (a *instrAnalyzer) body(el *xdm.Node) props {
	var ops []operand
	// §19.8.9.1 rule 8 asks whether ANY enclosing node N of the call has a
	// preceding sibling P, within N's own sequence constructor, whose sweep
	// is consuming. "Enclosing node" is the spec's word for the call's
	// ancestors, so the question is asked at every level at once, and a
	// "yes" found in an outer sequence constructor still holds inside a
	// nested one. That is why the flag is INHERITED here rather than reset:
	// accumulator-058 writes
	//
	//	<xsl:apply-templates/>
	//	<result>...<xsl:value-of select="accumulator-after('w')"/></result>
	//
	// where the consuming preceding sibling belongs to the outer
	// constructor and the call sits inside the literal result element's.
	// The spec's own note calls that shape legal: "it allows any number of
	// calls on accumulator-after to appear in instructions that follow the
	// call on <xsl:apply-templates/>."
	//
	// Members are walked in document order, so within one constructor the
	// answer is simply "has a member already assessed come out consuming".
	// Saving and restoring keeps a nested constructor's own additions from
	// leaking back out to its parent, where the following siblings have not
	// yet been reached.
	saved := a.accumAfter
	defer func() { a.accumAfter = saved }()
	a.accumAfter.known = true
	for _, c := range el.ChildElements() {
		if isSequenceConstructorExcluded(c) {
			continue
		}
		p := a.instruction(c)
		if p.sweep == sweepConsuming || p.sweep == sweepFreeRanging {
			a.accumAfter.precedingConsuming = true
		}
		ops = append(ops, operand{
			props:          p,
			usage:          usageTransmission,
			allowsChildren: true,
		})
	}
	return combine(ops, false)
}

// isSequenceConstructorExcluded reports whether a child element is not part of
// the sequence constructor. xsl:param, xsl:sort, xsl:with-param,
// xsl:on-completion, xsl:catch, xsl:matching-substring and the rest are
// operands of their parent instruction under its own rule, not members of its
// body, and assessing them twice would double-count a consuming operand.
func isSequenceConstructorExcluded(el *xdm.Node) bool {
	if el.Name.URI != xdm.NSXSL {
		return false
	}
	switch el.Name.Local {
	case "param", "sort", "with-param", "on-completion", "catch",
		"matching-substring", "non-matching-substring", "merge-source",
		"merge-key", "merge-action", "when", "otherwise", "map-entry",
		"context-item", "fallback":
		return true
	}
	return false
}

// instruction applies the §19.8.4 rule for a single instruction or literal
// result element.
func (a *instrAnalyzer) instruction(el *xdm.Node) props {
	if el.Name.URI != xdm.NSXSL {
		// §19.8.4.1: a literal result element. Its operands are the
		// contained sequence constructor, the expressions in its attribute
		// value templates, and any attribute sets it names -- all with
		// usage absorption.
		return a.literalResultElement(el)
	}
	switch el.Name.Local {

	// --- Instructions that are simply a table of operand usages. ---

	case "value-of":
		// §19.8.4.38.
		ops := a.avtOperands(el, "separator")
		if o, ok := a.selectOperand(el, usageAbsorption); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageAbsorption))
		}
		return combine(ops, false)

	case "copy-of":
		// §19.8.4.13: the select expression, usage absorption.
		if o, ok := a.selectOperand(el, usageAbsorption); ok {
			return combine([]operand{o}, false)
		}
		return groundedMotionless

	case "sequence":
		// §19.8.4.34: select and body both transmit.
		var ops []operand
		if o, ok := a.selectOperand(el, usageTransmission); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageTransmission))
		}
		return combine(ops, false)

	case "comment":
		// §19.8.4.11.
		return a.absorbingSelectOrBody(el, nil)

	case "document":
		// §19.8.4.14: the contained sequence constructor, usage absorption.
		return combine([]operand{a.bodyOperand(el, usageAbsorption)}, false)

	case "text":
		// §19.8.4.36: no operands.
		return groundedMotionless

	case "processing-instruction":
		// §19.8.4.32.
		return a.absorbingSelectOrBody(el, []string{"name"})

	case "namespace":
		// §19.8.4.27.
		return a.absorbingSelectOrBody(el, []string{"name"})

	case "attribute":
		// §19.8.4.7.
		return a.absorbingSelectOrBody(el, []string{"name", "namespace", "separator"})

	case "message":
		// §19.8.4.26.
		return a.absorbingSelectOrBody(el, []string{"terminate", "error-code"})

	case "assert":
		// §19.8.4.6: test inspects; select, error-code and body absorb.
		ops := a.avtOperands(el, "error-code")
		if o, ok := a.exprOperand(el, "test", usageInspection); ok {
			ops = append(ops, o)
		}
		if o, ok := a.selectOperand(el, usageAbsorption); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageAbsorption))
		}
		return combine(ops, false)

	case "result-document":
		// §19.8.4.33: href and the serialization AVTs absorb, as does the
		// body. The serialization attributes are all AVTs; treating every
		// attribute other than the ones with expression values as an AVT
		// covers them without listing forty names.
		ops := a.avtOperands(el, "href")
		ops = append(ops, a.bodyOperand(el, usageAbsorption))
		return combine(ops, false)

	case "if":
		// §19.8.4.21: test inspects, body transmits.
		var ops []operand
		if o, ok := a.exprOperand(el, "test", usageInspection); ok {
			ops = append(ops, o)
		}
		ops = append(ops, a.bodyOperand(el, usageTransmission))
		return combine(ops, false)

	case "choose":
		// §19.8.4.10: each xsl:when test inspects; the bodies of xsl:when
		// and xsl:otherwise transmit and form a choice operand group.
		var ops []operand
		for _, c := range el.ChildElements() {
			switch {
			case isXSL(c, "when"):
				if at := c.Attr("", "test"); at != nil {
					ops = append(ops, operand{
						props:          a.exprProps(at.Value, c),
						usage:          usageInspection,
						allowsChildren: true,
					})
				}
				o := a.bodyOperand(c, usageTransmission)
				o.choiceGroup = true
				ops = append(ops, o)
			case isXSL(c, "otherwise"):
				o := a.bodyOperand(c, usageTransmission)
				o.choiceGroup = true
				ops = append(ops, o)
			}
		}
		return combine(ops, false)

	case "try":
		// §19.8.4.37: the xsl:try select or body transmits; the xsl:catch
		// children transmit and form a choice operand group among
		// themselves. The try branch is not part of that group -- either
		// the try or a catch may consume, but not both.
		var ops []operand
		if o, ok := a.selectOperand(el, usageTransmission); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageTransmission))
		}
		for _, c := range el.ChildElements() {
			if !isXSL(c, "catch") {
				continue
			}
			var o operand
			if so, ok := a.selectOperand(c, usageTransmission); ok {
				o = so
			} else {
				o = a.bodyOperand(c, usageTransmission)
			}
			o.choiceGroup = true
			ops = append(ops, o)
		}
		return combine(ops, false)

	case "element":
		// §19.8.4.15: name, namespace, on-empty, the attribute sets, and
		// the body -- all absorption.
		ops := a.avtOperands(el, "name", "namespace")
		if o, ok := a.exprOperand(el, "on-empty", usageAbsorption); ok {
			ops = append(ops, o)
		}
		ops = append(ops, a.useAttributeSetOperands(el)...)
		ops = append(ops, a.bodyOperand(el, usageAbsorption))
		return combine(ops, false)

	case "break":
		// §19.8.4.8: the select expression and the contained sequence
		// constructor, both usage transmission.
		var ops []operand
		if o, ok := a.selectOperand(el, usageTransmission); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageTransmission))
		}
		return combine(ops, false)

	case "next-iteration":
		// §19.8.4.28: the xsl:with-param children and nothing else. Their
		// types come from the with-param, or from the corresponding
		// xsl:param on the containing xsl:iterate.
		var targets map[xdm.QName]string
		for p := el.Parent; p != nil && p.Kind == xdm.KindElement; p = p.Parent {
			if isXSL(p, "iterate") {
				targets = declaredParamTypes(p)
				break
			}
		}
		return combine(a.withParamOperands(el, targets), false)

	case "apply-imports", "next-match":
		// §19.8.4.4, which §19.8.4.29 makes the rule for xsl:next-match too:
		// an implicit context item operand (.) with usage absorption, plus
		// the xsl:with-param children.
		ops := []operand{{
			props:          props{a.ctxPosture, sweepMotionless},
			usage:          usageAbsorption,
			allowsChildren: a.ctxAllowsChildren,
		}}
		ops = append(ops, a.withParamOperands(el, nil)...)
		return combine(ops, false)

	case "analyze-string":
		// §19.8.4.3: select absorbs, the regex value template absorbs, and
		// the two substring constructors have usage navigation because they
		// can be evaluated more than once. Their context posture is
		// grounded: §19.8.4.3 says so explicitly, "reflecting the fact that
		// their context item type is xs:string".
		ops := a.avtOperands(el, "regex")
		if o, ok := a.selectOperand(el, usageAbsorption); ok {
			ops = append(ops, o)
		}
		sub := a.sub(postureGrounded, false)
		for _, c := range el.ChildElements() {
			if !isXSL(c, "matching-substring") && !isXSL(c, "non-matching-substring") {
				continue
			}
			ops = append(ops, operand{
				props:          sub.body(c),
				usage:          usageNavigation,
				allowsChildren: true,
			})
		}
		a.mergeFrom(sub)
		return combine(ops, false)

	case "number":
		// §19.8.4.30: value absorbs if present; otherwise select navigates,
		// defaulting to the context item expression when select is absent
		// too. The formatting attributes are value templates with usage
		// absorption. The from and count patterns are higher-order operands
		// with usage inspection, and the spec notes that "neither of these
		// properties affects the outcome", so they contribute nothing.
		ops := a.avtOperands(el, "format", "lang", "letter-value", "ordinal",
			"start-at", "grouping-separator", "grouping-size")
		if o, ok := a.exprOperand(el, "value", usageAbsorption); ok {
			ops = append(ops, o)
		} else if o, ok := a.selectOperand(el, usageNavigation); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, operand{
				props:          props{a.ctxPosture, sweepMotionless},
				usage:          usageNavigation,
				allowsChildren: a.ctxAllowsChildren,
			})
		}
		return combine(ops, false)

	case "copy":
		return a.copyInstruction(el)

	case "variable":
		// §19.8.4.39. With an as attribute both the select expression and
		// the contained sequence constructor take the type-determined usage
		// based on that type; without one the select navigates and the body
		// absorbs. Navigation from a streamed node is free-ranging, which is
		// the point of the rule: a variable cannot be bound to a streamed
		// node.
		selUsage, bodyUsage := usageNavigation, usageAbsorption
		if at := el.Attr("", "as"); at != nil {
			u := instrTypeDeterminedUsage(at.Value)
			selUsage, bodyUsage = u, u
		}
		var ops []operand
		if o, ok := a.selectOperand(el, selUsage); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, bodyUsage))
		}
		return combine(ops, false)

	case "map-entry":
		// §19.8.4.24: the key absorbs, the select navigates, the body
		// navigates. Navigation is what stops a streamed node being stored
		// in a map.
		var ops []operand
		if o, ok := a.exprOperand(el, "key", usageAbsorption); ok {
			ops = append(ops, o)
		}
		if o, ok := a.selectOperand(el, usageNavigation); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageNavigation))
		}
		return combine(ops, false)

	case "perform-sort":
		// §19.8.4.31: the select navigates, because sorting does not
		// preserve order; the xsl:sort AVTs and keys absorb, assessed with
		// a context posture based on the select expression.
		var ops []operand
		selPosture := a.ctxPosture
		if o, ok := a.selectOperand(el, usageNavigation); ok {
			ops = append(ops, o)
			selPosture = o.props.posture
		}
		ops = append(ops, a.sortOperands(el, selPosture)...)
		return combine(ops, false)

	case "fallback":
		// §19.8.4.17: this processor does not perform fallback when the
		// containing instruction is understood, so the instruction is
		// grounded and motionless.
		return groundedMotionless

	case "map":
		return a.mapInstruction(el)

	case "fork":
		return a.forkInstruction(el)

	case "for-each":
		return a.forEach(el)

	case "iterate":
		return a.iterate(el)

	case "for-each-group":
		return a.forEachGroup(el)

	case "merge":
		return a.merge(el)

	case "apply-templates":
		return a.applyTemplates(el)

	case "call-template":
		// §19.8.4.9: unless the called template declares that it is called
		// with no context item, there is an implicit context item operand
		// whose usage is type-determined by the xsl:context-item/@as of the
		// target, defaulting to item()*; plus the xsl:with-param children,
		// whose types come from the with-param or from the target's own
		// xsl:param declarations.
		target := calledTemplate(el)
		if target == nil {
			// The target was not found -- another package, or a name this
			// scan does not resolve. Its declarations decide the usages, so
			// without them there is no verdict.
			return a.unknown()
		}
		var ci *xdm.Node
		for _, c := range target.ChildElements() {
			if isXSL(c, "context-item") {
				ci = c
				break
			}
		}
		var ops []operand
		switch {
		case ci != nil && contextItemAbsent(ci):
			// No context item operand at all.
		case ci != nil:
			ops = append(ops, operand{
				props:          props{a.ctxPosture, sweepMotionless},
				usage:          instrTypeDeterminedUsage(ci.AttrValue("as")),
				allowsChildren: a.ctxAllowsChildren,
			})
		default:
			// No xsl:context-item child: the type defaults to item()*, whose
			// type-determined usage is navigation.
			ops = append(ops, operand{
				props:          props{a.ctxPosture, sweepMotionless},
				usage:          usageNavigation,
				allowsChildren: a.ctxAllowsChildren,
			})
		}
		ops = append(ops, a.withParamOperands(el, declaredParamTypes(target))...)
		return combine(ops, false)

	case "evaluate":
		// §19.8.4.16: xpath absorbs, context-item and with-params navigate,
		// base-uri and schema-aware are value templates with usage
		// absorption, namespace-context is inspected, and the
		// xsl:with-param children take their type-determined usage.
		ops := a.avtOperands(el, "base-uri", "schema-aware")
		if o, ok := a.exprOperand(el, "xpath", usageAbsorption); ok {
			ops = append(ops, o)
		}
		if o, ok := a.exprOperand(el, "context-item", usageNavigation); ok {
			ops = append(ops, o)
		}
		if o, ok := a.exprOperand(el, "with-params", usageNavigation); ok {
			ops = append(ops, o)
		}
		if o, ok := a.exprOperand(el, "namespace-context", usageInspection); ok {
			ops = append(ops, o)
		}
		ops = append(ops, a.withParamOperands(el, nil)...)
		return combine(ops, false)

	case "stream", "source-document":
		// §19.8.4.35. xsl:source-document is the Recommendation's name for
		// the instruction this Last Call draft calls xsl:stream; the test
		// set uses the later name throughout, and the rule is the same one.
		//
		// The document the instruction opens is assessed separately (§18.1);
		// what this rule measures is the effect of the instruction on the
		// construct that contains it. That separation is the whole point of
		// the rule: whatever the contained sequence constructor does to the
		// stream it opens, it does not read the stream of the construct that
		// encloses it, so it is grounded there.
		//
		// The two rejecting clauses concern current-group() and
		// current-merge-group() calls whose owning instruction is an ancestor
		// of the instruction, which the analysis below does not distinguish
		// from any other call on them; both are unmodelled, so a body
		// mentioning either stays unknown rather than being passed as
		// streamable.
		if bodyCallsAny(el, "current-group", "current-merge-group") {
			return a.unknown()
		}
		// Otherwise "the posture is grounded and the sweep is the sweep of
		// the href attribute value template".
		href, _ := a.avtProps(el, "href")
		return props{postureGrounded, href.sweep}

	default:
		// xsl:where-populated, xsl:on-empty, xsl:on-non-empty and the
		// declarations. Each needs a rule not written here; see the coverage
		// note in docs/conformance-gaps.md.
		return a.unknown()
	}
}

// absorbingSelectOrBody is the shape shared by xsl:comment, xsl:attribute,
// xsl:namespace, xsl:message and xsl:processing-instruction: a set of
// attribute value templates, plus either a select expression or a contained
// sequence constructor, every one of them with usage absorption.
func (a *instrAnalyzer) absorbingSelectOrBody(el *xdm.Node, avts []string) props {
	ops := a.avtOperands(el, avts...)
	if o, ok := a.selectOperand(el, usageAbsorption); ok {
		ops = append(ops, o)
	} else {
		ops = append(ops, a.bodyOperand(el, usageAbsorption))
	}
	return combine(ops, false)
}

// literalResultElement applies §19.8.4.1.
func (a *instrAnalyzer) literalResultElement(el *xdm.Node) props {
	var ops []operand
	for _, at := range el.Attrs {
		// xsl:use-attribute-sets is handled below, and is not itself an
		// attribute value template.
		if at.Name.URI == xdm.NSXSL {
			continue
		}
		p := a.avtPropsOf(at.Value, el)
		ops = append(ops, operand{props: p, usage: usageAbsorption, allowsChildren: true})
	}
	ops = append(ops, a.useAttributeSetOperands(el)...)
	ops = append(ops, a.bodyOperand(el, usageAbsorption))
	return combine(ops, false)
}

// useAttributeSetOperands builds the operands for the attribute sets named in
// a use-attribute-sets (or xsl:use-attribute-sets) attribute, per §19.8.4.1,
// §19.8.4.12 and §19.8.4.15.
//
// §19.8.6 adds the rule that decides three of the test sets: because an
// attribute set can be overridden in another package, "the streamability of a
// construct such as an xsl:element instruction containing a
// use-attribute-sets attribute is based on the declared streamability of the
// named attribute sets, as defined by the streamable attribute of the
// xsl:attribute-set element".
//
// So the analysis here does not look at what the attribute set actually does.
// A set declared streamable="yes" is taken at its word -- whether the
// declaration is honest is a separate check against the declaration itself,
// and si-element-903 is the case where it is not. A set not so declared may be
// overridden in another package by one that consumes, so the reference is
// free-ranging whatever this package's declaration happens to contain; that is
// what si-element-901 (streamable="no") and si-element-902 (no streamable
// attribute at all) test.
func (a *instrAnalyzer) useAttributeSetOperands(el *xdm.Node) []operand {
	names := attributeSetNames(el)
	if len(names) == 0 {
		return nil
	}
	if a.attrSets == nil {
		// Without the stylesheet's declarations the reference cannot be
		// assessed, and guessing would risk a spurious rejection.
		a.unknown()
		return []operand{{props: roamingFreeRanging, usage: usageAbsorption, allowsChildren: true}}
	}
	var ops []operand
	for _, n := range names {
		decls, ok := a.attrSets[n]
		if !ok {
			a.unknown()
			ops = append(ops, operand{props: roamingFreeRanging, usage: usageAbsorption, allowsChildren: true})
			continue
		}
		ops = append(ops, operand{
			props:          a.declaredAttributeSetProps(decls),
			usage:          usageAbsorption,
			allowsChildren: true,
		})
	}
	return ops
}

// declaredAttributeSetProps gives the properties a *reference* to an attribute
// set contributes, which §19.8.6 bases on the set's declared streamability
// rather than on its content.
//
// Every declaration making up the set must say streamable="yes"; one that does
// not could be overridden elsewhere by a consuming definition, so the
// reference cannot be guaranteed streamable. A set that is declared streamable
// is motionless here, and the honesty of that declaration is checked
// separately by attributeSetProps against the declaration itself.
func (a *instrAnalyzer) declaredAttributeSetProps(decls []*xdm.Node) props {
	for _, d := range decls {
		if !isYes(d.AttrValue("streamable")) {
			return roamingFreeRanging
		}
	}
	return groundedMotionless
}

// attributeSetProps applies §19.8.6 to the declarations making up one
// attribute set: the xsl:attribute children of every declaration, and the
// attribute sets they in turn reference, all with usage transmission.
//
// The context posture is striding throughout: an attribute set is used where
// a result element is being constructed, and the rule is applied to the
// declaration, not to the point of use.
func (a *instrAnalyzer) attributeSetProps(decls []*xdm.Node) props {
	var ops []operand
	for _, d := range decls {
		sub := a.sub(postureStriding, true)
		for _, c := range d.ChildElements() {
			if !isXSL(c, "attribute") {
				continue
			}
			ops = append(ops, operand{
				props:          sub.instruction(c),
				usage:          usageTransmission,
				allowsChildren: true,
			})
		}
		ops = append(ops, sub.useAttributeSetOperands(d)...)
		a.mergeFrom(sub)
	}
	return combine(ops, false)
}

// attributeSetNames returns the names in a use-attribute-sets attribute,
// spelled without a prefix on xsl:element and xsl:copy and as
// xsl:use-attribute-sets on a literal result element.
func attributeSetNames(el *xdm.Node) []xdm.QName {
	raw := ""
	if el.Name.URI == xdm.NSXSL {
		raw = el.AttrValue("use-attribute-sets")
	} else {
		if at := el.Attr(xdm.NSXSL, "use-attribute-sets"); at != nil {
			raw = at.Value
		}
	}
	if raw == "" {
		return nil
	}
	var out []xdm.QName
	for _, tok := range strings.Fields(raw) {
		n, err := resolveQNameAttr(el, tok)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// contextItemAbsent reports whether an xsl:context-item declaration says the
// template is called with no context item, so that §19.8.4.9 contributes no
// implicit context item operand.
//
// §19.8.4.9 words this as use="prohibited", but no such value exists: the
// grammar in §9.6 gives use? = "required" | "optional" | "absent", and it is
// use="absent" that makes "the global focus (context item, position, and size)
// absent". Both spellings are accepted here, since the prose and the grammar
// plainly mean the same thing and si-call-template-002 uses the grammar's.
func contextItemAbsent(ci *xdm.Node) bool {
	switch strings.TrimSpace(ci.AttrValue("use")) {
	case "absent", "prohibited":
		return true
	}
	return false
}

// calledTemplate finds the named template an xsl:call-template refers to, whose
// xsl:context-item and xsl:param declarations §19.8.4.9 needs.
//
// It returns nil when the template is not in this stylesheet -- declared in
// another package, say. The caller treats that as unmodelled rather than
// assuming a default, because those declarations are what decide the usages.
func calledTemplate(el *xdm.Node) *xdm.Node {
	raw := strings.TrimSpace(el.AttrValue("name"))
	if raw == "" {
		return nil
	}
	want, err := resolveQNameAttr(el, raw)
	if err != nil {
		return nil
	}
	root := documentElementOf(el)
	if root == nil {
		return nil
	}
	var found *xdm.Node
	walkElements(root, func(d *xdm.Node) bool {
		if !isXSL(d, "template") {
			return true
		}
		nm := strings.TrimSpace(d.AttrValue("name"))
		if nm == "" {
			return true
		}
		got, err := resolveQNameAttr(d, nm)
		if err != nil || got != want {
			return true
		}
		found = d
		return false
	})
	return found
}

// bodyCallsAny reports whether any XPath expression held in an attribute of el
// or of a descendant calls one of the named fn: functions.
//
// §19.8.4.35 asks a narrower question than this answers: whether the call's
// nearest containing xsl:for-each-group (or xsl:merge) is an ancestor of the
// xsl:stream. Answering the narrower question needs the grouping instruction
// the call binds to; this answers the broader one, which is a superset, so it
// can only report a call the narrower rule would have ignored. That direction
// is the safe one, because the caller treats a hit as unknown rather than as an
// error.
func bodyCallsAny(el *xdm.Node, names ...string) bool {
	found := false
	walkElements(el, func(d *xdm.Node) bool {
		for _, at := range d.Attrs {
			for _, n := range names {
				if strings.Contains(at.Value, n+"(") {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// withParamOperands builds the operands contributed by the xsl:with-param
// children of el.
//
// Every rule that takes parameters -- §19.8.4.4 (xsl:apply-imports and
// xsl:next-match), §19.8.4.5 (xsl:apply-templates), §19.8.4.9
// (xsl:call-template), §19.8.4.16 (xsl:evaluate) and §19.8.4.28
// (xsl:next-iteration) -- words the operand identically: "the select attribute
// or contained sequence constructor of each xsl:with-param child element, with
// type-determined usage based on the type declared in the
// xsl:with-param/@as attribute, or item()* if absent".
//
// §19.8.4.9 and §19.8.4.28 add that the type is "the xsl:with-param/@as
// attribute, or the xsl:param/@as attribute of the corresponding parameter on
// the target, whichever is more restrictive". targets supplies the target's
// declarations, indexed by parameter name; it is nil for the rules that do not
// have that clause (§19.8.4.4, §19.8.4.5 and §19.8.4.16), where the with-param's
// own type is the whole story.
//
// "More restrictive" is resolved on the resulting usage rather than on the
// types: of the three §19.4 answers, absorption is the one that reads a bounded
// amount and navigation the one that reads without bound, so the more
// restrictive requirement is the one that does not navigate. Taking the
// with-param's type alone would be wrong rather than merely conservative --
// si-call-template-002 passes a streamed PRICE to a parameter the target
// declares as xs:decimal, which atomizes it, and calling that navigation
// rejects a stylesheet the spec requires to run.
func (a *instrAnalyzer) withParamOperands(el *xdm.Node, targets map[xdm.QName]string) []operand {
	var ops []operand
	for _, c := range el.ChildElements() {
		if !isXSL(c, "with-param") {
			continue
		}
		u := instrTypeDeterminedUsage(c.AttrValue("as"))
		if targets != nil && c.Attr("", "as") == nil {
			// No type on the with-param, so the target's declaration is the
			// only one there is.
			if nm := strings.TrimSpace(c.AttrValue("name")); nm != "" {
				if q, err := resolveQNameAttr(c, nm); err == nil {
					if as, ok := targets[q]; ok {
						u = instrTypeDeterminedUsage(as)
					}
				}
			}
		}
		if o, ok := a.selectOperand(c, u); ok {
			ops = append(ops, o)
			continue
		}
		ops = append(ops, a.bodyOperand(c, u))
	}
	return ops
}

// declaredParamTypes indexes the as attributes of the xsl:param children of a
// template or xsl:iterate, for the "whichever is more restrictive" clause of
// §19.8.4.9 and §19.8.4.28. A parameter with no as attribute maps to the empty
// string, which instrTypeDeterminedUsage reads as item()*.
func declaredParamTypes(decl *xdm.Node) map[xdm.QName]string {
	out := map[xdm.QName]string{}
	for _, c := range decl.ChildElements() {
		if !isXSL(c, "param") {
			continue
		}
		nm := strings.TrimSpace(c.AttrValue("name"))
		if nm == "" {
			continue
		}
		q, err := resolveQNameAttr(c, nm)
		if err != nil {
			continue
		}
		out[q] = c.AttrValue("as")
	}
	return out
}

// instrTypeDeterminedUsage applies the §19.4 definition in full: "if the
// required type (ignoring occurrence indicator) is function(*) or a subtype
// thereof, then inspection; if the required type (ignoring occurrence
// indicator) is xs:anyAtomicType or a subtype thereof, then absorption;
// otherwise navigation."
//
// It is the whole of what §19.8.4 needs from §19.4. The definition is a
// function of the required type alone -- no posture, no sweep, no lattice --
// so the six instruction rules that were held to be blocked on "type-determined
// usage the lattice does not model" need no lattice support at all.
//
// The interesting case is the default. A required type of item()*, which
// §19.8.4 substitutes wherever an as attribute is absent, is neither atomic nor
// a function type, so the usage is navigation -- and navigation from a streamed
// node is free-ranging. That is the rule that stops a streamed node being bound
// to a variable, passed as a template parameter, or stored in a map, which is
// what the si-map, si-iterate and si-group cases test.
//
// streamfunctions.go has its own typeDeterminedUsage for the §19.8.5 function
// rules. That one splits two ways rather than three, because a function
// parameter's declared type is checked there against typePermitsNodes; this is
// kept separate rather than merged so that neither file's rule shifts under the
// other's feet.
//
// A type this does not recognise falls to navigation, the same answer as
// item()* and the conservative one: it can only refuse to prove a construct
// streamable, never prove a non-streamable one streamable.
func instrTypeDeterminedUsage(as string) usage {
	s := strings.TrimSpace(as)
	if s == "" {
		// No declared type means item()*, whose usage is navigation.
		return usageNavigation
	}
	if atomicSequenceType(s) {
		return usageAbsorption
	}
	// Strip an occurrence indicator before looking for a function type:
	// "function(*)*" is a function type just as "function(*)" is.
	if n := len(s); n > 0 {
		switch s[n-1] {
		case '?', '*', '+':
			s = strings.TrimSpace(s[:n-1])
		}
	}
	// function(*), and the map and array types, which XPath makes subtypes
	// of function(*). Their operands are inspected rather than absorbed: a
	// function item is opaque, so building one reads nothing from the stream.
	if strings.HasPrefix(s, "function(") ||
		strings.HasPrefix(s, "map(") || strings.HasPrefix(s, "array(") {
		return usageInspection
	}
	return usageNavigation
}

// sortOperands builds the operands contributed by xsl:sort children: their
// attribute value templates, and their sort keys assessed in the given context
// posture, all with usage absorption.
func (a *instrAnalyzer) sortOperands(el *xdm.Node, ctx posture) []operand {
	var ops []operand
	for _, c := range el.ChildElements() {
		if !isXSL(c, "sort") {
			continue
		}
		ops = append(ops, a.avtOperands(c,
			"lang", "order", "collation", "stable", "case-order", "data-type")...)
		sub := a.sub(ctx, true)
		if at := c.Attr("", "select"); at != nil {
			p, kids := sub.exprOperandIn(at.Value, c, ctx, true)
			ops = append(ops, operand{props: p, usage: usageAbsorption, allowsChildren: kids})
		} else {
			ops = append(ops, operand{
				props:          sub.body(c),
				usage:          usageAbsorption,
				allowsChildren: true,
			})
		}
		a.mergeFrom(sub)
	}
	return ops
}

// noteBodyModelled assesses the body of an instruction only for its effect on
// the known flag, discarding the properties.
//
// The cascades of §19.8.4.18, .19 and .22 have clauses that reject an
// instruction without consulting its body -- an xsl:sort child, a group-by
// outside an xsl:fork. Returning roaming from one of those without first
// establishing that the body is modelled would let an unmodelled construct be
// reported as an XTSE3430, which is precisely the mistake the known flag
// exists to prevent: the error would name the stylesheet when the real cause
// is a gap here.
func (a *instrAnalyzer) noteBodyModelled(el *xdm.Node, ctx posture) {
	if ctx == postureRoaming {
		ctx = postureStriding
	}
	sub := a.sub(ctx, true)
	sub.body(el)
	a.mergeFrom(sub)
}

// noteGroupBodyModelled is noteBodyModelled for the rejecting clauses of
// §19.8.4.14, where the body may call fn:current-group(). Without the group
// properties of §19.8.9.4 in scope such a call would be unmodelled, the body
// would clear `known`, and the rejection this establishes the body for would
// then be withheld -- which is exactly what it is being assessed to prevent.
func (a *instrAnalyzer) noteGroupBodyModelled(el *xdm.Node, sel props) {
	ctx := sel.posture
	if ctx == postureRoaming {
		ctx = postureStriding
	}
	sub := a.sub(ctx, true)
	sub.currentGroup, sub.groupInScope, sub.groupOutOfReach = sel, true, false
	sub.body(el)
	a.mergeFrom(sub)
}

func hasChild(el *xdm.Node, local string) bool {
	for _, c := range el.ChildElements() {
		if isXSL(c, local) {
			return true
		}
	}
	return false
}

// copyInstruction applies §19.8.4.12.
//
// The select expression, defaulting to ".", is focus-setting: it names the
// node being copied, and the contained sequence constructor is "assessed with
// context posture and context item type based on the select expression if
// present, or the outer focus otherwise".
//
// That makes the select's relationship to the body the same as an
// xsl:for-each's, and it has to be scored the same way: the select's sweep is
// folded into the instruction's with `wider`, not offered to combine() as a
// second operand. Scoring it as an operand instead makes a nested xsl:copy
// chain roaming -- each level's select is a child step, so each level counts
// as consuming twice -- and si-copy-028 and si-copy-052, which nest four and
// two deep, are both expected to run.
func (a *instrAnalyzer) copyInstruction(el *xdm.Node) props {
	bodyCtx, bodyAllows := a.ctxPosture, a.ctxAllowsChildren
	selSweep := sweepMotionless
	if at := el.Attr("", "select"); at != nil {
		sp, spKids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)
		if !sp.streamable() {
			return roamingFreeRanging
		}
		bodyCtx, bodyAllows = sp.posture, spKids
		selSweep = sp.sweep
	}

	// The remaining operands are the ones that genuinely compete for the
	// stream: on-empty, the attribute sets, and the body.
	var ops []operand
	if o, ok := a.exprOperand(el, "on-empty", usageAbsorption); ok {
		ops = append(ops, o)
	}
	ops = append(ops, a.useAttributeSetOperands(el)...)
	sub := a.sub(bodyCtx, bodyAllows)
	ops = append(ops, operand{
		props:          sub.body(el),
		usage:          usageAbsorption,
		allowsChildren: true,
	})
	a.mergeFrom(sub)

	p := combine(ops, false)
	if !p.streamable() {
		return roamingFreeRanging
	}
	// xsl:copy returns a newly constructed node, so it is grounded whatever
	// it copied; only the sweep carries the cost of the copy.
	return props{postureGrounded, wider(selSweep, p.sweep)}
}

// mapInstruction applies §19.8.4.23.
func (a *instrAnalyzer) mapInstruction(el *xdm.Node) props {
	// "If the sequence constructor consists exclusively of xsl:map-entry
	// instructions (and xsl:fallback, which are ignored)": each entry may
	// then read the stream independently, as an implicit fork.
	onlyEntries := true
	var entries []*xdm.Node
	for _, c := range el.ChildElements() {
		if isXSL(c, "fallback") {
			continue
		}
		if !isXSL(c, "map-entry") {
			onlyEntries = false
			break
		}
		entries = append(entries, c)
	}
	if onlyEntries {
		widest := sweepMotionless
		for _, e := range entries {
			p := a.instruction(e)
			if !p.streamable() {
				return roamingFreeRanging
			}
			widest = wider(widest, p.sweep)
		}
		return props{postureGrounded, widest}
	}
	// Otherwise the posture and sweep are those of the contained sequence
	// constructor -- which, because xsl:map-entry is excluded from a
	// sequence constructor, must be assessed with the entries included.
	var ops []operand
	for _, c := range el.ChildElements() {
		ops = append(ops, operand{
			props:          a.instruction(c),
			usage:          usageTransmission,
			allowsChildren: true,
		})
	}
	return combine(ops, false)
}

// forkInstruction applies §19.8.4.20.
//
// The cascade is short and entirely about grounding: none of the branches of
// an xsl:fork may return streamed nodes, because the fork has to reassemble
// its results in order and streamed nodes cannot be reordered. That single
// constraint is what si-fork-901 violates.
func (a *instrAnalyzer) forkInstruction(el *xdm.Node) props {
	var seqs []*xdm.Node
	var groups []*xdm.Node
	for _, c := range el.ChildElements() {
		switch {
		case isXSL(c, "sequence"):
			seqs = append(seqs, c)
		case isXSL(c, "for-each-group"):
			groups = append(groups, c)
		case isXSL(c, "fallback"):
		default:
			// A fork whose children are neither xsl:sequence nor
			// xsl:for-each-group is a static error under other rules; this
			// analysis has no opinion on it.
			return a.unknown()
		}
	}
	// "If there are no child xsl:sequence instructions, then grounded and
	// motionless" -- but a child xsl:for-each-group takes precedence in the
	// next clause, so the group case is checked first.
	if len(groups) > 0 {
		// "If there is a child xsl:for-each-group instruction, then the
		// posture and the sweep of that instruction."
		return a.instruction(groups[0])
	}
	if len(seqs) == 0 {
		return groundedMotionless
	}
	// What xsl:fork buys is that its branches all see the same single pass,
	// so -- unlike every other construct -- several of them MAY consume. The
	// spec's own streamable example relies on it:
	//
	//   <xsl:fork>
	//     <xsl:sequence select="copy-of(author)"/>
	//     <xsl:sequence select="copy-of(editor)"/>
	//   </xsl:fork>
	//
	// Both branches there are consuming, and both are grounded. What the
	// fork cannot do is reassemble streamed nodes from more than one branch
	// into document order, which is why the companion example, with "author"
	// and "editor" selected directly, is not streamable.
	//
	// So the constraint is on posture, not on sweep: at most one branch may
	// return streamed nodes. The Last Call draft states this as "any
	// non-grounded branch is roaming", but the test set is stricter than the
	// draft only in appearance -- si-fork-A's f-006, captioned "xsl:fork can
	// return streamed nodes if only one branch is consuming", pairs a
	// grounded branch with a striding one and is expected to run:
	//
	//   <xsl:fork>
	//     <xsl:sequence><xsl:attribute name="category" select="@CAT"/></xsl:sequence>
	//     <xsl:sequence select="TITLE"/>
	//   </xsl:fork>
	//
	// si-fork-901 differs from f-006 in exactly one respect: both of ITS
	// branches are non-grounded, and it is expected to raise XTSE3430.
	// Counting the non-grounded branches admits f-006 and the copy-of
	// example while still rejecting si-fork-901.
	widest := sweepMotionless
	streamed := 0
	for _, s := range seqs {
		p := a.instruction(s)
		if !p.streamable() {
			return roamingFreeRanging
		}
		if p.posture != postureGrounded {
			streamed++
		}
		widest = wider(widest, p.sweep)
	}
	if streamed > 1 {
		return roamingFreeRanging
	}
	if streamed == 0 {
		return props{postureGrounded, widest}
	}
	// The one non-grounded branch decides the posture.
	for _, s := range seqs {
		if p := a.instruction(s); p.posture != postureGrounded {
			return props{p.posture, widest}
		}
	}
	return props{postureGrounded, widest}
}

// forEach applies §19.8.4.18, whose clauses are tried in order.
// applyTemplates applies §19.8.4.5, whose clauses are "the first of the
// following that apply" and so are written in the spec's order.
//
// The rule needs to know whether the target mode is declared streamable, which
// is why modes are collected into the analyzer: an apply-templates to a
// non-streamable mode is roaming and free-ranging however simple its select
// expression is.
func (a *instrAnalyzer) applyTemplates(el *xdm.Node) props {
	// "If there is no select attribute, the following analysis assumes the
	// presence of an implicit operand select='child::node()'."
	sel, selKids := props{a.ctxPosture, sweepMotionless}, true
	if at := el.Attr("", "select"); at != nil {
		sel, selKids = a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)
	} else {
		// child::node() from the context item strides, and reading it
		// consumes; from a grounded context it stays grounded.
		if a.ctxPosture == postureStriding {
			sel = props{postureStriding, sweepMotionless}
		}
	}

	// Clause 1: a grounded select expression. The mode is irrelevant here --
	// nothing streamed is passed to it -- so this clause comes first, and
	// <xsl:apply-templates select="copy-of(.)"/> is grounded and consuming
	// exactly as the spec's example says.
	if sel.posture == postureGrounded {
		ops := []operand{{props: sel, usage: usageAbsorption, allowsChildren: selKids}}
		ops = append(ops, a.withParamOperands(el, nil)...)
		ops = append(ops, a.sortOperands(el, postureGrounded)...)
		return combine(ops, false)
	}

	// Clause 2: "If there is an xsl:sort child element, then roaming and
	// free-ranging." Sorting a streamed selection needs it all in memory.
	if hasChild(el, "sort") {
		return roamingFreeRanging
	}

	// Clause 3: a mode that is not declared streamable. mode="#current" is
	// treated as streamable, per the note: if the template was reached with a
	// streamed context item, the current mode must itself be streamable.
	switch a.modeStreamable(el) {
	case modeNotStreamable:
		return roamingFreeRanging
	case modeUnresolved:
		// The declaration is somewhere this scan cannot see -- an imported
		// or included module, which is not inlined when this check runs. The
		// clause cannot be decided, so the instruction is unmodelled rather
		// than rejected: si-apply-imports-068 declares its streamable mode in
		// the module it imports, and rejecting it here would refuse a
		// stylesheet the spec requires to run.
		return a.unknown()
	}

	// Clause 4: a climbing or crawling select expression.
	if sel.posture == postureClimbing || sel.posture == postureCrawling {
		return roamingFreeRanging
	}

	// Clause 5: the general rules, with the select absorbing.
	ops := []operand{{props: sel, usage: usageAbsorption, allowsChildren: selKids}}
	ops = append(ops, a.withParamOperands(el, nil)...)
	return combine(ops, false)
}

// defaultModeFor returns the [xsl:]default-mode in scope for el and the element
// that carried it, per §3.8.2: "the mode is taken from the [xsl:]default-mode
// attribute of the innermost ancestor element that has such an attribute. If
// there is no such element, then the default is the unnamed mode."
//
// The attribute is spelt default-mode on the XSLT elements that allow it and
// xsl:default-mode on a literal result element, so both are looked for.
func defaultModeFor(el *xdm.Node) (string, *xdm.Node) {
	for n := el; n != nil && n.Kind == xdm.KindElement; n = n.Parent {
		if n.Name.URI == xdm.NSXSL {
			if v := strings.TrimSpace(n.AttrValue("default-mode")); v != "" {
				return v, n
			}
		}
		if at := n.Attr(xdm.NSXSL, "default-mode"); at != nil {
			if v := strings.TrimSpace(at.Value); v != "" {
				return v, n
			}
		}
	}
	return "", nil
}

// modeVerdict is the three-way answer §19.8.4.5 clause 3 needs. The third
// value matters: this check runs before xsl:import and xsl:include are
// inlined, so a mode declared in another module is not merely "not declared
// streamable" -- it is not visible at all, and the two must not be confused.
type modeVerdict int

const (
	modeStreamableYes modeVerdict = iota
	modeNotStreamable
	modeUnresolved
)

// modeStreamable reports whether the mode named by an xsl:apply-templates
// instruction is declared streamable (§19.8.4.5 clause 3).
//
// An absent mode attribute names the unnamed mode, and "#current" is treated as
// streamable by the note in that clause.
//
// A mode with no matching xsl:mode declaration in this module answers
// modeNotStreamable only when the module imports and includes nothing: a
// stylesheet that stands alone has no other place for the declaration to be, so
// its absence is decisive. As soon as another module is pulled in, the
// declaration may be there, and the answer is modeUnresolved.
func (a *instrAnalyzer) modeStreamable(el *xdm.Node) modeVerdict {
	raw := strings.TrimSpace(el.AttrValue("mode"))
	if raw == "#current" {
		return modeStreamableYes
	}
	root := documentElementOf(el)
	if root == nil {
		return modeUnresolved
	}
	// §3.8.2: an omitted mode attribute, or "#default", takes the mode from
	// the [xsl:]default-mode of the innermost ancestor that has one, and only
	// falls back to the unnamed mode when there is none. sf-current-100
	// declares default-mode="m" on xsl:stylesheet and relies on a bare
	// xsl:apply-templates reaching the streamable mode m.
	at, on := el, el
	if raw == "" || raw == "#default" {
		raw, on = defaultModeFor(el)
	}
	if on != nil {
		at = on
	}
	want := xdm.QName{}
	if raw != "" && raw != "#default" && raw != "#unnamed" {
		n, err := resolveQNameAttr(at, raw)
		if err != nil {
			return modeUnresolved
		}
		want = n
	}
	declared := false
	streamable := false
	imports := false
	walkElements(root, func(d *xdm.Node) bool {
		if isXSL(d, "import") || isXSL(d, "include") || isXSL(d, "use-package") {
			imports = true
			return true
		}
		if !isXSL(d, "mode") {
			return true
		}
		got := xdm.QName{}
		if nm := strings.TrimSpace(d.AttrValue("name")); nm != "" {
			n, err := resolveQNameAttr(d, nm)
			if err != nil {
				return true
			}
			got = n
		}
		if got != want {
			return true
		}
		declared = true
		if isYes(d.AttrValue("streamable")) {
			streamable = true
			return false
		}
		return true
	})
	switch {
	case streamable:
		return modeStreamableYes
	case declared:
		// Declared here, and not as streamable. That is decisive whatever
		// else the stylesheet imports: a mode may be declared only once.
		return modeNotStreamable
	case imports:
		return modeUnresolved
	}
	return modeNotStreamable
}

// documentElementOf returns the outermost element containing el.
func documentElementOf(el *xdm.Node) *xdm.Node {
	top := el
	for top.Parent != nil && top.Parent.Kind == xdm.KindElement {
		top = top.Parent
	}
	return top
}

func (a *instrAnalyzer) forEach(el *xdm.Node) props {
	at := el.Attr("", "select")
	if at == nil {
		return a.unknown()
	}
	// The body's context item is what the select returns, so whether it can
	// have children is the select expression's own answer -- not "yes".
	// "for-each select='@value'" binds an attribute, whose whole subtree is
	// already in hand, so absorbing "." in the body reads nothing (§19.8.1).
	sel, selKids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)
	hasSort := hasChild(el, "sort")

	// Clause 1: a grounded select. The body is a higher-order operand
	// assessed with a grounded context posture, and the general rules
	// apply. A higher-order consuming operand is roaming (§19.8.1), which
	// is why a grounded select does not make everything streamable.
	if sel.posture == postureGrounded {
		ops := []operand{{props: sel, usage: usageInspection, allowsChildren: true}}
		sub := a.sub(postureGrounded, true)
		bo := operand{
			props:          sub.body(el),
			usage:          usageTransmission,
			allowsChildren: true,
			higherOrder:    true,
		}
		a.mergeFrom(sub)
		ops = append(ops, bo)
		for _, o := range a.sortOperands(el, postureGrounded) {
			o.higherOrder = true
			ops = append(ops, o)
		}
		return combine(ops, false)
	}

	// Clause 2: an xsl:sort child makes the instruction roaming, because
	// sorting cannot be done in a single forward pass.
	if hasSort {
		a.noteBodyModelled(el, sel.posture)
		return roamingFreeRanging
	}

	if !sel.streamable() {
		return roamingFreeRanging
	}

	// The body is assessed with the context posture of the select.
	sub := a.sub(sel.posture, selKids)
	bodyProps := sub.body(el)
	a.mergeFrom(sub)

	// Clause 3: a crawling select with a consuming body. The nodes selected
	// may be nested, so consuming each one's subtree would read the same
	// part of the stream twice.
	if sel.posture == postureCrawling && bodyProps.sweep == sweepConsuming {
		return roamingFreeRanging
	}
	if !bodyProps.streamable() {
		return roamingFreeRanging
	}

	// Otherwise: the posture is the body's, the sweep the wider of the two.
	return props{bodyProps.posture, wider(sel.sweep, bodyProps.sweep)}
}

// iterate applies §19.8.4.22.
func (a *instrAnalyzer) iterate(el *xdm.Node) props {
	at := el.Attr("", "select")
	if at == nil {
		return a.unknown()
	}
	sel, selKids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)

	// Clause 1: a grounded select follows the general rules. xsl:param and
	// xsl:on-completion operands are navigation and transmission
	// respectively; the body transmits.
	if sel.posture == postureGrounded {
		ops := []operand{{props: sel, usage: usageInspection, allowsChildren: true}}
		for _, c := range el.ChildElements() {
			if !isXSL(c, "param") {
				continue
			}
			if o, ok := a.selectOperand(c, usageNavigation); ok {
				ops = append(ops, o)
			} else {
				ops = append(ops, a.bodyOperand(c, usageNavigation))
			}
		}
		sub := a.sub(postureGrounded, true)
		ops = append(ops, operand{
			props:          sub.body(el),
			usage:          usageTransmission,
			allowsChildren: true,
		})
		a.mergeFrom(sub)
		if oc := childNamed(el, "on-completion"); oc != nil {
			// Assessed with a context posture of roaming: referring to the
			// context item inside xsl:on-completion is an error.
			ocSub := a.sub(postureRoaming, true)
			var p props
			if sat := oc.Attr("", "select"); sat != nil {
				p = ocSub.exprPropsIn(sat.Value, oc, postureRoaming, true)
			} else {
				p = ocSub.body(oc)
			}
			a.mergeFrom(ocSub)
			ops = append(ops, operand{props: p, usage: usageTransmission, allowsChildren: true})
		}
		return combine(ops, false)
	}

	if !sel.streamable() {
		return roamingFreeRanging
	}

	// Clause 2: an xsl:param whose initializer is not grounded and
	// motionless makes the instruction roaming -- the parameter is carried
	// across iterations, so it cannot hold a streamed node or read the
	// stream.
	for _, c := range el.ChildElements() {
		if !isXSL(c, "param") {
			continue
		}
		var p props
		if sat := c.Attr("", "select"); sat != nil {
			p = a.exprProps(sat.Value, c)
		} else {
			p = a.body(c)
		}
		if p.posture != postureGrounded || p.sweep != sweepMotionless {
			a.noteBodyModelled(el, sel.posture)
			return roamingFreeRanging
		}
	}

	// Clause 3: the same for xsl:on-completion.
	if oc := childNamed(el, "on-completion"); oc != nil {
		ocSub := a.sub(postureRoaming, true)
		var p props
		if sat := oc.Attr("", "select"); sat != nil {
			p = ocSub.exprPropsIn(sat.Value, oc, postureRoaming, true)
		} else {
			p = ocSub.body(oc)
		}
		a.mergeFrom(ocSub)
		if p.posture != postureGrounded || p.sweep != sweepMotionless {
			return roamingFreeRanging
		}
	}

	sub := a.sub(sel.posture, selKids)
	bodyProps := sub.body(el)
	a.mergeFrom(sub)

	// Clause 4: crawling select with a consuming body.
	if sel.posture == postureCrawling && bodyProps.sweep == sweepConsuming {
		return roamingFreeRanging
	}
	if !bodyProps.streamable() {
		return roamingFreeRanging
	}
	return props{bodyProps.posture, wider(sel.sweep, bodyProps.sweep)}
}

// groupPatternAttr returns the group-starting-with or group-ending-with
// attribute of an xsl:for-each-group, or nil when neither is present. The two
// are mutually exclusive under XTSE1080, so at most one is ever found.
func groupPatternAttr(el *xdm.Node) *xdm.Node {
	if at := el.Attr("", "group-starting-with"); at != nil {
		return at
	}
	return el.Attr("", "group-ending-with")
}

// forEachGroup applies §19.8.4.19.
func (a *instrAnalyzer) forEachGroup(el *xdm.Node) props {
	at := el.Attr("", "select")
	if at == nil {
		return a.unknown()
	}
	sel, selKids := a.exprOperandIn(at.Value, el, a.ctxPosture, a.ctxAllowsChildren)

	groupBy := el.Attr("", "group-by")
	groupAdj := el.Attr("", "group-adjacent")
	hasSort := hasChild(el, "sort")

	// The grouping keys are assessed with a grounded context posture.
	//
	// §19.8.4.19 lists "the group-starting-with or group-ending-with
	// patterns if present" as higher-order operands with usage inspection.
	// §19.8.10 gives a pattern one of two verdicts and no third: motionless
	// (and so grounded), or free-ranging (and so roaming). A motionless
	// pattern is not potentially-consuming and contributes nothing to
	// combine(); a free-ranging one makes the whole construct roaming and
	// free-ranging wherever it appears, since combine()'s first test is
	// decisive and no later cascade clause derives a narrower answer from
	// the select and the body. So the only case worth computing is the
	// free-ranging one, and it is computed once here for every clause
	// rather than only for the grounded one.
	//
	// It is taken only where the select expression is NOT grounded. A
	// grounded select has already materialised its result, so the pattern is
	// matched against nodes the processor holds and reading their children
	// advances nothing. si-group-203 selects "//Item/copy-of()" and starts
	// its groups on "Item[processing-instruction('start')]", a pattern
	// §19.8.10 calls free-ranging on the "p[b]" rule; the catalog asserts
	// OUTPUT for it, and it is right to, because the snapshot is in memory.
	//
	// The verdict is taken only when patternIsFreeRanging is sure of it, and
	// only when the free-ranging answer does not rest on the deliberately
	// over-broad numeric test that patternPredicateOnlySyntacticallyNumeric
	// identifies -- the same withholding checkStreamableModePatterns applies
	// to a rule's match pattern, and for the same reason.
	if p := groupPatternAttr(el); p != nil && sel.posture != postureGrounded {
		free, known := patternIsFreeRanging(p.Value, el)
		if !known {
			a.known = false
		} else if free && !patternPredicateOnlySyntacticallyNumeric(p.Value, el) {
			return roamingFreeRanging
		}
	}

	if sel.posture == postureGrounded {
		// Clause 1: a grounded select follows the general rules.
		ops := []operand{{props: sel, usage: usageInspection, allowsChildren: true}}
		ops = append(ops, a.avtOperands(el, "collation")...)
		for _, gat := range []*xdm.Node{groupBy, groupAdj} {
			if gat == nil {
				continue
			}
			p, kids := a.exprOperandIn(gat.Value, el, postureGrounded, true)
			ops = append(ops, operand{props: p, usage: usageAbsorption, allowsChildren: kids})
		}
		for _, o := range a.sortOperands(el, postureGrounded) {
			ops = append(ops, o)
		}
		sub := a.sub(postureGrounded, true)
		// §19.8.9.4: inside this body, fn:current-group() takes the posture
		// and sweep of the select expression.
		//
		// The sweep is taken from the select only where the select is not
		// already an operand in its own right. Here it is -- the first
		// operand above -- so charging the body a second consuming read for
		// the same one pass over the stream would count it twice, and
		// feg-004 of si-for-each-group-A, whose select atomizes attributes
		// into a grounded sequence, would be rejected for a single
		// well-behaved call. What the body sees is the group already
		// assembled, so the call keeps the select's posture with a
		// motionless sweep, and two calls still combine to free-ranging
		// through the select operand as the note to §19.8.4.19 describes.
		sub.currentGroup = props{sel.posture, sweepMotionless}
		sub.groupInScope, sub.groupOutOfReach = true, false
		ops = append(ops, operand{
			props:          sub.body(el),
			usage:          usageTransmission,
			allowsChildren: true,
			higherOrder:    true,
		})
		a.mergeFrom(sub)
		return combine(ops, false)
	}

	// Clause 2: group-by outside an xsl:fork is roaming. Grouping by an
	// arbitrary key needs every group held at once, which only the implicit
	// fork of a surrounding xsl:fork makes possible.
	//
	// A cascade clause that fires without looking at the body must still
	// establish that the body is modelled, or the verdict would be reported
	// as a fact about the stylesheet when it is really a fact about this
	// implementation. The body is therefore assessed first, purely for its
	// effect on `known`, and its properties are discarded.
	if groupBy != nil && !isXSL(el.Parent, "fork") {
		a.noteGroupBodyModelled(el, sel)
		return roamingFreeRanging
	}

	// Clause 3: a grouping key that is not motionless.
	//
	// The key is assessed with the SELECT expression's context posture, not
	// with grounded. Grounded is what clause 1 prescribes, and only clause 1:
	// there the select has already been materialised, so the key reads from
	// memory. In the cascade the select is still a stream, and §19.9's worked
	// example says so in as many words -- of the group-adjacent expression's
	// operand "@timestamp" it writes "the context posture is the posture of
	// the controlling operand of the focus-setting container, that is, the
	// select expression of the containing xsl:for-each-group instruction,
	// which as established above is striding".
	//
	// Assessing it as grounded made every key motionless and clause 3 dead:
	// si-group-901's "PRICE/text()" is motionless read from memory and
	// consuming read from the stream, and the catalog asserts XTSE3430 for it.
	for _, gat := range []*xdm.Node{groupBy, groupAdj} {
		if gat == nil {
			continue
		}
		p := a.exprPropsIn(gat.Value, el, sel.posture, selKids)
		if p.sweep != sweepMotionless {
			a.noteGroupBodyModelled(el, sel)
			return roamingFreeRanging
		}
	}

	// Clause 4: an xsl:sort child.
	if hasSort {
		a.noteGroupBodyModelled(el, sel)
		return roamingFreeRanging
	}

	if !sel.streamable() {
		return roamingFreeRanging
	}

	sub := a.sub(sel.posture, selKids)
	// §19.8.9.4, as in the grounded clause above.
	sub.currentGroup, sub.groupInScope, sub.groupOutOfReach = sel, true, false
	bodyProps := sub.body(el)
	a.mergeFrom(sub)

	// Clause 5: crawling select with a consuming body.
	if sel.posture == postureCrawling && bodyProps.sweep == sweepConsuming {
		return roamingFreeRanging
	}
	if !bodyProps.streamable() {
		return roamingFreeRanging
	}
	return props{bodyProps.posture, wider(sel.sweep, bodyProps.sweep)}
}

// merge applies §19.8.4.25, which is a flat condition rather than a cascade:
// every xsl:merge-source must be anchored by a grounded, motionless
// expression, and then the whole instruction is grounded and motionless.
func (a *instrAnalyzer) merge(el *xdm.Node) props {
	for _, c := range el.ChildElements() {
		if !isXSL(c, "merge-source") {
			continue
		}
		forEachItem := c.Attr("", "for-each-item")
		// for-each-source is the Recommendation's name for the attribute
		// this draft calls for-each-stream; the test set writes the later
		// name throughout, and the rest of the compiler reads it (merge.go).
		// Reading only the draft's name made every streamed merge source
		// look like the select-only case below.
		forEachStream := c.Attr("", "for-each-stream")
		if forEachStream == nil {
			forEachStream = c.Attr("", "for-each-source")
		}
		for _, at := range []*xdm.Node{forEachItem, forEachStream} {
			if at == nil {
				continue
			}
			p := a.exprProps(at.Value, c)
			if p.posture != postureGrounded || p.sweep != sweepMotionless {
				return roamingFreeRanging
			}
		}
		if forEachItem == nil && forEachStream == nil {
			sat := c.Attr("", "select")
			if sat == nil {
				return a.unknown()
			}
			p := a.exprProps(sat.Value, c)
			if p.posture != postureGrounded || p.sweep != sweepMotionless {
				return roamingFreeRanging
			}
		}
	}
	return groundedMotionless
}

// checkStreamableMergeSources raises XTSE3430 for an xsl:merge-source that
// asks to be streamed but is not guaranteed-streamable under §15.4.
//
// This is a different rule from §19.8.4.25, and it has to be: §19.8.4.25
// measures only "the (not very interesting) impact of the xsl:merge
// instruction on the streamability of its containing template rule", and its
// verdict is grounded and motionless for every merge whose sources are
// anchored -- which says nothing about whether each source can itself be read
// as a stream. §15.4 is the rule that does, and it lists four conditions an
// xsl:merge-source must satisfy to be guaranteed-streamable:
//
//	the actual or defaulted attribute value streamable="yes";
//	the for-each-stream attribute is present;
//	the expression in the select attribute has striding posture;
//	sort-before-merge is absent or takes its default value of no.
//
// The first two select which sources are in scope; the last two are what can
// fail. A source that does not ask to be streamed is not checked at all, so
// this cannot reject a stylesheet that never claimed streaming.
//
// The select expression is assessed with a striding context posture, because
// the node it is rooted at is the document the source opens -- §19.6 gives
// that posture to the body of any streamed document, and the merge source's
// select is evaluated against exactly that root.
func checkStreamableMergeSources(root *xdm.Node, sets map[xdm.QName][]*xdm.Node) error {
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		if !isXSL(el, "merge-source") {
			return true
		}
		// for-each-source is the Recommendation's spelling of
		// for-each-stream. Without one of them the source is not streamed,
		// whatever streamable says.
		stream := el.Attr("", "for-each-stream")
		if stream == nil {
			stream = el.Attr("", "for-each-source")
		}
		if stream == nil {
			return true
		}
		// "This is also the default value when the for-each-stream
		// attribute is present" -- so streamable defaults to yes here, and
		// only an explicit no opts out.
		if s := strings.TrimSpace(el.AttrValue("streamable")); s != "" && !isYes(s) {
			return true
		}
		sat := el.Attr("", "select")
		if sat == nil {
			return true
		}
		// Condition 4. sort-before-merge="yes" re-orders the selected nodes,
		// which cannot be done without holding them, so the source is not
		// streamable however striding its select is.
		if isYes(strings.TrimSpace(el.AttrValue("sort-before-merge"))) {
			err = fmt.Errorf(
				"xsl:merge-source is streamed but specifies sort-before-merge=\"yes\", " +
					"so it is not guaranteed-streamable (XTSE3430)")
			return false
		}
		// Condition 3. Only a striding select walks the opened document
		// once; a crawling one (log//record) revisits descendants, and a
		// grounded or climbing one is not reading the stream in order.
		a := &instrAnalyzer{
			ctxPosture:        postureStriding,
			ctxAllowsChildren: true,
			known:             true,
			attrSets:          sets,
		}
		p := a.exprProps(sat.Value, el)
		if !a.known {
			return true
		}
		if p.posture != postureStriding {
			err = fmt.Errorf(
				"the select expression of a streamed xsl:merge-source is %v, "+
					"but §15.4 requires striding posture, so it is not "+
					"guaranteed-streamable (XTSE3430)", p.posture)
			return false
		}
		return true
	})
	return err
}

// childNamed returns the first XSLT child of el with the given local name.
func childNamed(el *xdm.Node, local string) *xdm.Node {
	for _, c := range el.ChildElements() {
		if isXSL(c, local) {
			return c
		}
	}
	return nil
}
