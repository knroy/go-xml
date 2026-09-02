package xquery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// A flwor is a FLWOR expression: §3.10's initial clause, the clauses that
// follow it, and the return expression.
//
// XQuery's FLWOR is not XPath's nested "for ... return". §3.10 defines it over
// a *tuple stream*: the initial clause produces a stream of tuples, each
// tuple being one set of variable bindings, and every subsequent clause is a
// function from a stream to a stream. "for" multiplies the stream, "let" adds
// a column to each tuple, "where" filters, "order by" permutes, "group by"
// partitions and "count" numbers.
//
// Modelling it as nested iteration would work for "for" and "let" alone and
// then fall apart: "order by" has to see every tuple before it can emit the
// first, and "group by" has to see every tuple before it can decide which
// partition any of them belongs to. Neither is expressible as a rewrite of
// the loop body, which is why XPath's ForExpr is not a starting point.
type flwor struct {
	clauses []clause
	ret     *compiledExpr
	// retItems is set instead of ret when the return expression is a
	// constructor, which the expression parser cannot read.
	retItems []node
}

// A tuple is one set of variable bindings flowing through the pipeline.
//
// The bindings are held as a slice rather than a map because a FLWOR binds a
// handful of variables and copies a tuple once per clause per tuple; a linear
// scan over four entries beats hashing, and the order is meaningful for
// grouping, where the non-grouping variables are exactly the ones not named
// in the group-by clause.
type tuple struct {
	names []xdm.QName
	vals  []xdm.Sequence
}

// bind adds or replaces a binding, returning a tuple that shares nothing
// mutable with t.
//
// Copying rather than mutating is what makes "for" correct: the body of a
// downstream clause may capture the tuple (an inline function closing over
// $x, a grouped partition holding its members), and a tuple mutated in place
// would make every capture observe the last value.
func (t tuple) bind(name xdm.QName, val xdm.Sequence) tuple {
	for i, n := range t.names {
		if n == name {
			out := t.clone()
			out.vals[i] = val
			return out
		}
	}
	out := tuple{
		names: append(append([]xdm.QName(nil), t.names...), name),
		vals:  append(append([]xdm.Sequence(nil), t.vals...), val),
	}
	return out
}

func (t tuple) clone() tuple {
	return tuple{
		names: append([]xdm.QName(nil), t.names...),
		vals:  append([]xdm.Sequence(nil), t.vals...),
	}
}

func (t tuple) lookup(name xdm.QName) (xdm.Sequence, bool) {
	for i, n := range t.names {
		if n == name {
			return t.vals[i], true
		}
	}
	return nil, false
}

// context returns the evaluation context a clause expression sees.
//
// Every binding in the tuple is layered onto the query's context, so a
// variable bound by an earlier clause resolves in a later one and in the
// return expression. xpath resolves a variable reference at evaluation rather
// than at parse, so nothing about the names has to reach the expression
// parser: binding the values here is the whole of the scoping.
func (t tuple) context(ctx *evalContext) *xpath.Context {
	xp := ctx.xp
	for i, n := range t.names {
		xp = xp.WithVar(n, t.vals[i])
	}
	return xp
}

// sub is context wrapped back up as an evalContext, which is what a
// constructor bound by a clause needs in order to build in the right static
// context.
func (t tuple) sub(ctx *evalContext) *evalContext {
	return &evalContext{xp: t.context(ctx), sc: ctx.sc}
}

// evalBool is the effective boolean value of a clause expression, which
// "where" tests and a quantified expression's satisfies clause both need.
func evalBool(e *compiledExpr, ctx *evalContext) (bool, error) {
	if e.items == nil {
		return e.compiled.EvalBool(ctx.xp)
	}
	seq, err := e.eval(ctx)
	if err != nil {
		return false, err
	}
	return xpath.EffectiveBooleanValue(seq)
}

// A clause transforms a tuple stream.
//
// The stream is materialised rather than streamed because "order by" and
// "group by" are blocking in any case, and a FLWOR's stream is bounded by the
// sequences it iterates. Laziness would buy something only for a FLWOR whose
// clauses are all non-blocking, and would cost the straightforward shape that
// makes the blocking ones readable.
type clause interface {
	apply(in []tuple, ctx *evalContext) ([]tuple, error)
}

// eval runs the pipeline and concatenates the return expression's value over
// the surviving tuples, which is §3.10's final step.
func (f *flwor) eval(ctx *evalContext) (xdm.Sequence, error) {
	// The pipeline starts with one empty tuple: the initial clause binds into
	// it rather than creating the stream from nothing, which is what lets
	// "let" be an initial clause as legitimately as "for".
	stream := []tuple{{}}
	for _, c := range f.clauses {
		var err error
		stream, err = c.apply(stream, ctx)
		if err != nil {
			return nil, err
		}
	}
	var out xdm.Sequence
	for _, t := range stream {
		seq, err := f.evalReturn(t, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, seq...)
	}
	return out, nil
}

// evalReturn evaluates the return expression against one tuple.
func (f *flwor) evalReturn(t tuple, ctx *evalContext) (xdm.Sequence, error) {
	sub := t.sub(ctx)
	if f.ret != nil {
		return f.ret.eval(sub)
	}
	return evalItems(f.retItems, sub)
}

// evalNode implements node, so that a FLWOR may appear as content of a
// constructor and as an item of a query body.
func (f *flwor) evalNode(out *builderRef, ctx *evalContext) error {
	seq, err := f.eval(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

// evalItems evaluates a parsed item list to a sequence, which is what a
// return expression that is a constructor needs.
func evalItems(items []node, ctx *evalContext) (xdm.Sequence, error) {
	n := &enclosed{items: items}
	return n.sequence(ctx)
}

// --- for -------------------------------------------------------------------

// forClause is one binding of a "for" clause: §3.10.2.
//
// A "for" with several comma-separated bindings is compiled to one forClause
// each, applied in order, because that is exactly what the specification says
// it means — the second binding's stream is the first's, multiplied.
type forClause struct {
	name xdm.QName
	// pos is the positional variable of "at $i", zero-valued when absent.
	pos    xdm.QName
	hasPos bool
	// allowingEmpty makes an empty binding sequence yield one tuple with the
	// variable bound to the empty sequence, rather than none at all.
	allowingEmpty bool
	// emptyCheck tests the declared type against the empty sequence, for the
	// tuple allowingEmpty produces. The per-item check on seq cannot cover
	// that binding, because it loops over items and there are none. Nil when
	// the clause declares no type or does not allow empty.
	emptyCheck *compiledExpr
	seq        *compiledExpr
}

func (c *forClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	var out []tuple
	for _, t := range in {
		seq, err := c.seq.eval(t.sub(ctx))
		if err != nil {
			return nil, err
		}
		if len(seq) == 0 && c.allowingEmpty {
			// §3.10.2: "allowing empty" is the outer join. The variable is
			// bound to the empty sequence and the positional variable to 0,
			// which is the one position value no item can occupy.
			//
			// This is the one binding a declared type is checked against
			// here rather than per item: the per-item check runs over seq,
			// which is empty. A type that excludes the empty sequence — a
			// bare "xs:integer" rather than "xs:integer?" — is XPTY0004 at
			// exactly this point, and only at this point, since a clause
			// whose sequence is never empty never reaches it.
			if err := runEmptyCheck(c.emptyCheck); err != nil {
				return nil, err
			}
			n := t.bind(c.name, nil)
			if c.hasPos {
				n = n.bind(c.pos, xdm.One(xdm.NewInteger(0)))
			}
			out = append(out, n)
			continue
		}
		for i, it := range seq {
			n := t.bind(c.name, xdm.One(it))
			if c.hasPos {
				n = n.bind(c.pos, xdm.One(xdm.NewInteger(int64(i+1))))
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// --- let -------------------------------------------------------------------

// letClause binds one variable to a whole sequence: §3.10.3. It adds a column
// to every tuple and never changes how many there are.
type letClause struct {
	name xdm.QName
	seq  *compiledExpr
}

func (c *letClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	out := make([]tuple, 0, len(in))
	for _, t := range in {
		seq, err := c.seq.eval(t.sub(ctx))
		if err != nil {
			return nil, err
		}
		out = append(out, t.bind(c.name, seq))
	}
	return out, nil
}

// --- where -----------------------------------------------------------------

// whereClause keeps the tuples whose test has an effective boolean value of
// true: §3.10.5.
type whereClause struct{ test *compiledExpr }

func (c *whereClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	var out []tuple
	for _, t := range in {
		ok, err := evalBool(c.test, t.sub(ctx))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// --- count -----------------------------------------------------------------

// countClause numbers the tuples it receives, from 1: §3.10.4.
//
// It is placed after "order by" to number the sorted stream and before it to
// number the unsorted one, which is the whole reason it exists as a clause
// rather than as a form of "at $i" — a positional variable numbers one "for"'s
// items, and this numbers the stream however the clauses above it left it.
type countClause struct{ name xdm.QName }

func (c *countClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	out := make([]tuple, 0, len(in))
	for i, t := range in {
		out = append(out, t.bind(c.name, xdm.One(xdm.NewInteger(int64(i+1)))))
	}
	return out, nil
}

// --- order by --------------------------------------------------------------

// orderSpec is one comparison key of an "order by" clause.
type orderSpec struct {
	key        *compiledExpr
	descending bool
	// emptyGreatest is the empty-order for this spec: it is set from the
	// static context's default, which "declare default order empty
	// greatest|least" establishes for the module, and overridden by an
	// "empty greatest|least" written on the spec itself.
	emptyGreatest bool
	collation     string
}

// orderByClause sorts the tuple stream: §3.10.6.
type orderByClause struct {
	specs []orderSpec
	// stable is written in the query and is honoured by always sorting
	// stably. An unstable sort is permitted to reorder equal tuples and this
	// one does not, so "stable" costs nothing and "order by" alone gives the
	// stronger guarantee — which is legal, since the specification leaves
	// unstable ordering implementation-defined rather than requiring
	// instability.
	stable bool
}

func (c *orderByClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	if len(in) < 2 {
		return in, nil
	}
	// The keys are computed once per tuple per spec rather than on each
	// comparison. A sort makes O(n log n) comparisons and the key expression
	// may be arbitrarily expensive, so computing it n times instead of
	// n log n times is not an optimisation but the difference between a
	// FLWOR that finishes and one that does not.
	keys := make([][]*xdm.Atomic, len(in))
	for i, t := range in {
		row := make([]*xdm.Atomic, len(c.specs))
		for j := range c.specs {
			a, err := orderKey(c.specs[j].key, t, ctx)
			if err != nil {
				return nil, err
			}
			row[j] = a
		}
		keys[i] = row
	}

	idx := make([]int, len(in))
	for i := range idx {
		idx[i] = i
	}
	var sortErr error
	sort.SliceStable(idx, func(a, b int) bool {
		if sortErr != nil {
			return false
		}
		ka, kb := keys[idx[a]], keys[idx[b]]
		for j := range c.specs {
			cmp, err := compareOrderKeys(ka[j], kb[j], &c.specs[j], ctx)
			if err != nil {
				sortErr = err
				return false
			}
			if cmp != 0 {
				if c.specs[j].descending {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})
	if sortErr != nil {
		return nil, sortErr
	}
	out := make([]tuple, len(in))
	for i, j := range idx {
		out[i] = in[j]
	}
	return out, nil
}

// orderKey evaluates one ordering key and reduces it to the single atomic
// value a comparison needs.
//
// §3.10.6 atomises the key and requires the result to be at most one item;
// more than one is XPTY0004. An untypedAtomic is cast to xs:string, which is
// what makes "order by @price" on an unvalidated document compare
// lexicographically rather than raising a type error.
func orderKey(e *compiledExpr, t tuple, ctx *evalContext) (*xdm.Atomic, error) {
	seq, err := e.eval(t.sub(ctx))
	if err != nil {
		return nil, err
	}
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return nil, err
	}
	if len(atoms) == 0 {
		return nil, nil
	}
	if len(atoms) > 1 {
		return nil, fmt.Errorf(
			"XPTY0004: an ordering key must be at most one item, not %d",
			len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil, fmt.Errorf(
			"XPTY0004: %s cannot be used as an ordering key",
			atoms[0].TypeName())
	}
	if a.Type == xdm.TypeUntypedAtomic {
		return xdm.NewString(a.String()), nil
	}
	return a, nil
}

// compareOrderKeys orders two keys, placing the empty sequence and NaN where
// the clause's empty-order asks.
//
// §3.10.6 treats NaN as though it were the empty sequence for ordering, which
// is the one place in the language where it has a defined position rather
// than being incomparable. Without that rule a stylesheet sorting a column
// containing one NaN would get an order that depends on the sort algorithm.
func compareOrderKeys(a, b *xdm.Atomic, spec *orderSpec, ctx *evalContext) (int, error) {
	ea, eb := isOrderEmpty(a), isOrderEmpty(b)
	switch {
	case ea && eb:
		return 0, nil
	case ea, eb:
		// "empty least" ranks the empty key below every other and "empty
		// greatest" above it, and that is all this decides. The direction
		// is not applied here: the caller reverses the sign of whatever
		// this returns when the spec is descending, exactly as it does for
		// an ordinary comparison, so negating it here as well would cancel
		// out and leave the empty-order the same in both directions.
		//
		// The effect is that "descending" does move the empty key, and the
		// suite says it should: prod-EmptyOrderDecl 10-13 declare "empty
		// greatest" with "descending" and want the empty *first*, while
		// 18-21 declare "empty least" with "descending" and want it last.
		less := -1
		if spec.emptyGreatest {
			less = 1
		}
		if ea {
			return less, nil
		}
		return -less, nil
	}
	coll, err := ctx.collation(spec.collation)
	if err != nil {
		return 0, err
	}
	cmp, ok := xpath.OrderAtomics(a, b, coll, ctx.implicitTimezone())
	if !ok {
		return 0, fmt.Errorf("XPTY0004: %s and %s cannot be ordered together",
			a.TypeName(), b.TypeName())
	}
	return cmp, nil
}

// isOrderEmpty reports whether a key takes the empty-order position: the
// empty sequence itself, or a NaN, which §3.10.6 orders with it.
func isOrderEmpty(a *xdm.Atomic) bool {
	if a == nil {
		return true
	}
	return a.IsNaN()
}

// --- group by --------------------------------------------------------------

// groupSpec is one grouping variable of a "group by" clause.
type groupSpec struct {
	name xdm.QName
	// init is the expression of "group by $x := E", which binds $x before
	// grouping rather than grouping by an existing binding.
	init      *compiledExpr
	collation string
	// check is the declared type of "group by $k as T := E", applied to the
	// atomised value rather than to what the expression returned. See apply.
	check *compiledExpr
}

// groupByClause partitions the tuple stream: §3.10.7.
//
// The output is one tuple per partition. The grouping variables keep the
// partition's common value; every *other* variable in the tuple becomes the
// concatenation of its values across the partition's members, which is what
// makes "group by $k return count($x)" count the members rather than always
// answering one.
type groupByClause struct{ specs []groupSpec }

func (c *groupByClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	// Grouping variables declared with ":=" are bound first, so that the key
	// they compute is available to be grouped on.
	staged := make([]tuple, len(in))
	for i, t := range in {
		for _, s := range c.specs {
			if s.init == nil {
				continue
			}
			seq, err := s.init.eval(t.sub(ctx))
			if err != nil {
				return nil, err
			}
			t = t.bind(s.name, seq)
		}
		staged[i] = t
	}

	// §3.10.7 rebinds each grouping variable to its *atomised* value before
	// the partitions are formed, and the return expression sees that value
	// rather than the node it came from. It is not a detail: "let $state :=
	// .../state group by $state return <state>{$state}</state>" produces
	// <state>CA</state> under the rule and <state><state>CA</state></state>
	// without it, because the node would be copied in whole. The declared
	// type of "group by $k as T := E" is checked against the atomised value
	// for the same reason.
	for i, t := range staged {
		for _, sp := range c.specs {
			v, ok := t.lookup(sp.name)
			if !ok {
				return nil, fmt.Errorf(
					"XQST0094: the grouping variable $%s is not in scope",
					sp.name.Lexical())
			}
			atoms, err := xdm.AtomizeChecked(v)
			if err != nil {
				return nil, err
			}
			t = t.bind(sp.name, atoms)
			if sp.check != nil {
				if _, err := applyCheck(sp.check, atoms, t.sub(ctx)); err != nil {
					return nil, err
				}
			}
		}
		staged[i] = t
	}

	type partition struct {
		key    tuple
		member []tuple
	}
	var order []string
	byKey := map[string]*partition{}
	for _, t := range staged {
		key, err := c.groupingKey(t, ctx)
		if err != nil {
			return nil, err
		}
		p, ok := byKey[key]
		if !ok {
			p = &partition{key: t}
			byKey[key] = p
			// Insertion order is kept so that the output is deterministic.
			// §3.10.7 leaves the order of partitions implementation-defined,
			// and a map's iteration order in Go is deliberately randomised,
			// so a query with no "order by" after its "group by" would
			// otherwise return a different order on every run.
			order = append(order, key)
		}
		p.member = append(p.member, t)
	}

	out := make([]tuple, 0, len(order))
	for _, k := range order {
		p := byKey[k]
		var merged tuple
		for i, name := range p.key.names {
			if c.isGrouping(name) {
				// A grouping variable keeps one value: the one every member
				// of the partition compared equal on. The first member's is
				// taken because they are equal by construction.
				merged = merged.bind(name, p.key.vals[i])
				continue
			}
			var all xdm.Sequence
			for _, m := range p.member {
				v, _ := m.lookup(name)
				all = append(all, v...)
			}
			merged = merged.bind(name, all)
		}
		out = append(out, merged)
	}
	return out, nil
}

func (c *groupByClause) isGrouping(name xdm.QName) bool {
	for _, s := range c.specs {
		if s.name == name {
			return true
		}
	}
	return false
}

// groupingKey reduces a tuple's grouping variables to a string that compares
// equal exactly when the specification's grouping comparison would.
//
// §3.10.7 compares grouping keys with fn:deep-equal's atomic rule under the
// clause's collation, treating two empty sequences as equal and two NaNs as
// equal — which ordinary value comparison does not. xpath.GroupingKey is that
// rule, already written for XSLT's xsl:for-each-group and reused here rather
// than restated.
func (c *groupByClause) groupingKey(t tuple, ctx *evalContext) (string, error) {
	var sb strings.Builder
	for _, s := range c.specs {
		v, ok := t.lookup(s.name)
		if !ok {
			return "", fmt.Errorf(
				"XPST0008: the grouping variable $%s is not bound",
				s.name.Lexical())
		}
		atoms, err := xdm.AtomizeChecked(v)
		if err != nil {
			return "", err
		}
		if len(atoms) > 1 {
			return "", fmt.Errorf(
				"XPTY0004: a grouping key must be at most one item, not %d",
				len(atoms))
		}
		coll, err := ctx.collation(s.collation)
		if err != nil {
			return "", err
		}
		// The separator cannot occur in a grouping key, which is built from
		// a type code and a collation unit, so two specs cannot run together
		// into a key a third combination would also produce.
		sb.WriteByte(0x1f)
		if len(atoms) == 0 {
			continue
		}
		a, ok := atoms[0].(*xdm.Atomic)
		if !ok {
			return "", fmt.Errorf("XPTY0004: %s cannot be a grouping key",
				atoms[0].TypeName())
		}
		k, err := xpath.GroupingKey(a, coll, ctx.implicitTimezone())
		if err != nil {
			return "", err
		}
		sb.WriteString(k)
	}
	return sb.String(), nil
}

// flworNode and quantifiedNode adapt the two expressions to the node
// interface, so that either may be an item of a query body or content of a
// constructor.
//
// They are wrappers rather than methods on the expressions themselves because
// both expressions are also evaluated to a sequence directly — a clause binds
// one, and a nested one is compiled by the same path — and a type that both
// builds into a tree and returns a value has two evaluation entry points that
// must not be confused.
type flworNode struct{ f *flwor }

func (n *flworNode) eval(out *builderRef, ctx *evalContext) error {
	return n.f.evalNode(out, ctx)
}

type quantifiedNode struct{ q *quantified }

func (n *quantifiedNode) eval(out *builderRef, ctx *evalContext) error {
	return n.q.evalNode(out, ctx)
}
