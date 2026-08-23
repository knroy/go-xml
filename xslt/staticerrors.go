package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
	xdm.NSXSL: true,
	xdm.NSXS:  true,
	"http://www.w3.org/2001/XMLSchema-instance": true,
	xdm.NSFN: true,
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
		// A literal result element carries the prefix-list attributes in
		// their xsl: form, and the rules about them are the same there.
		if err := checkPrefixListAttrs(el); err != nil {
			return err
		}
		return checkDefaultCollation(el)
	}

	local := el.Name.Local

	// XTSE0808: "it is a static error if a namespace prefix is used within
	// the [xsl:]exclude-result-prefixes attribute and there is no namespace
	// binding in scope for that prefix."
	//
	// A literal result element's copy of this check is made where its
	// namespaces are copied to the output; an XSLT element carries the
	// attribute too — xsl:stylesheet most often — and nothing looked at it
	// there, so an unbound prefix on the stylesheet element went unreported.
	if err := checkPrefixListAttrs(el); err != nil {
		return err
	}
	if err := checkDefaultCollation(el); err != nil {
		return err
	}

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
		// "Any content other than comments or processing instructions,
		// including any whitespace text node preserved using the
		// xml:space="preserve" attribute, is a static error."
		//
		// The whitespace clause is why this asks about xml:space at all.
		// Indentation inside an empty element is ordinarily ignored, because
		// every stylesheet in the suite is indented; xml:space="preserve"
		// says that whitespace is content, and then it is an error like any
		// other content.
		preserve := false
		for n := el; n != nil; n = n.Parent {
			if a := n.Attr(xdm.NSXML, "space"); a != nil {
				preserve = strings.TrimSpace(a.Value) == "preserve"
				break
			}
		}
		for _, c := range el.Children {
			switch c.Kind {
			case xdm.KindComment, xdm.KindPI:
			case xdm.KindText:
				if strings.TrimSpace(c.Value) != "" || preserve {
					return fmt.Errorf("XTSE0260: xsl:%s must be empty", local)
				}
			default:
				return fmt.Errorf("XTSE0260: xsl:%s must be empty", local)
			}
		}
	}

	// XTSE1570: the method attribute of xsl:output "must (if present) be a
	// valid QName. If the QName does not have a prefix, then it identifies a
	// method specified in [XSLT and XQuery Serialization] and must be one of
	// xml, html, xhtml, or text."
	//
	// A prefixed name is an implementation-defined method, which this engine
	// does not offer but may not reject on lexical grounds; an unprefixed one
	// is a closed set. "your::xml" is neither, being not a QName at all.
	if local == "output" {
		if a := el.Attr("", "method"); a != nil {
			m := strings.TrimSpace(a.Value)
			if !isLexicalQName(m) {
				return fmt.Errorf(
					"XTSE1570: xsl:output/@method=%q is not a valid QName",
					a.Value)
			}
			if !strings.Contains(m, ":") {
				switch m {
				case "xml", "html", "xhtml", "text":
				default:
					return fmt.Errorf(
						"XTSE1570: xsl:output/@method=%q is not xml, html, "+
							"xhtml or text", a.Value)
				}
			}
		}
	}

	switch local {
	case "import-schema":
		// XTSE0215: "it is a static error if an xsl:import-schema element
		// that contains an xs:schema element has a schema-location
		// attribute, or if it has a namespace attribute that conflicts with
		// the target namespace of the contained schema."
		//
		// Section 3.14 spells out what "conflicts" means, and it is not
		// simple equality: exactly three combinations are permitted. Either
		// both @namespace and @targetNamespace are absent, meaning a
		// no-namespace schema; or both are present and equal; or @namespace
		// is absent and @targetNamespace present, in which case the inline
		// schema decides. Everything else conflicts — including a
		// @namespace with no @targetNamespace to match it, which the
		// equality reading would have let through.
		if inline := inlineSchema(el); inline != nil {
			if el.Attr("", "schema-location") != nil {
				return fmt.Errorf(
					"XTSE0215: xsl:import-schema contains an xs:schema, so " +
						"it may not also have a schema-location attribute")
			}
			ns := el.Attr("", "namespace")
			target := inline.Attr("", "targetNamespace")
			switch {
			case ns == nil:
				// Absent @namespace never conflicts: the inline schema's
				// target namespace, present or absent, is taken as given.
			case target == nil || target.Value != ns.Value:
				return fmt.Errorf(
					"XTSE0215: xsl:import-schema/@namespace=%q conflicts "+
						"with the target namespace of the contained schema",
					ns.Value)
			}
		}

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

	case "with-param":
		// XTSE0620 covers "a variable-binding element", which section 9
		// defines as xsl:variable, xsl:param and xsl:with-param alike. Only
		// the first two were checked, so a select attribute beside content
		// on an xsl:with-param went unreported.
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"XTSE0620: xsl:with-param has both a select attribute and content")
		}

	case "value-of":
		// XTSE0870: "it is a static error if the select attribute of the
		// xsl:value-of element is present when the content of the element is
		// non-empty, or if the select attribute is absent when the content is
		// empty." Unlike xsl:attribute, both directions are errors here:
		// xsl:value-of has nothing to say without a value, so an empty one
		// with no select is a mistake rather than an empty string.
		hasSelect := el.Attr("", "select") != nil
		switch {
		case hasSelect && hasRealContent(el):
			return fmt.Errorf(
				"XTSE0870: xsl:value-of has a select attribute, so it must " +
					"be empty")
		case !hasSelect && !hasRealContent(el):
			return fmt.Errorf(
				"XTSE0870: xsl:value-of has empty content, so it requires a " +
					"select attribute")
		}

	case "attribute", "processing-instruction":
		// XTSE0840 and XTSE0880: "it is a static error if the select
		// attribute ... is present unless the element has empty content."
		code := "XTSE0840"
		if local == "processing-instruction" {
			code = "XTSE0880"
		}
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"%s: xsl:%s has a select attribute, so it must be empty",
				code, local)
		}

	case "namespace":
		// XTSE0910 states both directions: a select attribute is an error
		// "when the element has content other than one or more xsl:fallback
		// instructions", and its absence is an error "when the element has
		// empty content". xsl:namespace is the one instruction of this shape
		// that must state its value one way or the other, because a
		// namespace node with no URI is not a thing the data model has.
		hasSelect := el.Attr("", "select") != nil
		content := false
		for _, c := range el.Children {
			switch c.Kind {
			case xdm.KindElement:
				if !isXSL(c, "fallback") {
					content = true
				}
			case xdm.KindText:
				if strings.TrimSpace(c.Value) != "" {
					content = true
				}
			}
		}
		switch {
		case hasSelect && content:
			return fmt.Errorf(
				"XTSE0910: xsl:namespace has a select attribute, so its " +
					"content may only be xsl:fallback")
		case !hasSelect && !content:
			return fmt.Errorf(
				"XTSE0910: xsl:namespace has empty content, so it requires " +
					"a select attribute")
		}

	case "key":
		// XTSE1205: "it is a static error if an xsl:key declaration has a use
		// attribute and has non-empty content, or if it has empty content and
		// no use attribute." The two halves are one rule: the key value must
		// be given exactly once, either way.
		hasUse := el.Attr("", "use") != nil
		switch {
		case hasUse && hasRealContent(el):
			return fmt.Errorf(
				"XTSE1205: xsl:key has both a use attribute and content")
		case !hasUse && !hasRealContent(el):
			return fmt.Errorf(
				"XTSE1205: xsl:key has neither a use attribute nor content")
		}

	case "variable", "param":
		// XTSE0620: select and content are mutually exclusive.
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"XTSE0620: xsl:%s has both a select attribute and content",
				local)
		}
		if local == "param" {
			if err := checkParamStatic(el); err != nil {
				return err
			}
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

	case "comment":
		// XTSE0940: @select requires empty content.
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"XTSE0940: xsl:comment has both a select attribute and content")
		}

	case "sort":
		// XTSE1015: @select requires empty content.
		if el.Attr("", "select") != nil && hasRealContent(el) {
			return fmt.Errorf(
				"XTSE1015: xsl:sort has both a select attribute and content")
		}
		// XTSE1017: only the first xsl:sort of a sibling run may carry
		// @stable, since stability is a property of the whole sort.
		if el.Attr("", "stable") != nil && el.Parent != nil {
			first := true
			for _, sib := range el.Parent.ChildElements() {
				if !isXSL(sib, "sort") {
					continue
				}
				if sib == el {
					break
				}
				first = false
				break
			}
			if !first {
				return fmt.Errorf(
					"XTSE1017: only the first xsl:sort of a group may have a " +
						"stable attribute")
			}
		}

	case "perform-sort":
		// XTSE1040: with @select, only xsl:sort and xsl:fallback may appear.
		if el.Attr("", "select") != nil {
			for _, c := range el.ChildElements() {
				if isXSL(c, "sort") || isXSL(c, "fallback") {
					continue
				}
				return fmt.Errorf(
					"XTSE1040: xsl:perform-sort with a select attribute may "+
						"only contain xsl:sort and xsl:fallback, found %s",
					c.Name.Lexical())
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

// checkParamStatic applies section 9.2's rules about where required and
// tunnel may appear on an xsl:param, and what a mandatory parameter may hold.
//
// All three are XTSE0020 rather than XTSE0090, because the attribute is one
// the element's summary allows — it is the *value* that is not permitted in
// this position. The exception is the mandatory-parameter rule, which is about
// the element's content and so is XTSE0010.
func checkParamStatic(el *xdm.Node) error {
	// Which rules apply depends on what the xsl:param is a parameter of, and
	// the summaries let it be a child of four different elements.
	parent := ""
	if el.Parent != nil && el.Parent.Name.URI == xdm.NSXSL {
		parent = el.Parent.Name.Local
	}
	inFunction := parent == "function"
	inTemplate := parent == "template"

	if a := el.Attr("", "required"); a != nil {
		// "This attribute may be specified for stylesheet parameters and for
		// template parameters; it must not be specified for function
		// parameters, which are always mandatory."
		if inFunction {
			// The code here is XTSE0090 rather than XTSE0020, which is what
			// the suite asks for: the rule is that the attribute is not
			// allowed on an xsl:param in this position at all, not that its
			// value is wrong. The element syntax summary cannot express
			// "allowed only under some parents", so the rule lives here while
			// the code stays the one the grammar check would have given.
			return fmt.Errorf(
				"XTSE0090: required may not be specified on an xsl:param of " +
					"a stylesheet function, which is always mandatory")
		}
		// "If the parameter is mandatory, then the xsl:param element must be
		// empty and must not have a select attribute."
		if strings.TrimSpace(a.Value) == "yes" {
			if el.Attr("", "select") != nil {
				return fmt.Errorf(
					"XTSE0010: an xsl:param with required=\"yes\" must not " +
						"have a select attribute")
			}
			if hasRealContent(el) {
				return fmt.Errorf(
					"XTSE0010: an xsl:param with required=\"yes\" must be empty")
			}
		}
	}

	if a := el.Attr("", "tunnel"); a != nil &&
		strings.TrimSpace(a.Value) == "yes" && !inTemplate {
		// "The default is no; the value yes may be specified only for
		// template parameters."
		return fmt.Errorf(
			"XTSE0020: tunnel=\"yes\" may only be specified on a template "+
				"parameter, and this xsl:param is a parameter of xsl:%s",
			parent)
	}
	return nil
}

// checkPrefixList verifies a whitespace-separated list of namespace prefixes.
//
// Three attributes take one — [xsl:]exclude-result-prefixes and
// [xsl:]extension-element-prefixes — and each pairs the same two rules with
// its own error codes: every named prefix must be bound (unbound), and
// "#default" must name a default namespace that exists (nodefault). Writing
// them once is what keeps the prefixed and unprefixed spellings, and the
// literal-result-element and XSLT-element cases, from drifting apart.
func checkPrefixList(el *xdm.Node, list, unbound, nodefault, attr string) error {
	for _, p := range strings.Fields(list) {
		switch p {
		case "#all":
			// Only exclude-result-prefixes accepts it, and where it is not
			// accepted the element's own summary has already refused the
			// value.
			continue
		case "#default":
			// LookupPrefix("") answers true with an empty URI when there is
			// no default namespace, because "no default namespace" is a
			// legitimate state for a name to be resolved in. It is the URI
			// rather than the flag that says whether one exists.
			if uri, _ := el.LookupPrefix(""); uri == "" {
				return fmt.Errorf(
					"%s: %s specifies #default, but there is no default "+
						"namespace in scope", nodefault, attr)
			}
			continue
		}
		if _, ok := el.LookupPrefix(p); !ok {
			return fmt.Errorf(
				"%s: %s names %q, which is not a namespace prefix in scope",
				unbound, attr, p)
		}
	}
	return nil
}

// checkPrefixListAttrs applies checkPrefixList to both attributes that take
// a prefix list, in both the unprefixed and the xsl: spelling.
//
// Which spelling an element uses is decided by what kind of element it is —
// an XSLT element writes exclude-result-prefixes, a literal result element
// writes xsl:exclude-result-prefixes — and both are checked here rather than
// asking which kind this is, because an element carrying the wrong spelling
// is XTSE0090 or XTSE0805 and has already been refused by the time this runs.
func checkPrefixListAttrs(el *xdm.Node) error {
	for _, spec := range []struct {
		attr, unbound, nodefault string
	}{
		{"exclude-result-prefixes", "XTSE0808", "XTSE0809"},
		{"extension-element-prefixes", "XTSE1430", "XTSE1430"},
	} {
		for _, uri := range []string{"", xdm.NSXSL} {
			// On a literal result element the unprefixed spelling is not a
			// directive at all but an ordinary attribute destined for the
			// output, which section 3.5 is explicit about: the attribute "must
			// be in the XSLT namespace only if its parent element is not in
			// the XSLT namespace". Checking it there rejected a stylesheet for
			// an attribute it was merely copying through.
			if uri == "" && el.Name.URI != xdm.NSXSL {
				continue
			}
			a := el.Attr(uri, spec.attr)
			if a == nil {
				continue
			}
			if err := checkPrefixList(el, a.Value, spec.unbound,
				spec.nodefault, spec.attr); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkDefaultCollation applies XTSE0125 to one element.
//
// "It is a static error if the value of an [xsl:]default-collation attribute,
// after resolving against the base URI, contains no URI that the
// implementation recognizes as a collation URI."
//
// The word is "contains": the value is a list of candidates and the first
// recognised one is used, so a list holding one usable URI among several
// unknown ones is correct rather than an error. Only a list with nothing
// usable in it fails, which is what makes naming a preferred collation with a
// fallback the intended way to write the attribute.
func checkDefaultCollation(el *xdm.Node) error {
	for _, uri := range []string{"", xdm.NSXSL} {
		a := el.Attr(uri, "default-collation")
		if a == nil {
			continue
		}
		candidates := strings.Fields(a.Value)
		if len(candidates) == 0 {
			return fmt.Errorf(
				"XTSE0125: default-collation is empty, so it names no collation")
		}
		for _, c := range candidates {
			if _, err := xpath.ResolveCollation(c); err == nil {
				return nil
			}
		}
		return fmt.Errorf(
			"XTSE0125: default-collation=%q names no collation this "+
				"implementation recognises", a.Value)
	}
	return nil
}

// inlineSchema returns the xs:schema child of an xsl:import-schema, or nil.
//
// The element it looks for comes from the content model — xsl:import-schema is
// the one XSLT element whose model names a foreign element outright — rather
// than from a hard-coded name here, so that the two cannot drift apart.
func inlineSchema(el *xdm.Node) *xdm.Node {
	cm, ok := contentModels[el.Name.Local]
	if !ok || cm.foreign == "" {
		return nil
	}
	for _, c := range el.ChildElements() {
		if c.Name.URI == xdm.NSXS && c.Name.Local == cm.foreign {
			return c
		}
	}
	return nil
}
