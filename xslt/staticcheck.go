package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
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
		if err := checkQNameAttr(el, a); err != nil {
			return err
		}
	}

	for name, ad := range def.attrs {
		if !ad.required {
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
			case el.Name.Local == "stylesheet" || el.Name.Local == "transform":
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
		// Section 3.9 ignores an unknown top-level XSLT element "and its
		// content", so the walk must not descend into it. Checking inside
		// rejected a stylesheet for a required attribute missing from an
		// element the processor was told to pretend it never saw.
		if forwards && n.Name.URI == xdm.NSXSL && isTopLevel(n) {
			if _, known := xsltElements[n.Name.Local]; !known {
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
		(p.Name.Local == "stylesheet" || p.Name.Local == "transform")
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
		if !isLexicalQName(n) {
			return fmt.Errorf(
				"%s: attribute %s=%q on xsl:%s is not a QName",
				code, a.Name.Local, a.Value, el.Name.Local)
		}
	}
	return nil
}
