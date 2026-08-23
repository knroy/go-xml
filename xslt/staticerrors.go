package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The static errors of Appendix E that are decidable from the stylesheet tree
// alone.
//
// elementtable.go decides the three that follow from the element grammar —
// XTSE0010, XTSE0020 and XTSE0090. The rules here are the ones that need more
// than the grammar: a value's lexical form, an element's position among its
// siblings, or a name that must not be in a reserved namespace. They are all
// still *static*, so they run once at compile time over the tree that
// conditional inclusion left behind.
//
// Each check names the clause it implements, because the value of a
// conformance error is that a caller can look it up.

// reservedNamespaces are the namespaces a stylesheet may not use for names it
// declares (XTSE0080).
//
// The rule exists so that a stylesheet cannot declare a template, function or
// variable whose name collides with one the specifications reserve for
// themselves.
var reservedNamespaces = map[string]bool{
	xdm.NSXSL:                              true,
	xdm.NSXS:                               true,
	"http://www.w3.org/2001/XMLSchema-instance": true,
	xdm.NSFN:                                true,
	"http://www.w3.org/2005/xpath-functions/math": true,
	"http://www.w3.org/XML/1998/namespace":        true,
}

// namedDeclarations are the elements whose name attribute must not be in a
// reserved namespace.
var namedDeclarations = map[string]bool{
	"template": true, "attribute-set": true, "key": true,
	"decimal-format": true, "variable": true, "param": true,
	"function": true, "output": true,
}

// checkStaticErrors applies the tree-decidable static rules to a module.
func checkStaticErrors(root *xdm.Node) error {
	if isXSL(root, "stylesheet") || isXSL(root, "transform") {
		if err := checkStylesheetElement(root); err != nil {
			return err
		}
	}
	return walkStaticErrors(root, false)
}

// checkStylesheetElement applies the rules about xsl:stylesheet itself.
func checkStylesheetElement(root *xdm.Node) error {
	// XTSE0110: the version attribute must be an xs:decimal.
	if a := root.Attr("", "version"); a != nil {
		if !isDecimalLexical(a.Value) {
			return fmt.Errorf(
				"XTSE0110: xsl:%s/@version=%q is not a number",
				root.Name.Local, a.Value)
		}
	}

	seenNonImport := false
	for _, c := range root.Children {
		switch c.Kind {
		case xdm.KindText:
			// XTSE0120: no text node children. Whitespace-only text is
			// stripped before this runs, so anything left is real content.
			if strings.TrimSpace(c.Value) != "" {
				return fmt.Errorf(
					"XTSE0120: xsl:%s must not have text node children",
					root.Name.Local)
			}
			continue
		case xdm.KindElement:
		default:
			continue
		}
		// XTSE0130: a child element in no namespace.
		if c.Name.URI == "" {
			return fmt.Errorf(
				"XTSE0130: top-level element %q is in no namespace",
				c.Name.Local)
		}
		// XTSE0200: xsl:import must precede every other element child.
		if c.Name.URI == xdm.NSXSL && c.Name.Local == "import" {
			if seenNonImport {
				return fmt.Errorf(
					"XTSE0200: xsl:import must precede every other top-level element")
			}
			continue
		}
		seenNonImport = true
	}
	return nil
}

// walkStaticErrors applies the per-element rules, carrying the
// forwards-compatible mode so that a module written for a later version is
// not judged by this one's rules.
func walkStaticErrors(n *xdm.Node, forwards bool) error {
	if n.Kind == xdm.KindElement {
		forwards = forwardsAt(n, forwards)
		if !forwards {
			if err := checkElementStatic(n); err != nil {
				return err
			}
		}
	}
	for _, c := range n.Children {
		if err := walkStaticErrors(c, forwards); err != nil {
			return err
		}
	}
	return nil
}

// checkElementStatic applies the rules that look at one element.
func checkElementStatic(el *xdm.Node) error {
	if el.Name.URI != xdm.NSXSL {
		// XTSE0805: an attribute on a literal result element may not be in
		// the XSLT namespace unless the specification defines it there.
		for _, a := range el.Attrs {
			if a.Name.URI != xdm.NSXSL {
				continue
			}
			if !literalResultXSLAttrs[a.Name.Local] {
				return fmt.Errorf(
					"XTSE0805: xsl:%s is not an attribute this specification "+
						"defines on a literal result element", a.Name.Local)
			}
		}
		return nil
	}

	local := el.Name.Local

	// XTSE0080: a declared name may not be in a reserved namespace.
	if namedDeclarations[local] {
		if a := el.Attr("", "name"); a != nil {
			// xsl:initial-template is the one name in the XSLT namespace a
			// stylesheet may declare: it is how a transform names the
			// template to start at, so the specification reserves the name
			// *for* stylesheets rather than against them.
			if qn, err := resolveQNameAttr(el, a.Value); err == nil &&
				reservedNamespaces[qn.URI] &&
				!(qn.URI == xdm.NSXSL && qn.Local == "initial-template") {
				return fmt.Errorf(
					"XTSE0080: xsl:%s/@name=%q is in a reserved namespace",
					local, a.Value)
			}
		}
	}

	// XTSE0260: an element required to be empty may hold only comments and
	// processing instructions.
	if emptyXSLElements[local] {
		for _, c := range el.Children {
			switch c.Kind {
			case xdm.KindComment, xdm.KindPI:
			case xdm.KindText:
				if strings.TrimSpace(c.Value) != "" {
					return fmt.Errorf("XTSE0260: xsl:%s must be empty", local)
				}
			default:
				return fmt.Errorf("XTSE0260: xsl:%s must be empty", local)
			}
		}
	}

	switch local {
	case "include", "import":
		// XTSE0170 and XTSE0190: both must be top-level, which means their
		// parent is the xsl:stylesheet element.
		p := el.Parent
		if p == nil || p.Name.URI != xdm.NSXSL ||
			(p.Name.Local != "stylesheet" && p.Name.Local != "transform") {
			code := "XTSE0170"
			if local == "import" {
				code = "XTSE0190"
			}
			return fmt.Errorf("%s: xsl:%s must be a top-level element",
				code, local)
		}

	case "template":
		// XTSE0530: the priority must be an xs:decimal.
		if a := el.Attr("", "priority"); a != nil &&
			!isDecimalLexical(a.Value) {
			return fmt.Errorf(
				"XTSE0530: xsl:template/@priority=%q is not a decimal number",
				a.Value)
		}

	case "variable", "param":
		// XTSE0620: select and content are mutually exclusive.
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"XTSE0620: xsl:%s has both a select attribute and content",
				local)
		}

	case "function":
		// XTSE0740: a stylesheet function's name must have a prefix, so that
		// it cannot collide with one in the default function namespace.
		if a := el.Attr("", "name"); a != nil &&
			!strings.Contains(a.Value, ":") {
			return fmt.Errorf(
				"XTSE0740: xsl:function/@name=%q must have a prefix", a.Value)
		}
		// XTSE0760: its xsl:param children may not specify a default, since
		// every argument of a function call must be supplied.
		for _, c := range el.ChildElements() {
			if !isXSL(c, "param") {
				continue
			}
			if c.Attr("", "select") != nil || hasRealContent(c) {
				return fmt.Errorf(
					"XTSE0760: an xsl:param of a stylesheet function may not " +
						"specify a default value")
			}
		}

	case "number":
		// XTSE0975: @value excludes select, level, count and from.
		if el.Attr("", "value") != nil {
			for _, other := range []string{"select", "level", "count", "from"} {
				if el.Attr("", other) != nil {
					return fmt.Errorf(
						"XTSE0975: xsl:number/@value cannot be combined with @%s",
						other)
				}
			}
		}

	case "call-template", "apply-templates", "apply-imports", "next-match":
		// XTSE0670: two xsl:with-param children may not share a name.
		seen := map[xdm.QName]bool{}
		for _, c := range el.ChildElements() {
			if !isXSL(c, "with-param") {
				continue
			}
			a := c.Attr("", "name")
			if a == nil {
				continue
			}
			qn, err := resolveQNameAttr(c, a.Value)
			if err != nil {
				continue
			}
			key := xdm.QName{URI: qn.URI, Local: qn.Local}
			if seen[key] {
				return fmt.Errorf(
					"XTSE0670: xsl:%s has two xsl:with-param elements named %q",
					local, a.Value)
			}
			seen[key] = true
		}
	}
	return nil
}

// literalResultXSLAttrs are the attributes the specification defines in the
// XSLT namespace for a literal result element (XTSE0805).
var literalResultXSLAttrs = map[string]bool{
	"version": true, "exclude-result-prefixes": true,
	"extension-element-prefixes": true, "xpath-default-namespace": true,
	"default-collation": true, "use-when": true,
	"use-attribute-sets": true, "type": true, "validation": true,
	"inherit-namespaces": true,
}

// emptyXSLElements are the XSLT elements whose content model is empty
// (XTSE0260).
// The list comes from the specification's own element syntax summaries — the
// ones whose content model is empty — not from intuition. Guessing produced a
// list with xsl:value-of, xsl:apply-imports and xsl:next-match on it, all of
// which legitimately take content, and refused 65 valid stylesheets.
var emptyXSLElements = map[string]bool{
	"include": true, "import": true, "strip-space": true,
	"preserve-space": true, "namespace-alias": true, "copy-of": true,
	"number": true, "decimal-format": true, "output": true,
	"output-character": true,
}

// hasRealContent reports whether an element has content other than whitespace,
// comments and processing instructions.
func hasRealContent(el *xdm.Node) bool {
	for _, c := range el.Children {
		switch c.Kind {
		case xdm.KindElement:
			return true
		case xdm.KindText:
			if strings.TrimSpace(c.Value) != "" {
				return true
			}
		}
	}
	return false
}

// isDecimalLexical reports whether s is a valid xs:decimal.
//
// It is deliberately stricter than strconv.ParseFloat, which accepts "1e5",
// "Infinity" and "NaN" — none of which is an xs:decimal, and the version
// attribute is required to be one.
func isDecimalLexical(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	digits, dots := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	if digits == 0 {
		return false
	}
	// A value that parses lexically may still be out of range for the
	// arbitrary-precision type, but nothing here depends on its magnitude.
	_, err := strconv.ParseFloat(strings.TrimPrefix(s, "+"), 64)
	return err == nil || dots <= 1
}
