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
	// selfStep records a "." step, which contributes no ICStep but does
	// satisfy the grammar's demand for a step after ".//".
	var selfStep bool
	p.skipSpace()

	// A leading ".//" is the only place a descendant step may appear. It is
	// not a general "//" — the spec permits it only at the start, which is
	// what confines a selector to the subtree it is anchored at.
	// XPath allows whitespace between the tokens, so ".//" is matched as
	// three of them rather than as one string: ". //." is the same path as
	// ".//.".
	if save := p.pos; p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		p.skipSpace()
		if strings.HasPrefix(p.src[p.pos:], "//") {
			alt.DescendantOrSelf = true
			p.pos += 2
			p.skipSpace()
		} else {
			p.pos = save
		}
	}

	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}

		if p.src[p.pos] == '@' || p.atAxis("attribute") {
			// atAxis consumes the axis and any space after it, so a
			// path ending in "attribute::" leaves p.pos at the end.
			// Everything below indexes p.src, so the exhausted case
			// is answered here rather than by a panic.
			if p.pos >= len(p.src) {
				return alt, fmt.Errorf(
					"identity-constraint path %q: an attribute "+
						"axis needs a name test", p.src)
			}
			if !p.field {
				return alt, fmt.Errorf(
					"identity-constraint selector %q: a selector may not "+
						"select attributes", p.src)
			}
			// atAxis has already consumed the unabbreviated form; only
			// the abbreviation still needs its "@" stepping over.
			if p.src[p.pos] == '@' {
				p.pos++
				p.skipSpace()
			}
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
			// A "." step selects the context node itself. It adds
			// no ICStep — nothing is narrowed — but it is a step
			// all the same, and the grammar's requirement that
			// ".//" be followed by one is satisfied by it. ".//."
			// is written by twenty-six suite schemas.
			selfStep = true
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

	if len(alt.Steps) == 0 && alt.Attribute == nil && alt.DescendantOrSelf && !selfStep {
		// The grammar is Path ::= ('.//')? Step ('/' Step)*: the
		// descendant prefix anchors a step, it is not a path in
		// itself. idI018 writes ".//" as a selector, idJ026 as a field.
		return alt, fmt.Errorf(
			"identity-constraint path %q: %q must be followed by a step",
			p.src, ".//")
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

// atAxis reports whether the named axis begins here, consuming it if so.
//
// Clause 2.2 of Fields Value OK admits the unabbreviated "attribute::a"
// wherever "@a" is allowed, just as it admits "child::e" for "e". Recognising
// only the abbreviation rejected the long form as a malformed path, which
// failed the whole schema rather than the one step.
func (p *icPathParser) atAxis(axis string) bool {
	save := p.pos
	p.skipSpace()
	if !strings.HasPrefix(p.src[p.pos:], axis) {
		p.pos = save
		return false
	}
	p.pos += len(axis)
	p.skipSpace()
	if !strings.HasPrefix(p.src[p.pos:], "::") {
		// An element named "attribute" is a name test, not an axis.
		p.pos = save
		return false
	}
	p.pos += 2
	p.skipSpace()
	return true
}

// skipAxis consumes a leading "child::" if present.
//
// XPath allows whitespace around "::" and either side of the axis name, so the
// axis is matched in pieces rather than as one string. "child :: e" and
// "child:: e" are the same step as "child::e"; matching only the closed
// spelling rejected the others as malformed paths, which failed the whole
// schema rather than the step.
func (p *icPathParser) skipAxis() {
	p.atAxis("child")
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

// icKindOf maps the local name of an identity-constraint element to its
// category.
func icKindOf(local string) IdentityConstraintKind {
	switch local {
	case "keyref":
		return ICKeyref
	case "unique":
		return ICUnique
	}
	return ICKey
}

// readIdentityConstraint reads an <xs:key>, <xs:keyref> or <xs:unique>.
func (p *parser) readIdentityConstraint(el *xdm.Node) *IdentityConstraint {
	// XSD 1.1 lets a declaration reference a constraint declared elsewhere
	// instead of defining one: <xs:key ref="s:some-key"/>. The referenced
	// constraint applies to this element as if written here, which is how a
	// schema states one key over several element declarations without
	// repeating its selector and fields.
	if ref := el.AttrValue("ref"); ref != "" {
		if el.AttrValue("name") != "" {
			p.errs = append(p.errs, errorAt(el, "src-identity-constraint",
				"an identity constraint may not have both name and ref"))
			return nil
		}
		// A ref= reuses a whole constraint, so there is nothing left for this
		// element to say about it. refer= in particular belongs to the keyref
		// being referenced, not to the reference. ibmData S2_2_4 s2_2_4si07
		// pins this with <keyref ref="v01:keyref" refer="v01:uniqueKey"/>.
		if el.AttrValue("refer") != "" {
			p.errs = append(p.errs, errorAt(el, "src-identity-constraint",
				"an identity constraint with ref may not also have refer"))
			return nil
		}
		target, err := p.resolveQName(el, "ref", ref)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		want := icKindOf(el.Name.Local)
		// The reference is a placeholder until the constraint it names
		// has been read, which may be in a document not yet seen.
		placeholder := &IdentityConstraint{Name: target}
		p.fixups = append(p.fixups, func() error {
			found, ok := p.schema.identityConstraints[target]
			if !ok {
				return errorAt(el, "src-resolve",
					"identity constraint ref %q names no key or unique", ref)
			}
			// XSD 1.1 §3.11.3 requires the category of the constraint named by
			// ref to match the element doing the naming: <xs:key ref="..."/>
			// may only name a key. ibmData S2_2_4 s2_2_4si02 pins this.
			if found.Kind != want {
				return errorAt(el, "src-identity-constraint",
					"%s ref=%q names a %s", el.Name.Local, ref, found.Kind)
			}
			// The reference resolves to the named component itself. Recording
			// it here lets resolveICRefs replace the placeholder in the
			// element's list, so validation keys its node table by the same
			// pointer the referring keyref's Refer holds.
			placeholder.resolved = found
			return nil
		})
		return placeholder
	}

	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"an identity constraint must have a name or a ref"))
		return nil
	}

	// The representation summary gives every identity constraint `name =
	// NCName`, so a name carrying a prefix or opening with a digit is a
	// representation fault rather than a constraint in some namespace.
	// MS-IdentityConstraint idA030 writes "a:b" and idA032 writes "1foo".
	if !isNCName(name) {
		p.errs = append(p.errs, errorAt(el, "src-identity-constraint",
			"identity constraint name %q is not an NCName", name))
		return nil
	}

	ic := &IdentityConstraint{Name: p.qnameFor(name), Kind: icKindOf(el.Name.Local)}

	sel := p.childElement(el, "selector")
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
	p.resolveICPath(sel, path)
	ic.Selector = path

	for _, c := range p.contentChildren(el) {
		if !c.IsElement(NSSchema, "field") {
			continue
		}
		f, err := parseICPath(c.AttrValue("xpath"), true)
		if err != nil {
			p.errs = append(p.errs, errorAt(c, "c-fields-xpaths", "%v", err))
			continue
		}
		p.resolveICPath(c, f)
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
	}

	// Key, keyref and unique share one symbol space (XSD 1.1 Structures 3.11),
	// so a keyref is registered under its name like any other constraint. That
	// is what lets <xs:keyref ref="a:kr1"/> find it; the refer= fixup above
	// keeps its own guard against a keyref naming another keyref, so admitting
	// keyrefs here does not weaken that check.
	if prev, ok := p.schema.identityConstraints[ic.Name]; ok && prev != ic {
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate identity constraint %s", name))
		return nil
	}
	p.schema.identityConstraints[ic.Name] = ic

	return ic
}

// resolveICPath binds each step's prefix to a namespace.
//
// Two sources decide the namespace of an unprefixed element name. XSD 1.1's
// xpathDefaultNamespace names one directly, which is how a schema writes a
// selector over qualified elements without inventing a prefix for them; it
// also takes the two tokens ##targetNamespace and ##defaultNamespace. Without
// it an unprefixed name is in the absent namespace, which is the XPath rule and
// the 1.0 behaviour.
//
// An attribute name is deliberately not given the default: XPath resolves an
// unprefixed attribute name to the absent namespace whatever the default
// element namespace is, and applying it here would retarget every unprefixed
// field at a namespace unqualified attributes are never in.
func (p *parser) resolveICPath(el *xdm.Node, path *ICPath) {
	if path == nil {
		return
	}
	def, hasDef := p.xpathDefaultNamespace(el)

	for i := range path.Alternatives {
		alt := &path.Alternatives[i]
		for j := range alt.Steps {
			step := &alt.Steps[j]
			if step.Wildcard {
				continue
			}
			if step.Name.Prefix != "" {
				if uri, ok := el.LookupPrefix(step.Name.Prefix); ok {
					step.Name.URI = uri
				} else {
					// src-resolve: a prefix in a selector or field must be
					// bound, exactly as in any other QName in a schema
					// document. Leaving it unresolved silently retargeted
					// the step at the absent namespace, where it matched
					// nothing and the schema still loaded.
					// MS-IdentityConstraint idI010 and idJ011 pin it.
					p.errs = append(p.errs, errorAt(el, "src-resolve",
						"identity-constraint path %q uses unbound prefix %q",
						path.Source, step.Name.Prefix))
				}
				continue
			}
			if hasDef {
				step.Name.URI = def
			}
		}
		if alt.Attribute != nil && alt.Attribute.Prefix != "" {
			if uri, ok := el.LookupPrefix(alt.Attribute.Prefix); ok {
				alt.Attribute.URI = uri
			}
		}
	}
}

// xpathDefaultNamespace returns the XSD 1.1 xpathDefaultNamespace in force at
// an element, looking outward to the schema element for the document default.
func (p *parser) xpathDefaultNamespace(el *xdm.Node) (string, bool) {
	for cur := el; cur != nil; cur = cur.Parent {
		a := cur.Attr("", "xpathDefaultNamespace")
		if a == nil {
			continue
		}
		switch a.Value {
		case "##targetNamespace":
			return p.doc.targetNS, true
		case "##defaultNamespace":
			// The default namespace in scope where the *expression*
			// is written, not where the attribute is. The attribute
			// is commonly on <xs:schema> and the xmlns= on the
			// element carrying the test — saxonData's cta0005 is
			// exactly that shape, and resolving at cur found no
			// default at all, so every unprefixed name in the test
			// went to the absent namespace and matched nothing.
			uri, ok := el.LookupPrefix("")
			return uri, ok
		case "##local":
			// Explicitly the absent namespace, which is also what no
			// attribute at all means — but saying so is not the same
			// as saying nothing, since an outer element may have set
			// a default this one is overriding.
			return "", true
		default:
			return a.Value, true
		}
	}
	return "", false
}
