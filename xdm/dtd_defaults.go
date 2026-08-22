package xdm

import (
	"encoding/xml"
	"strings"
)

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
	var out []attDefault
	for {
		i := strings.Index(subset, "<!ATTLIST")
		if i < 0 {
			return out
		}
		subset = subset[i+len("<!ATTLIST"):]
		end := strings.IndexByte(subset, '>')
		if end < 0 {
			return out
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
