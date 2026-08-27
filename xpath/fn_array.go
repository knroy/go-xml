package xpath

import (
	"math/big"
	"sort"

	"github.com/knroy/go-xml/xdm"
)

// registerArrayFuncs adds the array: functions of F&O 3.1 section 17.3.
//
// They live in their own namespace, like math:, so they need a registration
// helper of their own. Unlike math: they are gated on the version as well:
// arrays do not exist before 3.1, so a 3.0 expression that somehow bound the
// array URI must still get XPST0017 rather than a working call.
//
// Almost every one of them is a thin wrapper over xdm.ArrayItem. What is not
// thin, and what the suite spends most of its cases on, is the *index*
// handling: the array functions take xs:integer positions, and the difference
// between "out of range" (FOAY0001) and "not an integer at all" (XPTY0004) is
// asserted case by case.
func registerArrayFuncs(l *Library) {
	// array:size($array as array(*)) as xs:integer
	l.registerArray("size", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:size")
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewInteger(int64(a.Len()))), nil
	})

	// array:get($array as array(*), $position as xs:integer) as item()*
	l.registerArray("get", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:get")
		if err != nil {
			return nil, err
		}
		i, err := argArrayIndex(args, 1, "array:get")
		if err != nil {
			return nil, err
		}
		return a.Member(i)
	})

	// array:put($array, $position as xs:integer, $member as item()*) as array(*)
	l.registerArray("put", []int{3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:put")
		if err != nil {
			return nil, err
		}
		i, err := argArrayIndex(args, 1, "array:put")
		if err != nil {
			return nil, err
		}
		// Member is called for its bounds check rather than its value: put
		// replaces a member, so a position with no member is FOAY0001 exactly
		// as a get would be.
		if _, err := a.Member(i); err != nil {
			return nil, err
		}
		members := a.Members()
		members[i-1] = seqArg(args, 2)
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:append($array, $appendage as item()*) as array(*)
	l.registerArray("append", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:append")
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewArray(append(a.Members(), seqArg(args, 1))...)), nil
	})

	// array:subarray($array, $start as xs:integer[, $length as xs:integer])
	//
	// The bounds are stricter than fn:subsequence, which silently clips: here
	// a $start past the end is FOAY0001 and a negative $length is FOAY0002.
	// The one position past the end *is* legal for $start, since an empty
	// subarray has to start somewhere.
	l.registerArray("subarray", []int{2, 3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:subarray")
		if err != nil {
			return nil, err
		}
		start, err := argArrayIndex(args, 1, "array:subarray")
		if err != nil {
			return nil, err
		}
		if start < 1 || start > a.Len()+1 {
			return nil, xdm.Errorf("FOAY0001",
				"array:subarray: start %d is outside the array's bounds (1 to %d)",
				start, a.Len()+1)
		}
		length := a.Len() - start + 1
		if len(args) > 2 {
			n, err := argArrayIndex(args, 2, "array:subarray")
			if err != nil {
				return nil, err
			}
			if n < 0 {
				return nil, xdm.Errorf("FOAY0002",
					"array:subarray: length %d is negative", n)
			}
			if start+n > a.Len()+1 {
				return nil, xdm.Errorf("FOAY0001",
					"array:subarray: %d members from position %d runs past the end of a %d-member array",
					n, start, a.Len())
			}
			length = n
		}
		return xdm.One(xdm.NewArray(a.Members()[start-1 : start-1+length]...)), nil
	})

	// array:remove($array, $positions as xs:integer*) as array(*)
	//
	// The plural is the whole subtlety: remove takes a *sequence* of
	// positions, every one of which must be in range, and duplicates are not
	// an error — "(3, 2, 1, 2)" removes three distinct members.
	l.registerArray("remove", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:remove")
		if err != nil {
			return nil, err
		}
		drop := map[int]bool{}
		positions, err := argArrayIndexes(args, 1, "array:remove")
		if err != nil {
			return nil, err
		}
		for _, p := range positions {
			if _, err := a.Member(p); err != nil {
				return nil, err
			}
			drop[p] = true
		}
		members := make([]xdm.Sequence, 0, a.Len())
		for i, m := range a.Members() {
			if !drop[i+1] {
				members = append(members, m)
			}
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:insert-before($array, $position as xs:integer, $member as item()*)
	//
	// The legal positions run one past the end, which is what makes inserting
	// at the end expressible.
	l.registerArray("insert-before", []int{3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:insert-before")
		if err != nil {
			return nil, err
		}
		i, err := argArrayIndex(args, 1, "array:insert-before")
		if err != nil {
			return nil, err
		}
		if i < 1 || i > a.Len()+1 {
			return nil, xdm.Errorf("FOAY0001",
				"array:insert-before: position %d is outside the array's bounds (1 to %d)",
				i, a.Len()+1)
		}
		old := a.Members()
		members := make([]xdm.Sequence, 0, len(old)+1)
		members = append(members, old[:i-1]...)
		members = append(members, seqArg(args, 2))
		members = append(members, old[i-1:]...)
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:head($array) as item()*, array:tail($array) as array(*)
	//
	// Both are defined in terms of position 1, so both are FOAY0001 on an
	// empty array rather than returning nothing.
	l.registerArray("head", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:head")
		if err != nil {
			return nil, err
		}
		return a.Member(1)
	})
	l.registerArray("tail", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:tail")
		if err != nil {
			return nil, err
		}
		if _, err := a.Member(1); err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewArray(a.Members()[1:]...)), nil
	})

	// array:reverse($array) as array(*)
	l.registerArray("reverse", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:reverse")
		if err != nil {
			return nil, err
		}
		members := a.Members()
		for i, j := 0, len(members)-1; i < j; i, j = i+1, j-1 {
			members[i], members[j] = members[j], members[i]
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:join($arrays as array(*)*) as array(*)
	//
	// Concatenation of members, not of arrays: joining ([1,2],[3,4]) gives a
	// four-member array, and joining nothing gives the empty array.
	l.registerArray("join", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var members []xdm.Sequence
		for _, it := range seqArg(args, 0) {
			a, ok := it.(*xdm.ArrayItem)
			if !ok {
				return nil, xdm.ErrType(
					"array:join: expected an array, got %s", it.TypeName())
			}
			members = append(members, a.Members()...)
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:flatten($input as item()*) as item()*
	//
	// Defined over an arbitrary sequence rather than over an array, which is
	// why it takes item()* and why a map passes through untouched: only
	// arrays are opened up.
	l.registerArray("flatten", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return xdm.Flatten(seqArg(args, 0)), nil
	})

	// array:for-each($array, $action as function(item()*) as item()*) as array(*)
	//
	// The action is applied to each *member*, which is a sequence, not to each
	// item. That is the difference from fn:for-each and the reason
	// "array:for-each([10,20], upper-case#1)" is a type error the suite
	// checks: the member (10) is not an xs:string.
	l.registerArray("for-each", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:for-each")
		if err != nil {
			return nil, err
		}
		fn, err := argArrayFunction(args, 1, "array:for-each")
		if err != nil {
			return nil, err
		}
		members := a.Members()
		for i, m := range members {
			v, err := callFunction(ctx, fn, m)
			if err != nil {
				return nil, err
			}
			members[i] = v
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:filter($array, $function as function(item()*) as xs:boolean)
	l.registerArray("filter", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:filter")
		if err != nil {
			return nil, err
		}
		fn, err := argArrayFunction(args, 1, "array:filter")
		if err != nil {
			return nil, err
		}
		var members []xdm.Sequence
		for _, m := range a.Members() {
			v, err := callFunction(ctx, fn, m)
			if err != nil {
				return nil, err
			}
			keep, err := singleBoolean(v, "array:filter")
			if err != nil {
				return nil, err
			}
			if keep {
				members = append(members, m)
			}
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:fold-left($array, $zero, $function) — accumulator first, as in
	// fn:fold-left; array:fold-right takes the member first. The two are
	// written out rather than sharing a flagged helper because getting the
	// argument order backwards is silent for a commutative operation.
	l.registerArray("fold-left", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:fold-left")
		if err != nil {
			return nil, err
		}
		fn, err := argArrayFunction(args, 2, "array:fold-left")
		if err != nil {
			return nil, err
		}
		acc := seqArg(args, 1)
		for _, m := range a.Members() {
			acc, err = callFunction(ctx, fn, acc, m)
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	})
	l.registerArray("fold-right", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:fold-right")
		if err != nil {
			return nil, err
		}
		fn, err := argArrayFunction(args, 2, "array:fold-right")
		if err != nil {
			return nil, err
		}
		acc := seqArg(args, 1)
		members := a.Members()
		for i := len(members) - 1; i >= 0; i-- {
			acc, err = callFunction(ctx, fn, members[i], acc)
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	})

	// array:for-each-pair($array1, $array2, $function) as array(*)
	//
	// Stops at the shorter of the two, like fn:for-each-pair.
	l.registerArray("for-each-pair", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:for-each-pair")
		if err != nil {
			return nil, err
		}
		b, err := argArray(args, 1, "array:for-each-pair")
		if err != nil {
			return nil, err
		}
		fn, err := argArrayFunction(args, 2, "array:for-each-pair")
		if err != nil {
			return nil, err
		}
		am, bm := a.Members(), b.Members()
		n := len(am)
		if len(bm) < n {
			n = len(bm)
		}
		members := make([]xdm.Sequence, 0, n)
		for i := 0; i < n; i++ {
			v, err := callFunction(ctx, fn, am[i], bm[i])
			if err != nil {
				return nil, err
			}
			members = append(members, v)
		}
		return xdm.One(xdm.NewArray(members...)), nil
	})

	// array:sort($array[, $collation[, $key]]) as array(*)
	l.registerArray("sort", []int{1, 2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argArray(args, 0, "array:sort")
		if err != nil {
			return nil, err
		}
		members := a.Members()
		sorted, err := sortByKey(ctx, members, args, "array:sort")
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewArray(sorted...)), nil
	})
}

// registerFnSort adds fn:sort, which is array:sort over a sequence.
//
// It is here rather than with the other higher-order functions because it
// shares every line of its comparison logic with array:sort; the only
// difference is that its units are single items rather than array members.
func registerFnSort(l *Library) {
	// fn:sort($input as item()*[, $collation[, $key]]) as item()*
	l.registerFnSince(XPath31, "sort", []int{1, 2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		input := seqArg(args, 0)
		units := make([]xdm.Sequence, len(input))
		for i, it := range input {
			units[i] = xdm.One(it)
		}
		sorted, err := sortByKey(ctx, units, args, "fn:sort")
		if err != nil {
			return nil, err
		}
		out := make(xdm.Sequence, 0, len(sorted))
		for _, u := range sorted {
			out = append(out, u...)
		}
		return out, nil
	})
}

// sortByKey is the shared body of fn:sort and array:sort.
//
// Both take the same optional $collation and $key arguments in positions 1 and
// 2, and both sort *units* — array members for one, single items for the other
// — by the atomized sequence the key function produces. The sort is stable,
// which the specification requires and which the NaN cases depend on: two
// members whose keys start with NaN are incomparable, so only stability keeps
// them in their original order.
func sortByKey(ctx *Context, units []xdm.Sequence, args []xdm.Sequence, fname string) ([]xdm.Sequence, error) {
	// fn:sort and array:sort are the two functions whose collation parameter
	// is declared xs:string? rather than xs:string, so an explicitly supplied
	// empty sequence means "the default collation" rather than being the type
	// error it is everywhere else. The suite writes exactly that — "(),
	// fn:abs#1" — to reach the third argument without naming a collation, so
	// the empty case is turned into the omitted case before asking.
	collArgs := args
	if len(args) > 1 && len(args[1]) == 0 {
		collArgs = args[:1]
	}
	coll, err := collationArgCtx(ctx, fname, collArgs, 1)
	if err != nil {
		return nil, err
	}
	cmpCtx := withCollation(ctx, coll)

	// fn:data is the default key, so the keys are computed once up front
	// rather than at each comparison. That also fixes *where* a key function's
	// error surfaces: computing it lazily inside the comparator would make
	// whether an error is raised depend on which comparisons the sort happened
	// to perform.
	keys := make([]xdm.Sequence, len(units))
	var keyFn *xdm.FunctionItem
	if len(args) > 2 {
		if len(seqArg(args, 2)) > 0 {
			keyFn, err = argArrayFunction(args, 2, fname)
			if err != nil {
				return nil, err
			}
		}
	}
	for i, u := range units {
		v := u
		if keyFn != nil {
			v, err = callFunction(cmpCtx, keyFn, u)
			if err != nil {
				return nil, err
			}
		}
		// AtomizeChecked rather than Atomize so that a map in the input is
		// FOTY0013 rather than silently vanishing: array:sort([map{},1]) is an
		// error the suite asserts, and a dropped key would have sorted it.
		atoms, err := xdm.AtomizeChecked(v)
		if err != nil {
			return nil, err
		}
		keys[i] = atoms
	}

	// The comparison can fail — two keys of incomparable types are XPTY0004 —
	// but sort.SliceStable has nowhere to report that, so the first failure is
	// captured and the comparator degrades to "equal" for the rest. The sort
	// still terminates; its result is discarded.
	idx := make([]int, len(units))
	for i := range idx {
		idx[i] = i
	}
	var cmpErr error
	sort.SliceStable(idx, func(i, j int) bool {
		if cmpErr != nil {
			return false
		}
		c, err := compareKeySeq(cmpCtx, keys[idx[i]], keys[idx[j]])
		if err != nil {
			cmpErr = err
			return false
		}
		return c < 0
	})
	if cmpErr != nil {
		return nil, cmpErr
	}
	out := make([]xdm.Sequence, len(units))
	for i, k := range idx {
		out[i] = units[k]
	}
	return out, nil
}

// compareKeySeq orders two sort keys, each of which is a sequence.
//
// The specification defines this pairwise from the front, with the shorter
// sequence ordering first when it is a prefix of the longer. That is why
// array:sort([(1,0),(1,1),(0,1),(0,0),(),(1),(0,0,1)]) puts the empty key
// first and (0,0) before (0,0,1).
func compareKeySeq(ctx *Context, a, b xdm.Sequence) (int, error) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		c, err := compareKeyAtom(ctx, a[i], b[i])
		if err != nil || c != 0 {
			return c, err
		}
	}
	return len(a) - len(b), nil
}

// compareKeyAtom orders one pair of key items.
//
// It is not compareValues with "lt": sorting needs a three-way answer, and it
// needs NaN to be an ordinary value rather than the unordered one that "lt"
// makes it. The specification's sort order puts NaN before every other number
// so that a key containing one still has a definite place; leaving it
// unordered made the sort's result depend on the comparison order, and the
// NaN cases in both array-sort and fn-sort assert a definite one.
func compareKeyAtom(ctx *Context, x, y xdm.Item) (int, error) {
	a, aok := x.(*xdm.Atomic)
	b, bok := y.(*xdm.Atomic)
	if !aok || !bok {
		return 0, xdm.ErrType("a sort key must be an atomic value")
	}
	// xs:untypedAtomic sorts as a string, and only against another string:
	// the specification's sort key comparison is a value comparison, which
	// does not cast an untyped operand against a number. fn-sort-error-3
	// asserts that (1, xs:untypedAtomic("2")) is XPTY0004 rather than being
	// sorted numerically.
	if a.Type == xdm.TypeUntypedAtomic && b.Type == xdm.TypeUntypedAtomic {
		a, b = xdm.NewString(a.Str()), xdm.NewString(b.Str())
	}
	if a.IsNaN() || b.IsNaN() {
		switch {
		case a.IsNaN() && b.IsNaN():
			return 0, nil
		case !a.Type.IsNumeric() || !b.Type.IsNumeric():
			return 0, xdm.ErrType("cannot compare %s with %s", a.TypeName(), b.TypeName())
		case a.IsNaN():
			return -1, nil
		default:
			return 1, nil
		}
	}
	c, ordered, err := rawCompare(ctx, a, b)
	if err != nil {
		return 0, err
	}
	if !ordered {
		return 0, xdm.ErrType("values of type %s are not ordered", a.TypeName())
	}
	return c, nil
}

// registerArray is registerFn for the array: namespace, with the version gate
// registerMath does not need: math: existed in 3.0, arrays only in 3.1.
func (l *Library) registerArray(local string, arities []int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) {
	for _, a := range arities {
		l.Add(Function{
			Name:  xdm.QName{URI: xdm.NSArray, Local: local},
			Arity: a,
			Call:  call,
			Since: XPath31,
		})
	}
}

// argArray returns argument i as the single array it must be.
//
// An empty sequence or several items is XPTY0004, not FOAY0001: the argument
// is declared array(*), so a value that is not one fails the signature before
// any position is looked at. array:get((), 1) asserts exactly this.
func argArray(args []xdm.Sequence, i int, fname string) (*xdm.ArrayItem, error) {
	if i >= len(args) || len(args[i]) != 1 {
		return nil, xdm.ErrType("%s: expected a single array", fname)
	}
	a, ok := args[i][0].(*xdm.ArrayItem)
	if !ok {
		return nil, xdm.ErrType("%s: argument is %s, not an array",
			fname, args[i][0].TypeName())
	}
	return a, nil
}

// argArrayFunction returns argument i as a function item.
//
// It is argFunction widened to accept a map or an array, both of which are
// function items in the data model even though they are not represented as
// one here. array:for-each(["Monday"], map{"Monday":true()}) is a case the
// suite makes, and it must call the map as a function of one argument.
func argArrayFunction(args []xdm.Sequence, i int, fname string) (*xdm.FunctionItem, error) {
	if i >= len(args) || len(args[i]) != 1 {
		return nil, xdm.ErrType("%s: expected a single function item", fname)
	}
	if fn := functionItemView(args[i][0]); fn != nil {
		return fn, nil
	}
	return nil, xdm.ErrType("%s: argument is %s, not a function",
		fname, args[i][0].TypeName())
}

// argArrayIndex reads a position argument.
//
// The declared type is xs:integer with no occurrence indicator, so an empty
// sequence, several values, or a non-integral number are all XPTY0004 — the
// suite asserts that array:get([1,2,3], 1.2) is a type error rather than a
// truncation to 1. Only a value that *is* an integer and is out of range gets
// as far as FOAY0001.
func argArrayIndex(args []xdm.Sequence, i int, fname string) (int, error) {
	ns, err := argArrayIndexes(args, i, fname)
	if err != nil {
		return 0, err
	}
	if len(ns) != 1 {
		return 0, xdm.ErrType("%s: expected a single xs:integer position", fname)
	}
	return ns[0], nil
}

// argArrayIndexes reads a sequence of position arguments, for array:remove.
//
// A position too large to be an int is reported as out of bounds rather than
// wrapped: array:remove([1], 4294967297) is FOAY0001, and truncating to 32
// bits would have made it position 1.
func argArrayIndexes(args []xdm.Sequence, i int, fname string) ([]int, error) {
	atoms, err := xdm.AtomizeChecked(seqArg(args, i))
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(atoms))
	for _, it := range atoms {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			return nil, xdm.ErrType("%s: a position must be an xs:integer", fname)
		}
		n, err := integerPosition(a, fname)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// integerPosition converts one atomic value to a position.
//
// xs:untypedAtomic is cast to xs:integer by the function conversion rules,
// which is what makes a position read out of a document usable; every other
// non-integer type is XPTY0004.
func integerPosition(a *xdm.Atomic, fname string) (int, error) {
	if a.Type == xdm.TypeUntypedAtomic {
		c, err := CastAtomic(a, xdm.TypeInteger)
		if err != nil {
			return 0, xdm.ErrType("%s: %q is not an xs:integer position", fname, a.Str())
		}
		a = c
	}
	if a.Type != xdm.TypeInteger {
		return 0, xdm.ErrType("%s: a position must be an xs:integer, got %s",
			fname, a.TypeName())
	}
	r := a.Rat()
	if r == nil || !r.IsInt() {
		return 0, xdm.ErrType("%s: a position must be an xs:integer", fname)
	}
	n := r.Num()
	// Anything outside the range of an int cannot name a member of an array
	// that fits in memory, so it is out of bounds by construction. Saying so
	// here keeps the conversion from silently wrapping.
	if n.Cmp(big.NewInt(int64(maxArrayIndex))) > 0 ||
		n.Cmp(big.NewInt(int64(minArrayIndex))) < 0 {
		return 0, xdm.Errorf("FOAY0001",
			"%s: position %s is outside the array's bounds", fname, n.String())
	}
	return int(n.Int64()), nil
}

// The bounds outside which a position cannot possibly name a member. They are
// deliberately far narrower than an int64: an array with more members than
// this cannot be built, so a position beyond them is out of range whatever the
// array is.
const (
	maxArrayIndex = 1 << 40
	minArrayIndex = -(1 << 40)
)

// arrayMatchesFunctionTest decides "array instance of function(...) as ...".
//
// An array is a function of arity one from xs:integer to the member type, so
// the test is the map one with the parameter side narrowed: a map accepts any
// atomic key, an array only an integer position. prod-ArrayTest asserts both
// "instance of function(*)" and "instance of function(xs:integer) as item()*".
func arrayMatchesFunctionTest(t SequenceType, a *xdm.ArrayItem) bool {
	if !t.HasFunctionArity {
		return true // function(*)
	}
	if t.FunctionArity != 1 {
		return false
	}
	// Covariance on the result: every member sequence is a possible return
	// value, so each must be within what the test promises.
	if t.FunctionReturn != nil {
		for _, m := range a.Members() {
			if !t.FunctionReturn.Matches(m) {
				return false
			}
		}
	}
	// Contravariance on the parameter: the array must accept everything the
	// declared parameter type admits, and it accepts exactly one integer.
	for _, p := range t.FunctionParams {
		if !p.HasAtomicType || !atomicTypeMatches(xdm.TypeInteger, p.AtomicType) ||
			(p.Occurrence != "" && p.Occurrence != "?") {
			return false
		}
	}
	return true
}
