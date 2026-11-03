package xslt

// Set operators over streamed paths (full streaming, lane G2: the path
// algebra; §19.8.8.4 and §19.8.8.10 of XSLT 3.0).
//
// A union, intersect or except whose operands are each a consuming path of
// downward steps from the context (or the context item itself) is one P with
// several branches: the operands' steps, one after the other, each step
// carrying the number of its branch. The walk matches every branch at once,
// as it matches one path: a node is a candidate for every step whose context
// its parent (or an ancestor, for a descendant step) is, whatever branch the
// step is in. A node that ends a branch has that branch's bit set in its
// frame; the operator decides from those bits whether it is a match. So each
// node is tested once, and a match is delivered once, at its start tag, in
// document order -- what the tree's sort and de-duplication give. Two
// operands over one stream are one document, so identity is exact: a node is
// in both exactly when it ends both branches.
//
// §19.8.8 calls a union of striding or crawling operands crawling: the
// branches nest when any step is a descendant one (s.nests), as for one path.
// Refused: operands with a tail (what follows the downward steps), under
// outermost() or innermost(), of the current group; a predicate that may
// fail (the tree raises the left operand's error before the right's, the walk
// meets them in document order); a branch ending in descendant-or-self.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// splitSet decides, from the bits of the branches a node ends, whether it is
// a match: a leaf is any of bits lo to hi-1; otherwise op of l and r.
type splitSet struct {
	op     string
	l, r   *splitSet
	lo, hi uint
}

func (x *splitSet) holds(h uint64) bool {
	switch x.op {
	case "":
		return h>>x.lo&(1<<(x.hi-x.lo)-1) != 0
	case "union":
		return x.l.holds(h) || x.r.holds(h)
	case "intersect":
		return x.l.holds(h) && x.r.holds(h)
	}
	return x.l.holds(h) && !x.r.holds(h) // except
}

// shift is x with its bits moved up by k.
func (x *splitSet) shift(k uint) *splitSet {
	if x == nil {
		return nil
	}
	y := *x
	y.lo, y.hi = x.lo+k, x.hi+k
	y.l, y.r = x.l.shift(k), x.r.shift(k)
	return &y
}

// splitBranches is a consuming path taken apart as branches.
type splitBranches struct {
	bits []uint8   // the branch of each of the path's steps
	self uint64    // the branches with no step: the context itself
	n    uint      // how many branches
	set  *splitSet // nil: a match ends any branch
}

// branchesOf is pp as branches: one, unless it is a set operator's.
func branchesOf(pp splitPathParts) splitBranches {
	if pp.br != nil {
		return *pp.br
	}
	b := splitBranches{bits: make([]uint8, len(pp.steps)), n: 1}
	if pp.ctxItem {
		b.self = 1
	}
	return b
}

// setOf takes x apart as a set operator over consuming paths, when it is
// one (pathOf's case for a binary operator).
func (w *splitter) setOf(x *xpath.BinaryOp) (splitPathParts, bool) {
	var pp splitPathParts
	if x.Op != "union" && x.Op != "intersect" && x.Op != "except" || w.group {
		return pp, false
	}
	l, ok := w.pathOf(x.Left)
	if !ok {
		return pp, false
	}
	r, ok := w.pathOf(x.Right)
	if !ok {
		return pp, false
	}
	for _, side := range []splitPathParts{l, r} {
		if side.oi != "" || side.group || len(side.tail) > 0 {
			w.refuse(x.Op + " of a path with a tail")
			return pp, false
		}
	}
	lb, rb := branchesOf(l), branchesOf(r)
	if lb.n+rb.n > 64 {
		w.refuse(x.Op + " of more than 64 paths")
		return pp, false
	}
	b := &splitBranches{bits: lb.bits, self: lb.self | rb.self<<lb.n, n: lb.n + rb.n}
	for _, k := range rb.bits {
		b.bits = append(b.bits[:len(b.bits):len(b.bits)], k+uint8(lb.n))
	}
	if x.Op != "union" || lb.set != nil || rb.set != nil {
		ls, rs := lb.set, rb.set.shift(lb.n)
		if ls == nil {
			ls = &splitSet{lo: 0, hi: lb.n}
		}
		if rs == nil {
			rs = &splitSet{lo: lb.n, hi: b.n}
		}
		b.set = &splitSet{op: x.Op, l: ls, r: rs}
	}
	pp.steps = append(l.steps[:len(l.steps):len(l.steps)], r.steps...)
	pp.br = b
	return pp, true
}

// unionSteps is a parenthesized union followed by more of a path,
// (A | B)/C: each branch gets the downward steps of rest (A/C | B/C, which
// for child and descendant steps is the same nodes), and what follows them is
// the tail. Only a union: (A except B)//C is not A//C except B//C.
func (w *splitter) unionSteps(x *xpath.BinaryOp, rest []xpath.Expr) (splitPathParts, bool) {
	pp, ok := w.setOf(x)
	if !ok {
		return pp, false
	}
	k := 0
	for ; k < len(rest); k++ {
		st, ok := rest[k].(*xpath.Step)
		if !ok || !downward(st) {
			break
		}
	}
	if k == 0 {
		pp.tail = rest
		return pp, true
	}
	if x.Op != "union" || pp.br.set != nil || pp.br.self != 0 {
		w.refuse("a path after " + x.Op)
		return pp, false
	}
	// Each branch's steps, then the shared ones, as that branch's.
	var steps []*xpath.Step
	var bits []uint8
	for i := 0; i < len(pp.steps); {
		j := i
		for j < len(pp.steps) && pp.br.bits[j] == pp.br.bits[i] {
			j++
		}
		steps = append(steps, pp.steps[i:j]...)
		bits = append(bits, pp.br.bits[i:j]...)
		for _, x := range rest[:k] {
			steps = append(steps, x.(*xpath.Step))
			bits = append(bits, pp.br.bits[i])
		}
		i = j
	}
	pp.steps, pp.br.bits, pp.tail = steps, bits, rest[k:]
	return pp, true
}

// branched builds the branches of P from those w found, once s.steps holds
// the steps (path), or says why not.
func (s *streamSplit) branched(p splitPathParts) string {
	b := p.br
	if len(p.steps) == 0 {
		return "a set operator over the context item alone"
	}
	for i, st := range p.steps {
		if !stepPredsOK(st) {
			return "predicate of " + st.String() + " in a set operator: not motionless or positional"
		}
		if (i+1 == len(p.steps) || b.bits[i+1] != b.bits[i]) && st.Axis == xpath.AxisDescendantOrSelf {
			return "a path of a set operator ends in descendant-or-self"
		}
	}
	s.self, s.set = b.self, b.set
	// Child steps alone nest when branches end at different depths (rec |
	// rec/sub); at one depth they cannot. A descendant step nests anyway.
	depth := map[uint8]int{}
	for _, k := range b.bits {
		depth[k]++
	}
	if b.self != 0 {
		depth[255] = 0
	}
	for _, d := range depth {
		for _, e := range depth {
			if d != e {
				s.nests = true
			}
		}
	}
	return ""
}

// branchChecks are the checks on a split with branches once its mode is
// known: errors in the tree's order, and the context as a match read on its
// view.
func (s *streamSplit) branchChecks() string {
	if s.predFail {
		return "a predicate that may fail in a set operator"
	}
	if s.self != 0 && (s.mode != matchView || s.tail == nil) {
		return "the context itself is a result"
	}
	return ""
}

// hasBranches reports whether P is a set operator's.
func (s *streamSplit) hasBranches() bool {
	return s.self != 0 || s.set != nil || len(s.steps) > 0 && s.steps[len(s.steps)-1].branch > 0
}

// isEnd reports whether j is past the last step of a branch.
func (s *streamSplit) isEnd(j int) bool {
	return j == len(s.steps) || j > 0 && s.steps[j].branch != s.steps[j-1].branch
}

// starts reports whether step j is the first of its branch.
func (s *streamSplit) starts(j int) bool {
	return j == 0 || s.steps[j].branch != s.steps[j-1].branch
}

// matches reports whether a frame whose node ended the branches hit is a
// match.
func (s *streamSplit) matches(hit uint64) bool {
	if s.set == nil {
		return hit != 0
	}
	return s.set.holds(hit)
}

// rootFrame makes root the context of every branch's first step, and
// reports whether the context itself is a match.
func (s *streamSplit) rootFrame(root *splitFrame) bool {
	if !s.hasBranches() {
		return s.reach(root, 0)
	}
	root.hit = s.self
	for j := range s.steps {
		if !s.starts(j) {
			continue
		}
		// What reach does for step j of a fresh frame; reach itself would
		// take a later branch's first step for the end of the one before.
		if s.steps[j].axis == xpath.AxisChild {
			root.own = append(root.own, s.inst(j))
			continue
		}
		root.desc = append(root.desc, s.inst(j))
		if s.steps[j].axis == xpath.AxisDescendantOrSelf {
			s.reach(root, j+1)
		}
	}
	return s.matches(root.hit)
}

// branchMinDepth is minDepth for a split with branches: the least over them.
func (s *streamSplit) branchMinDepth() int {
	if s.self != 0 {
		return 0
	}
	least, n := -1, 0
	for j, st := range s.steps {
		if s.starts(j) {
			n = 0
		}
		if st.axis != xpath.AxisDescendantOrSelf {
			n++
		}
		if s.isEnd(j+1) && (least < 0 || n < least) {
			least = n
		}
	}
	return least
}

// mapOf takes P ! E apart as a consuming path, when it is one of two forms
// (pathOf's case for a simple map; anything else is walk's):
//
//   - E is a downward path Q, or a set operator over such paths, and P is
//     child steps: P's matches are disjoint subtrees in document order, so
//     the nodes E gives for them are those of P/Q, in that order, once each
//     (for intersect and except too: a node below one match is below no
//     other). Q's branches each get P's steps before their own.
//   - E gives only atomic values and reads no position(): P ! E is then
//     P's matches in document order, each giving E -- E is the tail, as in
//     P/E (where atomic values are neither sorted nor de-duplicated). P may
//     have a tail of steps (P/@a ! E is P/@a/E); not one ending in values.
func (w *splitter) mapOf(x *xpath.SimpleMap) (splitPathParts, bool) {
	l, ok := w.pathOf(x.Left)
	if !ok || l.oi != "" || l.group || l.ctxItem {
		return l, false
	}
	if len(l.tail) == 0 && l.br == nil && childSteps(l.steps) {
		if q, ok := w.relPath(x.Right); ok {
			return prefixed(l.steps, q), true
		}
		if w.why != "" {
			return l, false
		}
	}
	if !valueTail(x.Right) || readsPosition(x.Right) ||
		len(l.tail) > 0 && !isStep(l.tail[len(l.tail)-1]) {
		// After a tail giving values, E would be a path step over them.
		return l, false
	}
	l.tail = append(l.tail[:len(l.tail):len(l.tail)], x.Right)
	return l, true
}

// relPath is e as a consuming path relative to the context: no root step,
// no tail, not under outermost() or innermost().
func (w *splitter) relPath(e xpath.Expr) (splitPathParts, bool) {
	if pe, ok := e.(*xpath.PathExpr); ok && pe.Root {
		return splitPathParts{}, false
	}
	q, ok := w.pathOf(e)
	if !ok || q.oi != "" || q.group || len(q.tail) > 0 {
		return q, false
	}
	return q, true
}

// prefixed is q with steps before each of its branches' own.
func prefixed(steps []*xpath.Step, q splitPathParts) splitPathParts {
	b := branchesOf(q)
	out := splitPathParts{}
	nb := &splitBranches{n: b.n, set: b.set}
	for k := uint(0); k < b.n; k++ {
		if b.self&(1<<k) == 0 && !slicesHasBranch(b.bits, k) {
			continue
		}
		out.steps = append(out.steps, steps...)
		for range steps {
			nb.bits = append(nb.bits, uint8(k))
		}
		for i, st := range q.steps {
			if uint(b.bits[i]) == k {
				out.steps = append(out.steps, st)
				nb.bits = append(nb.bits, uint8(k))
			}
		}
	}
	if q.br == nil && !q.ctxItem {
		out.br = nil // one branch: a plain path
		return out
	}
	out.br = nb
	return out
}

func slicesHasBranch(bits []uint8, k uint) bool {
	for _, b := range bits {
		if uint(b) == k {
			return true
		}
	}
	return false
}

// childSteps reports whether every step is a child step: matches that
// cannot nest.
func childSteps(steps []*xpath.Step) bool {
	for _, st := range steps {
		if st.Axis != xpath.AxisChild {
			return false
		}
	}
	return len(steps) > 0
}

// liftData rewrites data(X[p]) as data(X)[p] where X gives only elements or
// attributes of the stream (noTextNodes) and p reads nothing but position():
// a streamed document is never validated, so each such node atomizes to one
// untyped value, and the n-th value is the n-th node's. The values are then
// read off the cursor instead of the nodes being collected
// (value-of select="(A | B)[position() mod 2 = 0]").
func liftData(e xpath.Expr) xpath.Expr {
	d, ok := e.(*xpath.FuncCall)
	if !ok || d.Name.URI != fnNS || d.Name.Local != "data" || len(d.Args) != 1 {
		return e
	}
	f, ok := d.Args[0].(*xpath.FilterExpr)
	if !ok || !noTextNodes(f.Base) || atomicValued(f.Base) {
		return e
	}
	for _, p := range f.Predicates {
		if !positionOnly(p) {
			return e
		}
	}
	return &xpath.FilterExpr{Base: &xpath.FuncCall{Name: d.Name, Args: []xpath.Expr{f.Base}}, Predicates: f.Predicates}
}

// positionOnly reports whether a predicate reads of its focus only its
// position: arithmetic and comparisons of position(), literals, variables.
func positionOnly(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal, *xpath.VarRef:
		return true
	case *xpath.BinaryOp:
		switch x.Op {
		case "is", "<<", ">>", "union", "intersect", "except":
			return false
		}
		return positionOnly(x.Left) && positionOnly(x.Right)
	case *xpath.UnaryOp:
		return positionOnly(x.Operand)
	case *xpath.FuncCall:
		return x.Name.URI == fnNS && x.Name.Local == "position" && len(x.Args) == 0
	}
	return false
}

// positionPred reports whether a step's predicate is a test of the
// candidate's position that cannot fail: position() compared with, or
// combined by + - * with, integer literals, divided only by a nonzero one
// (ITEM[position() lt 4], *[position() mod 2 = 1]). P counts it as it counts
// a literal [n], per context (splitPred.pe).
func positionPred(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return x.Val != nil && x.Val.Type == xdm.TypeInteger
	case *xpath.BinaryOp:
		switch x.Op {
		case "=", "!=", "<", "<=", ">", ">=", "eq", "ne", "lt", "le", "gt", "ge", "+", "-", "*", "to", "and", "or":
		case "mod", "idiv", "div":
			if l, ok := x.Right.(*xpath.Literal); !ok || l.Val == nil || l.Val.Type != xdm.TypeInteger || l.Val.String() == "0" {
				return false
			}
		default:
			return false
		}
		return positionPred(x.Left) && positionPred(x.Right)
	case *xpath.FuncCall:
		return x.Name.URI == fnNS && x.Name.Local == "position" && len(x.Args) == 0
	}
	return false
}

// foreignSets rewrites the set operators in e that join a path of the
// stream P with a foreign operand F -- one that reads no focus and no
// document (focusFree, no doc() family): a variable's nodes, which are never
// nodes of the stream (§19.8.4.41: no variable holds a streamed node). Their
// nodes are disjoint from P's, so:
//
//	P except F -> P                 F except P -> F | ()
//	P intersect F, F intersect P -> ()
//	count(P union F) -> count(P) + count(F | ())   (exists, empty, boolean, not by it)
//
// P is still read, for its errors: count(P)[false()] is appended to e as an
// empty item. F is checked as the operator checks it (F | (), XPTY0004 for a
// value): the checks are returned, for the split to evaluate before its walk
// (streamSplit.checks). A union under any other holder is left: its order
// depends on where F's trees fall among the stream's, drawn at run time.
func foreignSets(e xpath.Expr) (xpath.Expr, []xpath.Expr) {
	var extra, checks []xpath.Expr
	var re func(x xpath.Expr) xpath.Expr
	never := func(x xpath.Expr) xpath.Expr {
		return &xpath.FilterExpr{Base: x, Predicates: []xpath.Expr{fnCall("false")}}
	}
	checked := func(f xpath.Expr) xpath.Expr {
		u := &xpath.BinaryOp{Op: "union", Left: f, Right: &xpath.SequenceExpr{}}
		checks = append(checks, u)
		return u
	}
	consumed := func(p xpath.Expr) { extra = append(extra, never(fnCall("count", p))) }
	split := func(x *xpath.BinaryOp) (p, f xpath.Expr, left bool) {
		switch {
		case foreign(x.Right) && !foreign(x.Left):
			return x.Left, x.Right, true
		case foreign(x.Left) && !foreign(x.Right):
			return x.Right, x.Left, false
		}
		return nil, nil, false
	}
	re = func(x xpath.Expr) xpath.Expr {
		switch v := x.(type) {
		case *xpath.FuncCall:
			if v.Name.URI == fnNS && len(v.Args) == 1 {
				if b, ok := v.Args[0].(*xpath.BinaryOp); ok && b.Op == "union" {
					if p, f, _ := split(b); p != nil {
						n := &xpath.BinaryOp{Op: "+", Left: fnCall("count", re(p)),
							Right: fnCall("count", &xpath.BinaryOp{Op: "union", Left: f, Right: &xpath.SequenceExpr{}})}
						zero := &xpath.Literal{Val: xdm.NewInteger(0)}
						switch v.Name.Local {
						case "count":
							return n
						case "exists", "boolean":
							return &xpath.BinaryOp{Op: "gt", Left: n, Right: zero}
						case "empty", "not":
							return &xpath.BinaryOp{Op: "eq", Left: n, Right: zero}
						}
					}
				}
			}
			for i := range v.Args {
				v.Args[i] = re(v.Args[i])
			}
		case *xpath.BinaryOp:
			if v.Op == "intersect" || v.Op == "except" {
				if p, f, left := split(v); p != nil {
					p = re(p)
					switch {
					case v.Op == "except" && left:
						checked(f)
						return p
					case v.Op == "except":
						consumed(p)
						return checked(f)
					}
					consumed(p)
					checked(f)
					return &xpath.SequenceExpr{}
				}
			}
			v.Left, v.Right = re(v.Left), re(v.Right)
		case *xpath.FilterExpr:
			v.Base = re(v.Base)
		case *xpath.SimpleMap:
			v.Left = re(v.Left)
		case *xpath.SequenceExpr:
			for i := range v.Items {
				v.Items[i] = re(v.Items[i])
			}
		case *xpath.PathExpr:
			if len(v.Steps) > 0 && !isStep(v.Steps[0]) {
				v.Steps[0] = re(v.Steps[0])
			}
		case *xpath.UnaryOp:
			v.Operand = re(v.Operand)
		case *xpath.CastExpr:
			v.Operand = re(v.Operand)
		}
		return x
	}
	e = re(e)
	if len(extra) > 0 {
		e = &xpath.SequenceExpr{Items: append([]xpath.Expr{e}, extra...)}
	}
	return e, checks
}

// withChecks is e with the checks foreignSets returned as empty items after
// it, for a population (streampop.go), whose motionless parts come first.
func withChecks(e xpath.Expr, checks []xpath.Expr) xpath.Expr {
	if len(checks) == 0 {
		return e
	}
	items := []xpath.Expr{e}
	for _, c := range checks {
		items = append(items, &xpath.FilterExpr{Base: c, Predicates: []xpath.Expr{fnCall("false")}})
	}
	return &xpath.SequenceExpr{Items: items}
}

// runChecks evaluates the checks of s, before its walk.
func (s *streamSplit) runChecks(ctx *xpath.Context) error {
	for _, c := range s.checks {
		if _, err := c.Eval(ctx); err != nil {
			return err
		}
	}
	return nil
}

func fnCall(name string, args ...xpath.Expr) *xpath.FuncCall {
	return &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: name}, Args: args}
}

// foreign reports whether e reads neither the focus nor a document: its
// nodes, if any, are not the stream's.
func foreign(e xpath.Expr) bool {
	if !focusFree(e) {
		return false
	}
	s := e.String()
	for _, f := range []string{"doc(", "document(", "collection(", "uri-collection(", "unparsed-text", "json-doc(", "parse-xml"} {
		if strings.Contains(s, f) {
			return false
		}
	}
	return true
}
