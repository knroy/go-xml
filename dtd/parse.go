package dtd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xsd"
)

// DTD is the subset of a document type declaration this package applies.
type DTD struct {
	// Elements maps an element name to its content model.
	Elements map[string]*Element
	// Attributes maps an element name to its declared attributes.
	Attributes map[string][]*Attribute
	// HasExternalSubset records that the DOCTYPE named a SYSTEM or PUBLIC
	// identifier. Nothing is fetched, so validation is against the internal
	// subset alone and callers are told rather than misled.
	HasExternalSubset bool
}

// ContentKind is what an element's content model permits.
type ContentKind int

const (
	// ContentEmpty is EMPTY: no child elements and no character data.
	ContentEmpty ContentKind = iota
	// ContentAny is ANY: anything, unchecked.
	ContentAny
	// ContentMixed is (#PCDATA | a | b)*: text interleaved with a set of
	// element names, in any order and any number.
	ContentMixed
	// ContentChildren is an element-only model such as (a, b*, (c|d)?).
	ContentChildren
)

// Element is one <!ELEMENT> declaration.
type Element struct {
	Name string
	Kind ContentKind
	// Mixed is the set of names a mixed model admits. Order and repetition
	// are unconstrained there, so a set is the whole model.
	Mixed map[string]bool
	// Particle is the compiled model for ContentChildren, expressed in xsd's
	// component model so that the existing automaton can run it.
	Particle *xsd.Particle
}

// AttrDefault is how an attribute's presence is constrained.
type AttrDefault int

const (
	// AttrImplied is #IMPLIED: optional, no default.
	AttrImplied AttrDefault = iota
	// AttrRequired is #REQUIRED: must be present.
	AttrRequired
	// AttrFixed is #FIXED "v": if present the value must be v.
	AttrFixed
	// AttrDefaulted is a bare "v": supplied when absent.
	AttrDefaulted
)

// Attribute is one attribute definition within an <!ATTLIST>.
type Attribute struct {
	Element string
	Name    string
	// Type is the declared type: CDATA, ID, IDREF, IDREFS, NMTOKEN,
	// NMTOKENS, ENTITY, ENTITIES, NOTATION, or an enumeration.
	Type string
	// Enum holds the permitted values of an enumeration or NOTATION type.
	Enum    []string
	Default AttrDefault
	Value   string
}

// Parse reads the declarations out of a DOCTYPE's internal subset.
//
// The argument is the directive text as encoding/xml hands it over — the whole
// "DOCTYPE name [...]" including the brackets. Anything the grammar here does
// not recognise is skipped rather than guessed at, so an unusual declaration
// leaves the document less constrained rather than wrongly rejected.
func Parse(directive string) (*DTD, error) {
	// No DOCTYPE means no constraints, which is different from a DOCTYPE
	// declaring nothing: returning an empty ruleset here would make every
	// element undeclared and reject the document outright.
	if strings.TrimSpace(directive) == "" {
		return nil, nil
	}
	d := &DTD{
		Elements:   map[string]*Element{},
		Attributes: map[string][]*Attribute{},
	}
	head, subset := splitSubset(directive)
	// A SYSTEM or PUBLIC identifier in the head names an external subset.
	if f := fields(head); len(f) >= 2 {
		for _, tok := range f[1:] {
			if tok == "SYSTEM" || tok == "PUBLIC" {
				d.HasExternalSubset = true
				break
			}
		}
	}

	for _, decl := range declarations(subset) {
		switch {
		case strings.HasPrefix(decl, "ELEMENT"):
			el, err := parseElement(decl[len("ELEMENT"):])
			if err != nil {
				return nil, err
			}
			if el != nil {
				// XML §3.2: an element may be declared only once. A repeat is
				// a validity error, but reporting it here would refuse
				// documents this package is only asked to validate, so the
				// first declaration binds — matching how entities work.
				if _, dup := d.Elements[el.Name]; !dup {
					d.Elements[el.Name] = el
				}
			}
		case strings.HasPrefix(decl, "ATTLIST"):
			for _, a := range parseAttList(decl[len("ATTLIST"):]) {
				d.Attributes[a.Element] = append(d.Attributes[a.Element], a)
			}
		}
	}
	return d, nil
}

// splitSubset separates the DOCTYPE head from the bracketed internal subset.
func splitSubset(directive string) (head, subset string) {
	i := strings.IndexByte(directive, '[')
	if i < 0 {
		return directive, ""
	}
	j := strings.LastIndexByte(directive, ']')
	if j < i {
		return directive[:i], directive[i+1:]
	}
	return directive[:i], directive[i+1 : j]
}

// declarations splits a subset into the bodies of its <!...> declarations.
//
// Comments are removed first: a commented-out <!ELEMENT> is not a declaration,
// and the RFC 3986 type library in the W3C suite has several.
func declarations(subset string) []string {
	subset = stripComments(subset)
	var out []string
	for {
		i := strings.Index(subset, "<!")
		if i < 0 {
			return out
		}
		subset = subset[i+2:]
		// A declaration ends at the first '>' outside a quoted literal or a
		// parenthesised model — a content model may contain neither, but an
		// attribute default may contain '>'.
		depth, end := 0, -1
		for k := 0; k < len(subset); k++ {
			switch c := subset[k]; c {
			case '"', '\'':
				if q := strings.IndexByte(subset[k+1:], c); q >= 0 {
					k += q + 1
				}
			case '(':
				depth++
			case ')':
				depth--
			case '>':
				if depth <= 0 {
					end = k
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(subset[:end]))
		subset = subset[end+1:]
	}
}

func stripComments(s string) string {
	var sb strings.Builder
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			sb.WriteString(s)
			return sb.String()
		}
		sb.WriteString(s[:i])
		j := strings.Index(s[i:], "-->")
		if j < 0 {
			return sb.String()
		}
		s = s[i+j+3:]
	}
}

// parseElement reads "<!ELEMENT name model>".
func parseElement(body string) (*Element, error) {
	body = strings.TrimSpace(body)
	sp := strings.IndexFunc(body, isSpace)
	if sp < 0 {
		return nil, nil
	}
	name := body[:sp]
	model := strings.TrimSpace(body[sp:])
	el := &Element{Name: name}

	switch {
	case model == "EMPTY":
		el.Kind = ContentEmpty
		return el, nil
	case model == "ANY":
		el.Kind = ContentAny
		return el, nil
	case strings.HasPrefix(strings.TrimPrefix(model, "("), "#PCDATA"),
		strings.Contains(model, "#PCDATA"):
		el.Kind = ContentMixed
		el.Mixed = map[string]bool{}
		for _, n := range strings.FieldsFunc(model, func(r rune) bool {
			return strings.ContainsRune("()|,*+? \t\n\r", r)
		}) {
			if n != "#PCDATA" && n != "" {
				el.Mixed[n] = true
			}
		}
		return el, nil
	}

	p, err := parseModel(model)
	if err != nil {
		return nil, fmt.Errorf("element %s: %w", name, err)
	}
	el.Kind = ContentChildren
	el.Particle = p
	return el, nil
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func fields(s string) []string {
	return strings.FieldsFunc(s, isSpace)
}
