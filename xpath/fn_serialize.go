package xpath

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerSerialize adds fn:serialize, F&O 3.0 section 14.7.2.
//
// It renders a sequence as the string an XML serialiser would write. The
// serialiser here is deliberately its own: the XSLT one is driven by
// xsl:output and a character map, neither of which a bare XPath expression
// has, and importing it would invert the package dependency. What this needs
// is the default output method with the handful of parameters the second
// argument can set.
func registerSerialize(l *Library) {
	l.registerFnSince(XPath30, "serialize", []int{1, 2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		opts, err := serializationParams(args)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		// omit-xml-declaration defaults to yes here, since a serialised
		// fragment is more often embedded than written as a document. Asking
		// for it back produces the declaration the XML output method defines.
		// A standalone value can only be written in a declaration, so asking
		// for one is also a request for the declaration itself.
		if opts.method == "xml" && (!opts.omitXMLDecl || opts.standalone != "") {
			sb.WriteString(`<?xml version="1.0" encoding="UTF-8"`)
			if opts.standalone != "" {
				sb.WriteString(` standalone="` + opts.standalone + `"`)
			}
			sb.WriteString("?>\n")
		}
		// An explicit item-separator goes between every pair of items and
		// replaces the default. Without one, sequence normalization (step 4
		// of the Serialization spec) applies instead: adjacent atomic values
		// are joined by a single space, and nothing is inserted around nodes.
		// We used to write the items back to back, so serialize((1, true()))
		// came out as "1true" rather than "1 true".
		prevAtomic := false
		for i, it := range seqArg(args, 0) {
			_, atomic := it.(*xdm.Atomic)
			switch {
			case opts.hasItemSep && i > 0:
				sb.WriteString(opts.itemSeparator)
			case !opts.hasItemSep && atomic && prevAtomic:
				sb.WriteString(" ")
			}
			if err := serializeItem(&sb, it, opts); err != nil {
				return nil, err
			}
			prevAtomic = atomic
		}
		out := sb.String()
		if len(opts.charMap) > 0 {
			out = applyCharacterMap(out, opts.charMap)
		}
		return strSeq(out), nil
	})
}

// serializeOptions are the serialization parameters this implementation reads.
type serializeOptions struct {
	method         string
	omitXMLDecl    bool
	indent         bool
	itemSeparator  string
	hasItemSep     bool
	suppressIndent bool
	// standalone is the value of the standalone parameter, "" when it was not
	// given. It appears in the XML declaration, so asking for it also forces
	// the declaration to be written.
	standalone string
	// charMap maps a character to the string that replaces it on output.
	charMap map[rune]string
}

// serializationParams reads the second argument, an element whose children
// name the parameters.
//
// A parameter this implementation does not recognise is SEPM0017 rather than
// something to ignore: the spec makes an unknown serialization parameter an
// error, and accepting one silently would let a stylesheet believe it had
// asked for something it did not get.
func serializationParams(args []xdm.Sequence) (serializeOptions, error) {
	opts := serializeOptions{method: "xml", omitXMLDecl: true}
	if len(args) < 2 {
		return opts, nil
	}
	for _, it := range args[1] {
		n, ok := it.(*xdm.Node)
		if !ok || n.Kind != xdm.KindElement {
			continue
		}
		// A wrapper in the wrong namespace is not a serialization-parameters
		// element at all, so the argument does not match the declared type:
		// XPTY0004. A wrapper in the right namespace with the wrong local
		// name is a malformed parameter document: SEPM0017.
		if n.Name.URI != nsSerialization {
			return opts, xdm.ErrType(
				"XPTY0004: the serialization parameters must be an " +
					"output:serialization-parameters element")
		}
		// A wrapper with the wrong local name is not the element the
		// parameter is declared to be either, so it is the same type error
		// rather than a malformed-document one.
		if n.Name.Local != "serialization-parameters" {
			return opts, xdm.ErrType(
				"XPTY0004: %q is not a serialization-parameters element", n.Name.Local)
		}
		// An attribute on the wrapper is not a parameter either.
		for _, a := range n.Attrs {
			if a.Name.URI == "" && a.Name.Local != "" {
				return opts, fmt.Errorf(
					"SEPM0017: unexpected attribute %q on serialization-parameters", a.Name.Local)
			}
		}
		seen := map[string]bool{}
		for _, p := range n.Children {
			if p.Kind != xdm.KindElement {
				continue
			}
			// A parameter in another namespace is an implementation-defined
			// extension. It is ignored rather than refused — the spec allows
			// them — but it may still not be repeated.
			key := p.Name.Clark()
			if seen[key] {
				return opts, fmt.Errorf(
					"SEPM0019: serialization parameter %q appears more than once", p.Name.Local)
			}
			seen[key] = true
			if p.Name.URI != nsSerialization {
				// A child in *no* namespace is not an extension parameter —
				// an extension has to name its own namespace — so it is a
				// malformed parameter document.
				if p.Name.URI == "" {
					return opts, fmt.Errorf(
						"SEPM0017: serialization parameter %q is in no namespace",
						p.Name.Local)
				}
				continue
			}

			// use-character-maps carries child elements rather than a value.
			if p.Name.Local == "use-character-maps" {
				m, err := readCharacterMaps(p)
				if err != nil {
					return opts, err
				}
				opts.charMap = m
				continue
			}

			val, err := paramValue(p)
			if err != nil {
				return opts, err
			}
			switch p.Name.Local {
			case "method":
				if val != "xml" && val != "text" && val != "xhtml" && val != "html" {
					return opts, fmt.Errorf("SEPM0017: unsupported serialization method %q", val)
				}
				opts.method = val
			case "omit-xml-declaration":
				if err := checkYesNo(val, p.Name.Local); err != nil {
					return opts, err
				}
				opts.omitXMLDecl = val == "yes"
			case "standalone":
				// The parameter's type is xs:boolean, whose lexical space
				// permits surrounding whitespace, and the serialization spec
				// adds "omit" for "write no standalone at all".
				v := strings.TrimSpace(val)
				if v == "omit" {
					opts.standalone = ""
					break
				}
				if err := checkYesNo(v, p.Name.Local); err != nil {
					return opts, err
				}
				opts.standalone = v
			case "indent":
				if err := checkYesNo(val, p.Name.Local); err != nil {
					return opts, err
				}
				opts.indent = val == "yes"
			case "item-separator":
				opts.itemSeparator, opts.hasItemSep = val, true
			case "encoding", "version", "media-type",
				"doctype-public", "doctype-system", "cdata-section-elements",
				"normalization-form", "undeclare-prefixes",
				"byte-order-mark", "escape-uri-attributes", "include-content-type",
				"allow-duplicate-names", "json-node-output-method",
				"suppress-indentation":
				// Recognised and accepted; this serialiser does not vary its
				// output for them.
			default:
				// Includes use-character-maps and suppress-indentation, which
				// are real parameters this implementation does not support.
				// The spec makes an unsupported parameter an error rather than
				// something to ignore: accepting one silently would let a
				// caller believe it had asked for something it did not get.
				return opts, fmt.Errorf(
					"SEPM0017: serialization parameter %q is not supported", p.Name.Local)
			}
		}
	}
	return opts, nil
}

// readCharacterMaps reads a use-character-maps parameter.
//
// It carries output:character-map children rather than a value, each naming a
// character and the string to write in its place. Two of them mapping the same
// character is SEPM0018 — a conflict the caller could not have meant.
func readCharacterMaps(p *xdm.Node) (map[rune]string, error) {
	out := map[rune]string{}
	// The parameter carries its maps as children, so it has no attributes of
	// its own: "use-character-maps value='yes'" is not this parameter written
	// correctly, it is a malformed parameter document.
	for _, a := range p.Attrs {
		if a.Name.URI == "" {
			return nil, fmt.Errorf(
				"SEPM0017: unexpected attribute %q on use-character-maps", a.Name.Local)
		}
	}
	for _, c := range p.Children {
		if c.Kind != xdm.KindElement {
			continue
		}
		// Only output:character-map may appear here. A child of another name
		// — or in another namespace — is not a character map, and ignoring it
		// would accept a document that says something this does not do.
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
					"SEPM0017: unexpected attribute %q on character-map", a.Name.Local)
			}
		}
		if !haveChar || !haveTo {
			return nil, fmt.Errorf(
				"SEPM0017: a character-map needs both character and map-string")
		}
		// The character attribute holds exactly one character; anything else
		// does not name a character to map.
		r := []rune(ch)
		if len(r) != 1 {
			return nil, fmt.Errorf(
				"SEPM0017: a character-map must name exactly one character, got %q", ch)
		}
		if _, seen := out[r[0]]; seen {
			return nil, fmt.Errorf(
				"SEPM0018: character %q is mapped more than once", ch)
		}
		out[r[0]] = to
	}
	return out, nil
}

// checkYesNo rejects a parameter value that is not the "yes" or "no" its type
// allows.
//
// The value is a boolean, so a spelling outside its lexical space means the
// parameter document is malformed rather than that the caller asked for
// something unsupported.
func checkYesNo(val, name string) error {
	switch strings.TrimSpace(val) {
	case "yes", "no", "true", "false", "1", "0":
		return nil
	}
	return fmt.Errorf(
		"SEPM0017: serialization parameter %q takes yes or no, got %q", name, val)
}

// nsSerialization is the namespace a serialization parameter document uses.
const nsSerialization = "http://www.w3.org/2010/xslt-xquery-serialization"

// paramValue reads a parameter element's "value" attribute.
//
// Exactly one attribute, named "value" and in no namespace: a second one, or a
// differently named one, means the document is not the parameter document it
// claims to be.
func paramValue(p *xdm.Node) (string, error) {
	val := ""
	found := false
	for _, a := range p.Attrs {
		if a.Name.URI != "" {
			continue
		}
		if a.Name.Local != "value" {
			return "", fmt.Errorf(
				"SEPM0017: unexpected attribute %q on serialization parameter %q",
				a.Name.Local, p.Name.Local)
		}
		val, found = a.Value, true
	}
	if !found {
		return "", fmt.Errorf(
			"SEPM0017: serialization parameter %q has no value", p.Name.Local)
	}
	return val, nil
}

// serializeItem writes one item.
func serializeItem(sb *strings.Builder, it xdm.Item, opts serializeOptions) error {
	switch v := it.(type) {
	case *xdm.Node:
		serializeNode(sb, v, opts)
		return nil
	case *xdm.FunctionItem:
		// A function item has no serialization: SENR0001 is the code for an
		// item that cannot be written.
		return fmt.Errorf("SENR0001: a function item cannot be serialized")
	case *xdm.Atomic:
		sb.WriteString(v.String())
		return nil
	}
	return nil
}

// serializeNode writes a node and its descendants.
func serializeNode(sb *strings.Builder, n *xdm.Node, opts serializeOptions) {
	switch n.Kind {
	case xdm.KindDocument:
		for _, c := range n.Children {
			serializeNode(sb, c, opts)
		}
	case xdm.KindElement:
		sb.WriteString("<")
		sb.WriteString(elementName(n))
		writeNamespaceDecls(sb, n)
		// Attributes are written in the order the document had them, which is
		// what a round-trip test compares against.
		for _, a := range n.Attrs {
			sb.WriteString(" ")
			sb.WriteString(elementName(a))
			sb.WriteString(`="`)
			sb.WriteString(escapeAttr(a.Value))
			sb.WriteString(`"`)
		}
		if len(n.Children) == 0 {
			sb.WriteString("/>")
			return
		}
		sb.WriteString(">")
		for _, c := range n.Children {
			serializeNode(sb, c, opts)
		}
		sb.WriteString("</")
		sb.WriteString(elementName(n))
		sb.WriteString(">")
	case xdm.KindText:
		sb.WriteString(escapeText(n.Value))
	case xdm.KindComment:
		sb.WriteString("<!--")
		sb.WriteString(n.Value)
		sb.WriteString("-->")
	case xdm.KindPI:
		sb.WriteString("<?")
		sb.WriteString(n.Name.Local)
		if n.Value != "" {
			sb.WriteString(" ")
			sb.WriteString(n.Value)
		}
		sb.WriteString("?>")
	case xdm.KindAttribute:
		// A free-standing attribute serialises as its value; the spec makes
		// serializing one directly an error in some methods, but the suite
		// compares the value.
		sb.WriteString(n.Value)
	case xdm.KindNamespace:
		sb.WriteString(n.Value)
	}
}

// elementName renders a node's name with its prefix, when it has one.
func elementName(n *xdm.Node) string {
	return n.Name.Lexical()
}

// writeNamespaceDecls writes the namespace declarations an element introduces.
//
// Only those not already in force on the parent: repeating an inherited
// declaration on every descendant is legal but makes the output differ from
// what the round-trip tests expect.
func writeNamespaceDecls(sb *strings.Builder, n *xdm.Node) {
	inherited := map[string]string{}
	for p := n.Parent; p != nil; p = p.Parent {
		for _, ns := range p.Namespaces {
			if _, seen := inherited[ns.Name.Local]; !seen {
				inherited[ns.Name.Local] = ns.Value
			}
		}
	}
	type decl struct{ prefix, uri string }
	var decls []decl
	for _, ns := range n.Namespaces {
		if inherited[ns.Name.Local] == ns.Value {
			continue
		}
		decls = append(decls, decl{ns.Name.Local, ns.Value})
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].prefix < decls[j].prefix })
	for _, d := range decls {
		if d.prefix == "" {
			sb.WriteString(` xmlns="`)
		} else {
			sb.WriteString(` xmlns:`)
			sb.WriteString(d.prefix)
			sb.WriteString(`="`)
		}
		sb.WriteString(escapeAttr(d.uri))
		sb.WriteString(`"`)
	}
}

// escapeText escapes the characters that may not appear in content.
func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// escapeAttr escapes the characters that may not appear in an attribute value.
func escapeAttr(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
		"\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
	return r.Replace(s)
}

// applyCharacterMap replaces the mapped characters in the serialised output.
//
// It runs over the finished text rather than at each write, because a
// character map applies to the output as a whole — including characters that
// escaping would otherwise have turned into references — and doing it once
// keeps the writers free of the concern.
func applyCharacterMap(s string, m map[rune]string) string {
	var sb strings.Builder
	for _, r := range s {
		if to, ok := m[r]; ok {
			sb.WriteString(to)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
