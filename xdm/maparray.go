package xdm

import (
	"fmt"
	"math"
	"math/big"
)

// MapItem is the fourth kind of XDM item, added in XPath 3.1.
//
// A map associates atomic keys with arbitrary sequences. It is a *function*
// item as well: calling a map with one argument looks a key up, which is what
// makes "$m('k')" and the lookup operator "$m?k" the same operation. That dual
// nature is in the specification rather than a convenience here — map:merge
// and fn:for-each can be handed a map wherever a function of arity one is
// expected.
//
// Keys are compared by value, not by type identity, but xs:untypedAtomic is
// not admitted: a key arrives already atomized, and leaving an untyped one
// comparable to both a string and a number would make lookup depend on which
// happened to be asked for.
type MapItem struct {
	// entries preserves insertion order so that serialising a map twice gives
	// the same text. The specification fixes no order for map:keys, and an
	// unstable one would make a test comparing two calls flap.
	entries []mapEntry
	// index finds an entry by its key's canonical form. Built lazily, since
	// most maps are small and built once.
	index map[string]int
}

type mapEntry struct {
	key   *Atomic
	value Sequence
}

func (m *MapItem) isItem() {}

// TypeName implements Item.
func (m *MapItem) TypeName() string { return "map(*)" }

// NewMap returns an empty map.
func NewMap() *MapItem { return &MapItem{index: map[string]int{}} }

// MapKeyOf returns the canonical form under which a key is compared.
//
// Two keys are the same key when they are equal under the "eq" operator with
// no type promotion beyond the numeric hierarchy, so 1 and 1.0 collide while
// "1" stands apart. Encoding that as a string keeps the lookup a plain map
// access rather than a scan with a comparison function.
func MapKeyOf(a *Atomic) (string, error) {
	if a == nil {
		return "", Errorf("XPTY0004", "a map key must be a single atomic value")
	}
	switch {
	case a.Type.IsNumeric():
		f := a.Float64()
		if math.IsNaN(f) {
			// NaN is equal to nothing, itself included, so it can never be
			// looked up through the "eq" rule. The specification carves it
			// out anyway: map:get(map:entry(xs:double('NaN'), 1), xs:float('NaN'))
			// is required to find the entry, so every NaN — of whatever
			// numeric type — is one key.
			return "num:NaN", nil
		}
		// Compared as an exact rational so that an integer and the double
		// spelling of the same value share an entry, while an xs:decimal that
		// merely rounds to the same double does not: map-put-023 stores
		// 1.0000000000100000000001 alongside the double 1.00000000001 and
		// requires two entries.
		if math.IsInf(f, 0) {
			// An infinity has no rational value, so big.Rat.SetFloat64
			// returns nil for it and the dereference below panicked. The two
			// infinities are still keys, one apiece, and they are shared
			// across the numeric types the way every other numeric key is.
			if f > 0 {
				return "num:INF", nil
			}
			return "num:-INF", nil
		}
		if r := a.Rat(); r != nil {
			return "num:" + r.RatString(), nil
		}
		return "num:" + new(big.Rat).SetFloat64(f).RatString(), nil
	case a.Type == TypeBoolean:
		if a.Bool() {
			return "bool:true", nil
		}
		return "bool:false", nil
	case a.Type == TypeQName:
		q := a.QName()
		return "qname:" + q.URI + "\x00" + q.Local, nil
	case a.Type == TypeDate || a.Type == TypeTime || a.Type == TypeDateTime:
		// A zoned value keys on the instant it denotes, not on its spelling:
		// xs:time('17:00:00Z') and xs:time('12:00:00-05:00') are the same
		// moment and so the same key (same-key-027).
		//
		// An unzoned value keys on its spelling instead, and so occupies a
		// space of its own. It could be normalised through the implicit
		// timezone, but that would make an unzoned value collide with its own
		// adjustment to that timezone, which same-key-013 through 015 require
		// to stay distinct — a map key is decided by the value, and a value
		// without a timezone is a different value from one with it.
		if dt := a.DateTimeVal(); dt != nil && dt.HasTZ {
			return a.Type.String() + ":tz:" + dt.ToSeconds(0).RatString(), nil
		}
	case a.Type == TypeDuration || a.Type == TypeYearMonthDuration ||
		a.Type == TypeDayTimeDuration:
		// The three duration types are one key family, and equality is over
		// the (months, seconds) pair rather than the lexical form: map-get-017
		// looks an entry keyed xs:duration('P1Y') up under
		// xs:yearMonthDuration('P12M') and must find it.
		if d := a.DurationVal(); d != nil {
			return fmt.Sprintf("dur:%d/%s", d.SignedMonths(), d.SignedSeconds().RatString()), nil
		}
		return "dur:" + a.String(), nil
	}
	// Everything else compares by its lexical value under its own type
	// family. The type name is part of the key so that xs:date("2001-01-01")
	// and the string of the same spelling are different keys.
	return typeFamilyOf(a) + ":" + a.String(), nil
}

// typeFamilyOf groups the types whose values are interchangeable as keys.
//
// The string family is one group because xs:string, xs:anyURI and the derived
// string types compare with one another under "eq"; every other type stands
// alone.
//
// xs:untypedAtomic is in that group rather than being rejected. The data model
// admits it as a key — map:entry(xs:untypedAtomic("foo"), "bar") is a legal
// map — and map:get on it applies the function conversion rules, which cast an
// untyped key to xs:string. So the untyped spelling of "foo" finds the string
// entry and vice versa (map-get-006, map-get-007), while the untyped spelling
// of "12" does *not* find the integer entry (map-get-008): the cast is to
// string, never to a number.
func typeFamilyOf(a *Atomic) string {
	switch a.Type {
	case TypeString, TypeAnyURI, TypeUntypedAtomic:
		return "str"
	}
	return a.Type.String()
}

// Put adds or replaces an entry, returning a new map and leaving the receiver
// untouched.
//
// Maps are immutable in the data model: map:put returns a map rather than
// changing one, and a caller holding the original must still see it.
func (m *MapItem) Put(key *Atomic, value Sequence) (*MapItem, error) {
	k, err := MapKeyOf(key)
	if err != nil {
		return nil, err
	}
	out := m.clone()
	if i, ok := out.index[k]; ok {
		out.entries[i] = mapEntry{key: key, value: value}
		return out, nil
	}
	out.index[k] = len(out.entries)
	out.entries = append(out.entries, mapEntry{key: key, value: value})
	return out, nil
}

// MapBuilder accumulates entries into a map in one pass.
//
// Put clones the whole map, which is right for the immutable data model but
// quadratic when a map is assembled entry by entry. map:merge is handed half a
// million singleton maps by the suite (map-keys-014), and building the result
// with Put took time proportional to the square of that. The builder owns its
// map until Build hands it over, so it can mutate in place; nothing else has a
// reference to observe the intermediate states.
type MapBuilder struct {
	m *MapItem
}

// NewMapBuilder returns a builder over an empty map.
func NewMapBuilder() *MapBuilder { return &MapBuilder{m: NewMap()} }

// NewMapBuilderFrom returns a builder seeded with a copy of m's entries, for
// the operations that start from an existing map.
func NewMapBuilderFrom(m *MapItem) *MapBuilder { return &MapBuilder{m: m.clone()} }

// Set adds or replaces an entry.
func (b *MapBuilder) Set(key *Atomic, value Sequence) error {
	k, err := MapKeyOf(key)
	if err != nil {
		return err
	}
	if i, ok := b.m.index[k]; ok {
		b.m.entries[i] = mapEntry{key: key, value: value}
		return nil
	}
	b.m.index[k] = len(b.m.entries)
	b.m.entries = append(b.m.entries, mapEntry{key: key, value: value})
	return nil
}

// Lookup reports the value already held under key, if any. map:merge's
// duplicate policies need to see the incumbent before deciding what to store.
func (b *MapBuilder) Lookup(key *Atomic) (Sequence, bool, error) {
	k, err := MapKeyOf(key)
	if err != nil {
		return nil, false, err
	}
	i, ok := b.m.index[k]
	if !ok {
		return nil, false, nil
	}
	return b.m.entries[i].value, true, nil
}

// Build returns the finished map. The builder must not be used afterwards, so
// that the map it hands out really is immutable.
func (b *MapBuilder) Build() *MapItem {
	m := b.m
	b.m = nil
	return m
}

// RemoveAll returns a map without any of the given keys.
//
// map:remove takes a *sequence* of keys, and removing them one at a time would
// rebuild the map once per key. Absent keys are ignored rather than being an
// error, which is what makes map:remove($m, ("a", "nosuch")) legal.
func (m *MapItem) RemoveAll(keys []*Atomic) (*MapItem, error) {
	drop := make(map[string]bool, len(keys))
	for _, key := range keys {
		k, err := MapKeyOf(key)
		if err != nil {
			return nil, err
		}
		drop[k] = true
	}
	// Nothing to do is answered with the receiver itself: a map is immutable,
	// so sharing it is safe and saves copying half a million entries for the
	// common map:remove($m, ()) .
	any := false
	for k := range drop {
		if _, ok := m.index[k]; ok {
			any = true
			break
		}
	}
	if !any {
		return m, nil
	}
	out := &MapItem{
		entries: make([]mapEntry, 0, len(m.entries)),
		index:   make(map[string]int, len(m.index)),
	}
	for _, e := range m.entries {
		ek, err := MapKeyOf(e.key)
		if err != nil {
			return nil, err
		}
		if drop[ek] {
			continue
		}
		out.index[ek] = len(out.entries)
		out.entries = append(out.entries, e)
	}
	return out, nil
}

// Remove returns a map without the given key.
func (m *MapItem) Remove(key *Atomic) (*MapItem, error) {
	return m.RemoveAll([]*Atomic{key})
}

// Get returns the value a key maps to, and whether the key is present.
//
// An absent key is the empty sequence rather than an error, which is what
// makes "$m?missing" usable in a predicate.
func (m *MapItem) Get(key *Atomic) (Sequence, bool, error) {
	k, err := MapKeyOf(key)
	if err != nil {
		return nil, false, err
	}
	i, ok := m.index[k]
	if !ok {
		return Empty(), false, nil
	}
	return m.entries[i].value, true, nil
}

// Len is the number of entries.
func (m *MapItem) Len() int { return len(m.entries) }

// Keys returns the keys in insertion order.
func (m *MapItem) Keys() []*Atomic {
	out := make([]*Atomic, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.key)
	}
	return out
}

// Entries calls f for each entry in insertion order, stopping on the first
// error.
func (m *MapItem) Entries(f func(key *Atomic, value Sequence) error) error {
	for _, e := range m.entries {
		if err := f(e.key, e.value); err != nil {
			return err
		}
	}
	return nil
}

func (m *MapItem) clone() *MapItem {
	out := &MapItem{
		entries: make([]mapEntry, len(m.entries)),
		index:   make(map[string]int, len(m.index)),
	}
	copy(out.entries, m.entries)
	for k, v := range m.index {
		out.index[k] = v
	}
	return out
}

// ArrayItem is the fifth kind of XDM item, added in XPath 3.1.
//
// An array holds a sequence of *members*, each of which is itself a sequence.
// That is the difference from a sequence, which is flat: [(1,2),(3)] has two
// members where (1,2,3) has three items, and the distinction survives every
// operation until fn:data or array:flatten deliberately removes it.
//
// Like a map, an array is also a function item: "$a(1)" is the first member,
// which is what the lookup operator "$a?1" lowers to.
type ArrayItem struct {
	members []Sequence
}

func (a *ArrayItem) isItem() {}

// TypeName implements Item.
func (a *ArrayItem) TypeName() string { return "array(*)" }

// NewArray returns an array of the given members.
func NewArray(members ...Sequence) *ArrayItem {
	return &ArrayItem{members: members}
}

// Len is the number of members.
func (a *ArrayItem) Len() int { return len(a.members) }

// Member returns the i'th member, counting from 1 as the data model does.
//
// An index outside the array is FOAY0001, which is a different error from
// asking a map for a key it does not have: an array's positions are its whole
// domain, so a position outside them is a mistake rather than an absence.
func (a *ArrayItem) Member(i int) (Sequence, error) {
	if i < 1 || i > len(a.members) {
		return nil, Errorf("FOAY0001",
			"array index %d is outside the array's bounds (1 to %d)", i, len(a.members))
	}
	return a.members[i-1], nil
}

// Members returns the members in order. The slice is a copy, so a caller
// cannot reach into the array through it.
func (a *ArrayItem) Members() []Sequence {
	out := make([]Sequence, len(a.members))
	copy(out, a.members)
	return out
}

// Flatten replaces every array in a sequence with its members, recursively,
// which is what array:flatten and the function conversion rules do.
func Flatten(seq Sequence) Sequence {
	out := make(Sequence, 0, len(seq))
	for _, it := range seq {
		arr, ok := it.(*ArrayItem)
		if !ok {
			out = append(out, it)
			continue
		}
		for _, m := range arr.members {
			out = append(out, Flatten(m)...)
		}
	}
	return out
}

// mapArrayString renders a map or array for an error message. It is
// deliberately terse: these appear in type errors, where the point is which
// kind of item was found rather than what it held.
func mapArrayString(it Item) string {
	switch v := it.(type) {
	case *MapItem:
		return fmt.Sprintf("map with %d entr%s", v.Len(), plural(v.Len(), "y", "ies"))
	case *ArrayItem:
		return fmt.Sprintf("array with %d member%s", v.Len(), plural(v.Len(), "", "s"))
	}
	return it.TypeName()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
