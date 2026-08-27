package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// MapConstructor is "map { k : v, ... }", production [69] of XPath 3.1.
type MapConstructor struct {
	Keys   []Expr
	Values []Expr
}

// Eval implements Expr.
func (e *MapConstructor) Eval(ctx *Context) (xdm.Sequence, error) {
	m := xdm.NewMap()
	for i, ke := range e.Keys {
		kv, err := ke.Eval(ctx)
		if err != nil {
			return nil, err
		}
		// The key is atomized and must be exactly one atomic value: a map
		// entry with no key, or with several, is not an entry.
		atoms, err := xdm.AtomizeChecked(kv)
		if err != nil {
			return nil, err
		}
		it, err := atoms.Single()
		if err != nil {
			return nil, xdm.ErrType("a map key must be a single atomic value")
		}
		key, ok := it.(*xdm.Atomic)
		if !ok {
			return nil, xdm.ErrType("a map key must be an atomic value")
		}
		val, err := e.Values[i].Eval(ctx)
		if err != nil {
			return nil, err
		}
		// A duplicate key in a *constructor* is an error, unlike map:merge,
		// which takes a duplicates option: the constructor names its entries
		// outright, so writing one twice is a mistake rather than a choice.
		if _, present, err := m.Get(key); err != nil {
			return nil, err
		} else if present {
			return nil, xdm.Errorf("XQDY0137",
				"the map constructor names the key %q twice", key.String())
		}
		m, err = m.Put(key, val)
		if err != nil {
			return nil, err
		}
	}
	return xdm.One(m), nil
}

// String implements Expr.
func (e *MapConstructor) String() string {
	var sb strings.Builder
	sb.WriteString("map {")
	for i := range e.Keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(e.Keys[i].String() + ": " + e.Values[i].String())
	}
	sb.WriteString("}")
	return sb.String()
}

// ArrayConstructor is "array { ... }" or "[ ... ]", productions [70]-[72].
//
// The two spellings differ in how they take their members, which is the whole
// point of having both: the square form makes one member per expression, so
// "[(1,2), 3]" has two members, while the curly form flattens whatever its
// single expression evaluates to, so "array { (1,2), 3 }" has three.
type ArrayConstructor struct {
	Members []Expr
	// Curly marks the "array { ... }" spelling.
	Curly bool
}

// Eval implements Expr.
func (e *ArrayConstructor) Eval(ctx *Context) (xdm.Sequence, error) {
	var members []xdm.Sequence
	for _, me := range e.Members {
		v, err := me.Eval(ctx)
		if err != nil {
			return nil, err
		}
		if !e.Curly {
			members = append(members, v)
			continue
		}
		for _, it := range v {
			members = append(members, xdm.One(it))
		}
	}
	return xdm.One(xdm.NewArray(members...)), nil
}

// String implements Expr.
func (e *ArrayConstructor) String() string {
	parts := make([]string, 0, len(e.Members))
	for _, m := range e.Members {
		parts = append(parts, m.String())
	}
	if e.Curly {
		return "array {" + strings.Join(parts, ", ") + "}"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// LookupExpr is the postfix lookup operator, "$m?k" and "$a?1", production
// [54].
//
// UnaryLookup ("?k" with no operand) is the same node with a nil Base, applied
// to the context item, which is what makes "$maps ! ?name" work.
type LookupExpr struct {
	Base Expr
	// Name, Index and Wildcard are the three shapes a key specifier takes;
	// exactly one is set. Expr is the parenthesised form, "?($k)".
	Name     string
	HasName  bool
	Index    int
	HasIndex bool
	Wildcard bool
	Expr     Expr
}

// Eval implements Expr.
func (e *LookupExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	var base xdm.Sequence
	if e.Base != nil {
		v, err := e.Base.Eval(ctx)
		if err != nil {
			return nil, err
		}
		base = v
	} else {
		if ctx.Item == nil {
			return nil, fmt.Errorf("XPDY0002: no context item for a lookup")
		}
		base = xdm.One(ctx.Item)
	}

	out := xdm.Sequence{}
	for _, it := range base {
		vals, err := e.lookupIn(ctx, it)
		if err != nil {
			return nil, err
		}
		out = append(out, vals...)
	}
	return out, nil
}

// lookupIn applies the key specifier to one item.
func (e *LookupExpr) lookupIn(ctx *Context, it xdm.Item) (xdm.Sequence, error) {
	switch v := it.(type) {
	case *xdm.MapItem:
		if e.Wildcard {
			// "?*" on a map is every value, in the map's own order.
			out := xdm.Sequence{}
			err := v.Entries(func(_ *xdm.Atomic, val xdm.Sequence) error {
				out = append(out, val...)
				return nil
			})
			return out, err
		}
		keys, err := e.keyValues(ctx)
		if err != nil {
			return nil, err
		}
		out := xdm.Sequence{}
		for _, k := range keys {
			val, _, err := v.Get(k)
			if err != nil {
				return nil, err
			}
			out = append(out, val...)
		}
		return out, nil

	case *xdm.ArrayItem:
		if e.Wildcard {
			out := xdm.Sequence{}
			for _, m := range v.Members() {
				out = append(out, m...)
			}
			return out, nil
		}
		keys, err := e.keyValues(ctx)
		if err != nil {
			return nil, err
		}
		out := xdm.Sequence{}
		for _, k := range keys {
			// An array is indexed by position, so the key has to be an
			// xs:integer: "?a" on an array is a type error rather than an
			// absent member, and so is "?(1.0)" — an xs:decimal is not an
			// integer, and admitting it because it happens to be numeric made
			// Lookup-119 answer with the members instead of XPTY0004.
			if k.Type != xdm.TypeInteger {
				return nil, xdm.ErrType(
					"an array is looked up by position, but the key is %s", k.TypeName())
			}
			m, err := v.Member(int(k.Float64()))
			if err != nil {
				return nil, err
			}
			out = append(out, m...)
		}
		return out, nil
	}
	// Anything else has nothing to look up in. FOTY0013 is the code for an
	// item type that does not support the operation.
	return nil, xdm.Errorf("XPTY0004",
		"the lookup operator needs a map or an array, got %s", it.TypeName())
}

// keyValues evaluates the key specifier to the atomic keys it names.
func (e *LookupExpr) keyValues(ctx *Context) ([]*xdm.Atomic, error) {
	switch {
	case e.HasName:
		// An NCName key is a string key, which is what makes "$m?name" the
		// same as "$m('name')".
		return []*xdm.Atomic{xdm.NewString(e.Name)}, nil
	case e.HasIndex:
		return []*xdm.Atomic{xdm.NewInteger(int64(e.Index))}, nil
	case e.Expr != nil:
		v, err := e.Expr.Eval(ctx)
		if err != nil {
			return nil, err
		}
		atoms, err := xdm.AtomizeChecked(v)
		if err != nil {
			return nil, err
		}
		// The parenthesised form may name several keys at once, which is how
		// "$m?(1, 2)" selects two entries.
		out := make([]*xdm.Atomic, 0, len(atoms))
		for _, it := range atoms {
			a, ok := it.(*xdm.Atomic)
			if !ok {
				return nil, xdm.ErrType("a lookup key must be an atomic value")
			}
			out = append(out, a)
		}
		return out, nil
	}
	return nil, fmt.Errorf("XPST0003: a lookup needs a key")
}

// String implements Expr.
func (e *LookupExpr) String() string {
	base := ""
	if e.Base != nil {
		base = e.Base.String()
	}
	switch {
	case e.Wildcard:
		return base + "?*"
	case e.HasName:
		return base + "?" + e.Name
	case e.HasIndex:
		return fmt.Sprintf("%s?%d", base, e.Index)
	case e.Expr != nil:
		return base + "?(" + e.Expr.String() + ")"
	}
	return base + "?"
}
