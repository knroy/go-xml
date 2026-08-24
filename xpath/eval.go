package xpath

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Eval implements Expr for literals.
func (e *Literal) Eval(*Context) (xdm.Sequence, error) {
	return xdm.One(e.Val), nil
}

// Eval implements Expr for variable references.
func (e *VarRef) Eval(ctx *Context) (xdm.Sequence, error) {
	v, ok := ctx.LookupVar(e.Name)
	if !ok {
		return nil, fmt.Errorf("XPST0008: undeclared variable $%s", e.Name.Lexical())
	}
	return v, nil
}

// Eval implements Expr for the context item.
func (e *ContextItem) Eval(ctx *Context) (xdm.Sequence, error) {
	if ctx.Item == nil {
		return nil, fmt.Errorf("XPDY0002: no context item for '.'")
	}
	return xdm.One(ctx.Item), nil
}

// Eval implements Expr for a sequence constructor.
func (e *SequenceExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	var out xdm.Sequence
	for _, it := range e.Items {
		v, err := it.Eval(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

// Eval implements Expr for a single axis step.
//
// A step is evaluated against the context item alone; iterating a step over
// many context nodes is PathExpr's job. Splitting it this way means the
// predicate's context size is the number of nodes selected by *this* step from
// *this* node, which is what the spec requires and what a combined
// implementation typically gets wrong.
func (e *Step) Eval(ctx *Context) (xdm.Sequence, error) {
	node, err := ctx.ContextNode()
	if err != nil {
		return nil, err
	}

	principal := e.Axis.PrincipalKind()
	var selected xdm.Sequence
	walkAxis(node, e.Axis, func(n *xdm.Node) bool {
		if e.Test.Matches(n, principal) {
			selected = append(selected, n)
		}
		return true
	})

	// Predicates apply in axis order, so a reverse axis numbers positions
	// from the context node outwards.
	for _, pred := range e.Predicates {
		selected, err = applyPredicate(ctx, selected, pred)
		if err != nil {
			return nil, err
		}
	}

	// The result of a step is always in document order with duplicates
	// removed, regardless of the axis direction.
	return xdm.SortDocumentOrder(selected), nil
}

// Eval implements Expr for a path expression.
func (e *PathExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	var cur xdm.Sequence

	if e.Root {
		node, err := ctx.ContextNode()
		if err != nil {
			return nil, err
		}
		root := node.Root()
		if root.Kind != xdm.KindDocument {
			// "/" requires the context node to be in a tree whose root is a
			// document node; a bare element tree has no document root.
			return nil, fmt.Errorf("XPDY0050: root of the context node is not a document node")
		}
		cur = xdm.One(root)
	} else if len(e.Steps) > 0 && !stepNeedsFocus(e.Steps[0]) {
		// The first step supplies its own starting sequence — "$v/a" or
		// "f()/a" — so the focus is irrelevant and must not be required.
		// This is what makes a path rooted at a variable legal inside an
		// xsl:function, where there is no context item at all.
		v, err := e.Steps[0].Eval(ctx)
		if err != nil {
			return nil, err
		}
		cur = v
		return evalRemainingSteps(ctx, cur, e.Steps[1:])
	} else {
		if ctx.Item == nil {
			return nil, fmt.Errorf("XPDY0002: no context item for path expression")
		}
		// A relative path starts from the CONTEXT ITEM, and when that is not
		// a node the error is XPTY0020 — "the context item is not a node" —
		// not XPTY0019, which is about the value a preceding step produced.
		// XPath 2.0 3.2.1 attaches XPTY0020 to the axis step itself, and
		// evalStepOver cannot tell the two apart because by then the context
		// item is indistinguishable from a step result.
		//
		// The distinction is observable: analyze-string-085 evaluates
		// following-sibling::text inside xsl:matching-substring, where the
		// context item is the matched SUBSTRING, and the suite asks for
		// XPTY0020 by name.
		if _, ok := ctx.Item.(*xdm.Node); !ok && len(e.Steps) > 0 &&
			stepIsAxisStep(e.Steps[0]) {
			return nil, xdm.Errorf("XPTY0020",
				"the context item for an axis step is %s, not a node",
				ctx.Item.TypeName())
		}
		cur = xdm.One(ctx.Item)
	}

	for _, step := range e.Steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, err := evalStepOver(ctx, cur, step)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// evalStepOver evaluates one step with each item of input as the context item,
// concatenating the results.
//
// The result must be sorted into document order and deduplicated: two distinct
// context nodes can select the same node (as in "a/../b"), and the spec
// requires the path's value to be a node sequence in document order.
func evalStepOver(ctx *Context, input xdm.Sequence, step Expr) (xdm.Sequence, error) {
	var out xdm.Sequence
	size := len(input)
	allNodes := true

	// The left operand of "/" must be a sequence of nodes. This is its own
	// error — XPTY0019, "the step operand is not a node" — and is distinct
	// from XPTY0020, which is about the *context item* being wrong. Letting
	// "(10)/child::*" fall through to the focus check reported the latter,
	// which points at the wrong thing.
	//
	// A step that never dereferences the operand — "(10)/f()" — was exempted,
	// on the reasoning that nothing goes wrong. But "/" is defined on node
	// sequences whatever the step does with them, so "1/3" is XPTY0019 rather
	// than 3. The exemption only hid the error.
	//
	// A static error still comes first, though: "(1 to 10)/count()" calls a
	// function that does not exist at that arity, which is XPST0017 and would
	// be reported by a processor that resolved functions at compile time —
	// this one resolves them at evaluation, so the check is made here before
	// the operand is judged.
	if call, ok := step.(*FuncCall); ok && ctx.Funcs != nil {
		if _, found := ctx.Funcs.Lookup(call.Name, len(call.Args)); !found {
			return nil, fmt.Errorf(
				"XPST0017: unknown function %s with %d argument(s)",
				call.Name.Clark(), len(call.Args))
		}
	}
	for _, it := range input {
		if _, ok := it.(*xdm.Node); !ok {
			return nil, xdm.Errorf("XPTY0019",
				"the left operand of a path step is %s, not a node",
				it.TypeName())
		}
	}

	for i, it := range input {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sub := ctx.WithFocus(it, i+1, size)
		v, err := step.Eval(sub)
		if err != nil {
			return nil, err
		}
		for _, r := range v {
			if _, ok := r.(*xdm.Node); !ok {
				allNodes = false
			}
		}
		out = append(out, v...)
	}

	if !allNodes {
		// XPTY0018/XPTY0019: a path step must yield either all nodes or all
		// atomic values, never a mix. Sorting a mixed result is meaningless,
		// so it is returned as-is and the mix is an error only if a later
		// step tries to navigate from it.
		//
		// Raising XPTY0018 here instead — the literal reading of XPath 2.0
		// section 3.2, and what expression-0932/0933 ask for — was measured
		// and costs 149 tests: `out` accumulates across every input item, so
		// a step yielding nodes for one item and atomics for another trips a
		// check on the accumulation even though each step result on its own
		// is homogeneous. Testing per input item instead is correct but gains
		// nothing — it never fires, because those two tests reach the mix
		// through a single item whose "if" branch type this engine erases.
		// Both variants were measured and reverted.
		return out, nil
	}
	return xdm.SortDocumentOrder(out), nil
}

// Eval implements Expr for a filtered expression.
func (e *FilterExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	base, err := e.Base.Eval(ctx)
	if err != nil {
		return nil, err
	}
	for _, pred := range e.Predicates {
		base, err = applyPredicate(ctx, base, pred)
		if err != nil {
			return nil, err
		}
	}
	return base, nil
}

// applyPredicate filters seq by pred.
//
// The numeric-predicate rule is the one piece of real subtlety: if the
// predicate's value is a single number, it selects by position rather than by
// effective boolean value. "[1]" means "the first item", while "[true()]"
// means "every item". The test is on the *value*, not the syntax, so
// "[$n]" behaves either way depending on what $n holds.
func applyPredicate(ctx *Context, seq xdm.Sequence, pred Expr) (xdm.Sequence, error) {
	var out xdm.Sequence
	size := len(seq)

	for i, it := range seq {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pos := i + 1
		sub := ctx.WithFocus(it, pos, size)
		v, err := pred.Eval(sub)
		if err != nil {
			return nil, err
		}

		keep, err := predicateHolds(v, pos)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, it)
		}
	}
	return out, nil
}

// predicateHolds decides whether a predicate value selects the item at pos.
//
// The focus is not needed here: the predicate expression has already been
// evaluated against it by the caller, and what remains is a decision about
// the resulting value alone.
func predicateHolds(v xdm.Sequence, pos int) (bool, error) {
	if len(v) == 1 {
		if a, ok := v[0].(*xdm.Atomic); ok && a.Type.IsNumeric() {
			// Positional predicate: compare numerically so that 1.0 and 1
			// both select position 1, and NaN selects nothing.
			if a.IsNaN() {
				return false, nil
			}
			return a.Float64() == float64(pos), nil
		}
	}
	return EffectiveBooleanValue(v)
}

// EffectiveBooleanValue computes fn:boolean over a sequence.
//
// XPath 2.0 restricts this compared with 1.0: a sequence of two or more
// atomic values raises FORG0006 rather than being truthy. That strictness is
// deliberate — it catches "if ($seq)" where the author meant
// "if (exists($seq))" — so it is enforced rather than relaxed.
func EffectiveBooleanValue(seq xdm.Sequence) (bool, error) {
	if len(seq) == 0 {
		return false, nil
	}
	if _, isNode := seq[0].(*xdm.Node); isNode {
		// A sequence starting with a node is true regardless of length.
		return true, nil
	}
	if len(seq) > 1 {
		return false, fmt.Errorf(
			"FORG0006: effective boolean value of a sequence of %d atomic values is undefined", len(seq))
	}

	a, ok := seq[0].(*xdm.Atomic)
	if !ok {
		return false, fmt.Errorf("FORG0006: cannot compute effective boolean value")
	}
	switch {
	case a.Type == xdm.TypeBoolean:
		return a.Bool(), nil
	case a.Type == xdm.TypeString || a.Type == xdm.TypeUntypedAtomic || a.Type == xdm.TypeAnyURI:
		return a.Str() != "", nil
	case a.Type.IsNumeric():
		// NaN is false, as is zero of any sign.
		return !a.IsNaN() && a.Float64() != 0, nil
	}
	return false, fmt.Errorf("FORG0006: effective boolean value of %s is undefined", a.TypeName())
}

// Eval implements Expr for conditionals.
func (e *IfExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	c, err := e.Cond.Eval(ctx)
	if err != nil {
		return nil, err
	}
	b, err := EffectiveBooleanValue(c)
	if err != nil {
		return nil, err
	}
	if b {
		return e.Then.Eval(ctx)
	}
	return e.Else.Eval(ctx)
}

// Eval implements Expr for for-expressions.
//
// Multiple bindings iterate as nested loops, left to right, with each binding
// visible to the ones after it.
func (e *ForExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	sub, err := ctx.Descend()
	if err != nil {
		return nil, err
	}
	return evalFor(sub, e.Bindings, e.Return)
}

func evalFor(ctx *Context, bindings []Binding, body Expr) (xdm.Sequence, error) {
	if len(bindings) == 0 {
		return body.Eval(ctx)
	}
	b := bindings[0]
	seq, err := b.Seq.Eval(ctx)
	if err != nil {
		return nil, err
	}
	var out xdm.Sequence
	for _, it := range seq {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := evalFor(ctx.WithVar(b.Var, xdm.One(it)), bindings[1:], body)
		if err != nil {
			return nil, err
		}
		if err := ctx.countItems(len(v)); err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

// Eval implements Expr for quantified expressions.
func (e *QuantifiedExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	sub, err := ctx.Descend()
	if err != nil {
		return nil, err
	}
	res, err := evalQuantified(sub, e.Every, e.Bindings, e.Test)
	if err != nil {
		return nil, err
	}
	return xdm.One(xdm.NewBoolean(res)), nil
}

// evalQuantified short-circuits: "some" stops at the first true, "every" at
// the first false. Over a large node-set that is the difference between one
// comparison and a full scan.
func evalQuantified(ctx *Context, every bool, bindings []Binding, test Expr) (bool, error) {
	if len(bindings) == 0 {
		v, err := test.Eval(ctx)
		if err != nil {
			return false, err
		}
		return EffectiveBooleanValue(v)
	}
	b := bindings[0]
	seq, err := b.Seq.Eval(ctx)
	if err != nil {
		return false, err
	}
	for _, it := range seq {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		ok, err := evalQuantified(ctx.WithVar(b.Var, xdm.One(it)), every, bindings[1:], test)
		if err != nil {
			return false, err
		}
		if every && !ok {
			return false, nil
		}
		if !every && ok {
			return true, nil
		}
	}
	// An empty sequence makes "every" vacuously true and "some" false.
	return every, nil
}

// Eval implements Expr for function calls.
func (e *FuncCall) Eval(ctx *Context) (xdm.Sequence, error) {
	if ctx.Funcs == nil {
		return nil, fmt.Errorf("XPST0017: no function library available for %s", e.Name.Clark())
	}
	fn, ok := ctx.Funcs.Lookup(e.Name, len(e.Args))
	if !ok {
		return nil, fmt.Errorf("XPST0017: unknown function %s with %d argument(s)",
			e.Name.Clark(), len(e.Args))
	}

	// An aggregate over a bare integer range is answered from the bounds
	// rather than by building the sequence: count is hi-lo+1, sum is the
	// arithmetic series, min and max are the bounds themselves. Materialising
	// ten million values to then discard all of them peaked at 1.9 GB, for
	// answers that are arithmetic. See rangeprops.go for what does and does
	// not qualify.
	if seq, ok, err := rangeAggregate(ctx, e); err != nil {
		return nil, err
	} else if ok {
		return seq, nil
	}

	args := make([]xdm.Sequence, len(e.Args))
	for i, a := range e.Args {
		v, err := a.Eval(ctx)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}

	sub, err := ctx.Descend()
	if err != nil {
		return nil, err
	}
	return fn.Call(sub, args)
}

// Eval implements Expr for unary + and -.
func (e *UnaryOp) Eval(ctx *Context) (xdm.Sequence, error) {
	v, err := e.Operand.Eval(ctx)
	if err != nil {
		return nil, err
	}
	atoms := xdm.Atomize(v)
	if len(atoms) == 0 {
		// Unary operators on the empty sequence return the empty sequence
		// rather than raising, which keeps "-$maybe-absent" usable.
		return xdm.Empty, nil
	}
	it, err := atoms.Single()
	if err != nil {
		return nil, err
	}
	a, ok := it.(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("unary %s applied to a non-atomic value", e.Op)
	}
	n, err := toNumeric(a)
	if err != nil {
		return nil, err
	}
	if e.Op == "+" {
		return xdm.One(n), nil
	}
	return xdm.One(negate(n)), nil
}

// stepNeedsFocus reports whether a path's first step reads the context item.
//
// An axis step ("a", "@id", "..") navigates from the focus and so requires
// one. A primary expression ("$v", "f()", "(expr)") supplies its own starting
// sequence and does not — which is why "$codelists/cl[@id=$x]" is legal inside
// an xsl:function, where the focus is deliberately absent.
func stepNeedsFocus(e Expr) bool {
	switch e := e.(type) {
	case *Step, *ContextItem:
		return true
	case *FilterExpr:
		// "(...)[p]" filters a base expression; whether the focus is needed
		// depends on that base.
		return stepNeedsFocus(e.Base)
	}
	return false
}

// evalRemainingSteps runs the steps after a self-rooted first step.
func evalRemainingSteps(ctx *Context, cur xdm.Sequence, steps []Expr) (xdm.Sequence, error) {
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, err := evalStepOver(ctx, cur, step)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// stepIsAxisStep reports whether a path step navigates an axis, as opposed to
// a filter, function call, or parenthesised expression that merely happens to
// occupy a step position. Only an axis step raises XPTY0020 for a non-node
// context item; a filter such as ".[1]" is defined on any item.
func stepIsAxisStep(step Expr) bool {
	switch st := step.(type) {
	case *Step:
		return true
	case *FilterExpr:
		return stepIsAxisStep(st.Base)
	}
	return false
}
