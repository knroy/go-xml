package xpath

import "github.com/knroy/go-xml/xdm"

// OrderAtomics orders two atomic values the way a sorting host language needs
// to, returning -1, 0 or 1 and whether the pair is ordered at all.
//
// XQuery's "order by" (section 3.10.6) and XSLT's xsl:sort both need a
// three-way answer, and the operators in this package give a two-way one:
// "lt" is a boolean, and deriving an order from a pair of boolean comparisons
// costs two harmonisations per comparison in a sort that already makes
// n log n of them. The rest of the pipeline — which type is promoted to
// which, how an untypedAtomic is treated, which pairs are ordered at all — is
// the value-comparison rule exactly, so this exposes that rule rather than
// restating it.
//
// ok is false when the two types have no ordering between them (a string
// against a number), which the caller reports in whatever code its context
// requires: XPTY0004 for an ordering key, a separate group for grouping.
//
// NaN is not special-cased here. A host that needs a position for it — which
// "order by" does, since it orders NaN with the empty sequence — decides that
// before calling, because the position depends on the clause's empty-order
// and not on the values.
func OrderAtomics(a, b *xdm.Atomic, coll Collation, implicitTZ int) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	ctx := &Context{ImplicitTimezone: implicitTZ}
	if coll != nil {
		ctx.collation = coll
	}
	// The value-comparison harmonisation is what makes xs:integer and
	// xs:double comparable, and what casts an untypedAtomic to a string
	// rather than to the other operand's type.
	ha, hb, err := harmonize(a, b, false, nil)
	if err != nil {
		return 0, false
	}
	cmp, ordered, err := rawCompare(ctx, ha, hb)
	if err != nil || !ordered {
		return 0, false
	}
	return cmp, true
}
