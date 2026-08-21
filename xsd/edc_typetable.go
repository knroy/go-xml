package xsd

// XSD 1.1 extends Element Declarations Consistent (§3.8.6) to type tables.
//
// In 1.0 the constraint asks only that co-occurring same-name element
// declaration particles share one top-level type definition. 1.1 adds
// conditional type assignment, so the declared {type definition} is no longer
// the whole story: two particles may name the same type and still assign
// different types at validation time, because each carries its own
// {type table} of <xs:alternative>s. The constraint therefore requires the
// type tables to agree as well, and "agree" is component identity — same
// alternatives in the same order, and the same default type.
//
// cta9009err pins the case where both particles have a type table but one has
// an extra alternative; cta9010err pins the case where one has a type table
// and the other has none. Both declare <xs:element name="a" type="zz"/> twice,
// so the 1.0 type comparison sees one type and passes.
//
// The tests are compared by their source text rather than by the compiled
// XPath, because two alternatives are the same component only if they were
// written the same way; equivalent-but-differently-spelled expressions are
// distinct alternatives to the spec, and comparing compiled forms would need
// an expression equivalence test that does not exist and is not required here.
func sameTypeTable(a, b *ElementDecl) bool {
	if len(a.Alternatives) != len(b.Alternatives) {
		return false
	}
	for i, alt := range a.Alternatives {
		other := b.Alternatives[i]
		if alt == nil || other == nil {
			if alt != other {
				return false
			}
			continue
		}
		if alt.Source != other.Source || alt.Type != other.Type {
			return false
		}
	}
	return true
}
