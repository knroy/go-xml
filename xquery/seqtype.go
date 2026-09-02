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
		case '[':
			depth++
		case '{':
			// The only "{" a type contains is the one opening a braced URI
			// literal, which is always written directly after its "Q". A bare
			// one at depth zero is the function body that follows the return
			// type — "as item()*{ () }" is legal, with no space to end the
			// type on.
			if depth == 0 && (p.pos == start || p.src[p.pos-1] != 'Q') {
				goto done
			}
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
			if depth == 0 && !p.continuesType(start) {
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
//
// start is where the type began, so that the text already scanned can be
// consulted. The one thing it is asked is whether the whitespace follows a
// bare "as": that "as" is a function test's, its return type has not been read
// yet, and whatever comes next belongs to the type whatever it looks like.
// Without that, "as function(xs:string) as xs:string" ended at the second
// "as" and left a return type that would not compile.
func (p *parser) continuesType(start int) bool {
	if endsWithBareWord(p.src[start:p.pos], "as") {
		return true
	}
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

// endsWithBareWord reports whether src ends with word as a whole name rather
// than as the tail of a longer one, so that "as" is found in "function(x) as"
// and not in "xs:gas".
func endsWithBareWord(src, word string) bool {
	if !strings.HasSuffix(src, word) {
		return false
	}
	i := len(src) - len(word)
	return i == 0 || !isNameByte(src[i-1]) && src[i-1] != ':'
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
	return t.convertWith(seq, what, true)
}

// match is convert without the xs:untypedAtomic cast, which is what §4.14
// requires of a variable declaration.
//
// The two rules genuinely differ, and the suite is explicit about it.
// K2-ExternalVariablesWith-12 asserts that "declare variable $i as xs:integer
// := xs:untypedAtomic('1')" is XPTY0004 -- its description is "Variable
// declarations doesn't cause type conversion" -- where the identical value
// returned from a function declared "as xs:integer" is converted and returned
// as 1. §4.15 invokes the function conversion rules by name for a function's
// parameters and result; §4.14 says only that the value must *match* the
// declared type.
//
// Promotion is not affected: subtype substitution and numeric promotion apply
// to a variable binding as they do everywhere, so "as xs:double := 1" is a
// double. Only the cast from untypedAtomic is withheld.
func (t *sequenceType) match(seq xdm.Sequence, what string) (xdm.Sequence, error) {
	return t.convertWith(seq, what, false)
}

func (t *sequenceType) convertWith(seq xdm.Sequence, what string, cast bool) (xdm.Sequence, error) {
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
		if a.Type == xdm.TypeUntypedAtomic && !cast {
			return nil, fmt.Errorf(
				"XPTY0004: %s does not match its declared type %s: "+
					"a variable declaration does not convert %s",
				what, t.src, a.TypeName())
		}
		// The conversion rules stop short of a namespace-sensitive type.
		// Casting an xs:untypedAtomic to xs:QName means resolving whatever
		// prefix its string happens to carry, and the only namespaces in
		// scope at a call are the callee's, which have nothing to do with
		// where the value was written. §3.1.5 therefore excludes the case
		// outright and gives it its own code, XPTY0117, rather than letting
		// it read as an ordinary type mismatch: nothing the caller could
		// write would have made the conversion succeed.
		if a.Type == xdm.TypeUntypedAtomic && t.namespaceSensitive() {
			return nil, fmt.Errorf(
				"XPTY0117: %s is namespace-sensitive, so an %s value may "+
					"not be converted to its declared type %s",
				what, a.TypeName(), t.src)
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
// Only two conversions are permitted and neither is a general cast. An
// xs:untypedAtomic is cast to the declared type — which is what makes
// "declare function f() as xs:integer { <e>1</e> }" return the integer 1
// rather than raise, since atomising the element gives an untypedAtomic. And a
// numeric or xs:anyURI value is *promoted*: xs:integer to xs:double,
// xs:decimal to xs:float, xs:anyURI to xs:string. Casting anything else would
// admit "as xs:integer" against the string "3", which the rules deliberately
// do not.
func (t *sequenceType) castOne(a *xdm.Atomic) (xdm.Item, error) {
	// A pure union type is converted by trying its members in order, which is
	// a different rule from the single-target cast below.
	if c, ok := xpath.CastToUnion(a, t.stype); ok {
		return c, nil
	}
	switch a.Type {
	case xdm.TypeUntypedAtomic:
	case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeFloat, xdm.TypeDouble,
		xdm.TypeAnyURI:
		// Promotion, not conversion: only to a type the rules name. Anything
		// else keeps the value it had and fails the match below.
		if !promotes(a.Type, t.stype.AtomicType) {
			return nil, fmt.Errorf("%s is not %s", a.TypeName(), t.src)
		}
	default:
		return nil, fmt.Errorf("%s is not %s", a.TypeName(), t.src)
	}
	c, err := xpath.CastToDerived(a, t.stype.AtomicType, t.stype.FacetName)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// promotes reports whether the conversion rules promote from to to.
//
// XPath 3.1 §3.1.5 names exactly three promotions and no more: a numeric type
// to xs:float or xs:double, and xs:anyURI to xs:string. In particular
// xs:double does not promote to xs:integer, which is why a function declared
// "as xs:integer" that returns 1.5 must fail rather than truncate.
func promotes(from, to xdm.TypeCode) bool {
	switch to {
	case xdm.TypeFloat, xdm.TypeDouble:
		switch from {
		case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeFloat, xdm.TypeDouble:
			return true
		}
	case xdm.TypeString:
		return from == xdm.TypeAnyURI
	}
	return false
}

// namespaceSensitive reports whether t's item type is one whose value space is
// the QName one: xs:QName, xs:NOTATION, and the types derived from either.
//
// The distinction matters only for the conversion rules, which refuse such a
// target with XPTY0117 where any other target would merely be cast. xs:NOTATION
// and its derivations are recognised by FacetName because the sequence-type
// parser resolves them onto xs:string and records the real name there, the
// atomic type code having no NOTATION of its own.
func (t *sequenceType) namespaceSensitive() bool {
	if t.stype.AtomicType == xdm.TypeQName {
		return true
	}
	return t.stype.FacetName == "NOTATION"
}
