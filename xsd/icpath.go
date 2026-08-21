package xsd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The identity-constraint path grammar (§3.11.6).
//
//	Selector ::= Path ( '|' Path )*
//	Path     ::= ('.//')? Step ( '/' Step )*
//	Step     ::= '.' | NameTest
//	NameTest ::= QName | '*' | NCName ':' '*'
//
// A field replaces the Path production with one that permits a trailing
// attribute step:
//
//	Path ::= ('.//')? ( Step '/' )* ( Step | '@' NameTest )
//
// This is parsed by a dedicated parser rather than by the XPath engine in this
// repository, even though that engine would accept every expression here. The
// subset is closed: no predicates, no axes other than child (and attribute in
// final position), no functions, no absolute paths. Accepting more would accept
// schemas that conforming processors reject, and a schema that validates here
// but not elsewhere is worse than one that fails in both places.
type icPathParser struct {
	src   string
	pos   int
	field bool
}

// parseICPath parses a selector or field expression.
func parseICPath(src string, field bool) (*ICPath, error) {
	p := &icPathParser{src: src, field: field}
	out := &ICPath{Source: src}
	for {
		alt, err := p.parseAlternative()
		if err != nil {
			return nil, err
		}
		out.Alternatives = append(out.Alternatives, alt)
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}
		if p.src[p.pos] != '|' {
			return nil, fmt.Errorf(
				"identity-constraint path %q: unexpected %q at offset %d",
				src, p.src[p.pos], p.pos)
		}
		p.pos++
	}
	return out, nil
}

func (p *icPathParser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// parseAlternative parses one "|"-separated branch.
func (p *icPathParser) parseAlternative() (ICPathAlternative, error) {
	var alt ICPathAlternative
	p.skipSpace()

	// A leading ".//" is the only place a descendant step may appear. It is
	// not a general "//" — the spec permits it only at the start, which is
	// what confines a selector to the subtree it is anchored at.
	if strings.HasPrefix(p.src[p.pos:], ".//") {
		alt.DescendantOrSelf = true
		p.pos += 3
	}

	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}

		if p.src[p.pos] == '@' {
			if !p.field {
				return alt, fmt.Errorf(
					"identity-constraint selector %q: a selector may not "+
						"select attributes", p.src)
			}
			p.pos++
			name, err := p.parseNameTest()
			if err != nil {
				return alt, err
			}
			// "@*" and "@prefix:*" are grammatical: NameTest admits
			// both. Whether such a field selects exactly one node is a
			// validation question, answered per instance document by
			// Identity-constraint Satisfied clause 3 — not a parse
			// error. Rejecting it here refused schemas that conforming
			// processors accept.
			alt.Attribute = &xdm.QName{Prefix: name.prefix, Local: name.local}
			alt.AttributeWildcard = name.wildcard
			p.skipSpace()
			if p.pos < len(p.src) && p.src[p.pos] != '|' {
				return alt, fmt.Errorf(
					"identity-constraint field %q: an attribute step must "+
						"be last", p.src)
			}
			break
		}

		if p.src[p.pos] == '.' {
			// A "." step selects the context node itself.
			p.pos++
			p.skipSpace()
			if p.pos < len(p.src) && p.src[p.pos] == '/' {
				p.pos++
				continue
			}
			break
		}

		// Clause 2.2 of both Selector Value OK and Fields Value OK
		// permits "an XPath expression involving the child axis whose
		// abbreviated form is as given above", so "child::foo" is legal
		// wherever "foo" is. The axis is stripped and the step read as
		// if it had been written in the abbreviated form.
		p.skipAxis()

		name, err := p.parseNameTest()
		if err != nil {
			return alt, err
		}
		step := ICStep{Wildcard: name.wildcard}
		if !name.wildcard {
			step.Name = xdm.QName{Prefix: name.prefix, Local: name.local}
		}
		alt.Steps = append(alt.Steps, step)

		p.skipSpace()
		if p.pos < len(p.src) && p.src[p.pos] == '/' {
			p.pos++
			continue
		}
		break
	}

	if len(alt.Steps) == 0 && alt.Attribute == nil && !alt.DescendantOrSelf {
		// A bare "." is legal and selects the context node; anything
		// that parsed to nothing at all is not.
		if p.pos == 0 {
			return alt, fmt.Errorf(
				"identity-constraint path %q is empty", p.src)
		}
	}
	return alt, nil
}

// skipAxis consumes a leading "child::" if present.
func (p *icPathParser) skipAxis() {
	const axis = "child::"
	if strings.HasPrefix(p.src[p.pos:], axis) {
		p.pos += len(axis)
	}
}

// nameTest is a parsed NameTest, before its prefix is resolved.
type nameTest struct {
	prefix   string
	local    string
	wildcard bool
}

// parseNameTest parses a QName, "*", or "prefix:*".
func (p *icPathParser) parseNameTest() (nameTest, error) {
	start := p.pos
	if p.pos < len(p.src) && p.src[p.pos] == '*' {
		p.pos++
		return nameTest{wildcard: true}, nil
	}
	for p.pos < len(p.src) && isNameChar(p.src[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return nameTest{}, fmt.Errorf(
			"identity-constraint path %q: expected a name at offset %d",
			p.src, start)
	}
	first := p.src[start:p.pos]

	if p.pos < len(p.src) && p.src[p.pos] == ':' {
		p.pos++
		if p.pos < len(p.src) && p.src[p.pos] == '*' {
			p.pos++
			return nameTest{prefix: first, wildcard: true}, nil
		}
		s2 := p.pos
		for p.pos < len(p.src) && isNameChar(p.src[p.pos]) {
			p.pos++
		}
		if p.pos == s2 {
			return nameTest{}, fmt.Errorf(
				"identity-constraint path %q: expected a local name after %q",
				p.src, first)
		}
		return nameTest{prefix: first, local: p.src[s2:p.pos]}, nil
	}
	return nameTest{local: first}, nil
}

// isNameChar reports whether c may appear in an NCName.
//
// The test is deliberately generous about the bytes above ASCII: a multi-byte
// UTF-8 sequence has every continuation byte at or above 0x80, and rejecting
// them here would refuse names the XML parser already accepted.
func isNameChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c == '.':
		return true
	case c >= 0x80:
		return true
	}
	return false
}

// readIdentityConstraint reads an <xs:key>, <xs:keyref> or <xs:unique>.
func (p *parser) readIdentityConstraint(el *xdm.Node) *IdentityConstraint {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"an identity constraint must have a name"))
		return nil
	}

	ic := &IdentityConstraint{Name: p.qnameFor(name)}
	switch el.Name.Local {
	case "key":
		ic.Kind = ICKey
	case "unique":
		ic.Kind = ICUnique
	case "keyref":
		ic.Kind = ICKeyref
	}

	sel := childElement(el, "selector")
	if sel == nil {
		p.errs = append(p.errs, errorAt(el, "",
			"%s %q has no selector", el.Name.Local, name))
		return nil
	}
	path, err := parseICPath(sel.AttrValue("xpath"), false)
	if err != nil {
		p.errs = append(p.errs, errorAt(sel, "c-selector-xpath", "%v", err))
		return nil
	}
	ic.Selector = path

	for _, c := range contentChildren(el) {
		if !c.IsElement(NSSchema, "field") {
			continue
		}
		f, err := parseICPath(c.AttrValue("xpath"), true)
		if err != nil {
			p.errs = append(p.errs, errorAt(c, "c-fields-xpaths", "%v", err))
			continue
		}
		ic.Fields = append(ic.Fields, f)
	}
	if len(ic.Fields) == 0 {
		p.errs = append(p.errs, errorAt(el, "",
			"%s %q has no fields", el.Name.Local, name))
		return nil
	}

	if ic.Kind == ICKeyref {
		refer := el.AttrValue("refer")
		if refer == "" {
			p.errs = append(p.errs, errorAt(el, "src-identity-constraint",
				"a keyref must have a refer attribute"))
			return nil
		}
		target, err := p.resolveQName(el, "refer", refer)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		p.fixups = append(p.fixups, func() error {
			key, ok := p.schema.identityConstraints[target]
			if !ok {
				return errorAt(el, "src-resolve",
					"keyref refer=%q names no key or unique constraint", refer)
			}
			if key.Kind == ICKeyref {
				return errorAt(el, "c-props-correct.2",
					"keyref refer=%q names another keyref", refer)
			}
			// The referenced key must have the same number of fields:
			// a key sequence of a different length could never match.
			if len(key.Fields) != len(ic.Fields) {
				return errorAt(el, "c-props-correct.2",
					"keyref %q has %d fields but %s has %d",
					name, len(ic.Fields), refer, len(key.Fields))
			}
			ic.Refer = key
			return nil
		})
	} else {
		if prev, ok := p.schema.identityConstraints[ic.Name]; ok && prev != ic {
			p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
				"duplicate identity constraint %s", name))
			return nil
		}
		p.schema.identityConstraints[ic.Name] = ic
	}

	return ic
}
