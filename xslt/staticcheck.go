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
	"version":                   true,
	"exclude-result-prefixes":   true,
	"extension-element-prefixes": true,
	"xpath-default-namespace":   true,
	"default-collation":         true,
	"use-when":                  true,
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
	return nil
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
