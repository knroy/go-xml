package xpath

import "github.com/knroy/go-xml/xdm"

// Cardinality helpers for parameters declared as a single atomic value.
//
// The shape these replace is
//
//	atoms := xdm.Atomize(args[i])
//	if len(atoms) > 0 { a := atoms[0].(*xdm.Atomic) }
//
// which reads the first item of whatever it is given. For a parameter the
// spec declares as a singleton — "$value as numeric?", "$code as xs:QName?" —
// a two-item argument is XPTY0004 under the function conversion rules, not a
// request to format the first item and drop the rest. Taking atoms[0] made
// format-number((1,2), '0') answer "1", which is a wrong answer rather than a
// refused one, and the same silent truncation reached format-integer,
// format-date and fn:dateTime.
//
// Atomization is checked rather than plain because these are the places that
// demand a typed value: a function item cannot be atomized, and Atomize drops
// it silently where AtomizeChecked reports FOTY0013.

// argAtomicOptional returns argument i as the at-most-one atomic value a
// parameter declared "T?" allows, or nil for the empty sequence and for an
// argument that was not supplied.
func argAtomicOptional(args []xdm.Sequence, i int, fn string) (*xdm.Atomic, error) {
	atoms, err := xdm.AtomizeChecked(seqArg(args, i))
	if err != nil {
		return nil, err
	}
	switch len(atoms) {
	case 0:
		return nil, nil
	case 1:
		a, ok := atoms[0].(*xdm.Atomic)
		if !ok {
			return nil, xdm.ErrType("%s: argument %d is not an atomic value", fn, i+1)
		}
		return a, nil
	default:
		return nil, xdm.ErrType(
			"%s: argument %d expects at most one item, got %d", fn, i+1, len(atoms))
	}
}

// argAtomicRequired returns argument i as the exactly-one atomic value a
// parameter declared "T" allows. An empty sequence is an error here, which is
// the difference from argAtomicOptional.
func argAtomicRequired(args []xdm.Sequence, i int, fn string) (*xdm.Atomic, error) {
	atoms, err := xdm.AtomizeChecked(seqArg(args, i))
	if err != nil {
		return nil, err
	}
	if len(atoms) != 1 {
		return nil, xdm.ErrType(
			"%s: argument %d expects exactly one item, got %d", fn, i+1, len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("%s: argument %d is not an atomic value", fn, i+1)
	}
	return a, nil
}
