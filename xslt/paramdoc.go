package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// nsSerialization is the namespace of a serialization parameter document.
const nsSerialization = "http://www.w3.org/2010/xslt-xquery-serialization"

// applyParameterDocument folds an external serialization parameter document
// into a set of output settings.
//
// Section 25.1: "The parameter-document attribute allows serialization
// parameters to be supplied in an external document. The external document
// must contain an output:serialization-parameters element with the format
// described in Section 3.1 Setting Serialization Parameters by Means of a Data
// Model Instance." Its parameters win: "a serialization parameter specified in
// the parameter-document takes precedence over a value supplied directly as an
// attribute of xsl:result-document, which in turn takes precedence over a
// value supplied in the selected output definition" -- which is why this runs
// last, over settings already carrying the instruction's own attributes.
//
// The document is read here, at run time, rather than at compile time,
// because the spec asks for exactly that: "the parameter document should be
// read during run-time evaluation of the stylesheet. If the location of the
// stylesheet at development time is different from the deployed location, any
// relative reference should be resolved against the deployed location."
//
// A document that cannot be found is not an error. 25.1: "A serialization
// error occurs if the result of dereferencing the URI is ill-formed or
// invalid; but if no document can be found at the specified location, the
// attribute should be ignored." So a retrieval failure leaves the settings
// alone and a malformed document is reported.
func applyParameterDocument(rt *runtime, o *OutputSettings, baseURI string) error {
	href := o.ParameterDocument
	// Cleared before anything else can go wrong: the URI has been consumed,
	// and leaving it set would make a settings value that is copied onward --
	// a named xsl:output selected by @format, say -- fetch the document a
	// second time.
	o.ParameterDocument = ""
	o.ParameterDocumentBase = ""
	if href == "" {
		return nil
	}
	docs := rt.ctx.Docs
	if docs == nil {
		return nil
	}
	tree, err := resolveDocumentIn(rt.ctx, href, baseURI)
	if err != nil || tree == nil {
		// "if no document can be found at the specified location, the
		// attribute should be ignored".
		return nil
	}
	root := docElement(tree.Root)
	if root == nil || root.Name.URI != nsSerialization ||
		root.Name.Local != "serialization-parameters" {
		return fmt.Errorf(
			"SEPM0017: the parameter document %q is not an "+
				"output:serialization-parameters document", href)
	}
	return ApplyParameterDocument(root, o)
}

// docElement returns a document node's element child, or n itself when n is
// already an element.
func docElement(n *xdm.Node) *xdm.Node {
	if n == nil {
		return nil
	}
	if n.Kind == xdm.KindElement {
		return n
	}
	for _, c := range n.Children {
		if c.Kind == xdm.KindElement {
			return c
		}
	}
	return nil
}

// ApplyParameterDocument reads the children of an
// output:serialization-parameters element into output settings.
//
// root is that element. Each child names one parameter: its local name is the
// parameter, and a "value" attribute carries the value -- except
// use-character-maps, which spells its entries out as children because it has
// no xsl:character-map declaration to point at. The element form is the same
// one fn:serialize accepts as its second argument.
//
// A parameter in another namespace is an implementation-defined extension the
// spec allows and this ignores; one in no namespace is a malformed document
// rather than an extension, since an extension must name its own namespace.
//
// It is exported for the sake of an XQuery main module, whose prolog names
// this document with "declare option output:parameter-document" (XQuery 3.1
// §2.2.4) and gets the same parameters by the same route. The fetching stays
// with the caller: XSLT resolves the URI against the element that wrote it
// and XQuery against the query's static base URI, and neither rule belongs to
// the reading of a document already in hand.
func ApplyParameterDocument(root *xdm.Node, o *OutputSettings) error {
	for _, p := range root.Children {
		if p.Kind != xdm.KindElement {
			continue
		}
		if p.Name.URI != nsSerialization {
			if p.Name.URI == "" {
				return fmt.Errorf(
					"SEPM0017: serialization parameter %q is in no namespace",
					p.Name.Local)
			}
			continue
		}
		if p.Name.Local == "use-character-maps" {
			m, err := readParamCharacterMaps(p)
			if err != nil {
				return err
			}
			// By value, so it replaces rather than joins the named maps
			// @use-character-maps asked for: the two are the same parameter
			// from two sources, and this is the source that wins.
			o.InlineCharMap = m
			continue
		}
		val, err := paramDocValue(p)
		if err != nil {
			return err
		}
		// A list-of-QNames parameter in a parameter document is written with
		// *lexical* QNames, resolved against the in-scope namespaces of the
		// element that carries the value. XSLT states the rule for the same
		// parameters on xsl:output -- "the effective value of the attribute
		// contains one or more lexical QNames. The prefix in such a QName is
		// expanded using the in-scope namespaces ... In the case of
		// cdata-section-elements, an unprefixed element name is expanded
		// using the default namespace" -- and Serialization 3.1 §3.1 applies
		// it to a parameter document too.
		//
		// SetSerializationParam takes only a name and a string, with no
		// namespace context, so it can accept nothing but EQNames. The
		// bindings are on the parameter element and are known only here, so
		// this is where a lexical QName is rewritten into the EQName form
		// that survives the flattening. Serialization-035 is exactly this:
		// the value "Q{...a}e b:e Q{...c}e e" sits on an element carrying
		// xmlns:b and a default namespace, and b:e was being dropped
		// altogether while the bare e was being read as no-namespace.
		if qnameListParam[p.Name.Local] {
			if val, err = expandParamQNames(p, val); err != nil {
				return err
			}
		}
		if err := SetSerializationParam(o, p.Name.Local, val); err != nil {
			return err
		}
	}
	return nil
}

// SetSerializationParam applies one serialization parameter, named by its
// local name in the serialization namespace and carrying its lexical value,
// to a set of output settings.
//
// It is the single place the parameter names of Serialization 3.1 §3 are
// turned into fields, so that every source of them agrees: an external
// parameter document read by applyParameterDocument, and an XQuery prolog's
// "declare option output:*", which states the same parameters in a different
// syntax and must not acquire a second, subtly different reading of them.
// xsl:output is not routed through here, because its values arrive already
// separated into attributes with their own AVT and QName-expansion rules.
//
// An unsupported parameter is SEPM0017 rather than something to ignore:
// accepting one silently would let a caller believe it had asked for
// something it did not get.
func SetSerializationParam(o *OutputSettings, name, val string) error {
	yes := func(v string) bool {
		v = strings.TrimSpace(v)
		if alias, ok := boolAliases[v]; ok {
			v = alias
		}
		return v == "yes"
	}
	switch name {
	case "method":
		o.Method = strings.TrimSpace(val)
	case "indent":
		o.Indent = yes(val)
	case "encoding":
		o.Encoding = val
	case "media-type":
		o.MediaType = val
	case "doctype-public":
		o.DocTypePublic = val
	case "doctype-system":
		o.DocTypeSystem = val
	case "omit-xml-declaration":
		o.OmitXMLDecl = yes(val)
	case "byte-order-mark":
		o.ByteOrderMark = yes(val)
	case "undeclare-prefixes":
		o.UndeclarePrefixes = yes(val)
	case "escape-uri-attributes":
		b := yes(val)
		o.EscapeURIAttributes = &b
	case "include-content-type":
		b := yes(val)
		o.IncludeContentType = &b
	case "allow-duplicate-names":
		o.AllowDuplicateNames = yes(val)
	case "json-node-output-method":
		o.JSONNodeOutputMethod = strings.TrimSpace(val)
	case "build-tree":
		b := yes(val)
		o.BuildTree = &b
	case "item-separator":
		v := val
		o.ItemSeparator = &v
	case "normalization-form":
		o.NormalizationForm = strings.TrimSpace(val)
	case "standalone":
		v := strings.TrimSpace(val)
		if v == "omit" {
			v = ""
		} else if alias, ok := boolAliases[v]; ok {
			v = alias
		}
		o.Standalone = v
	case "version":
		o.Version = strings.TrimSpace(val)
	case "html-version":
		o.HTMLVersion = strings.TrimSpace(val)
	case "cdata-section-elements", "suppress-indentation":
		// Both take a list of names, and only names already in EQName
		// notation can be read here: a lexical QName means nothing without
		// the namespace bindings it was written under, and neither source
		// that reaches this function carries them. A parameter document is
		// not the stylesheet, and an XQuery prolog option is a string
		// literal rather than markup -- so the prolog expands its list
		// before handing it over, which is where the bindings are, and a
		// parameter document's lexical name is left to the setting the
		// stylesheet gave rather than half-applied.
		names, err := parseEQNameList(val)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			break
		}
		if name == "cdata-section-elements" {
			o.CDataElements = names
		} else {
			o.SuppressIndentation = names
		}
	default:
		return fmt.Errorf(
			"SEPM0017: serialization parameter %q is not supported", name)
	}
	return nil
}

// paramDocValue reads a parameter element's "value" attribute. Exactly one
// attribute, named "value" and in no namespace: a differently named one means
// the document is not the parameter document it claims to be.
func paramDocValue(p *xdm.Node) (string, error) {
	for _, a := range p.Attrs {
		if a.Name.URI != "" {
			continue
		}
		if a.Name.Local != "value" {
			return "", fmt.Errorf(
				"SEPM0017: unexpected attribute %q on serialization parameter %q",
				a.Name.Local, p.Name.Local)
		}
		return a.Value, nil
	}
	return "", fmt.Errorf(
		"SEPM0017: serialization parameter %q has no value", p.Name.Local)
}

// readParamCharacterMaps reads a use-character-maps parameter, whose entries
// are output:character-map children rather than a value. Two of them mapping
// the same character is SEPM0018 -- a conflict the caller cannot have meant.
func readParamCharacterMaps(p *xdm.Node) (map[rune]string, error) {
	out := map[rune]string{}
	for _, c := range p.Children {
		if c.Kind != xdm.KindElement {
			continue
		}
		if c.Name.URI != nsSerialization || c.Name.Local != "character-map" {
			return nil, fmt.Errorf(
				"SEPM0017: %q is not a character-map element", c.Name.Local)
		}
		ch, to := "", ""
		haveChar, haveTo := false, false
		for _, a := range c.Attrs {
			if a.Name.URI != "" {
				continue
			}
			switch a.Name.Local {
			case "character":
				ch, haveChar = a.Value, true
			case "map-string":
				to, haveTo = a.Value, true
			default:
				return nil, fmt.Errorf(
					"SEPM0017: unexpected attribute %q on character-map",
					a.Name.Local)
			}
		}
		if !haveChar || !haveTo {
			return nil, fmt.Errorf(
				"SEPM0017: a character-map needs both character and map-string")
		}
		r := []rune(ch)
		if len(r) != 1 {
			return nil, fmt.Errorf(
				"SEPM0017: a character-map must name exactly one character, "+
					"got %q", ch)
		}
		if _, seen := out[r[0]]; seen {
			return nil, fmt.Errorf(
				"SEPM0018: character %q is mapped more than once", ch)
		}
		out[r[0]] = to
	}
	return out, nil
}

// qnameListParam names the serialization parameters whose value is a list of
// element names, and which therefore take lexical QNames resolved against the
// namespaces in scope where the value was written.
//
// use-character-maps is a list of names too, but of character maps rather than
// of elements, and XSLT's rule about the default namespace is written for
// element names; it is left alone.
var qnameListParam = map[string]bool{
	"cdata-section-elements": true,
	"suppress-indentation":   true,
}

// expandParamQNames rewrites the lexical QNames in a parameter document's
// list-of-names value into the EQName form parseEQNameList understands,
// resolving each prefix against the in-scope namespaces of the element that
// carried the value.
//
// An unprefixed name takes the default namespace, which is the rule XSLT
// states for cdata-section-elements specifically: unlike almost everywhere
// else a name appears, an element name here is not in no namespace merely
// because it has no prefix. A name already written as an EQName is left as it
// is -- the two notations are both allowed in the same list.
func expandParamQNames(p *xdm.Node, val string) (string, error) {
	scope := p.InScopeNamespaces()
	fields := strings.Fields(val)
	for i, n := range fields {
		if strings.HasPrefix(n, "Q{") {
			continue
		}
		prefix, local := "", n
		if j := strings.IndexByte(n, ':'); j >= 0 {
			prefix, local = n[:j], n[j+1:]
		}
		uri, ok := scope[prefix]
		if !ok && prefix != "" {
			// An unbound prefix is an error here, unlike in parseEQNameList:
			// there the reader simply had no bindings, while here the
			// bindings are in hand and the name is genuinely unresolvable.
			return "", fmt.Errorf(
				"SEPM0017: the prefix %q in serialization parameter %q is not bound",
				prefix, p.Name.Local)
		}
		fields[i] = "Q{" + uri + "}" + local
	}
	return strings.Join(fields, " "), nil
}

// parseEQNameList reads a whitespace-separated list of names in EQName
// notation, "Q{uri}local" or a bare local name for one in no namespace.
//
// A lexical QName -- one with a prefix -- is skipped rather than refused. It
// is not an error for a caller to write one; it is simply a name this reader
// has no bindings to resolve, and the parameter is then left to whatever set
// it before. Refusing the whole document over it would turn an unresolvable
// name into a failure of everything around it.
func parseEQNameList(val string) ([]xdm.QName, error) {
	var out []xdm.QName
	for _, n := range strings.Fields(val) {
		if !strings.HasPrefix(n, "Q{") {
			if strings.Contains(n, ":") {
				continue
			}
			out = append(out, xdm.QName{Local: n})
			continue
		}
		end := strings.IndexByte(n, '}')
		if end < 0 {
			return nil, fmt.Errorf(
				"SEPM0017: %q is not a well-formed EQName", n)
		}
		out = append(out, xdm.QName{URI: n[2:end], Local: n[end+1:]})
	}
	return out, nil
}
