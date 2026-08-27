package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// sequenceType is a compiled "as" attribute from xsl:param, xsl:variable or
// xsl:function.
//
// The declaration is not documentation: XSLT applies the function conversion
// rules to the supplied value, which for an atomic target means atomising it
// and casting untypedAtomic to the declared type. Skipping that leaves a
// parameter declared "as=xs:decimal?" holding an untypedAtomic string, and
// then "$a - $b" inside the function does double arithmetic instead of exact
// decimal arithmetic — which is how a validator produces a false positive on
// a rounding-tolerance check.
type sequenceType struct {
	src   string
	stype xpath.SequenceType
}

// compileSequenceType parses an "as" attribute.
//
// The syntax is XPath's SequenceType production, so it is parsed by compiling
// a throwaway "() treat as T" expression rather than duplicating the grammar.
func compileSequenceType(src string, ns xpath.NamespaceResolver) (*sequenceType, error) {
	if src == "" {
		return nil, nil
	}
	// The version decides what the type grammar admits, exactly as it does
	// for an expression: map(*) and array(*) are item types in 3.1 and
	// nothing at all in 2.0, so an as="map(*)" would be a syntax error
	// against the wrong grammar. It is read from the resolver the same way
	// compileExpr reads it.
	v := xpath.XPath20
	if r, ok := ns.(*nsResolver); ok {
		v = r.xpathVersion
	}
	expr, err := xpath.ParseVersion("() treat as "+src, ns, v)
	if err != nil {
		return nil, fmt.Errorf("invalid type %q: %w", src, err)
	}
	treat, ok := expr.(*xpath.TreatExpr)
	if !ok {
		return nil, fmt.Errorf("invalid type %q", src)
	}
	return &sequenceType{src: src, stype: treat.Type}, nil
}

// convert applies the function conversion rules to a value.
func (t *sequenceType) convert(seq xdm.Sequence, what string) (xdm.Sequence, error) {
	return t.convertAs(seq, what, "XPTY0004")
}

// convertAs is convert with the error code the caller's context requires.
//
// The same conversion failure has different codes depending on what was being
// converted: a variable or parameter whose value will not convert is XTTE0570,
// a function result is XTTE0780, and a plain expression is XPTY0004. The
// machinery is identical, so only the code is passed in.
func (t *sequenceType) convertAs(seq xdm.Sequence, what, code string) (xdm.Sequence, error) {
	if t == nil {
		return seq, nil
	}

	// A node-typed or item() declaration constrains but does not convert; the
	// value either matches or it does not.
	if !t.stype.HasAtomicType {
		if t.stype.Matches(seq) {
			return seq, nil
		}
		return nil, fmt.Errorf("%s: %s does not match its declared type %s", code, what, t.src)
	}

	// Atomise, then cast each untypedAtomic item to the declared type. A
	// value already of the right type passes through untouched.
	atoms := xdm.Atomize(seq)
	out := make(xdm.Sequence, 0, len(atoms))
	for _, it := range atoms {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			out = append(out, it)
			continue
		}
		// Subtype substitution: an item that already conforms to the
		// required item type is passed through untouched. Without this an
		// xs:integer bound to a variable declared as="xs:decimal" was
		// promoted to xs:decimal and stopped being an xs:integer, and every
		// value bound to as="xs:anyAtomicType" was cast to a string.
		if t.stype.MatchesItem(a) {
			out = append(out, a)
			continue
		}
		// The function conversion rules cast in exactly three cases:
		// untypedAtomic to anything, xs:anyURI to xs:string, and the numeric
		// promotion ladder. Anything else keeps its type and is checked
		// against the declaration below.
		cast := a.Type == xdm.TypeUntypedAtomic ||
			(a.Type == xdm.TypeAnyURI && t.stype.AtomicType == xdm.TypeString) ||
			numericPromotes(a.Type, t.stype.AtomicType)
		if !cast {
			out = append(out, a)
			continue
		}
		// A derived type carries a facet the code alone cannot express, so
		// the written name is used when there is one: casting to xs:token
		// has to normalise, not merely widen to xs:string.
		conv, err := xpath.CastToDerived(a, t.stype.AtomicType, t.stype.FacetName)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", code, what, err)
		}
		if t.stype.SchemaType != "" {
			// The declared type is an imported schema type, and the
			// conversion rules make the converted value an instance of it.
			// Without recording the annotation the cast produced a bare
			// primitive, which matchesItem then rejected because an
			// unannotated value is an instance of no named type — so every
			// variable declared as a schema atomic type raised XTTE0570 on a
			// value the rules had just converted for it.
			conv = conv.WithDerived(t.stype.SchemaType)
		}
		out = append(out, conv)
	}

	if !t.stype.Matches(out) {
		return nil, fmt.Errorf("%s: %s does not match its declared type %s (got %d item(s))",
			code,
			what, t.src, len(out))
	}
	return out, nil
}

// bindParam applies the function conversion rules to a value supplied for a
// template parameter.
//
// Section 10.1.1: the supplied value is converted to the parameter's required
// type using the function conversion rules, and a value that will not convert
// is XTTE0590. Binding the value untouched left an xsl:param declared
// as="xs:double" holding whatever the caller passed — so a node bound to it
// stayed a node, and the template's own "instance of xs:double" answered
// false on a value the rules should have atomised and cast.
func bindParam(p *Variable, v xdm.Sequence, t *Template) (xdm.Sequence, error) {
	if p.asType == nil {
		return v, nil
	}
	return p.asType.convertAs(v, "parameter $"+p.Name.Lexical()+
		" of template "+templateLabel(t), "XTTE0590")
}

// numericPromotes reports whether the function conversion rules promote one
// numeric type to another.
//
// The ladder runs one way only: xs:decimal (and xs:integer, which derives
// from it) promotes to xs:float and to xs:double, and xs:float promotes to
// xs:double. Nothing promotes *down*. Treating every numeric pair as
// convertible let a variable declared as="xs:float" silently accept an
// xs:double and lose precision, where the rules make it a type error —
// which is exactly what type-0174 and type-0175 are written to detect.
func numericPromotes(from, to xdm.TypeCode) bool {
	if !from.IsNumeric() || !to.IsNumeric() {
		return false
	}
	switch to {
	case xdm.TypeDouble:
		return true
	case xdm.TypeFloat:
		return from != xdm.TypeDouble
	}
	// A promotion to xs:decimal or xs:integer is not a promotion at all: an
	// item already of that type was passed through by the subtype check
	// above, and anything else would be a narrowing.
	return false
}

// source returns the written form of the declared type, for error messages.
func (t *sequenceType) source() string {
	if t == nil {
		return "item()*"
	}
	return t.src
}

// hasExplicitDefault reports whether a parameter declares a default value.
//
// Section 10.1.1 turns on this exact distinction: "if there is either a
// select attribute or a non-empty sequence constructor" the parameter has an
// explicitly given default, and a default that will not convert is XTTE0600.
// A parameter with neither has the empty sequence as its default, which is a
// different rule with a different code (XTDE0610).
func hasExplicitDefault(p *Variable) bool {
	return p.Select != nil || len(p.Body) > 0
}

// recodeError replaces the leading error code of a conversion failure.
//
// The conversion machinery is shared, so the code it stamps in is the one
// its caller asked for. Where the surrounding context requires a different
// code — a template parameter's explicit default is XTTE0600 rather than the
// XTTE0570 that evalVariable stamps on every variable — only the prefix
// changes; the explanatory text after it is already correct.
func recodeError(err error, code string) error {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i > 0 && strings.HasPrefix(msg, "XT") {
		return fmt.Errorf("%s%s", code, msg[i:])
	}
	return fmt.Errorf("%s: %s", code, msg)
}
