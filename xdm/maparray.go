package xdm

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
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
	// root is a hash array mapped trie over the canonical key (hamt.go); nil
	// for the empty map. Lookup, put and remove each touch one path of the
	// trie and share the rest with the map they were derived from.
	root *hamtNode
	// n is the number of entries, kept so that map:size stays O(1).
	n int
	// nextSeq is the sequence number the next new key receives. Iteration
	// sorts entries by seq, which is what preserves insertion order so that
	// serialising a map twice gives the same text: the specification fixes no
	// order for map:keys, and an unstable one would make a test comparing
	// two calls flap. A replaced key keeps its number, so map:put over an
	// existing key leaves it where it was.
	nextSeq uint64
}

type mapEntry struct {
	key   *Atomic
	value Sequence
	// ckey is the canonical form of key, the string the trie is keyed by.
	//
	// It is stored rather than recomputed because MapKeyOf is not cheap for a
	// numeric key -- it goes through big.Rat.RatString and string
	// concatenation -- and op:same-key-024 removes keys 11,250 times over.
	ckey string
	// hash is hamtHash(ckey), stored so that a removal or a rebuild never
	// rehashes.
	hash uint64
	// seq is the entry's place in insertion order.
	seq uint64
}

func (m *MapItem) isItem() {}

// TypeName implements Item.
func (m *MapItem) TypeName() string { return "map(*)" }

// NewMap returns an empty map.
func NewMap() *MapItem { return &MapItem{} }

// MapKeyOf returns the canonical form under which a key is compared.
//
// Two keys are the same key when they are equal under the "eq" operator with
// no type promotion beyond the numeric hierarchy, so 1 and 1.0 collide while
// "1" stands apart. Encoding that as a string keeps the lookup a plain map
// access rather than a scan with a comparison function.
//
// This is NOT the same relation as xpath.GroupingKey, and the difference is
// deliberate rather than an omission. Grouping substitutes the implicit
// timezone into an unzoned value, so xs:date("2015-04-08") and
// xs:date("2015-04-08Z") fall in one group. A map key must not: same-key-013,
// -014 and -015 build a three-entry map from exactly that pair and require all
// three entries to survive, because a key that depended on the implicit
// timezone would give one map different sizes in different dynamic contexts.
// The two keys answer different questions; neither is the other lagging behind.
//
// The relation this encodes is stated directly as SameKey in
// samekey_oracle_test.go, and TestMapKeyOfMatchesSameKey asserts that the
// encoding and the relation agree in both directions. Change one and that test
// tells you whether the other has to follow.
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
	case a.Type == TypeHexBinary || a.Type == TypeBase64Binary:
		// A binary value is a sequence of octets, and its spelling is not
		// part of it: XSD Part 2 §3.2.15 makes xs:hexBinary case-insensitive,
		// so "0F" and "0f" are one value, and §3.2.16 lets xs:base64Binary
		// carry whitespace between its characters. Keying on the lexical form
		// split those apart, so a value parsed from a document could fail to
		// find its own entry — xdm/node.go:1179 builds these straight from
		// the element text without canonicalising, and the "eq" operator
		// already decodes (xpath/operators.go:511), so the key had to as
		// well. The octets are the key; an undecodable value keeps its
		// spelling rather than erroring, since MapKeyOf is not a validator.
		if o, ok := binaryKeyOctets(a); ok {
			return a.Type.String() + ":bin:" + o, nil
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

// binaryKeyOctets decodes a binary value to the octets it denotes, returning
// them in one canonical spelling so that every lexical form of one value
// produces one key. Reported as a string because that is what the key is.
func binaryKeyOctets(a *Atomic) (string, bool) {
	if a.Type == TypeHexBinary {
		b, err := hex.DecodeString(strings.ToLower(a.String()))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	// Whitespace is legal between base64 characters, not merely around them.
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(a.String()), ""))
	if err != nil {
		return "", false
	}
	return string(b), true
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
// changing one, and a caller holding the original must still see it. The
// trie shares every node but the ones on the changed path, so this is
// O(log n) rather than the whole-map copy it used to be -- which, at the
// 421,875 entries of op:same-key-023, was 3.66ms per call and hours per case.
func (m *MapItem) Put(key *Atomic, value Sequence) (*MapItem, error) {
	k, err := MapKeyOf(key)
	if err != nil {
		return nil, err
	}
	e := &mapEntry{key: key, value: value, ckey: k, hash: hamtHash(k), seq: m.nextSeq}
	root, added := m.root.put(e, 0, nil)
	out := &MapItem{root: root, n: m.n, nextSeq: m.nextSeq}
	if added {
		out.n++
		out.nextSeq++
	}
	return out, nil
}

// MapBuilder accumulates entries into a map in one pass.
//
// map:merge is handed half a million singleton maps by the suite
// (map-keys-014), and building the result with Put would allocate a trie
// path per entry. The builder owns the nodes it creates until Build hands the
// map over, so it writes through them in place; nothing else has a reference
// to observe the intermediate states, and a node that belonged to an earlier
// map is copied before it is written.
type MapBuilder struct {
	m    *MapItem
	edit *hamtEdit
}

// NewMapBuilder returns a builder over an empty map.
func NewMapBuilder() *MapBuilder { return &MapBuilder{m: NewMap(), edit: &hamtEdit{}} }

// NewMapBuilderFrom returns a builder seeded with m's entries, for the
// operations that start from an existing map. m itself is never written.
func NewMapBuilderFrom(m *MapItem) *MapBuilder {
	return &MapBuilder{m: &MapItem{root: m.root, n: m.n, nextSeq: m.nextSeq}, edit: &hamtEdit{}}
}

// Set adds or replaces an entry.
func (b *MapBuilder) Set(key *Atomic, value Sequence) error {
	k, err := MapKeyOf(key)
	if err != nil {
		return err
	}
	e := &mapEntry{key: key, value: value, ckey: k, hash: hamtHash(k), seq: b.m.nextSeq}
	root, added := b.m.root.put(e, 0, b.edit)
	b.m.root = root
	if added {
		b.m.n++
		b.m.nextSeq++
	}
	return nil
}

// Lookup reports the value already held under key, if any. map:merge's
// duplicate policies need to see the incumbent before deciding what to store.
func (b *MapBuilder) Lookup(key *Atomic) (Sequence, bool, error) {
	k, err := MapKeyOf(key)
	if err != nil {
		return nil, false, err
	}
	e := b.m.root.get(hamtHash(k), 0, k)
	if e == nil {
		return nil, false, nil
	}
	return e.value, true, nil
}

// Build returns the finished map. The builder must not be used afterwards, so
// that the map it hands out really is immutable: the edit token its nodes
// carry is dropped here, and no later builder will match it.
func (b *MapBuilder) Build() *MapItem {
	m := b.m
	b.m = nil
	b.edit = nil
	return m
}

// RemoveAll returns a map without any of the given keys.
//
// map:remove takes a *sequence* of keys, and each is removed along its own
// trie path. Absent keys are ignored rather than being an error, which is
// what makes map:remove($m, ("a", "nosuch")) legal, and a call that removes
// nothing is answered with the receiver itself: a map is immutable, so
// sharing it is safe.
func (m *MapItem) RemoveAll(keys []*Atomic) (*MapItem, error) {
	root := m.root
	n := m.n
	for _, key := range keys {
		k, err := MapKeyOf(key)
		if err != nil {
			return nil, err
		}
		if root == nil {
			continue
		}
		next, removed := root.remove(hamtHash(k), 0, k)
		if !removed {
			continue
		}
		root, _ = next.(*hamtNode)
		n--
	}
	if n == m.n {
		return m, nil
	}
	return &MapItem{root: root, n: n, nextSeq: m.nextSeq}, nil
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
	e := m.root.get(hamtHash(k), 0, k)
	if e == nil {
		return Empty(), false, nil
	}
	return e.value, true, nil
}

// Len is the number of entries.
func (m *MapItem) Len() int { return m.n }

// ordered returns the entries in insertion order.
//
// The trie stores them in hash order, so they are collected and sorted by
// sequence number. That makes iteration O(n log n) where a slice was O(n);
// it is the price of a put that no longer copies the slice, and iteration
// over a large map is rare where put and remove over one are the whole of
// same-key-023.
func (m *MapItem) ordered() []*mapEntry {
	out := make([]*mapEntry, 0, m.n)
	if m.root != nil {
		m.root.walk(func(e *mapEntry) { out = append(out, e) })
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// Keys returns the keys in insertion order.
func (m *MapItem) Keys() []*Atomic {
	entries := m.ordered()
	out := make([]*Atomic, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.key)
	}
	return out
}

// Entries calls f for each entry in insertion order, stopping on the first
// error.
func (m *MapItem) Entries(f func(key *Atomic, value Sequence) error) error {
	for _, e := range m.ordered() {
		if err := f(e.key, e.value); err != nil {
			return err
		}
	}
	return nil
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
