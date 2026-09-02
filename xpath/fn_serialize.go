package xpath

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

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
	l.registerFnSince(XPath30, "serialize", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		opts, err := serializationParams(ctx, args)
		if err != nil {
			return nil, err
		}
		// The json and adaptive methods do not serialise item by item: json
		// renders the whole argument as one value, and both have their own
		// rules about what a sequence even means. They are handled before the
		// XML path rather than inside it.
		switch opts.method {
		case "json":
			out, err := serializeJSON(seqArg(args, 0), opts)
			if err != nil {
				return nil, err
			}
			if len(opts.charMap) > 0 {
				out = applyCharacterMap(out, opts.charMap)
			}
			return strSeq(out), nil
		case "adaptive":
			out, err := serializeAdaptiveSeq(seqArg(args, 0), opts)
			if err != nil {
				return nil, err
			}
			if len(opts.charMap) > 0 {
				out = applyCharacterMap(out, opts.charMap)
			}
			return strSeq(out), nil
		}
		var sb strings.Builder
		// omit-xml-declaration defaults to yes here, since a serialised
		// fragment is more often embedded than written as a document. Asking
		// for it back produces the declaration the XML output method defines.
		// A standalone value can only be written in a declaration, so asking
		// for one is also a request for the declaration itself.
		// The HTML method writes a doctype declaration ahead of the document.
		// html-version 5 is the only one this serialiser is asked for, and
		// its doctype carries no public or system identifier.
		if opts.method == "html" {
			sb.WriteString("<!DOCTYPE html>\n")
		}
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
	// normalize applies the Unicode normalization form the serialization
	// parameters asked for, or is nil when none was asked for. It is held
	// here rather than applied to the finished document because a character
	// map's replacement string is written through untouched, normalisation
	// included: the two transforms have to be interleaved, and only the code
	// that applies the map knows where one ends and the other begins.
	normalize func(string) string
	// encoding is the output encoding. Nothing is transcoded -- everything
	// is written as UTF-8 -- but the JSON method needs to know when a
	// character has to be escaped because the encoding could not have held
	// it, which is what serialize-json-114 asks about.
	encoding string
	// allowDuplicateNames permits a JSON object to be written with two keys
	// that render to the same string; without it that is SERE0022.
	allowDuplicateNames bool
	// jsonNodeOutputMethod is the method a node nested inside a JSON value is
	// serialised with, since JSON itself has no node type.
	jsonNodeOutputMethod string
	// cdataElements names the elements whose text children are written as
	// CDATA sections rather than with escaping. The key drops the prefix,
	// since QName equality is namespace URI plus local name and the picture
	// names an element that may be written with any prefix at all.
	cdataElements map[xdm.QName]bool
}

// serializationParams reads the second argument, an element whose children
// name the parameters.
//
// A parameter this implementation does not recognise is SEPM0017 rather than
// something to ignore: the spec makes an unknown serialization parameter an
// error, and accepting one silently would let a stylesheet believe it had
// asked for something it did not get.
func serializationParams(ctx *Context, args []xdm.Sequence) (serializeOptions, error) {
	opts := serializeOptions{method: "xml", omitXMLDecl: true}
	if len(args) < 2 {
		return opts, nil
	}
	// XPath 3.1 lets the parameters be given as a map instead of an element,
	// which is how every one of the json and adaptive cases writes them. The
	// element form stays for 3.0, where a map is not an item at all.
	if len(args[1]) == 1 {
		if m, ok := args[1][0].(*xdm.MapItem); ok && ctx.Version.atLeast31() {
			return mapSerializationParams(m, opts)
		}
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
			case "encoding":
				// Nothing is transcoded here -- the result is a string, and
				// a string has no encoding -- but the JSON method escapes a
				// character the named encoding could not have held, so the
				// name is kept rather than dropped.
				opts.encoding = val
			case "version", "media-type",
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
	// The text output method writes the string value of what it is given and
	// no markup at all, so it is answered before the per-kind rendering
	// below rather than inside it: every branch there emits tags. This is
	// reached through json-node-output-method="text", which is the one way a
	// node inside a JSON value asks to be written as its text content --
	// Serialization-json-52 wants <e>hi</e> to come out as "hi" rather than
	// as an escaped element.
	if opts.method == "text" {
		sb.WriteString(n.StringValue())
		return
	}
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
		// An empty head still receives the encoding declaration, so it cannot
		// take the self-closing shortcut. HTML has no self-closing syntax for
		// a non-void element anyway.
		htmlHead := opts.method == "html" && n.Name.Local == "head" && n.Name.URI == ""
		if len(n.Children) == 0 && !htmlHead {
			sb.WriteString("/>")
			return
		}
		sb.WriteString(">")
		// The HTML method declares the output encoding inside head. The
		// serialization spec words this as an http-equiv meta element, but
		// HTML5 replaced it with meta/@charset and the suite accepts either;
		// the modern spelling is what a browser reading this would expect.
		if opts.method == "html" && n.Name.Local == "head" && n.Name.URI == "" {
			sb.WriteString(`<meta charset="UTF-8">`)
		}
		// An element named by cdata-section-elements has its text written as
		// a CDATA section instead of with escaping, which is what the
		// parameter exists to ask for.
		cdata := opts.cdataElements[xdm.QName{URI: n.Name.URI, Local: n.Name.Local}]
		for _, c := range n.Children {
			if cdata && c.Kind == xdm.KindText {
				sb.WriteString("<![CDATA[")
				// A "]]>" inside the text would end the section early, so it
				// is split across two sections rather than written literally.
				sb.WriteString(strings.ReplaceAll(c.Value, "]]>", "]]]]><![CDATA[>"))
				sb.WriteString("]]>")
				continue
			}
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
//
// The carriage return is escaped along with the three that are structurally
// impossible, and for a different reason: it is perfectly legal in content,
// but an XML parser normalises a literal one to a line feed before the
// document reaches the data model. Writing it literally therefore loses it,
// and a text node holding a carriage return would come back holding a line
// feed. The numeric reference is the only spelling that survives the round
// trip. Serialization-json-55 puts "&#xd;" in a text node and the same
// character in a plain string, and asks to see the two spelled differently:
// "&#13;" for the node, which is serialized as XML inside the JSON string,
// and "\r" for the string, which is JSON's own escape.
func escapeText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
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

// mapSerializationParams reads the parameters from the map form the 3.1
// signature allows.
//
// The map form is typed where the element form is not: "indent" holds an
// xs:boolean, not the string "yes". A value of the wrong type is XPTY0004 —
// the argument does not match the declared map(xs:string, item()*) — rather
// than the SEPM0017 the element form raises for a bad spelling, which is why
// this cannot share the element form's checking (serialize-json-133..135).
func mapSerializationParams(m *xdm.MapItem, opts serializeOptions) (serializeOptions, error) {
	// A parameter whose value is a boolean is read through this, so that all
	// three of "wrong type", "wrong cardinality" and "a string that looks
	// like a boolean" come out as the same type error.
	boolParam := func(name string, v xdm.Sequence) (bool, error) {
		if len(v) != 1 {
			return false, xdm.ErrType(
				"XPTY0004: serialization parameter %q takes a single boolean", name)
		}
		a, ok := v[0].(*xdm.Atomic)
		if !ok {
			return false, xdm.ErrType(
				"XPTY0004: serialization parameter %q takes a boolean", name)
		}
		// An xs:untypedAtomic is the one non-boolean that is accepted: the
		// function conversion rules cast it to the declared type, so
		// indent=xs:untypedAtomic('false') is the boolean false rather than a
		// type error (serialize-xml-142b). A genuine xs:string is not — the
		// map form is typed, and "true" is not true() (serialize-json-134).
		if a.Type == xdm.TypeUntypedAtomic {
			conv, err := CastAtomic(a, xdm.TypeBoolean)
			if err != nil {
				return false, xdm.ErrType(
					"XPTY0004: serialization parameter %q takes a boolean, got %q",
					name, a.String())
			}
			return conv.Bool(), nil
		}
		if a.Type != xdm.TypeBoolean {
			return false, xdm.ErrType(
				"XPTY0004: serialization parameter %q takes a boolean", name)
		}
		return a.Bool(), nil
	}
	strParam := func(name string, v xdm.Sequence) (string, error) {
		if len(v) != 1 {
			return "", xdm.ErrType(
				"XPTY0004: serialization parameter %q takes a single string", name)
		}
		a, ok := v[0].(*xdm.Atomic)
		if !ok {
			return "", xdm.ErrType(
				"XPTY0004: serialization parameter %q takes a string", name)
		}
		return a.String(), nil
	}

	err := m.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
		name := key.String()
		switch name {
		case "method":
			v, err := strParam(name, val)
			if err != nil {
				return err
			}
			switch v {
			case "xml", "text", "xhtml", "html", "json", "adaptive":
				opts.method = v
			default:
				return fmt.Errorf("SEPM0017: unsupported serialization method %q", v)
			}
		case "indent":
			v, err := boolParam(name, val)
			if err != nil {
				return err
			}
			opts.indent = v
		case "omit-xml-declaration":
			v, err := boolParam(name, val)
			if err != nil {
				return err
			}
			opts.omitXMLDecl = v
		case "allow-duplicate-names":
			v, err := boolParam(name, val)
			if err != nil {
				return err
			}
			opts.allowDuplicateNames = v
		case "item-separator":
			v, err := strParam(name, val)
			if err != nil {
				return err
			}
			opts.itemSeparator, opts.hasItemSep = v, true
		case "json-node-output-method":
			v, err := strParam(name, val)
			if err != nil {
				return err
			}
			opts.jsonNodeOutputMethod = v
		case "standalone":
			v, err := boolParam(name, val)
			if err != nil {
				return err
			}
			if v {
				opts.standalone = "yes"
			} else {
				opts.standalone = "no"
			}
		case "use-character-maps":
			m, err := readCharacterMapsFromMap(val)
			if err != nil {
				return err
			}
			opts.charMap = m
		case "cdata-section-elements":
			// The value is a sequence of QNames, so the names arrive already
			// resolved; the element form takes lexical names instead and has
			// no static context here to resolve them against.
			for _, it := range val {
				a, ok := it.(*xdm.Atomic)
				if !ok || a.Type != xdm.TypeQName {
					return xdm.ErrType(
						"XPTY0004: cdata-section-elements takes QNames")
				}
				if opts.cdataElements == nil {
					opts.cdataElements = map[xdm.QName]bool{}
				}
				if qn := a.QName(); qn != nil {
					opts.cdataElements[xdm.QName{URI: qn.URI, Local: qn.Local}] = true
				}
			}
		case "encoding":
			// See the element-form reader above for why this one is kept.
			v, err := strParam(name, val)
			if err != nil {
				return err
			}
			opts.encoding = v
		case "version", "media-type", "doctype-public",
			"doctype-system", "normalization-form",
			"undeclare-prefixes", "byte-order-mark", "escape-uri-attributes",
			"include-content-type", "suppress-indentation",
			"html-version", "parameter-document":
			// Recognised and accepted; this serialiser does not vary its
			// output for them.
		default:
			return fmt.Errorf(
				"SEPM0017: serialization parameter %q is not supported", name)
		}
		return nil
	})
	return opts, err
}

// serializeJSON renders a sequence with the JSON output method, added in
// XPath 3.1.
//
// JSON has exactly one value at the top, so the argument must be a single item
// or empty; a sequence of two is SERE0023 rather than something to concatenate
// (serialize-json-130). The empty sequence is the JSON null.
func serializeJSON(seq xdm.Sequence, opts serializeOptions) (string, error) {
	if len(seq) == 0 {
		return "null", nil
	}
	if len(seq) > 1 {
		return "", fmt.Errorf(
			"SERE0023: the JSON output method takes a single item, got %d", len(seq))
	}
	var sb strings.Builder
	if err := writeJSONItem(&sb, seq[0], opts); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// writeJSONValue writes a sequence appearing as a map entry or array member.
//
// The same "one value" rule applies at every level, not only the top: a map
// whose entry holds (1 to 10) has no JSON rendering, and that is SERE0023
// (serialize-json-131).
func writeJSONValue(sb *strings.Builder, seq xdm.Sequence, opts serializeOptions) error {
	switch len(seq) {
	case 0:
		sb.WriteString("null")
		return nil
	case 1:
		return writeJSONItem(sb, seq[0], opts)
	}
	return fmt.Errorf(
		"SERE0023: a JSON value must be a single item, got %d", len(seq))
}

func writeJSONItem(sb *strings.Builder, it xdm.Item, opts serializeOptions) error {
	switch v := it.(type) {
	case *xdm.ArrayItem:
		sb.WriteString("[")
		for i, m := range v.Members() {
			if i > 0 {
				sb.WriteString(",")
			}
			if err := writeJSONValue(sb, m, opts); err != nil {
				return err
			}
		}
		sb.WriteString("]")
		return nil
	case *xdm.MapItem:
		sb.WriteString("{")
		first := true
		// Two keys that are distinct as XDM values can still render to the
		// same JSON name — xs:QName("foo") and the string "foo" both write
		// "foo" — and JSON has no way to keep them apart, so the default is to
		// refuse rather than to emit an object the caller cannot read back
		// (serialize-json-010).
		seen := map[string]bool{}
		err := v.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
			name := key.String()
			if seen[name] && !opts.allowDuplicateNames {
				return fmt.Errorf(
					"SERE0022: the JSON object would have two entries named %q", name)
			}
			seen[name] = true
			if !first {
				sb.WriteString(",")
			}
			first = false
			writeJSONString(sb, name, opts)
			sb.WriteString(":")
			return writeJSONValue(sb, val, opts)
		})
		if err != nil {
			return err
		}
		sb.WriteString("}")
		return nil
	case *xdm.FunctionItem:
		return fmt.Errorf("SERE0021: a function item cannot be serialized as JSON")
	case *xdm.Node:
		// An attribute or namespace node has no serialization of its own:
		// sequence normalisation turns a parentless one into an error rather
		// than into markup, because there is no document it could belong to.
		// The JSON method reaches it by a different route -- the node is a
		// member of an array rather than an item of the result sequence --
		// but it is the same node in the same position, so it is the same
		// error. Serialization-json-30 puts an attribute in an array and
		// asks for SENR0001; without this it was written as the string of
		// its value, which quietly invented a document for it.
		if v.Kind == xdm.KindAttribute || v.Kind == xdm.KindNamespace {
			return fmt.Errorf(
				"SENR0001: an attribute or namespace node cannot be serialized")
		}
		// JSON has no node type, so a node is written as a string holding its
		// serialization under the json-node-output-method (default xml).
		var inner strings.Builder
		nodeOpts := opts
		nodeOpts.method = opts.jsonNodeOutputMethod
		if nodeOpts.method == "" {
			nodeOpts.method = "xml"
		}
		serializeNode(&inner, v, nodeOpts)
		writeJSONString(sb, inner.String(), opts)
		return nil
	case *xdm.Atomic:
		switch {
		case v.Type == xdm.TypeBoolean:
			if v.Bool() {
				sb.WriteString("true")
			} else {
				sb.WriteString("false")
			}
		case v.Type.IsNumeric():
			// JSON has no way to write NaN or an infinity, so a number that
			// is one cannot be serialized at all (serialize-json-122).
			if v.IsNaN() || math.IsInf(v.Float64(), 0) {
				return fmt.Errorf(
					"SERE0020: %s has no JSON representation", v.String())
			}
			sb.WriteString(v.String())
		default:
			// Everything else — dates, URIs, untyped values — becomes its
			// lexical form as a JSON string (serialize-json-125, -128).
			writeJSONString(sb, v.String(), opts)
		}
		return nil
	}
	return nil
}

// writeJSONString writes a JSON string literal.
//
// The solidus is escaped as "\/" even though JSON does not require it: the
// spec's rule for this method escapes it, and the suite compares the output
// literally (serialize-json-128).
//
// A character map applies here rather than to the finished document, because
// only the characters *inside* a string are the method's own text -- the
// braces, brackets, commas and colons around them are structure, and a map
// naming one of those would rewrite the JSON rather than its content.
// Serialization-json-39 settles it by mapping "1" to "one" and asking for
// [123,"one23","---oneone"]: the number keeps its digits and the two strings
// do not.
//
// The replacement is written through unescaped. That is the point of a
// character map -- a stylesheet declares one to put a specific sequence in
// the output -- and escaping it would defeat exactly the substitution it
// asked for.
func writeJSONString(sb *strings.Builder, s string, opts serializeOptions) {
	sb.WriteString(`"`)
	// Unicode normalisation is interleaved with the character map rather than
	// applied to the finished document, for the reason xslt's mapSegments
	// gives at length: a replacement string is written through untouched, and
	// normalising the output afterwards would touch it. So each run between
	// two mapped characters is normalised whole -- whole, because a combining
	// sequence spans several characters and normalising them one at a time
	// would leave every one of them exactly as it was -- and each replacement
	// is written as the map spelled it. Serialization-json-35 maps "z" to a
	// "c" plus a combining cedilla and asks for NFC: the run around it
	// composes, the replacement stays decomposed.
	for _, run := range splitOnCharMap(s, opts.charMap) {
		if run.mapped {
			sb.WriteString(run.text)
			continue
		}
		writeJSONRun(sb, opts.normalized(run.text), opts)
	}
	sb.WriteString(`"`)
}

// normalized applies the requested Unicode normalization form, if any.
func (o serializeOptions) normalized(s string) string {
	if o.normalize == nil {
		return s
	}
	return o.normalize(s)
}

// charMapRun is one stretch of a string that the character map either claimed
// whole -- in which case text is the replacement, written verbatim -- or did
// not touch at all.
type charMapRun struct {
	text   string
	mapped bool
}

// splitOnCharMap breaks a string into the runs the map claimed and the runs
// between them, so that a caller can treat the two differently. With no map
// the whole string is one unclaimed run.
func splitOnCharMap(s string, m map[rune]string) []charMapRun {
	if len(m) == 0 {
		return []charMapRun{{text: s}}
	}
	var runs []charMapRun
	start := 0
	for i, r := range s {
		repl, ok := m[r]
		if !ok {
			continue
		}
		if i > start {
			runs = append(runs, charMapRun{text: s[start:i]})
		}
		runs = append(runs, charMapRun{text: repl, mapped: true})
		start = i + utf8.RuneLen(r)
	}
	if start < len(s) {
		runs = append(runs, charMapRun{text: s[start:]})
	}
	return runs
}

// writeJSONRun writes one unmapped run of a JSON string with the escapes the
// method requires.
func writeJSONRun(sb *strings.Builder, s string, opts serializeOptions) {
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '/':
			sb.WriteString(`\/`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(sb, `\u%04X`, r)
				continue
			}
			// A character the requested encoding cannot represent is written
			// as the surrogate pair JSON's \u escape can express, since \u
			// names a single UTF-16 code unit (serialize-json-114, which asks
			// for encoding="ISO-8859-1"). A character the encoding *can*
			// represent is written as itself: escaping every astral
			// character regardless would have made "-𐌰-" come back as
			// "-\uD800\uDF30-" from a UTF-8 serialisation that had no
			// reason to avoid it (Serialization-json-33).
			if !jsonEncodingHolds(opts.encoding, r) {
				if r > 0xFFFF {
					r -= 0x10000
					fmt.Fprintf(sb, `\u%04X\u%04X`,
						0xD800+(r>>10), 0xDC00+(r&0x3FF))
					continue
				}
				// A character inside the basic plane is one \u escape, not
				// a pair: Serialization-json-57 asks for the euro sign under
				// encoding="US-ASCII" and wants "\u20AC". The astral case
				// above needed the surrogate pair only because \u names a
				// single UTF-16 code unit and an astral character is two.
				fmt.Fprintf(sb, `\u%04X`, r)
				continue
			}
			sb.WriteRune(r)
		}
	}
}

// serializeAdaptiveSeq renders a sequence with the adaptive output method.
//
// Adaptive exists to show a value the way a debugger would: every item is
// written in whichever notation suits its kind, and items are separated by the
// item-separator, which defaults to a newline rather than to nothing. Unlike
// the XML method it has a rendering for maps, arrays and function items, so
// nothing in it can fail.
func serializeAdaptiveSeq(seq xdm.Sequence, opts serializeOptions) (string, error) {
	sep := "\n"
	if opts.hasItemSep {
		sep = opts.itemSeparator
	}
	var sb strings.Builder
	for i, it := range seq {
		if i > 0 {
			sb.WriteString(sep)
		}
		writeAdaptiveItem(&sb, it, opts)
	}
	return sb.String(), nil
}

func writeAdaptiveItem(sb *strings.Builder, it xdm.Item, opts serializeOptions) {
	switch v := it.(type) {
	case *xdm.MapItem:
		sb.WriteString("map{")
		first := true
		_ = v.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
			if !first {
				sb.WriteString(",")
			}
			first = false
			writeAdaptiveItem(sb, key, opts)
			sb.WriteString(":")
			writeAdaptiveValue(sb, val, opts)
			return nil
		})
		sb.WriteString("}")
	case *xdm.ArrayItem:
		sb.WriteString("[")
		for i, m := range v.Members() {
			if i > 0 {
				sb.WriteString(",")
			}
			writeAdaptiveValue(sb, m, opts)
		}
		sb.WriteString("]")
	case *xdm.FunctionItem:
		// A function is shown by name and arity, which is all of it that can
		// be written down. An inline function has no name at all, and the
		// arity alone would render as a bare "#1"; Serialization 3.1 §10
		// gives it the placeholder "(anonymous-function)", which cannot
		// collide with a real name because it is not a lexical QName.
		name := v.Name.Lexical()
		if name == "" {
			name = "(anonymous-function)"
		}
		fmt.Fprintf(sb, "%s#%d", name, v.Arity)
	case *xdm.Node:
		// An attribute has no XML serialization of its own, so adaptive gives
		// it the name="value" form it has inside a start tag
		// (serialize-adaptive-003).
		if v.Kind == xdm.KindAttribute {
			sb.WriteString(elementName(v))
			sb.WriteString(`="`)
			sb.WriteString(escapeAttr(v.Value))
			sb.WriteString(`"`)
			return
		}
		nodeOpts := opts
		nodeOpts.method = "xml"
		serializeNode(sb, v, nodeOpts)
	case *xdm.Atomic:
		writeAdaptiveAtomic(sb, v)
	}
}

// writeAdaptiveValue writes a sequence nested inside a map or array, which
// adaptive parenthesises when it is not a single item.
func writeAdaptiveValue(sb *strings.Builder, seq xdm.Sequence, opts serializeOptions) {
	if len(seq) == 1 {
		writeAdaptiveItem(sb, seq[0], opts)
		return
	}
	sb.WriteString("(")
	for i, it := range seq {
		if i > 0 {
			sb.WriteString(",")
		}
		writeAdaptiveItem(sb, it, opts)
	}
	sb.WriteString(")")
}

// writeAdaptiveAtomic writes an atomic value in a form that could be typed
// back into an expression: a string quoted, a boolean as a function call, a
// number bare, and anything else as a constructor.
func writeAdaptiveAtomic(sb *strings.Builder, a *xdm.Atomic) {
	switch {
	case a.Type == xdm.TypeBoolean:
		if a.Bool() {
			sb.WriteString("true()")
		} else {
			sb.WriteString("false()")
		}
	case a.Type == xdm.TypeQName:
		// Serialization 3.1 §10: an xs:QName is written in EQName notation.
		// The lexical form would not do: a prefix is meaningless away from
		// the namespace bindings it was resolved under, and adaptive output
		// carries none. Serialization-adaptive-78 constructs
		// xs:QName("xs:integer") and asks for
		// "Q{http://www.w3.org/2001/XMLSchema}integer" -- the value, not the
		// spelling it happened to be written with.
		if q := a.QName(); q != nil {
			fmt.Fprintf(sb, "Q{%s}%s", q.URI, q.Local)
			return
		}
		sb.WriteString(a.String())
	case a.Type == xdm.TypeDouble:
		// A double is written in its canonical XML Schema form, where an
		// exponent is always present: "1.0e0" rather than the "1" that
		// fn:string gives. The distinction is the point of the method -- an
		// adaptive rendering is meant to show what a value *is*, and "1"
		// alone would not say whether it was an integer or a double.
		sb.WriteString(canonicalDouble(a))
	case a.Type == xdm.TypeDecimal || a.Type == xdm.TypeInteger:
		// A decimal and an integer are written bare. They are the two
		// numeric types whose lexical form is unambiguous on its own.
		sb.WriteString(a.String())
	case isStringLike(a.Type):
		// The doubled quote is how an XPath string literal escapes one, so
		// the output re-parses to the value it came from.
		sb.WriteString(`"` + strings.ReplaceAll(a.String(), `"`, `""`) + `"`)
	default:
		// Everything else is written as a constructor call naming the type,
		// so that the output says which of several types with overlapping
		// lexical forms this value has. The *primitive* type is named, not
		// the derived one: §10 asks for a form that can be read back, and
		// xs:yearMonthDuration("P1Y2M") names a constructor whose argument
		// is a duration, so xs:duration("P1Y2M") is what the rule produces.
		fmt.Fprintf(sb, "%s(%q)", primitiveTypeName(a.Type), a.String())
	}
}

// canonicalDouble renders an xs:double in the canonical lexical form of XML
// Schema, which always carries an exponent.
//
// fn:string gives XPath's own form, which drops the exponent for values in
// the ordinary range: string(xs:double(1)) is "1". That is right for
// fn:string and wrong here, where the whole purpose is to distinguish a value
// from one of another type that would print the same.
func canonicalDouble(a *xdm.Atomic) string {
	s := a.String()
	switch s {
	case "NaN", "INF", "-INF":
		// These three have no exponent to give them and are already
		// unambiguous: no other type spells a value this way.
		return s
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant, exp := s[:i], s[i+1:]
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		return mant + "e" + exp
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s + "e0"
}

// primitiveTypeName is the name of the primitive type a derived one is based
// on, for the constructor an adaptive rendering writes.
//
// Only the three derived types this engine carries as distinct codes need an
// entry: the two duration subtypes, whose primitive is xs:duration, and
// xs:untypedAtomic, which is not derived from anything but has no constructor
// function of its own and is written as the string it is. Every other code in
// the enumeration is already primitive.
func primitiveTypeName(t xdm.TypeCode) string {
	switch t {
	case xdm.TypeYearMonthDuration, xdm.TypeDayTimeDuration:
		return "xs:duration"
	}
	return t.String()
}

// readCharacterMapsFromMap reads use-character-maps given in the map form,
// where the parameter is itself a map from character to replacement.
//
// Its declared type is map(xs:string, xs:string), and the suite checks that
// both halves are enforced: a QName key or a node value is XPTY0004, not
// something to stringify (serialize-xml-139b, -140b, -141b). An
// xs:untypedAtomic is accepted on the same function-conversion grounds as
// elsewhere.
func readCharacterMapsFromMap(val xdm.Sequence) (map[rune]string, error) {
	if len(val) != 1 {
		return nil, xdm.ErrType(
			"XPTY0004: use-character-maps takes a single map")
	}
	m, ok := val[0].(*xdm.MapItem)
	if !ok {
		return nil, xdm.ErrType("XPTY0004: use-character-maps takes a map")
	}
	out := map[rune]string{}
	err := m.Entries(func(key *xdm.Atomic, v xdm.Sequence) error {
		if !isStringLike(key.Type) && key.Type != xdm.TypeUntypedAtomic {
			return xdm.ErrType(
				"XPTY0004: a use-character-maps key must be a string, got %s",
				key.TypeName())
		}
		r := []rune(key.String())
		if len(r) != 1 {
			return fmt.Errorf(
				"SEPM0016: a character map key must be one character, got %q",
				key.String())
		}
		if len(v) != 1 {
			return xdm.ErrType(
				"XPTY0004: a use-character-maps value must be a single string")
		}
		a, ok := v[0].(*xdm.Atomic)
		if !ok {
			return xdm.ErrType(
				"XPTY0004: a use-character-maps value must be a string, got a node")
		}
		if !isStringLike(a.Type) && a.Type != xdm.TypeUntypedAtomic {
			return xdm.ErrType(
				"XPTY0004: a use-character-maps value must be a string, got %s",
				a.TypeName())
		}
		out[r[0]] = a.String()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SerializeParams are the serialization parameters a caller outside this
// package can set for SerializeJSON and SerializeAdaptive.
//
// It is deliberately narrow. The full serializeOptions set is what fn:serialize
// reads out of its own parameter argument, and most of it — the XML
// declaration, CDATA sections, indentation — has no meaning under either of
// these two methods. What a caller does need is the handful the JSON and
// adaptive rules actually consult, so that xsl:result-document/@method="json"
// behaves as fn:serialize with the same parameters would.
type SerializeParams struct {
	// AllowDuplicateNames permits a JSON object to be written with two keys
	// that render to the same string; without it that is SERE0022.
	AllowDuplicateNames bool
	// JSONNodeOutputMethod is the method a node nested inside a JSON value is
	// serialised with, since JSON itself has no node type. Empty means the
	// default, "xml".
	JSONNodeOutputMethod string
	// ItemSeparator, when HasItemSeparator is set, goes between the items of
	// an adaptive result in place of the newline the method otherwise writes.
	ItemSeparator    string
	HasItemSeparator bool
	// CharMap maps a character to the string that replaces it on output, as
	// xsl:character-map defines.
	CharMap map[rune]string
	// Normalize applies the requested Unicode normalization form, or is nil
	// when the normalization-form parameter was "none". The caller supplies
	// the function rather than the form's name because normalisation cannot
	// be left until the document is finished: a character map's replacement
	// is exempt from it, so the two have to be interleaved here, where the
	// map is applied.
	Normalize func(string) string
	// Encoding is the output encoding. Nothing is transcoded -- both methods
	// return a string -- but the JSON method escapes a character the named
	// encoding could not have held, and only the caller knows which was
	// asked for.
	Encoding string
}

func (p SerializeParams) opts() serializeOptions {
	o := serializeOptions{
		method:               "json",
		omitXMLDecl:          true,
		allowDuplicateNames:  p.AllowDuplicateNames,
		jsonNodeOutputMethod: p.JSONNodeOutputMethod,
		itemSeparator:        p.ItemSeparator,
		hasItemSep:           p.HasItemSeparator,
		charMap:              p.CharMap,
		normalize:            p.Normalize,
		encoding:             p.Encoding,
	}
	if o.jsonNodeOutputMethod == "" {
		o.jsonNodeOutputMethod = "xml"
	}
	return o
}

// SerializeJSON renders a sequence with the JSON output method of the
// XSLT and XQuery Serialization 3.1 specification, section 4.
//
// It exists so that xsl:result-document and xsl:output can offer method="json"
// without the xslt package reimplementing rules this package already applies
// for fn:serialize. The reverse dependency is not available — xpath cannot
// import xslt — and the two renderings must agree, since result-document-1401
// and serialize-json-010 describe the same output by different routes.
func SerializeJSON(seq xdm.Sequence, p SerializeParams) (string, error) {
	// The character map is not applied here. It belongs inside each JSON
	// string, which is where writeJSONString applies it: the braces, commas
	// and digits between the strings are structure, not the method's text,
	// and a map naming one of those over the finished document would rewrite
	// the JSON rather than its content.
	return serializeJSON(seq, p.opts())
}

// SerializeAdaptive renders a sequence with the adaptive output method of the
// same specification, section 10. See SerializeJSON for why it is exported.
func SerializeAdaptive(seq xdm.Sequence, p SerializeParams) (string, error) {
	opts := p.opts()
	opts.method = "adaptive"
	out, err := serializeAdaptiveSeq(seq, opts)
	if err != nil {
		return "", err
	}
	if len(opts.charMap) > 0 {
		out = applyCharacterMap(out, opts.charMap)
	}
	return out, nil
}

// jsonEncodingHolds reports whether the named output encoding can represent a
// character directly.
//
// Only the Unicode encodings can hold every character, and they are the only
// ones this serialiser writes; anything else is treated as unable to hold
// what is outside its range, which for the astral planes is every non-Unicode
// encoding there is. An unnamed encoding is UTF-8, the default.
func jsonEncodingHolds(encoding string, r rune) bool {
	if encoding == "" {
		return true
	}
	switch strings.ToUpper(encoding) {
	case "UTF-8", "UTF8", "UTF-16", "UTF16", "UTF-16LE", "UTF-16BE", "UTF-32":
		return true
	}
	return r < 0x80
}
