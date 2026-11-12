package xdm

import (
	"sort"
	"strings"

	xml "github.com/knroy/go-xml/v2/internal/xmltok"
)

// attDeclaredType is one attribute whose ATTLIST declaration gives it a type
// other than CDATA. ID, IDREF and IDREFS are observable as annotations; every
// non-CDATA type is observable as a collapsed value (XML 1.0 §3.3.3).
//
// Section 4.5 of the XSLT specification and F&O's fn:id both work from the
// attribute's *type*, not its name, and a DTD is the other way — besides a
// schema — that a document says which attributes are IDs. Parsing the type and
// then discarding it made fn:id fall back to guessing from the name.
type attDeclaredType struct {
	element string
	attr    string
	typ     string
	outside bool // an external markup declaration (see declOrigins)
}

// attDefault is one defaulted attribute from an ATTLIST declaration.
type attDefault struct {
	element string
	name    string
	value   string // as written; normalizeAttDefaults makes it the value

	// at is the index of its ATTLIST among the subset's declarations, and
	// entities maps each general entity the subset declares to the index of
	// its first declaration: a default may refer only to an entity declared
	// before it (XML 1.0 §4.1, WFC: Entity Declared).
	at       int
	entities map[string]int
	outside  bool // an external markup declaration (see declOrigins)
}

// declIndex maps the element name an ATTLIST declaration was written with to
// the positions of its declarations, in order. Every element is matched
// against the declarations, so scanning them all cost a document of n elements
// and m declarations n*m.
type declIndex map[string][]int

func indexDecls(n int, element func(i int) string) declIndex {
	ix := declIndex{}
	for i := range n {
		e := element(i)
		ix[e] = append(ix[e], i)
	}
	return ix
}

// at returns the positions of the declarations that apply to an element
// written prefix:local: those naming it as written and those naming its local
// name alone, in declaration order.
func (ix declIndex) at(prefix, local string) []int {
	byLocal := ix[local]
	if prefix == "" {
		return byLocal
	}
	var buf [64]byte // stays on the stack for a name that fits
	b := append(append(append(buf[:0], prefix...), ':'), local...)
	lexical := ix[string(b)]
	if len(byLocal) == 0 {
		return lexical
	}
	if len(lexical) == 0 {
		return byLocal
	}
	out := make([]int, 0, len(lexical)+len(byLocal))
	for len(lexical) > 0 && len(byLocal) > 0 {
		if lexical[0] < byLocal[0] {
			out, lexical = append(out, lexical[0]), lexical[1:]
		} else {
			out, byLocal = append(out, byLocal[0]), byLocal[1:]
		}
	}
	return append(append(out, lexical...), byLocal...)
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
// anything, or reads a file; a default's entity references are expanded
// later, by normalizeAttDefaults, under the entity table's own bounds. The subset arrives from encoding/xml as one
// opaque Directive token and is scanned as text, so a declaration this does
// not understand is skipped rather than acted on.
//
// A namespace declaration can arrive this way — "xmlns:xlink CDATA #FIXED
// '...'" is how a DTD supplies a binding — which is why the result has to
// reach the element before its namespaces are built.
func parseAttListDefaults(subset string) []attDefault {
	out, _ := parseAttList(subset, nil)
	return out
}

// parseAttList reads both the defaults and the declared ID types from the
// internal subset. outside, when not nil, is declOrigins of subset, and marks
// what an external markup declaration supplies.
func parseAttList(subset string, outside []bool) ([]attDefault, []attDeclaredType) {
	var out []attDefault
	var types []attDeclaredType
	// XML 1.0 §3.3: when an attribute is declared more than once, the first
	// declaration is binding and later ones are ignored. The subset arrives
	// internal part first, so first in the text is first in the DTD.
	seen := map[string]bool{}
	entities := map[string]int{}
	at := -1
	for kw, body := range markupDecls(subset) {
		at++
		if kw == "ENTITY" {
			if f := attListFields(body); len(f) > 0 && f[0] != "%" {
				if _, dup := entities[f[0]]; !dup {
					entities[f[0]] = at
				}
			}
			continue
		}
		if kw != "ATTLIST" {
			continue
		}
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
			key, nOut, nTypes := element+" "+name, len(out), len(types)
			ext := at < len(outside) && outside[at]
			switch {
			case decl == "CDATA", decl == "#REQUIRED", decl == "#IMPLIED", decl == "#FIXED",
				strings.HasPrefix(decl, `"`), strings.HasPrefix(decl, `'`):
			default:
				// Every non-CDATA type is recorded, not only the ID family:
				// XML 1.0 §3.3.3 requires a processor that has read the
				// declaration to collapse the value's spaces, whatever the
				// type. applyAttTypes annotates only ID, IDREF and IDREFS.
				types = append(types, attDeclaredType{element, name, decl, ext})
			}
			if decl == "NOTATION" && i < len(fields) {
				i++ // the parenthesised notation list is part of the type
			}
			switch {
			case decl == "#REQUIRED", decl == "#IMPLIED":
				// No default to supply.
			case decl == "#FIXED":
				if i < len(fields) {
					out = append(out, attDefault{element, name, unquote(fields[i]), at, entities, ext})
					i++
				}
			case strings.HasPrefix(decl, `"`), strings.HasPrefix(decl, `'`):
				out = append(out, attDefault{element, name, unquote(decl), at, entities, ext})
			default:
				// decl was the attribute *type*; the default follows it.
				if i < len(fields) {
					d := fields[i]
					i++
					switch {
					case d == "#REQUIRED", d == "#IMPLIED":
					case d == "#FIXED":
						if i < len(fields) {
							out = append(out, attDefault{element, name, unquote(fields[i]), at, entities, ext})
							i++
						}
					case strings.HasPrefix(d, `"`), strings.HasPrefix(d, `'`):
						out = append(out, attDefault{element, name, unquote(d), at, entities, ext})
					}
				}
			}
			if seen[key] {
				out, types = out[:nOut], types[:nTypes]
			}
			seen[key] = true
		}
	}
	return out, types
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

// applyAttDefaults adds any declared default the element does not carry, and
// names one of them that an external markup declaration supplied, or "".
//
// An attribute present in the document always wins: a default supplies a value
// that was omitted, it does not override one that was given. Even a #FIXED
// default only constrains what may be written, and validating that constraint
// is DTD validation, which this parser does not do.
func applyAttDefaults(t xml.StartElement, defs []attDefault, ix declIndex) (xml.StartElement, string) {
	var add []xml.Attr
	outside := ""
	// A DTD is not namespace-aware: an ATTLIST names the element as
	// written, prefix included. Matching the local name alone missed
	// "p:doc", and with it a defaulted xmlns:p the element's own name
	// needs. The local-name match stays, as applyAttTypes has it.
	at := ix.at(t.Name.Space, t.Name.Local)
	// A tag the declarations give many attributes is looked up rather than
	// scanned once per default: k defaults on a tag of k attributes cost k*k.
	var given map[string]bool
	if len(at)*len(t.Attr) > smallAttrScan {
		given = make(map[string]bool, len(t.Attr))
		for _, a := range t.Attr {
			given[attrLexical(a.Name)] = true
		}
	}
	for _, i := range at {
		d := defs[i]
		if given != nil && given[d.name] || given == nil && hasAttrNamed(t.Attr, d.name) {
			continue
		}
		add = append(add, xml.Attr{Name: attrNameOf(d.name), Value: d.value})
		if d.outside {
			outside = d.name
		}
	}
	if len(add) == 0 {
		return t, ""
	}
	// Copy rather than append in place: encoding/xml reuses the attribute
	// slice across tokens, so appending would leak a defaulted attribute into
	// the next element it decodes.
	attrs := make([]xml.Attr, len(t.Attr), len(t.Attr)+len(add))
	copy(attrs, t.Attr)
	t.Attr = append(attrs, add...)
	return t, outside
}

// smallAttrScan is the declarations-times-attributes product up to which a
// tag's attributes are scanned for each declaration rather than indexed.
const smallAttrScan = 64

// attrsByName indexes a tag's n attributes by the names an ATTLIST
// declaration can match them by, their lexical and their local name, when
// the tag has many declarations as well as many attributes: scanning them for
// each declaration cost k declarations on a tag of k attributes k*k. A small
// tag is scanned, and gets nil.
func attrsByName(decls, n int, name func(i int) (prefix, local string)) map[string][]int {
	if decls*n <= smallAttrScan {
		return nil
	}
	by := make(map[string][]int, 2*n)
	for i := range n {
		p, l := name(i)
		by[l] = append(by[l], i)
		if p != "" {
			lex := p + ":" + l
			by[lex] = append(by[lex], i)
		}
	}
	return by
}

// eachAttrNamed calls f with the position of each attribute, in order, that
// a declaration of attr names: by its lexical name, or by its local name.
func eachAttrNamed(by map[string][]int, n int, name func(i int) (prefix, local string), attr string, f func(i int)) {
	if by != nil {
		for _, i := range by[attr] {
			f(i)
		}
		return
	}
	for i := range n {
		if p, l := name(i); lexicalIs(p, l, attr) || l == attr {
			f(i)
		}
	}
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

// lexicalIs reports whether s is the lexical QName prefix:local (or local
// with no prefix), without building it. DTD attribute typing compares every
// declaration with every element and attribute name, so building the names
// to compare cost an allocation per declaration per element.
func lexicalIs(prefix, local, s string) bool {
	if prefix == "" {
		return s == local
	}
	return len(s) == len(prefix)+1+len(local) && s[len(prefix)] == ':' &&
		s[:len(prefix)] == prefix && s[len(prefix)+1:] == local
}

// applyAttTypes stamps the ID, IDREF and IDREFS annotations a DTD declares,
// and collapses the spaces in every attribute declared with a non-CDATA type.
//
// A DTD is one of the two ways a document says which of its attributes are
// IDs, and fn:id is defined over the attribute's type rather than its name.
// Recording the annotation here puts a DTD-validated document on the same
// footing as a schema-validated one, so fn:id finds the same attributes in
// both.
func applyAttTypes(el *Node, types []attDeclaredType, ix declIndex) {
	en := el.Name()
	at := ix.at(en.Prefix, en.Local)
	n := el.NumAttrs()
	name := func(i int) (string, string) {
		an := el.AttrAt(i).Name()
		return an.Prefix, an.Local
	}
	by := attrsByName(len(at), n, name)
	for _, i := range at {
		t := types[i]
		eachAttrNamed(by, n, name, t.attr, func(j int) {
			a := el.AttrAt(j)
			// XML 1.0 §3.3.3: a value whose declared type is not CDATA
			// loses leading and trailing spaces, and each run of spaces
			// becomes one. Only #x20: a tab written as &#9; survives.
			if v := a.Value(); strings.Contains(v, " ") {
				if c := strings.Join(strings.FieldsFunc(v, func(r rune) bool { return r == ' ' }), " "); c != v {
					a.SetValue(c)
				}
			}
			switch t.typ {
			case "ID", "IDREF", "IDREFS":
			default:
				return
			}
			if a.TypeAnnotation() == "" {
				// SetTypeAnnotation rather than a bare assignment: a DTD
				// declaring ID/IDREF/IDREFS is one of the two ways a
				// document establishes the is-id and is-idrefs properties,
				// and those must survive input-type-annotations="strip"
				// even though the annotation itself does not.
				a.SetTypeAnnotation(t.typ)
			}
		})
	}
}

// What the parse keeps of an element type's <!ELEMENT> declarations
// (parseElementDecls), as bits.
const (
	declElementOnly uint8 = 1 << iota // element content: see parseElementDecls
	declEmpty                         // EMPTY
	declOutside                       // an external markup declaration (see declOrigins)
)

// parseElementDecls returns, by element name, what the <!ELEMENT>
// declarations say that the parse needs: whether the model is element-only,
// whether it is EMPTY, and, where outside is declOrigins of subset, whether
// the declaration is an external markup declaration.
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
func parseElementDecls(subset string, outside []bool) map[string]uint8 {
	var out map[string]uint8
	at := -1
	for kw, body := range markupDecls(subset) {
		at++
		if kw != "ELEMENT" {
			continue
		}
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		model := strings.Join(fields[1:], " ")
		var k uint8
		switch {
		case isElementOnlyModel(model):
			k = declElementOnly
		case model == "EMPTY":
			k = declEmpty
		default:
			continue
		}
		if at < len(outside) && outside[at] {
			k |= declOutside
		}
		if out == nil {
			out = map[string]uint8{}
		}
		out[name] |= k
	}
	return out
}

// declKindOf is what parseElementDecls recorded for el's name, as written or
// by its local name alone.
func declKindOf(el *Node, kinds map[string]uint8) uint8 {
	en := el.Name()
	k := kinds[en.Local]
	if en.Prefix != "" {
		k |= kinds[en.Lexical()]
	}
	return k
}

// declOrigins says, for each item markupDecls yields from full, whether it is
// an external markup declaration: XML 1.0 §2.9 defines one as "a markup
// declaration occurring in the external subset or in a parameter entity
// (external or internal, the latter being included because non-validating
// processors are not required to read them)". full is the internal subset,
// its parameter-entity references substituted, for its first n bytes, and
// the external subset after; peSpans are where in those n bytes the
// substitutions landed, in order.
func declOrigins(full string, n int, peSpans [][2]int) []bool {
	var out []bool
	scanMarkupDecls(full, func(at int, _, _ string) bool {
		out = append(out, at >= n || inSpans(peSpans, at))
		return true
	})
	return out
}

// inSpans reports whether off falls in one of the ordered, disjoint spans.
func inSpans(spans [][2]int, off int) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i][1] > off })
	return i < len(spans) && spans[i][0] <= off
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

// ignorableWhitespaceIn reports whether whitespace-only text children of el
// are ignorable: el's DTD content model is element-only.
//
// This runs independently of, and before, the stylesheet's own strip-space
// rules: it is not a preference that xsl:preserve-space can turn off. An
// explicit xml:space="preserve" is still honoured, since XML §2.10 makes that
// the document's own statement about its whitespace.
func ignorableWhitespaceIn(el *Node, kinds map[string]uint8) bool {
	if declKindOf(el, kinds)&declElementOnly == 0 {
		return false
	}
	a := el.Attr(NSXML, "space")
	return a == nil || a.Value() != "preserve"
}
