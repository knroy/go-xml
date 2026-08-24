package xdm

import (
	"encoding/xml"
	"strings"
)

// attDeclaredType is one attribute whose ATTLIST declaration gives it a type
// the data model can observe: ID, IDREF or IDREFS.
//
// Section 4.5 of the XSLT specification and F&O's fn:id both work from the
// attribute's *type*, not its name, and a DTD is the other way — besides a
// schema — that a document says which attributes are IDs. Parsing the type and
// then discarding it made fn:id fall back to guessing from the name.
type attDeclaredType struct {
	element string
	attr    string
	typ     string
}

// attDefault is one defaulted attribute from an ATTLIST declaration.
type attDefault struct {
	element string
	name    string
	value   string
}

// parseAttListDefaults extracts the defaulted attributes from a DOCTYPE
// internal subset.
//
// This is deliberately *not* a DTD parser. It reads the one construct whose
// absence is observable in the data model — an ATTLIST giving an attribute a
// #FIXED or literal default, which an XML processor is required to add to
// every matching element — and ignores everything else, entities included.
//
// Entities are the reason DOCTYPE is refused by default: expanding them is
// where billion-laughs and XXE live. Nothing here expands anything, resolves
// anything, or reads a file. The subset arrives from encoding/xml as one
// opaque Directive token and is scanned as text, so a declaration this does
// not understand is skipped rather than acted on.
//
// A namespace declaration can arrive this way — "xmlns:xlink CDATA #FIXED
// '...'" is how a DTD supplies a binding — which is why the result has to
// reach the element before its namespaces are built.
func parseAttListDefaults(subset string) []attDefault {
	out, _ := parseAttList(subset)
	return out
}

// parseAttList reads both the defaults and the declared ID types from the
// internal subset.
func parseAttList(subset string) ([]attDefault, []attDeclaredType) {
	var out []attDefault
	var types []attDeclaredType
	for {
		i := strings.Index(subset, "<!ATTLIST")
		if i < 0 {
			return out, types
		}
		subset = subset[i+len("<!ATTLIST"):]
		end := strings.IndexByte(subset, '>')
		if end < 0 {
			return out, types
		}
		body := subset[:end]
		subset = subset[end+1:]

		fields := attListFields(body)
		if len(fields) < 2 {
			continue
		}
		element := fields[0]
		// Each attribute definition is: name, type, then either #REQUIRED,
		// #IMPLIED, #FIXED "value", or a bare "value" default. The type is
		// one token except for a NOTATION or enumeration, which is
		// parenthesised and already collected as one field.
		for i := 1; i+1 < len(fields); {
			name, decl := fields[i], fields[i+1]
			i += 2
			switch decl {
			case "ID", "IDREF", "IDREFS":
				types = append(types, attDeclaredType{element, name, decl})
			}
			switch {
			case decl == "#REQUIRED", decl == "#IMPLIED":
				// No default to supply.
			case decl == "#FIXED":
				if i < len(fields) {
					out = append(out, attDefault{element, name, unquote(fields[i])})
					i++
				}
			case strings.HasPrefix(decl, `"`), strings.HasPrefix(decl, `'`):
				out = append(out, attDefault{element, name, unquote(decl)})
			default:
				// decl was the attribute *type*; the default follows it.
				if i < len(fields) {
					d := fields[i]
					i++
					switch {
					case d == "#REQUIRED", d == "#IMPLIED":
					case d == "#FIXED":
						if i < len(fields) {
							out = append(out, attDefault{element, name, unquote(fields[i])})
							i++
						}
					case strings.HasPrefix(d, `"`), strings.HasPrefix(d, `'`):
						out = append(out, attDefault{element, name, unquote(d)})
					}
				}
			}
		}
	}
}

// attListFields splits an ATTLIST body into tokens, keeping a quoted value or
// a parenthesised enumeration as one field.
func attListFields(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				out = append(out, s[i:])
				return out
			}
			out = append(out, s[i:i+1+j+1])
			i += 1 + j + 1
		case c == '(':
			j := strings.IndexByte(s[i:], ')')
			if j < 0 {
				out = append(out, s[i:])
				return out
			}
			out = append(out, s[i:i+j+1])
			i += j + 1
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\n\r", rune(s[j])) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// applyAttDefaults adds any declared default the element does not carry.
//
// An attribute present in the document always wins: a default supplies a value
// that was omitted, it does not override one that was given. Even a #FIXED
// default only constrains what may be written, and validating that constraint
// is DTD validation, which this parser does not do.
func applyAttDefaults(t xml.StartElement, defs []attDefault) xml.StartElement {
	var add []xml.Attr
	for _, d := range defs {
		if d.element != t.Name.Local {
			continue
		}
		if hasAttrNamed(t.Attr, d.name) {
			continue
		}
		add = append(add, xml.Attr{Name: attrNameOf(d.name), Value: d.value})
	}
	if len(add) == 0 {
		return t
	}
	// Copy rather than append in place: encoding/xml reuses the attribute
	// slice across tokens, so appending would leak a defaulted attribute into
	// the next element it decodes.
	attrs := make([]xml.Attr, len(t.Attr), len(t.Attr)+len(add))
	copy(attrs, t.Attr)
	t.Attr = append(attrs, add...)
	return t
}

func hasAttrNamed(attrs []xml.Attr, name string) bool {
	for _, a := range attrs {
		if attrLexical(a.Name) == name {
			return true
		}
	}
	return false
}

// attrNameOf turns the lexical name written in the DTD back into the shape
// encoding/xml produces, so that buildElement sees a namespace declaration the
// same way whether it was written on the element or defaulted from an ATTLIST.
func attrNameOf(lexical string) xml.Name {
	if i := strings.IndexByte(lexical, ':'); i >= 0 {
		prefix, local := lexical[:i], lexical[i+1:]
		if prefix == "xmlns" {
			return xml.Name{Space: "xmlns", Local: local}
		}
		return xml.Name{Space: prefix, Local: local}
	}
	if lexical == "xmlns" {
		return xml.Name{Local: "xmlns"}
	}
	return xml.Name{Local: lexical}
}

// attrLexical is the inverse: the name as it appears in the document.
func attrLexical(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// applyAttTypes stamps the ID, IDREF and IDREFS annotations a DTD declares.
//
// A DTD is one of the two ways a document says which of its attributes are
// IDs, and fn:id is defined over the attribute's type rather than its name.
// Recording the annotation here puts a DTD-validated document on the same
// footing as a schema-validated one, so fn:id finds the same attributes in
// both.
func applyAttTypes(el *Node, types []attDeclaredType) {
	for _, t := range types {
		if t.element != el.Name.Lexical() && t.element != el.Name.Local {
			continue
		}
		for _, a := range el.Attrs {
			if a.Name.Lexical() != t.attr && a.Name.Local != t.attr {
				continue
			}
			if a.TypeAnnotation == "" {
				// SetTypeAnnotation rather than a bare assignment: a DTD
				// declaring ID/IDREF/IDREFS is one of the two ways a
				// document establishes the is-id and is-idrefs properties,
				// and those must survive input-type-annotations="strip"
				// even though the annotation itself does not.
				a.SetTypeAnnotation(t.typ)
			}
		}
	}
}

// parseElementOnlyDecls returns the set of element names whose <!ELEMENT>
// declaration gives them an element-only content model.
//
// XML §3.2.1 calls such a model "element content", and §2.10 makes the
// whitespace between its children *ignorable*: a validating processor knows
// no character data can appear there, so the text nodes carry no information.
// XSLT 2.0 §4.4 turns that into a rule with priority over the stylesheet:
// whitespace-only text in element-only content is stripped regardless of
// xsl:preserve-space, because the schema- or DTD-derived fact outranks the
// stylesheet-declared preference.
//
// Like parseAttList this is deliberately not a DTD parser. It reads the one
// bit of an <!ELEMENT> declaration the data model can observe — whether the
// model mentions #PCDATA or is EMPTY/ANY — and skips anything it does not
// understand. A model it cannot classify is treated as NOT element-only, so a
// misparse loses the optimisation rather than deleting content.
func parseElementOnlyDecls(subset string) map[string]bool {
	var out map[string]bool
	for {
		i := strings.Index(subset, "<!ELEMENT")
		if i < 0 {
			return out
		}
		subset = subset[i+len("<!ELEMENT"):]
		end := strings.IndexByte(subset, '>')
		if end < 0 {
			return out
		}
		body := subset[:end]
		subset = subset[end+1:]

		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		model := strings.Join(fields[1:], " ")
		if !isElementOnlyModel(model) {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[name] = true
	}
}

// isElementOnlyModel reports whether a content model is element content.
//
// EMPTY and ANY are not: EMPTY admits nothing at all, and ANY admits character
// data, so neither makes surrounding whitespace ignorable. A mixed model is
// written (#PCDATA | ...) and is excluded by the same reasoning — there the
// whitespace is real content. What remains is a parenthesised model naming
// only elements, which is exactly element content.
func isElementOnlyModel(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || model == "EMPTY" || model == "ANY" {
		return false
	}
	if !strings.HasPrefix(model, "(") {
		return false
	}
	return !strings.Contains(model, "#PCDATA")
}

// stripIgnorableWhitespace removes the whitespace-only text children of an
// element whose DTD content model is element-only.
//
// This runs independently of, and before, the stylesheet's own strip-space
// rules: it is not a preference that xsl:preserve-space can turn off. An
// explicit xml:space="preserve" is still honoured, since XML §2.10 makes that
// the document's own statement about its whitespace.
func stripIgnorableWhitespace(el *Node, elementOnly map[string]bool) {
	if !elementOnly[el.Name.Lexical()] && !elementOnly[el.Name.Local] {
		return
	}
	if a := el.Attr(NSXML, "space"); a != nil && a.Value == "preserve" {
		return
	}
	kept := el.Children[:0]
	for _, c := range el.Children {
		if c.Kind == KindText && IsXMLWhitespace(c.Value) {
			continue
		}
		kept = append(kept, c)
	}
	el.Children = kept
}
