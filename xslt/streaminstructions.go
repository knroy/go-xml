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
func analyzeSequenceConstructor(el *xdm.Node, ctx posture, attrSets map[xdm.QName][]*xdm.Node) (props, bool) {
	a := &instrAnalyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: true,
		known:             true,
		attrSets:          attrSets,
	}
	p := a.body(el)
	return p, a.known
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
	an := &analyzer{ctxPosture: ctx, ctxAllowsChildren: allowsChildren, known: a.known}
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
	for _, c := range el.ChildElements() {
		if isSequenceConstructorExcluded(c) {
			continue
		}
		ops = append(ops, operand{
			props:          a.instruction(c),
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

	case "copy":
		return a.copyInstruction(el)

	case "variable":
		// §19.8.4.39. With an as attribute the usages are type-determined,
		// which is not modelled; without one the select navigates and the
		// body absorbs. Navigation from a streamed node is free-ranging,
		// which is the point of the rule: a variable cannot be bound to a
		// streamed node.
		if el.Attr("", "as") != nil {
			return a.unknown()
		}
		var ops []operand
		if o, ok := a.selectOperand(el, usageNavigation); ok {
			ops = append(ops, o)
		} else {
			ops = append(ops, a.bodyOperand(el, usageAbsorption))
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

	default:
		// xsl:apply-templates, xsl:call-template, xsl:apply-imports,
		// xsl:next-match, xsl:next-iteration, xsl:break, xsl:analyze-string,
		// xsl:number, xsl:evaluate, xsl:stream, xsl:source-document,
		// xsl:where-populated, xsl:on-empty, xsl:on-non-empty and the
		// declarations. Each needs either type-determined usage, a mode
		// lookup, or a rule not written here; see the coverage note in
		// docs/conformance-gaps.md.
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
	// A group-starting-with or group-ending-with pattern is a higher-order
	// inspection operand; patterns are not analysed here, and an inspection
	// operand that is motionless contributes nothing, so a pattern is
	// treated as unmodelled only when it is the sole grouping attribute and
	// the verdict would otherwise be a rejection. Since neither of the two
	// pattern forms can make the instruction roaming under these rules,
	// they are simply not operands here.

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
		a.noteBodyModelled(el, sel.posture)
		return roamingFreeRanging
	}

	// Clause 3: a grouping key that is not motionless.
	for _, gat := range []*xdm.Node{groupBy, groupAdj} {
		if gat == nil {
			continue
		}
		p := a.exprPropsIn(gat.Value, el, postureGrounded, true)
		if p.sweep != sweepMotionless {
			a.noteBodyModelled(el, sel.posture)
			return roamingFreeRanging
		}
	}

	// Clause 4: an xsl:sort child.
	if hasSort {
		a.noteBodyModelled(el, sel.posture)
		return roamingFreeRanging
	}

	if !sel.streamable() {
		return roamingFreeRanging
	}

	sub := a.sub(sel.posture, selKids)
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
		forEachStream := c.Attr("", "for-each-stream")
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

// childNamed returns the first XSLT child of el with the given local name.
func childNamed(el *xdm.Node, local string) *xdm.Node {
	for _, c := range el.ChildElements() {
		if isXSL(c, local) {
			return c
		}
	}
	return nil
}
