package xslt

import (
	"fmt"

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
	expr, err := xpath.Parse("() treat as "+src, ns)
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
		if a.Type == t.stype.AtomicType {
			out = append(out, a)
			continue
		}
		if a.Type == xdm.TypeUntypedAtomic {
			conv, err := xpath.CastAtomic(a, t.stype.AtomicType)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			out = append(out, conv)
			continue
		}
		// Numeric promotion is permitted without an explicit cast; anything
		// else keeps its type and is checked against the declaration below.
		if a.Type.IsNumeric() && t.stype.AtomicType.IsNumeric() {
			conv, err := xpath.CastAtomic(a, t.stype.AtomicType)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			out = append(out, conv)
			continue
		}
		out = append(out, a)
	}

	if !t.stype.Matches(out) {
		return nil, fmt.Errorf("%s: %s does not match its declared type %s (got %d item(s))",
			code,
			what, t.src, len(out))
	}
	return out, nil
}
