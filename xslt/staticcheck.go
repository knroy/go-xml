package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Static checks that the element grammar decides, from elementtable.go.
//
// Three of the errors in Appendix E are questions about the shape of the
// stylesheet rather than about what it computes, and all three are answered by
// the specification's own element syntax summaries:
//
//   - XTSE0090: an attribute that is not allowed on the element it is on;
//   - XTSE0020: an attribute whose value is not one the summary permits;
//   - XTSE0010: an element in the XSLT namespace that is not an XSLT element.
//
// Checking them here rather than in each instruction's compiler is what makes
// the coverage uniform: an attribute misspelled on xsl:sort is caught by the
// same code as one misspelled on xsl:template, and adding an element to the
// table is all that a new instruction needs.

// standardAttributes may appear on any XSLT element.
//
// Section 3.5 lists them: they are not part of any element's own summary
// because they belong to all of them.
var standardAttributes = map[string]bool{
	"version":                    true,
	"exclude-result-prefixes":    true,
	"extension-element-prefixes": true,
	"xpath-default-namespace":    true,
	"default-collation":          true,
	"use-when":                   true,
	// default-mode is an XSLT 3.0 standard attribute (section 6.6.2), and
	// like the rest of that list it may appear on any XSLT element. It is
	// accepted at 2.0 for the same reason expand-text is: the alternative is
	// XTSE0090 over an attribute that names a mode the 2.0 rules already
	// dispatch to as the unnamed one.
	"default-mode": true,
	// expand-text is an XSLT 3.0 standard attribute (3.0 section 3.5), and
	// like the other members of that list it may appear on any XSLT element.
	// A 2.0 processor has no text value templates, so the attribute selects
	// behaviour it cannot produce; but the alternative to accepting it is
	// XTSE0090, which rejects the whole stylesheet over a declaration that in
	// almost every case governs a branch never taken. The suite settles the
	// question: function-1902 is an XSLT 2.0 test whose expected output is
	// produced by a run in which the xsl:message carrying expand-text is
	// never instantiated. Accepting it here and ignoring it is what lets that
	// run happen at all.
	"expand-text": true,
}

// checkStaticGrammar verifies one element against the table.
//
// It is applied to the XSLT elements of a stylesheet after conditional
// inclusion has run, so an element excluded by use-when is never asked about —
// which section 3.12 requires, since no error may be reported for one.
func checkStaticGrammar(el *xdm.Node, forwards bool) error {
	if el.Name.URI != xdm.NSXSL {
		// A literal result element may carry the standard attributes in their
		// prefixed form, and anything else on it is an ordinary attribute of
		// the output rather than an error.
		return nil
	}

	def, known := xsltElements[el.Name.Local]
	// An element XSLT 3.0 introduced is not an XSLT element to a stylesheet
	// declaring an earlier version, so it is treated exactly as an unknown
	// one: XTSE0010 outside forwards-compatible mode, ignored within it.
	// Recognising it because this engine also implements 3.0 would accept a
	// stylesheet every other conforming 2.0 processor rejects.
	// xsl:package is exempt: it is the package declaration itself, and the
	// @version it carries describes the contents it introduces rather than
	// the element. A 3.0 package routinely declares version="2.0", so reading
	// that version back to decide whether xsl:package is recognised would
	// reject the package on the strength of its own body's version. Whether a
	// package is allowed at all is the processor's cap to decide, and
	// compileRoot has already applied it before reaching here.
	if known && def.since30 && !xpathVersionAt(el).AtLeast31() &&
		!(el.Name.Local == "package" && el.Parent != nil &&
			el.Parent.Kind == xdm.KindDocument) {
		known = false
	}
	if !known {
		if forwards {
			// Section 3.9: where forwards-compatible behaviour is enabled, an
			// XSLT element this version does not define is ignored rather
			// than rejected — a top-level one outright, and one in a sequence
			// constructor if it supplies an xsl:fallback. Being lenient here
			// is what makes a stylesheet written for a later version run.
			return nil
		}
		return fmt.Errorf(
			"xsl:%s is not an XSLT 2.0 element (XTSE0010)", el.Name.Local)
	}

	for _, a := range el.Attrs {
		switch a.Name.URI {
		case "":
			// An unprefixed attribute is the element's own.
		case xdm.NSXML:
			// xml:space, xml:lang and xml:base are always permitted.
			continue
		case xdm.NSXSL:
			// XTSE0090 names the XSLT namespace alongside the null one: an
			// attribute written xsl:name on an XSLT element is as wrong as
			// an unrecognised unprefixed one, because the summaries define
			// every attribute of an XSLT element in no namespace. The
			// prefixed form belongs on a literal result element, where
			// XTSE0805 governs it instead.
			if forwards {
				continue
			}
			return fmt.Errorf(
				"attribute xsl:%s is not allowed on xsl:%s (XTSE0090)",
				a.Name.Local, el.Name.Local)
		default:
			// An attribute in another namespace is allowed on any XSLT
			// element and is ignored, which is how extension attributes
			// work. Only the *no-namespace* attributes are governed by the
			// element's summary.
			continue
		}

		ad, ok := def.attrs[a.Name.Local]
		if ok && ad.processor30 && !processorAtLeast30() {
			// See attrDef.processor30: what decides is the processor, not
			// the module's declared version.
			ok = false
		}
		if ok && ad.since30 && !moduleAtLeast30(el) {
			// An attribute XSLT 3.0 added to an older element is not one that
			// element has, to a stylesheet declaring an earlier version. It is
			// treated exactly as a name the summary never defined.
			//
			// What decides is the version on the MODULE element, not the
			// nearest one in scope. A version attribute written lower down
			// selects forwards-compatible behaviour for that element's body;
			// it does not move the element into an earlier stylesheet.
			// static-015 writes version="1.0" on a static xsl:param of a
			// version="3.0" module and expects the parameter to work.
			ok = false
		}
		if !ok {
			if standardAttributes[a.Name.Local] {
				continue
			}
			if forwards {
				// "if an element has an attribute that XSLT 2.0 does not
				// allow the element to have, then the attribute must be
				// ignored."
				continue
			}
			return fmt.Errorf(
				"attribute %q is not allowed on xsl:%s (XTSE0090)",
				a.Name.Local, el.Name.Local)
		}
		if err := checkAttrValue(el, a, ad); err != nil {
			return err
		}
		// package-version has a grammar rather than a closed set of values,
		// so the attribute table cannot state it and checkAttrValue cannot
		// check it.
		if a.Name.Local == "package-version" && isXSL(el, "package") &&
			!validPackageVersion(a.Value) {
			return fmt.Errorf(
				"attribute package-version=%q on xsl:package is not a valid "+
					"version number (XTSE0020)", a.Value)
		}
		if err := checkQNameAttr(el, a); err != nil {
			return err
		}
	}

	for name, ad := range def.attrs {
		if !ad.required {
			continue
		}
		if ad.optional30 && xpathVersionAt(el).AtLeast31() {
			continue
		}
		if el.Attr("", name) == nil {
			return fmt.Errorf(
				"xsl:%s requires a %s attribute (XTSE0010)",
				el.Name.Local, name)
		}
	}

	return checkContentModel(el, forwards)
}

// checkContentModel verifies an element's children against its content model.
//
// XTSE0010 covers three things, and this is the third: "the content of the
// element does not correspond to the content that is allowed for the element."
// The attribute half of the check was already made from the syntax summaries;
// the Content line of those same summaries answers this half.
//
// The distinction that matters is whether the model admits a sequence
// constructor. When it does, any instruction, literal result element or text
// may appear and only the elements named alongside it are constrained — an
// xsl:when inside xsl:template is still wrong. When it does not, the list of
// named children is exhaustive: xsl:apply-imports takes xsl:with-param and
// nothing else, so an xsl:sort inside one is an error rather than something to
// ignore.
//
// Order and cardinality are deliberately not checked here. "(xsl:when+,
// xsl:otherwise?)" also says an xsl:otherwise may not precede an xsl:when, but
// those are decided by each instruction's own compiler, which has to walk the
// children in order anyway.
func checkContentModel(el *xdm.Node, forwards bool) error {
	cm, ok := contentModels[el.Name.Local]
	if !ok {
		return nil
	}
	// An element whose content became a sequence constructor in 3.0 is
	// checked as one only for a stylesheet that declares 3.0; a 2.0
	// stylesheet still gets the narrower model its version defines.
	if cm.seqCtor30 && xpathVersionAt(el).AtLeast31() {
		cm.seqCtor = true
	}
	for _, ch := range el.Children {
		switch ch.Kind {
		case xdm.KindElement:
			if ch.Name.URI != xdm.NSXSL {
				// A literal result element or extension element is content
				// only where a sequence constructor is.
				if cm.seqCtor {
					continue
				}
				// Section 3.6: an element from another namespace is allowed
				// as a top-level declaration and is ignored, which is how a
				// stylesheet carries data or another vocabulary's
				// configuration alongside its own declarations.
				if cm.decls {
					continue
				}
				// A model may also name a non-XSLT element outright:
				// xsl:import-schema contains an inline xs:schema.
				if cm.foreign != "" && ch.Name.Local == cm.foreign {
					continue
				}
				if cm.model == "" {
					return fmt.Errorf(
						"xsl:%s is required to be empty, so the %s child is "+
							"a static error (XTSE0260)",
						el.Name.Local, ch.Name.Lexical())
				}
				return fmt.Errorf(
					"xsl:%s may not contain %s: its content is %s (XTSE0010)",
					el.Name.Local, ch.Name.Lexical(), cm.model)
			}
			if cm.kids[ch.Name.Local] || (cm.decls && xsltDeclarations[ch.Name.Local]) {
				continue
			}
			// An unknown XSLT element is XTSE0010 from the table check when
			// it is reached; here it is only a question of placement, and
			// forwards-compatible mode ignores what it does not know.
			if _, known := xsltElements[ch.Name.Local]; !known && forwards {
				continue
			}
			if cm.seqCtor && isInstruction(ch.Name.Local) {
				continue
			}
			// Two of these have a code of their own, which says the same
			// thing about a specific element rather than about the model.
			switch {
			case ch.Name.Local == "include":
				return fmt.Errorf(
					"an xsl:include element must be a top-level element, "+
						"and this one is inside xsl:%s (XTSE0170)", el.Name.Local)
			case ch.Name.Local == "import":
				return fmt.Errorf(
					"an xsl:import element must be a top-level element, "+
						"and this one is inside xsl:%s (XTSE0190)", el.Name.Local)
			}
			return fmt.Errorf(
				"xsl:%s may not contain xsl:%s: its content is %s (XTSE0010)",
				el.Name.Local, ch.Name.Local, cm.model)

		case xdm.KindText:
			if cm.seqCtor || cm.pcdata {
				continue
			}
			// Whitespace between elements is layout, not content, and every
			// stylesheet in the suite is indented.
			//
			// XTSE0260 says a whitespace text node preserved by
			// xml:space="preserve" *is* an error inside an element required
			// to be empty. That is checked where xml:space is known rather
			// than assumed here, so plain indentation stays legal.
			if xdm.IsXMLWhitespace(ch.Value) {
				continue
			}
			// An empty element and a non-empty one give different codes for
			// text: XTSE0260 is specifically about content in an element
			// required to be empty, and xsl:stylesheet has XTSE0120 of its
			// own for a text node child.
			switch {
			case isStylesheetRootName(el.Name.Local):
				return fmt.Errorf(
					"an xsl:%s element must not have text node children (XTSE0120)",
					el.Name.Local)
			case cm.model == "":
				return fmt.Errorf(
					"xsl:%s is required to be empty, so its text content is "+
						"a static error (XTSE0260)", el.Name.Local)
			}
			return fmt.Errorf(
				"xsl:%s may not contain text: its content is %s (XTSE0010)",
				el.Name.Local, cm.model)
		}
	}
	return checkModelOrder(el, cm)
}

// checkModelOrder checks the order and cardinality the content model states.
//
// The model strings in the table are the specification's own, and two shapes
// of them recur often enough to be worth reading mechanically rather than
// leaving to each instruction's compiler:
//
//   - a leading "xsl:sort*" or "xsl:sort+" before a sequence constructor,
//     which says every xsl:sort must precede the content being sorted;
//   - "(xsl:when+, xsl:otherwise?)", which says an xsl:choose needs at least
//     one xsl:when, allows at most one xsl:otherwise, and requires the
//     xsl:otherwise to come last.
//
// Both are XTSE0010: "the content of the element does not correspond to the
// content that is allowed for the element." Reading them off the model string
// rather than hard-coding the element names is what keeps the check honest —
// if the table changes, so does the rule it enforces.
func checkModelOrder(el *xdm.Node, cm contentModel) error {
	// A model naming a foreign element with a "?" allows at most one of it.
	// xsl:import-schema is the only element of that shape — its model is
	// "xs:schema?" — and a second inline schema there has no defined meaning:
	// section 3.14's rules about the target namespace are written about "the
	// contained schema", singular. Reading the cardinality off the model
	// string rather than naming xsl:import-schema is what keeps this true if
	// the table gains another foreign model.
	if cm.foreign != "" && strings.HasSuffix(cm.model, "?") {
		n := 0
		for _, ch := range el.ChildElements() {
			if ch.Name.URI != xdm.NSXSL && ch.Name.Local == cm.foreign {
				n++
			}
		}
		if n > 1 {
			return fmt.Errorf(
				"xsl:%s: at most one %s child is allowed, its content is "+
					"%s (XTSE0010)", el.Name.Local, cm.foreign, cm.model)
		}
	}

	switch {
	case strings.HasPrefix(cm.model, "(xsl:sort*,") ||
		strings.HasPrefix(cm.model, "(xsl:sort+,"):
		// Everything before the sequence constructor is xsl:sort, so an
		// xsl:sort after any other content is out of place. xsl:fallback is
		// not part of these models, so only real content counts.
		seenOther := false
		for _, ch := range el.Children {
			switch ch.Kind {
			case xdm.KindText:
				if !xdm.IsXMLWhitespace(ch.Value) {
					seenOther = true
				}
			case xdm.KindElement:
				if isXSL(ch, "sort") {
					if seenOther {
						return fmt.Errorf(
							"xsl:%s: every xsl:sort must precede the sequence "+
								"constructor, its content is %s (XTSE0010)",
							el.Name.Local, cm.model)
					}
					continue
				}
				seenOther = true
			}
		}

	case cm.model == "(xsl:when+, xsl:otherwise?)":
		whens, otherwises := 0, 0
		for _, ch := range el.ChildElements() {
			switch {
			case isXSL(ch, "when"):
				if otherwises > 0 {
					return fmt.Errorf(
						"xsl:choose: an xsl:when may not follow the " +
							"xsl:otherwise, its content is " +
							"(xsl:when+, xsl:otherwise?) (XTSE0010)")
				}
				whens++
			case isXSL(ch, "otherwise"):
				otherwises++
				if otherwises > 1 {
					return fmt.Errorf(
						"xsl:choose: at most one xsl:otherwise is allowed, " +
							"its content is (xsl:when+, xsl:otherwise?) " +
							"(XTSE0010)")
				}
			}
		}
		if whens == 0 {
			return fmt.Errorf(
				"xsl:choose: at least one xsl:when is required, its content " +
					"is (xsl:when+, xsl:otherwise?) (XTSE0010)")
		}
	}
	return nil
}

// isInstruction reports whether an XSLT element may appear in a sequence
// constructor, which is the "instruction" category of the syntax summaries.
func isInstruction(local string) bool {
	return xsltInstructions[local]
}

// checkAttrValue verifies an attribute against the closed set of values the
// summary gives for it, when it gives one.
func checkAttrValue(el *xdm.Node, a *xdm.Node, ad attrDef) error {
	if len(ad.values) == 0 {
		return nil
	}
	v := strings.TrimSpace(a.Value)
	// An attribute value template's value is not known until the instruction
	// runs, so a "{...}" here is checked then rather than now. Rejecting it
	// would refuse the legal order="{$dir}".
	if ad.avt && strings.Contains(v, "{") {
		return nil
	}
	for _, want := range ad.values {
		if v == want {
			return nil
		}
	}
	// XSLT 3.0 admits true/false/1/0 wherever 2.0 admits only yes/no, so a
	// 3.0 module's spelling is normalised before the value is refused.
	//
	// xsl:message/@terminate is the exception: message-0009 spells it "true"
	// on a version="2.0" module and is scoped XSLT30+, so there the spelling
	// follows the processor rather than the module -- as @error-code beside
	// it does.
	allow := allowsBoolAliases(el)
	if isXSL(el, "message") && a.Name.Local == "terminate" {
		allow = allow || processorAtLeast30()
	}
	if alias, ok := boolAliases[v]; ok && allow {
		for _, want := range ad.values {
			if alias == want {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"attribute %s=%q on xsl:%s is not one of %s (XTSE0020)",
		a.Name.Local, a.Value, el.Name.Local, strings.Join(ad.values, ", "))
}

// checkStaticGrammarTree applies the check to every XSLT element in a module.
//
// forwards carries the compatibility mode down the tree. An element with a
// [xsl:]version attribute establishes it for itself and its descendants, and
// "the compatibility behavior established by an element overrides any
// compatibility behavior established by an ancestor element" — so it is
// recomputed at every element that carries one rather than only at the root.
func checkStaticGrammarTree(n *xdm.Node, forwards bool) error {
	if n.Kind == xdm.KindElement {
		forwards = forwardsAt(n, forwards)
		if err := checkStaticGrammar(n, forwards); err != nil {
			return err
		}
		// expand-text is a standard attribute, so it is exempted from the
		// per-element attribute table and has to be validated separately.
		// Section 3.7 makes it boolean, which rules out the attribute value
		// template a stylesheet might reach for: cvt-008 writes
		// expand-text="{$yesOrNo}" and expects to be told it cannot.
		if err := checkExpandText(n); err != nil {
			return err
		}
		// Section 3.9 ignores an unknown top-level XSLT element "and its
		// content", so the walk must not descend into it. Checking inside
		// rejected a stylesheet for a required attribute missing from an
		// element the processor was told to pretend it never saw.
		if forwards && n.Name.URI == xdm.NSXSL && isTopLevel(n) {
			// Unknown in the same sense checkStaticGrammar means it: an
			// element of a later version is unknown to this stylesheet's.
			def, known := xsltElements[n.Name.Local]
			if !known || (def.since30 && !xpathVersionAt(n).AtLeast31()) {
				return nil
			}
		}
	}
	for _, c := range n.Children {
		if err := checkStaticGrammarTree(c, forwards); err != nil {
			return err
		}
	}
	return nil
}

// forwardsAt reports the compatibility mode in force at el, given the mode
// inherited from its parent.
func forwardsAt(el *xdm.Node, inherited bool) bool {
	v := ""
	if el.Name.URI == xdm.NSXSL {
		// The version attribute of xsl:output is the *output* version and
		// has nothing to do with compatibility, which section 3.9 says in
		// so many words.
		if el.Name.Local == "output" {
			return inherited
		}
		if a := el.Attr("", "version"); a != nil {
			v = a.Value
		}
	}
	if v == "" {
		if a := el.Attr(xdm.NSXSL, "version"); a != nil {
			v = a.Value
		}
	}
	if v == "" {
		return inherited
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		// An unparseable version is not a compatibility statement. Whether
		// it is an error is decided where the version is compiled, not here.
		return inherited
	}
	return f > 2.0
}

// isExtensionInstruction reports whether el, an element in a sequence
// constructor, sits in a namespace some ancestor-or-self designated as an
// extension namespace.
//
// Section 18.2.1: the designation is made by an [xsl:]extension-element-prefixes
// attribute and "is effective for the element bearing the attribute and for all
// descendants of that element within the same stylesheet module". An element
// whose namespace is so designated "is treated as an instruction rather than as
// a literal result element", which is what changes an unknown element from
// harmless output into something requiring fallback.
func isExtensionInstruction(el *xdm.Node) bool {
	if el.Name.URI == "" || el.Name.URI == xdm.NSXSL {
		return false
	}
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		lists := []string{}
		// The attribute is unprefixed on an XSLT element and xsl:-prefixed on
		// any other, and section 18.2.1 accepts either spelling wherever it is
		// not ambiguous, so both are read.
		if cur.Name.URI == xdm.NSXSL {
			lists = append(lists, cur.AttrValue("extension-element-prefixes"))
		}
		if a := cur.Attr(xdm.NSXSL, "extension-element-prefixes"); a != nil {
			lists = append(lists, a.Value)
		}
		for _, list := range lists {
			for _, p := range strings.Fields(list) {
				if p == "#default" {
					p = ""
				}
				if uri, ok := cur.LookupPrefix(p); ok && uri == el.Name.URI {
					return true
				}
			}
		}
	}
	return false
}

// isTopLevel reports whether el is a child of xsl:stylesheet or xsl:transform.
func isTopLevel(el *xdm.Node) bool {
	p := el.Parent
	return p != nil && p.Kind == xdm.KindElement && p.Name.URI == xdm.NSXSL &&
		isStylesheetRootName(p.Name.Local)
}

// forwardsMode reports whether forwards-compatible behaviour is in force at
// el, working it out from el's own ancestry.
//
// checkStaticGrammarTree threads the mode down as it descends, but the
// instruction compiler reaches an element without that context, so the mode
// has to be recovered by walking up. The nearest ancestor-or-self carrying a
// [xsl:]version attribute decides, since section 3.9 says the compatibility
// behaviour an element establishes overrides any established by an ancestor.
func forwardsMode(el *xdm.Node) bool {
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		if forwardsAt(cur, false) {
			return true
		}
		// A version attribute that does not enable the mode positively
		// disables it, so the walk must stop rather than consult an ancestor.
		if hasVersionAttr(cur) {
			return false
		}
	}
	return false
}

// compatModeAt reports whether XPath 1.0 compatibility mode is in force for
// the expressions written on el.
//
// Section 3.8 makes the mode a property of the nearest ancestor-or-self
// carrying a [xsl:]version attribute, and the mode is on when that version is
// below 2.0. It is the same walk forwardsMode makes, run against the other end
// of the range, and unlike forwardsMode it consults the module element too: a
// module-wide version="1.0" is how the great majority of 1.0-era stylesheets
// are written -- 314 of the suite's own stylesheets are shaped that way -- and
// predicate-001 and select-3301 declare it on xsl:stylesheet and nowhere else.
//
// A predecessor of this function refused such a stylesheet outright with
// XTDE0160, on the grounds that the processor did not implement the behaviour,
// and had to exempt the module element to avoid rejecting all 314. That
// objection does not apply now the behaviour exists. Every rule the mode turns
// on is a coercion that admits an expression 2.0 refuses, so a module-level
// declaration can change what a 1.0 stylesheet produces but cannot make one
// fail that did not.
func compatModeAt(el *xdm.Node) bool {
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		if !hasVersionAttr(cur) {
			continue
		}
		return versionAt(cur) < 2.0
	}
	return false
}

// versionAt returns the version stated on el, or 2.0 when el states none or
// states something unparseable. It reads the same attributes as forwardsAt.
func versionAt(el *xdm.Node) float64 {
	v := ""
	if el.Name.URI == xdm.NSXSL {
		if el.Name.Local == "output" {
			return 2.0
		}
		if a := el.Attr("", "version"); a != nil {
			v = a.Value
		}
	}
	if v == "" {
		if a := el.Attr(xdm.NSXSL, "version"); a != nil {
			v = a.Value
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 2.0
	}
	return f
}

// hasVersionAttr reports whether el carries a version attribute that
// forwardsAt would consult — that is, one that states a compatibility mode.
func hasVersionAttr(el *xdm.Node) bool {
	if el.Name.URI == xdm.NSXSL {
		// xsl:output/@version is the output method's version, not a
		// compatibility statement, so it establishes nothing.
		if el.Name.Local == "output" {
			return false
		}
		if el.Attr("", "version") != nil {
			return true
		}
	}
	return el.Attr(xdm.NSXSL, "version") != nil
}

// checkQNameAttr verifies an attribute the summary types as a QName.
//
// XTSE0020 is about "a value that is not one of the permitted values for that
// attribute", and for an attribute with no enumeration the permitted values
// are its lexical space. A name attribute typed "qname" therefore rejects
// "12foo" and "x/y" for the same reason an enumerated attribute rejects an
// unlisted token.
//
// The parenthesis in that clause — "other than an attribute written using
// curly brackets in a position where an attribute value template is
// permitted" — is why the table records whether the summary bracketed the
// type. Where it did not, a curly-bracket value is not a template that
// escapes the check but simply a value outside the lexical space, which is
// what xsl:decimal-format/@name="{concat('f','f')}" is.
func checkQNameAttr(el *xdm.Node, a *xdm.Node) error {
	qd, ok := qnameAttrs[el.Name.Local][a.Name.Local]
	if !ok {
		return nil
	}
	v := strings.TrimSpace(a.Value)
	if qd.avt {
		// The value is only known once the instruction runs, so it is
		// checked then.
		if strings.Contains(v, "{") {
			return nil
		}
	}
	names := []string{v}
	if qd.list {
		names = strings.Fields(v)
		// An empty list is vacuously fine: it names nothing.
	}
	code := qd.code
	if code == "" {
		code = "XTSE0020"
	}
	for _, n := range names {
		// An EQName counts as a QName here. resolveQNameAttr already accepts
		// Q{uri}local for every QName-valued attribute in the stylesheet —
		// the suite writes it wherever a QName is accepted — so rejecting it
		// in the static check refused a name the resolver would have taken,
		// and did so before the resolver ever saw it. The two disagreed only
		// because the check predates the resolver's support.
		if !isLexicalQName(n) && !isEQName(n) {
			return fmt.Errorf(
				"%s: attribute %s=%q on xsl:%s is not a QName",
				code, a.Name.Local, a.Value, el.Name.Local)
		}
	}
	return nil
}

// isEQName reports whether s has the Q{uri}local form.
//
// The braced-URI spelling names a namespace directly rather than through a
// prefix, so it needs no in-scope binding. Only the local part is checked
// here: the URI is taken as written, exactly as resolveQNameAttr takes it.
func isEQName(s string) bool {
	if !strings.HasPrefix(s, "Q{") {
		return false
	}
	end := strings.IndexByte(s, '}')
	if end < 0 {
		return false
	}
	return xdm.IsNCName(s[end+1:])
}

// xpathVersionAt returns the version of XPath the expressions written on el
// are in, from the nearest ancestor-or-self stating a version.
//
// The mapping is not the identity it looks like. XSLT 3.0 is defined against
// XPath 3.1 — section 2.2 of the XSLT 3.0 Recommendation requires a processor
// to support XPath 3.1, so maps, arrays and the lookup operator are available
// to a version="3.0" stylesheet, not merely the 3.0 additions. XSLT 2.0 and
// 1.0 are defined against XPath 2.0; 1.0 differs from 2.0 in the backwards
// compatibility rules rather than in the grammar, which compatModeAt reads
// separately.
//
// A version this processor does not know is read as the highest it does. That
// is what forwards compatibility means: a version="4.0" stylesheet is not
// rejected outright, it is processed as far as this implementation reaches.
func xpathVersionAt(el *xdm.Node) xpath.Version {
	if overrideXPathVersion != nil {
		return *overrideXPathVersion
	}
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement || !hasVersionAttr(cur) {
			continue
		}
		switch v := versionAt(cur); {
		case v < 3.0:
			return xpath.XPath20
		default:
			return xpath.XPath31
		}
	}
	return xpath.XPath20
}

// overrideXPathVersion pins the XPath version for the stylesheet being
// compiled, ignoring what the stylesheet itself declares.
//
// It is package state guarded by compileMu, for the same reason compileSchema
// is: newNSResolver is called from a dozen places that have no compiler in
// scope, and threading the option through all of them would touch far more
// code than the option is worth. See compileSchema for the full argument.
var overrideXPathVersion *xpath.Version
