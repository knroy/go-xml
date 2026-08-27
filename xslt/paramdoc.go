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
	tree, err := docs.ResolveDocument(href, baseURI)
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
	return applySerializationParams(root, o)
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

// applySerializationParams reads the children of an
// output:serialization-parameters element into output settings.
//
// The element form is the same one fn:serialize accepts as its second
// argument, and each child names one parameter: its local name is the
// parameter, and a "value" attribute carries the value -- except
// use-character-maps, which spells its entries out as children because it has
// no xsl:character-map declaration to point at.
//
// A parameter in another namespace is an implementation-defined extension the
// spec allows and this ignores; one in no namespace is a malformed document
// rather than an extension, since an extension must name its own namespace.
func applySerializationParams(root *xdm.Node, o *OutputSettings) error {
	yes := func(v string) bool {
		v = strings.TrimSpace(v)
		if alias, ok := boolAliases[v]; ok {
			v = alias
		}
		return v == "yes"
	}
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
		switch p.Name.Local {
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
			// Recognised. Both take a list of EQNames, which this reader has
			// no in-scope namespaces to expand them against -- the parameter
			// document is not the stylesheet -- so they are accepted and left
			// to the setting the stylesheet gave rather than half-applied.
		default:
			// An unsupported parameter is an error rather than something to
			// ignore: accepting one silently would let a stylesheet believe
			// it had asked for something it did not get.
			return fmt.Errorf(
				"SEPM0017: serialization parameter %q is not supported",
				p.Name.Local)
		}
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
