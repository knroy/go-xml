package xslt

// Accumulator rules that read accumulators (lane L4; XSLT 3.0 §18.2.4).
//
// A rule may call accumulator-before or accumulator-after of its own node
// (the focus of the rule, never a node a path reaches), naming the
// accumulator by a literal: -before in either phase, -after in an end-phase
// rule only. The observer answers such a call as the tree's walk does:
//
//   - Another accumulator B of the node: the tree computes B's walk over the
//     whole document and reads its value at the node. The observer steps B at
//     the node first (stepOne: once per step), so B's start (or end) value is
//     there; -before in an end-phase rule is the value B had before the
//     node's start rules ended, kept on the element's frame (a leaf's in rd).
//   - The accumulator itself: -before in an end-phase rule is the node's
//     value before; anything else is what the tree's walk has not computed
//     yet, XTDE3400 (fnAccumulator's message).
//
// Two accumulators reading each other (a cycle through two or more) are
// refused when the stylesheet compiles (accumReadsWhy): the tree's walks of
// such a pair hand each other partial tables, whose answers depend on which
// walk started first; that order is not the stream's.
//
// Errors. B's walk failing anywhere fails every walk that has read B by
// then, with B's error, at the point of that read. The observer meets B's
// error only where it arises, so it records when each accumulator first
// read each other one, and when each failed of itself (accumRuleReads.seq),
// and effErr resolves an accumulator's error as the tree's: its earliest
// read of an accumulator whose resolved error is set, or its own error if
// that came first.

import (
	"errors"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// errAccumCircular is a rule's read of the value its accumulator is
// computing; fnAccumulator words it as the tree does (XTDE3400).
var errAccumCircular = errors.New("accumulator read circularly")

// accumRuleReads is the state of one streamed run's step: the node the
// rules are applied to, the phase, and who reads whom.
type accumRuleReads struct {
	n      *xdm.Node      // the node of the step in progress, nil between steps
	post   bool           // its end rules
	before []xdm.Sequence // an element's values before its start rules ended (end phase)
	leaf   []xdm.Sequence // a leaf's values before its end rules (end phase, before nil)
	phase  uint64         // counts the steps
	done   []uint64       // done[i] == phase: accumulator i was stepped in this step
	busy   []bool         // i's rules are being applied
	at     int            // the accumulator whose rules are being applied, -1 none
	prev   []int          // the ones whose rules read it, innermost last
	seq    int            // counts reads and failures, for their order
	errAt  []int          // when i failed of itself (0: it has not)
	dep    [][]int        // dep[i][j]: when i first read j (0: never)
}

func (rd *accumRuleReads) init(n int) {
	rd.done, rd.busy, rd.errAt, rd.leaf = make([]uint64, n), make([]bool, n), make([]int, n), make([]xdm.Sequence, n)
	rd.dep, rd.at = make([][]int, n), -1
}

func (rd *accumRuleReads) begin(n *xdm.Node, post bool, before []xdm.Sequence) {
	rd.n, rd.post, rd.before = n, post, before
	rd.phase++
}

func (rd *accumRuleReads) end() { rd.n, rd.before = nil, nil }

// enter starts stepping i, whose value is cur, and reports whether it is
// still to be stepped in this step.
func (rd *accumRuleReads) enter(i int, cur xdm.Sequence) bool {
	if rd.done[i] == rd.phase {
		return false
	}
	rd.done[i], rd.busy[i] = rd.phase, true
	rd.leaf[i] = cur
	rd.prev = append(rd.prev, rd.at)
	rd.at = i
	return true
}

// leave ends stepping i.
func (rd *accumRuleReads) leave(i int) {
	rd.busy[i] = false
	rd.at = rd.prev[len(rd.prev)-1]
	rd.prev = rd.prev[:len(rd.prev)-1]
}

// failed records that i has failed of itself, now.
func (rd *accumRuleReads) failed(i int) {
	rd.seq++
	rd.errAt[i] = rd.seq
}

// stepValue is fn:accumulator-before (or -after: post) of accumulator j at
// node, read by a rule of the step in progress.
func (sa *streamAccums) stepValue(j int, node *xdm.Node, post bool) (xdm.Sequence, error) {
	rd := &sa.rd
	i := rd.at
	if node != rd.n || i < 0 {
		return nil, errAccumOffStream // accumReadsOK admits the rule's node only
	}
	if j == i {
		if post || !rd.post {
			return nil, errAccumCircular
		}
		return sa.beforeOf(j), nil
	}
	if rd.busy[j] || post && !rd.post {
		return nil, errAccumOffStream // a cycle, or -after at a start: refused when compiled
	}
	if rd.dep[i] == nil {
		rd.dep[i] = make([]int, len(sa.defs))
	}
	if rd.dep[i][j] == 0 {
		rd.seq++
		rd.dep[i][j] = rd.seq
	}
	if !post && rd.post {
		if err := sa.effErr(j); err != nil {
			return nil, err
		}
		return sa.beforeOf(j), nil
	}
	sa.stepOne(j)
	if err := sa.effErr(j); err != nil {
		return nil, err
	}
	return sa.cur[j], nil
}

// beforeOf is j's value at the step's node once its start rules have been
// applied, in an end-phase step: an element's frame has it; a leaf's is
// the value j had when this step entered it, or has now if it has not.
func (sa *streamAccums) beforeOf(j int) xdm.Sequence {
	rd := &sa.rd
	switch {
	case rd.before != nil:
		return rd.before[j]
	case rd.done[j] == rd.phase:
		return rd.leaf[j]
	}
	return sa.cur[j]
}

// effErr is the error the tree's walk of accumulator i fails with, as far
// as the document has been read: its own, or that of the earliest
// accumulator it read whose walk fails, whichever came first.
func (sa *streamAccums) effErr(i int) error {
	return sa.effErrN(i, len(sa.defs))
}

func (sa *streamAccums) effErrN(i, depth int) error {
	rd := &sa.rd
	best, err := rd.errAt[i], sa.errs[i]
	if depth > 0 && rd.dep[i] != nil {
		for j, at := range rd.dep[i] {
			if at > 0 && (best == 0 || at < best) {
				if e := sa.effErrN(j, depth-1); e != nil {
					best, err = at, e
				}
			}
		}
	}
	return err
}

// --- Compiling --------------------------------------------------------------

// accumRuleExpr is an expression of an accumulator rule; held says its
// value is kept (the rule's value, or a variable's), so it may hold no node
// of the parse.
type accumRuleExpr struct {
	at   *xdm.Node
	src  string
	held bool
}

// accumBodyExprs returns the expressions of a rule's sequence constructor
// when it is xsl:variable instructions and then one xsl:sequence with a
// select, the rule's value (held unless the type is atomic). A variable has
// a select, or one xsl:evaluate with no context item (§10.4.2: its target
// expression then has no focus, and no XSLT function, so it reads nothing
// of the node; what it is given is checked).
func accumBodyExprs(rule *xdm.Node, atomic bool) ([]accumRuleExpr, bool) {
	var out []accumRuleExpr
	last := false
	for ch := range rule.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
			continue
		case ch.Kind() != xdm.KindElement || last:
			return nil, false
		case isXSL(ch, "sequence") && ch.Attr("", "select") != nil && ch.NumAttrs() == 1 && !hasContent(ch):
			out = append(out, accumRuleExpr{at: ch, src: ch.AttrValue("select"), held: !atomic})
			last = true
		case isXSL(ch, "variable"):
			x, ok := accumVariableExprs(ch)
			if !ok {
				return nil, false
			}
			out = append(out, x...)
		default:
			return nil, false
		}
	}
	return out, last
}

// accumVariableExprs is accumBodyExprs for one xsl:variable.
func accumVariableExprs(v *xdm.Node) ([]accumRuleExpr, bool) {
	for a := range v.Attrs() {
		switch n := a.Name(); {
		case n.URI != "":
		case n.Local == "name", n.Local == "as", n.Local == "select":
		default:
			return nil, false
		}
	}
	if v.Attr("", "select") != nil {
		if hasContent(v) {
			return nil, false
		}
		return []accumRuleExpr{{at: v, src: v.AttrValue("select"), held: true}}, true
	}
	var ev *xdm.Node
	for ch := range v.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		case ch.Kind() == xdm.KindElement && ev == nil && isXSL(ch, "evaluate") && !hasContent(ch):
			ev = ch
		default:
			return nil, false
		}
	}
	if ev == nil || ev.Attr("", "xpath") == nil {
		return nil, false
	}
	out := []accumRuleExpr{{at: ev, src: ev.AttrValue("xpath")}} // atomized to a string
	for a := range ev.Attrs() {
		switch n := a.Name(); {
		case n.URI != "":
		case n.Local == "xpath", n.Local == "as":
		case n.Local == "with-params":
			out = append(out, accumRuleExpr{at: ev, src: a.Value(), held: true})
		default:
			return nil, false // context-item, namespace-context, base-uri, schema-aware
		}
	}
	return out, true
}

// hasContent reports whether el has a child other than whitespace text,
// comments and processing instructions.
func hasContent(el *xdm.Node) bool {
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		default:
			return true
		}
	}
	return false
}

// accumReadsOK reports whether every call of accumulator-before or -after
// in e reads, by a literal name, an accumulator of the rule's node: at e's
// own focus, not inside a path, a filter or a simple map; -after only in an
// end-phase rule (post). The names read are added to names.
func accumReadsOK(e xpath.Expr, post bool, names *[]string) bool {
	all := func(es ...xpath.Expr) bool {
		for _, x := range es {
			if x != nil && !accumReadsOK(x, post, names) {
				return false
			}
		}
		return true
	}
	binds := func(bs []xpath.Binding) bool {
		for _, b := range bs {
			if !all(b.Seq) {
				return false
			}
		}
		return true
	}
	switch x := e.(type) {
	case *xpath.VarRef, *xpath.Literal, *xpath.ContextItem:
		return true
	case *xpath.FuncCall:
		if x.Name.URI == fnNS && (x.Name.Local == "accumulator-before" || x.Name.Local == "accumulator-after") {
			if len(x.Args) != 1 || x.Name.Local == "accumulator-after" && !post {
				return false
			}
			lit, ok := x.Args[0].(*xpath.Literal)
			if !ok || lit.Val == nil {
				return false
			}
			*names = append(*names, strings.TrimSpace(lit.Val.String()))
			return true
		}
		return all(x.Args...)
	case *xpath.BinaryOp:
		return all(x.Left, x.Right)
	case *xpath.StringConcat:
		return all(x.Left, x.Right)
	case *xpath.UnaryOp:
		return all(x.Operand)
	case *xpath.CastExpr:
		return all(x.Operand)
	case *xpath.InstanceOfExpr:
		return all(x.Operand)
	case *xpath.TreatExpr:
		return all(x.Operand)
	case *xpath.IfExpr:
		return all(x.Cond, x.Then, x.Else)
	case *xpath.SequenceExpr:
		return all(x.Items...)
	case *xpath.LetExpr:
		return binds(x.Bindings) && all(x.Return)
	case *xpath.ForExpr:
		return binds(x.Bindings) && all(x.Return)
	case *xpath.QuantifiedExpr:
		return binds(x.Bindings) && all(x.Test)
	case *xpath.MapConstructor:
		return all(x.Keys...) && all(x.Values...)
	case *xpath.ArrayConstructor:
		return all(x.Members...)
	case *xpath.DynamicCall:
		return all(x.Target) && all(x.Args...)
	case *xpath.LookupExpr:
		return all(x.Base, x.Expr)
	}
	src := e.String()
	return !strings.Contains(src, "accumulator-before") && !strings.Contains(src, "accumulator-after")
}

// accumReadsWhy reports why the accumulators of a streamed run, with the
// names their rules read, cannot be computed by the observer: a name that
// does not resolve, or two or more accumulators reading each other.
func (s *Stylesheet) accumReadsWhy(reads map[*accumulatorDef][]string) string {
	edges := map[*accumulatorDef][]*accumulatorDef{}
	for def, names := range reads {
		for _, lex := range names {
			clark, ok := s.accumClark(lex)
			if !ok {
				return "accumulator " + def.name.Lexical() + ": a rule reads accumulator " + lex
			}
			to, ok := s.accumulatorFor(0, clark)
			if ok && to != def {
				edges[def] = append(edges[def], to)
			}
		}
	}
	// A cycle: a def reached again while its walk is on the stack.
	state := map[*accumulatorDef]uint8{} // 1 on the stack, 2 done
	var visit func(d *accumulatorDef) bool
	visit = func(d *accumulatorDef) bool {
		switch state[d] {
		case 1:
			return false
		case 2:
			return true
		}
		state[d] = 1
		for _, t := range edges[d] {
			if !visit(t) {
				return false
			}
		}
		state[d] = 2
		return true
	}
	for def := range reads {
		if !visit(def) {
			return "accumulator " + def.name.Lexical() + ": rules of accumulators read each other"
		}
	}
	return ""
}

// accumClark resolves an accumulator name as fnAccumulator does.
func (s *Stylesheet) accumClark(lex string) (string, bool) {
	switch {
	case isEQName(lex):
		end := strings.IndexByte(lex, '}')
		return xdm.QName{URI: lex[2:end], Local: lex[end+1:]}.Clark(), true
	case isLexicalQName(lex):
		prefix, local := xdm.SplitQName(lex)
		uri := ""
		if prefix != "" {
			var ok bool
			if uri, ok = s.prefixes[prefix]; !ok {
				return "", false
			}
		}
		return xdm.QName{URI: uri, Local: local}.Clark(), true
	}
	return "", false
}
