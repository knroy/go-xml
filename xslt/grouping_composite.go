package xslt

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Composite grouping keys, XSLT 3.0 section 14.2.
//
// With composite="yes" the whole atomized result of group-by or
// group-adjacent is one key rather than a set of keys. That changes three
// things at once: an item joins exactly one group instead of one per distinct
// value, a group-adjacent key of any length is legal where the single-key
// form demands exactly one item, and current-grouping-key() returns the whole
// sequence.

// compositeKey renders a sequence of atomic values as one comparison key.
//
// The parts are joined with a separator that cannot occur in a part, because
// the single-value keys are already escaped by GroupingKey into a form where
// the unit separator does not appear -- so ("a", "bc") and ("ab", "c") stay
// distinct, which a plain concatenation would not.
func compositeKey(rt *runtime, vals []xdm.Item, coll xpath.Collation) (string, error) {
	var sb strings.Builder
	for i, v := range vals {
		if i > 0 {
			sb.WriteByte(0x1f)
		}
		k, err := groupingKey(rt, v.(*xdm.Atomic), coll)
		if err != nil {
			return "", err
		}
		sb.WriteString(k)
	}
	return sb.String(), nil
}

// groupByCompositeKey is groupByKey for composite="yes": one key per item,
// and the key is the whole atomized sequence.
func groupByCompositeKey(rt *runtime, seq xdm.Sequence, key *xpath.Compiled,
	coll xpath.Collation) ([]group, error) {
	index := map[string]int{}
	var groups []group
	for idx, it := range seq {
		sub := rt.withFocus(it, idx+1, len(seq))
		vals, err := key.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		atoms := atomizeGroupingKey(vals)
		k, err := compositeKey(rt, atoms, coll)
		if err != nil {
			return nil, err
		}
		g, seen := index[k]
		if !seen {
			g = len(groups)
			index[k] = g
			groups = append(groups, group{key: xdm.Sequence(atoms)})
		}
		groups[g].items = append(groups[g].items, it)
	}
	return groups, nil
}

// groupAdjacentCompositeKey is groupAdjacentKey for composite="yes".
//
// There is no XTTE1100 here: the single-key form requires exactly one item
// because a longer key would be ambiguous, and a composite key is defined as
// the whole sequence however long -- including empty, which is a key in its
// own right that every item producing nothing shares.
func groupAdjacentCompositeKey(rt *runtime, seq xdm.Sequence, key *xpath.Compiled,
	coll xpath.Collation) ([]group, error) {
	var groups []group
	var prev string
	first := true
	for idx, it := range seq {
		sub := rt.withFocus(it, idx+1, len(seq))
		vals, err := key.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		atoms := atomizeGroupingKey(vals)
		k, err := compositeKey(rt, atoms, coll)
		if err != nil {
			return nil, err
		}
		if first || k != prev {
			groups = append(groups, group{key: xdm.Sequence(atoms)})
			first, prev = false, k
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)
	}
	return groups, nil
}

// atomizeGroupingKey atomizes a key expression's result and casts any
// xs:untypedAtomic to xs:string, as section 14.2 requires before the values
// are compared or handed to current-grouping-key().
func atomizeGroupingKey(vals xdm.Sequence) []xdm.Item {
	atoms := xdm.Atomize(vals)
	out := make([]xdm.Item, len(atoms))
	for i, a := range atoms {
		v := a.(*xdm.Atomic)
		if v.Type == xdm.TypeUntypedAtomic {
			v = xdm.NewString(v.String())
		}
		out[i] = v
	}
	return out
}
