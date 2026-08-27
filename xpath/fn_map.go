package xpath

import (
	"github.com/knroy/go-xml/xdm"
)

// registerMapFuncs adds the map: functions of F&O 3.1 section 17.1.
//
// They live in their own namespace, so — like math: — reaching one requires an
// expression to bind the map URI to a prefix, which no 2.0 or 3.0 stylesheet
// has reason to do. They are still marked XPath31 rather than left ungated,
// because a 3.0 expression that *does* bind the prefix must be told the
// function does not exist (XPST0017) rather than being given a 3.1 feature.
//
// Every one of them takes the map as its first argument and is strict about
// it: map:get((), "a") is XPTY0004, not the empty sequence. A map is a single
// item, and an empty or multi-item first argument is a type error before the
// key is even looked at.
func registerMapFuncs(l *Library) {
	// map:get($map, $key) as item()*
	//
	// An absent key is the empty sequence, not an error. That is what makes a
	// map usable as a partial function, and it is the difference from an
	// array, where a position outside the bounds is FOAY0001.
	l.registerMap("get", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		key, err := argMapKey(args, 1)
		if err != nil {
			return nil, err
		}
		val, _, err := m.Get(key)
		return val, err
	})

	// map:contains($map, $key) as xs:boolean
	//
	// Distinct from "exists(map:get(...))": an entry whose value is the empty
	// sequence is still an entry, so map:contains is true where map:get
	// answers empty (map-contains-019).
	l.registerMap("contains", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		key, err := argMapKey(args, 1)
		if err != nil {
			return nil, err
		}
		_, present, err := m.Get(key)
		if err != nil {
			return nil, err
		}
		return boolSeq(present), nil
	})

	// map:size($map) as xs:integer
	l.registerMap("size", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		return intSeq(int64(m.Len())), nil
	})

	// map:keys($map) as xs:anyAtomicType*
	//
	// The specification fixes no order. Insertion order is used because it is
	// stable: a test that calls map:keys twice and compares the results would
	// otherwise flap.
	l.registerMap("keys", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		keys := m.Keys()
		out := make(xdm.Sequence, 0, len(keys))
		for _, k := range keys {
			out = append(out, k)
		}
		return out, nil
	})

	// map:put($map, $key, $value) as map(*)
	l.registerMap("put", []int{3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		key, err := argMapKey(args, 1)
		if err != nil {
			return nil, err
		}
		out, err := m.Put(key, args[2])
		if err != nil {
			return nil, err
		}
		return xdm.One(out), nil
	})

	// map:remove($map, $keys) as map(*)
	//
	// The second argument is a *sequence* of keys, and a key that is not
	// present is ignored rather than being an error: map:remove($m, ()) is the
	// map unchanged, and so is removing a key it never had.
	l.registerMap("remove", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		atoms, err := xdm.AtomizeChecked(args[1])
		if err != nil {
			return nil, err
		}
		keys := make([]*xdm.Atomic, 0, len(atoms))
		for _, it := range atoms {
			a, ok := it.(*xdm.Atomic)
			if !ok {
				return nil, xdm.ErrType("map:remove: a key must be an atomic value")
			}
			keys = append(keys, a)
		}
		out, err := m.RemoveAll(keys)
		if err != nil {
			return nil, err
		}
		return xdm.One(out), nil
	})

	// map:entry($key, $value) as map(*)
	l.registerMap("entry", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		key, err := argMapKey(args, 0)
		if err != nil {
			return nil, err
		}
		out, err := xdm.NewMap().Put(key, args[1])
		if err != nil {
			return nil, err
		}
		return xdm.One(out), nil
	})

	// map:merge($maps) and map:merge($maps, $options) as map(*)
	l.registerMap("merge", []int{1, 2}, mapMerge)

	// map:for-each($map, $action) as item()*
	//
	// The action is called once per entry with the key and the value, and the
	// results are concatenated. The order is the map's own, which the
	// specification leaves implementation-dependent.
	l.registerMap("for-each", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		m, err := argMap(args, 0)
		if err != nil {
			return nil, err
		}
		fn, err := argFunctionArity(args, 1, 2, "map:for-each")
		if err != nil {
			return nil, err
		}
		out := xdm.Sequence{}
		err = m.Entries(func(k *xdm.Atomic, v xdm.Sequence) error {
			r, err := fn.Invoke(ctx, []xdm.Sequence{xdm.One(k), v})
			if err != nil {
				return err
			}
			out = append(out, r...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	// map:find($input, $key) as array(*)
	//
	// Unlike every other function here, the input is an arbitrary sequence
	// rather than a map: map:find searches *recursively* through maps and
	// arrays anywhere in it, and collects every value found under the key into
	// one array. Anything that is neither a map nor an array is passed over
	// rather than being a type error, which is what makes it usable on a
	// json-doc whose shape is not known in advance.
	l.registerMap("find", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		key, err := argMapKey(args, 1)
		if err != nil {
			return nil, err
		}
		var found []xdm.Sequence
		var walk func(seq xdm.Sequence) error
		walk = func(seq xdm.Sequence) error {
			for _, it := range seq {
				switch v := it.(type) {
				case *xdm.MapItem:
					// The entry under $key is collected as one member, and
					// every value is then descended into — including that
					// one, since a matching entry may itself contain further
					// matches deeper down.
					if val, ok, err := v.Get(key); err != nil {
						return err
					} else if ok {
						found = append(found, val)
					}
					if err := v.Entries(func(_ *xdm.Atomic, val xdm.Sequence) error {
						return walk(val)
					}); err != nil {
						return err
					}
				case *xdm.ArrayItem:
					for _, mem := range v.Members() {
						if err := walk(mem); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
		if err := walk(args[0]); err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewArray(found...)), nil
	})
}

// mapMerge implements map:merge, which is the only function here with an
// options map.
//
// The interesting part is what happens to a key that appears in more than one
// input map. The default is "use-first", so the earliest map wins; the other
// policies are named by the "duplicates" option, and "reject" makes a
// duplicate an error rather than a choice.
func mapMerge(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	const (
		useFirst = "use-first"
		useLast  = "use-last"
		useAny   = "use-any"
		combine  = "combine"
		reject   = "reject"
	)
	policy := useFirst
	if len(args) > 1 {
		// The options map is not optional once written: passing () there is
		// XPTY0004, since the parameter is declared map(*) with no "?".
		// map-merge-026 writes a conditional that yields () and requires the
		// error rather than the default policy.
		opts, err := argMap(args, 1)
		if err != nil {
			return nil, err
		}
		val, present, err := opts.Get(xdm.NewString("duplicates"))
		if err != nil {
			return nil, err
		}
		if present {
			s, err := argString([]xdm.Sequence{val}, 0)
			if err != nil {
				return nil, err
			}
			switch s {
			case useFirst, useLast, useAny, combine, reject:
				policy = s
			default:
				return nil, xdm.Errorf("FOJS0005",
					"map:merge: %q is not a value of the duplicates option", s)
			}
		}
	}

	b := xdm.NewMapBuilder()
	for _, it := range args[0] {
		m, ok := it.(*xdm.MapItem)
		if !ok {
			return nil, xdm.ErrType(
				"map:merge: the input must be a sequence of maps, got %s", it.TypeName())
		}
		err := m.Entries(func(k *xdm.Atomic, v xdm.Sequence) error {
			prev, dup, err := b.Lookup(k)
			if err != nil {
				return err
			}
			if !dup {
				return b.Set(k, v)
			}
			switch policy {
			case useFirst, useAny:
				// "use-any" lets the implementation pick either; keeping the
				// first makes the answer the same on every run, which the
				// suite's any-of assertions permit and a stable result needs.
				return nil
			case useLast:
				return b.Set(k, v)
			case combine:
				joined := make(xdm.Sequence, 0, len(prev)+len(v))
				joined = append(joined, prev...)
				joined = append(joined, v...)
				return b.Set(k, joined)
			}
			return xdm.Errorf("FOJS0003",
				"map:merge: the key %q appears in more than one map", k.String())
		})
		if err != nil {
			return nil, err
		}
	}
	return xdm.One(b.Build()), nil
}

// mapItemMatches decides "instance of map(K, V)".
//
// Every entry has to satisfy both halves: the key against K, which is an
// atomic type with no occurrence indicator, and the whole value *sequence*
// against V. An empty map satisfies any typed map test vacuously, which is why
// "map{} instance of map(xs:integer, xs:string)" is true.
func mapItemMatches(t SequenceType, m *xdm.MapItem) bool {
	if t.MapKey == nil || t.MapValue == nil {
		return true // map(*)
	}
	ok := true
	_ = m.Entries(func(k *xdm.Atomic, v xdm.Sequence) error {
		if !t.MapKey.MatchesItem(k) || !t.MapValue.Matches(v) {
			ok = false
		}
		return nil
	})
	return ok
}

// mapMatchesFunctionTest decides "instance of function(...)" for a map.
//
// A map answers the empty sequence for a key it does not hold, so the test's
// return type has to admit the empty sequence as well as every value the map
// actually stores: "map{12:'z'} instance of function(xs:decimal) as xs:string"
// is false for that reason alone, while the same test written "xs:string?" is
// true. The parameter side is contravariant and trivially satisfied, since a
// map accepts xs:anyAtomicType, which subsumes every atomic type a test can
// name.
func mapMatchesFunctionTest(t SequenceType, m *xdm.MapItem) bool {
	if !t.HasFunctionArity {
		return true // function(*)
	}
	if t.FunctionArity != 1 {
		return false
	}
	if t.FunctionReturn != nil {
		ret := *t.FunctionReturn
		if !ret.Matches(xdm.Empty()) {
			return false
		}
		ok := true
		_ = m.Entries(func(_ *xdm.Atomic, v xdm.Sequence) error {
			if !ret.Matches(v) {
				ok = false
			}
			return nil
		})
		if !ok {
			return false
		}
	}
	// Contravariance: the map must accept everything the test's parameter
	// type admits. It accepts any single atomic value, so the only way to
	// fail is a parameter type that is not one — a node test, or a
	// cardinality wider than exactly one.
	for _, p := range t.FunctionParams {
		if !p.HasAtomicType || (p.Occurrence != "" && p.Occurrence != "?") {
			return false
		}
	}
	return true
}

// registerMap is registerFn for the map: namespace, gated at XPath 3.1.
func (l *Library) registerMap(local string, arities []int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) {
	for _, a := range arities {
		l.Add(Function{
			Name:  xdm.QName{URI: xdm.NSMap, Local: local},
			Arity: a,
			Call:  call,
			Since: XPath31,
		})
	}
}

// argMap returns argument i as the single map it must be.
//
// A map is one item, so an empty sequence, several items, or an item of
// another kind are all XPTY0004 — including a function item, which a map
// resembles closely enough (map-get-905 passes abs#1) that saying so
// explicitly is worth the line.
func argMap(args []xdm.Sequence, i int) (*xdm.MapItem, error) {
	if i >= len(args) || len(args[i]) != 1 {
		n := 0
		if i < len(args) {
			n = len(args[i])
		}
		return nil, xdm.ErrType(
			"argument %d: expected a single map, got %d items", i+1, n)
	}
	m, ok := args[i][0].(*xdm.MapItem)
	if !ok {
		return nil, xdm.ErrType(
			"argument %d: expected a map, got %s", i+1, args[i][0].TypeName())
	}
	return m, nil
}

// argMapKey returns argument i as the single atomic key it must be.
//
// The parameter is declared xs:anyAtomicType with no occurrence indicator, so
// the empty sequence is XPTY0004 rather than "no such key": map-get-901 passes
// (1 to 5)[10] and requires the error.
func argMapKey(args []xdm.Sequence, i int) (*xdm.Atomic, error) {
	if i >= len(args) {
		return nil, xdm.ErrType("argument %d: expected a key", i+1)
	}
	atoms, err := xdm.AtomizeChecked(args[i])
	if err != nil {
		return nil, err
	}
	if len(atoms) != 1 {
		return nil, xdm.ErrType(
			"argument %d: a key must be exactly one atomic value, got %d", i+1, len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("argument %d: a key must be an atomic value", i+1)
	}
	return a, nil
}

// argFunctionArity returns argument i as a function item of the given arity.
func argFunctionArity(args []xdm.Sequence, i, arity int, who string) (*xdm.FunctionItem, error) {
	if i >= len(args) || len(args[i]) != 1 {
		return nil, xdm.ErrType("%s: argument %d must be a single function", who, i+1)
	}
	fn := functionItemView(args[i][0])
	if fn == nil {
		return nil, xdm.ErrType("%s: argument %d is %s, not a function",
			who, i+1, args[i][0].TypeName())
	}
	if fn.Arity != arity {
		return nil, xdm.ErrType("%s: argument %d must take %d argument(s), not %d",
			who, i+1, arity, fn.Arity)
	}
	return fn, nil
}
