package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// sequenceType is a declared type: what "as xs:integer*" means once resolved.
//
// XQuery's type syntax is XPath's, and the xpath package parses it — but only
// as part of an expression, because a SequenceType is not a production its
// public entry points expose on its own. So it is reached the way the XSLT
// layer reaches it: by compiling "() treat as T" and taking the type off the
// resulting TreatExpr. That is not a trick to be clever with; it is the only
// way to get at a conformant type parser without duplicating one, and it
// inherits every one of that parser's resolutions — prefixes, the default
// element namespace, the 3.1-only item types — for free.
type sequenceType struct {
	src   string
	stype xpath.SequenceType
}

// parseSequenceType reads a SequenceType from the source at p.pos.
//
// The extent has to be found before the type can be compiled, and the grammar
// makes that harder than it looks: "as xs:integer*" ends at the "*", but
// "as function(xs:integer) as xs:string" contains a nested "as" and a
// parenthesised list, and "as element(*, xs:untyped)" contains a comma that
// does not end anything. So this scans with a bracket depth and stops at the
// first delimiter that can follow a type at depth zero.
func (p *parser) parseSequenceType() (*sequenceType, error) {
	start := p.pos
	depth := 0
	for !p.eof() {
		c := p.src[p.pos]
		switch c {
		case '(':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == ':' {
				end, err := skipComment(p.src, p.pos)
				if err != nil {
					return nil, err
				}
				p.pos = end + 1
				continue
			}
			depth++
		case '[', '{':
			depth++
		case ')', ']':
			if depth == 0 {
				goto done
			}
			depth--
		case '}':
			if depth == 0 {
				goto done
			}
			depth--
		case '\'', '"':
			end, err := skipString(p.src, p.pos)
			if err != nil {
				return nil, err
			}
			p.pos = end + 1
			continue
		case ',', ';':
			if depth == 0 {
				goto done
			}
		case ':':
			// "Q{...}" and "p:local" both contain characters this loop would
			// otherwise treat as structure; a lone ":" is neither, and ":="
			// ends a variable declaration's type.
			if depth == 0 && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=' {
				goto done
			}
		case ' ', '\t', '\r', '\n':
			// Whitespace inside a type is legal — "element ( * )" — but at
			// depth zero it may equally be the space before ":=", "external"
			// or "{". Look past it: if what follows can continue a type, it
			// is part of one.
			if depth == 0 && !p.continuesType() {
				goto done
			}
		}
		p.pos++
	}
done:
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return nil, p.errorf("XPST0003: expected a type")
	}
	return p.compileSequenceType(src)
}

// continuesType reports whether the run of whitespace at p.pos is inside a
// type rather than after it.
//
// The types that carry an internal space are the function tests — "function(A)
// as B" — and the occurrence indicators, which may be written detached. Every
// other spelling of a type is one token.
func (p *parser) continuesType() bool {
	i := p.pos
	for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t' ||
		p.src[i] == '\r' || p.src[i] == '\n') {
		i++
	}
	if i >= len(p.src) {
		return false
	}
	switch p.src[i] {
	case '*', '+', '?', '(':
		return true
	}
	return strings.HasPrefix(p.src[i:], "as ") || strings.HasPrefix(p.src[i:], "as\t")
}

// compileSequenceType resolves a type against the query's static context.
func (p *parser) compileSequenceType(src string) (*sequenceType, error) {
	e, err := xpath.ParseVersion("() treat as "+src, p.sc, p.version)
	if err != nil {
		return nil, fmt.Errorf("XPST0051: invalid type %q: %w", src, err)
	}
	treat, ok := e.(*xpath.TreatExpr)
	if !ok {
		return nil, fmt.Errorf("XPST0051: invalid type %q", src)
	}
	return &sequenceType{src: src, stype: treat.Type}, nil
}

// convert applies the function conversion rules of XPath 3.1 §3.1.5 to a
// value being bound to a declared type.
//
// A nil type declares nothing, which is item()*, and everything matches it.
// This is the same shape as the XSLT layer's conversion and for the same
// reason: a declared type in either language both *constrains* and *converts*,
// so an xs:untypedAtomic bound to "as xs:integer" is cast rather than refused,
// and a node bound to "as xs:string" is atomised.
func (t *sequenceType) convert(seq xdm.Sequence, what string) (xdm.Sequence, error) {
	if t == nil {
		return seq, nil
	}
	if t.stype.Matches(seq) {
		return seq, nil
	}
	// A function item supplied where a typed function test is declared is
	// wrapped so its arguments and result are converted at call time; an
	// inline function records no signature, so without this every parameter
	// declared "as function(xs:string) as xs:string" rejected the values the
	// rules exist to admit.
	if conv, ok := xpath.CoerceFunctionItem(seq, t.stype); ok {
		return conv, nil
	}
	if !t.stype.HasAtomicType {
		return nil, fmt.Errorf("XPTY0004: %s does not match its declared type %s",
			what, t.src)
	}
	// Atomise, then cast each untypedAtomic item to the declared type. An
	// item already of the right type passes through untouched, which is what
	// keeps an xs:integer bound to "as xs:decimal" an xs:integer.
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return nil, err
	}
	out := make(xdm.Sequence, 0, len(atoms))
	for _, it := range atoms {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			out = append(out, it)
			continue
		}
		if t.stype.MatchesItem(a) {
			out = append(out, a)
			continue
		}
		conv, err := t.castOne(a)
		if err != nil {
			return nil, fmt.Errorf(
				"XPTY0004: %s does not match its declared type %s: %w",
				what, t.src, err)
		}
		out = append(out, conv)
	}
	if !t.stype.Matches(out) {
		return nil, fmt.Errorf("XPTY0004: %s does not match its declared type %s",
			what, t.src)
	}
	return out, nil
}

// castOne applies the single-item half of the conversion rules.
//
// Only two conversions are permitted and neither is a general cast: an
// xs:untypedAtomic is cast to the declared type, and a numeric or anyURI value
// is *promoted* — xs:integer to xs:double, xs:anyURI to xs:string. Casting
// anything else would admit "as xs:integer" against the string "3", which the
// rules deliberately do not.
func (t *sequenceType) castOne(a *xdm.Atomic) (xdm.Item, error) {
	switch a.Type {
	case xdm.TypeUntypedAtomic:
		if c, ok := xpath.CastToUnion(a, t.stype); ok {
			return c, nil
		}
		return nil, fmt.Errorf("%s cannot be cast to %s", a.TypeName(), t.src)
	case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeFloat, xdm.TypeDouble,
		xdm.TypeAnyURI:
		if c, ok := xpath.CastToUnion(a, t.stype); ok {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%s is not %s", a.TypeName(), t.src)
}
