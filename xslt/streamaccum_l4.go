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
// Errors (§18.2.5): a read fails when the value it reads depends on a
// failed computation. The observer records at which step each accumulator
// failed (accumRuleReads.errAt) and at which step each kept value was
// computed; a value computed before the failure stands, any later one is
// the error. The tree's walk answers the same (accumulatorValues.err).

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
	n        *xdm.Node      // the node of the step in progress, nil between steps
	post     bool           // its end rules
	before   []xdm.Sequence // an element's values before its start rules ended (end phase)
	beforeAt uint64         // the step they were recorded at
	leaf     []xdm.Sequence // a leaf's values before its end rules (end phase, before nil)
	phase    uint64         // counts the steps: the step in progress, or the last one
	done     []uint64       // done[i] == phase: accumulator i was stepped in this step
	busy     []bool         // i's rules are being applied
	at       int            // the accumulator whose rules are being applied, -1 none
	prev     []int          // the ones whose rules read it, innermost last
	// errAt is the step at which i failed (0: its initial value), -1 none:
	// a value recorded at an earlier step stands (§18.2.5), any other is
	// i's error (valid).
	errAt []int64
}

func (rd *accumRuleReads) init(n int) {
	rd.done, rd.busy, rd.errAt, rd.leaf = make([]uint64, n), make([]bool, n), make([]int64, n), make([]xdm.Sequence, n)
	for i := range rd.errAt {
		rd.errAt[i] = -1
	}
	rd.at = -1
}

func (rd *accumRuleReads) begin(n *xdm.Node, post bool, before []xdm.Sequence, beforeAt uint64) {
	rd.n, rd.post, rd.before, rd.beforeAt = n, post, before, beforeAt
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

// failed records that i has failed, at the step in progress (0 before any:
// its initial value).
func (rd *accumRuleReads) failed(i int) { rd.errAt[i] = int64(rd.phase) }

// valid reports whether i's value recorded at step at stands: it was
// computed before i failed, if i did.
func (rd *accumRuleReads) valid(i int, at uint64) bool {
	return rd.errAt[i] < 0 || int64(at) < rd.errAt[i]
}

// stepValue is fn:accumulator-before (or -after: post) of accumulator j at
// node, read by a rule of the step in progress. A value that depends on a
// failed computation is the accumulator's error (§18.2.5), as the tree's
// walk of j answers the read.
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
		return sa.beforeOf(j)
	}
	if rd.busy[j] || post && !rd.post {
		return nil, errAccumOffStream // a cycle, or -after at a start: refused when compiled
	}
	if !post && rd.post {
		return sa.beforeOf(j)
	}
	sa.stepOne(j)
	if sa.errs[j] != nil {
		return nil, sa.errs[j]
	}
	return sa.cur[j], nil
}

// beforeOf is j's value at the step's node once its start rules have been
// applied, in an end-phase step: an element's frame has it; a leaf's is
// the value j had when this step entered it, or has now if it has not
// (recorded at the leaf's start step, the one before).
func (sa *streamAccums) beforeOf(j int) (xdm.Sequence, error) {
	rd := &sa.rd
	v, at := sa.cur[j], rd.phase-1
	switch {
	case rd.before != nil:
		v, at = rd.before[j], rd.beforeAt
	case rd.done[j] == rd.phase:
		v = rd.leaf[j]
	}
	if !rd.valid(j, at) {
		return nil, sa.errs[j]
	}
	return v, nil
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

// accumDocURIOK reports whether x, in an accumulator rule, is base-uri of
// the document node: of "/" or root(), or root(.). (Not document-uri: a
// validated tree copy has none, the parse's root keeps it.) The
// rule runs on the parse's own node, whose root is the parse's document
// node, which the parse gives the document's URIs as the tree's parse does
// (xdm.Parse: the options' BaseURI and DocumentURI), with no content read
// (merge-065a: concat(base-uri(/), ...)).
func accumDocURIOK(vc viewCheck, x *xpath.FuncCall) bool {
	if !vc.accum || x.Name.URI != fnNS || x.Name.Local != "base-uri" || len(x.Args) != 1 {
		return false
	}
	switch a := x.Args[0].(type) {
	case *xpath.PathExpr:
		return a.Root && len(a.Steps) == 0
	case *xpath.FuncCall:
		return a.Name.URI == fnNS && a.Name.Local == "root" &&
			(len(a.Args) == 0 || len(a.Args) == 1 && isContextItem(a.Args[0]))
	}
	return false
}
