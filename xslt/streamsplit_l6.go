package xslt

// Lane L6: shapes and populations the planner left on the tree.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// leafLoop plans the body of el, an xsl:for-each or xsl:iterate (its
// parameters and xsl:on-completion checked by loopOp) whose select gives only
// attributes, text, comment or processing-instruction nodes, or values
// (leafItems), when every instruction of it reads of each item only what a
// complete parentless node has (leafBody). Each item the loop is handed is
// such a node -- an attribute of its match's view, or a leaf's copy
// (Cursor.Leaf) -- and the body runs off the cursor (matchView, or a
// population's off), as a template rule on a leaf does (planRule). Planned
// at an element's view (members), data(.) and copy-of(.) are refused; a
// body that climbs (`..`) is refused here and is left to members, as before.
func (b *streamBuilder) leafLoop(el *xdm.Node, body []*xdm.Node) bool {
	if !b.leafBody(el, body) {
		return false
	}
	b.kinds[streamtrace.KindSeq]++
	b.kinds[streamtrace.KindTree] += int64(len(body))
	return true
}

// leafBody checks body, the instructions of el, an xsl:for-each or xsl:iterate over
// leafItems, at focus vLeaf, as a leaf rule's body is checked: a strict
// builder (every expression checked), noIdent, no upward step. It runs in a
// builder on a plan of its own: nothing it meets is registered in b's, so a
// refusal leaves b as it was; a body in which anything would be registered
// (a consumer, a value template's op) is refused.
func (b *streamBuilder) leafBody(el *xdm.Node, body []*xdm.Node) bool {
	if !isXSL(el, "for-each") && !isXSL(el, "iterate") || !leafItems(parseAt(el, el.AttrValue("select"))) {
		return false
	}
	for ch := range el.Children() {
		if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
			return false
		}
	}
	var ops []*streamOp
	var kinds [streamtrace.NumKinds]int64
	sb := &streamBuilder{pl: &streamPlan{recs: map[any]*planRec{}}, x: newStreamExec(),
		ops: &ops, kinds: &kinds, noIdent: true, key: b.key}
	for _, ch := range body {
		if !fallbackIgnored(ch) && !sb.motionless(ch, vLeaf) {
			return false
		}
	}
	return len(ops) == 0 && len(sb.x.ops) == 0 && len(sb.x.exprOps) == 0 && len(sb.x.tries) == 0
}

// leafItems reports whether e gives no element and no document node: a
// path whose last step selects attributes, or text, comment or PI nodes by
// kind, a value, or a sequence of those.
func leafItems(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !leafItems(it) {
				return false
			}
		}
		return len(x.Items) > 0
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && leafStep(x.Steps[len(x.Steps)-1])
	case *xpath.Step:
		return leafStep(x)
	case nil:
		return false
	}
	return atomicValued(e)
}

func leafStep(e xpath.Expr) bool {
	st, ok := e.(*xpath.Step)
	if !ok {
		return false
	}
	if st.Axis == xpath.AxisAttribute {
		return true
	}
	leaf, elem := testKinds(st.Test)
	return leaf && !elem
}

// atomicSelect reports whether el, an xsl:sequence or xsl:variable, has a
// select that gives only values: it cannot hand a copied node on as the node
// it stands for (the noIdent refusal in motionless).
func atomicSelect(el *xdm.Node) bool {
	if el.Attr("", "select") == nil || el.Attr("", "as") != nil {
		return false
	}
	e := parseAt(el, el.AttrValue("select"))
	return e != nil && atomicValued(e)
}

// --- xsl:apply-templates over copies of the stream's matches ---------------
//
// copy-of(P), P/copy-of(.), P/copy-of() (and snapshot(P)) as the select of
// an xsl:apply-templates make a population whose matches are grounded one
// at a time and copied (streamPop.ground). What the rules are handed are
// those copies: new trees, which the tree's own rules process as in tree
// mode, in any mode, streamable or not. The copies are collected as the
// stream is read, then dispatched by the tree's code (applySelected): the
// select is evaluated whole first, then xsl:sort and xsl:with-param, as
// applyTemplatesInstr.Execute does, so positions, last(), sorting and the
// order of errors are the tree's. Memory: the tree mode holds the document
// and the same copies (its select's value); the stream holds the copies and
// one grounded match at a time (a nested match's copy is taken from its
// outermost one's).

// copyOfTail reads P/copy-of(.) (or P/copy-of()) as copy-of(P): each
// match's copy, in trees made in P's order, which the path's sort keeps (as
// snapshotTail does for snapshot).
func copyOfTail(e xpath.Expr) (*xpath.FuncCall, bool) {
	pe, ok := e.(*xpath.PathExpr)
	if !ok || len(pe.Steps) < 2 {
		return nil, false
	}
	f, ok := pe.Steps[len(pe.Steps)-1].(*xpath.FuncCall)
	if !ok || !isFn(f, "copy-of", len(f.Args)) || !copyTail(f) { // streamsplit_fns.go
		return nil, false
	}
	rest := &xpath.PathExpr{Root: pe.Root, Steps: pe.Steps[:len(pe.Steps)-1]}
	return &xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{rest}}, true
}

// groundApply reports whether p hands only copies of the stream's matches
// (and motionless parts' items): no choice, no values read off the cursor.
func groundApply(p *streamPop) bool {
	return p.cond == nil && p.ground && p.fn != nil && !p.values
}

// groundApplyOp reports whether op is an apply op over such a population.
func groundApplyOp(op *streamOp) bool {
	return op.pop != nil && groundApply(op.pop)
}

// applyCopies takes, for an apply or loop op whose select the split refused
// (a nested P in P/copy-of(.): "not within the match"), the population of
// copies instead; it reports whether it did.
func (c *compiler) applyCopies(op *streamOp, ns xpath.NamespaceResolver) bool {
	e := reparse(op.sel, ns)
	if e == nil {
		return false
	}
	if _, ok := copyOfTail(e); !ok {
		return false
	}
	pop, s, why := c.popFor(op, ns)
	if why != "" || pop == nil || !groundApply(pop) || op.apply != applyNone && !applyPop(pop, op.instr) {
		return false
	}
	op.pop, op.split, op.dead = pop, s, ""
	return true
}

// readyCopies readies an apply op over copies: each match is grounded
// (none is the document node).
func (op *streamOp) readyCopies() string {
	if op.split.minDepth() < 1 {
		return "apply-templates over copies: a match may be the document node"
	}
	op.split.mode = matchGround
	return ""
}

// copiesLinkWhy is why an apply op over copies cannot run: the rules would
// read accumulator values of the copies, which the grounded matches' copies
// (copy-accumulators) do not hold once the stream has moved on.
func (s *Stylesheet) copiesLinkWhy() string {
	if len(s.accumulators) != 0 {
		return "apply-templates over copies with accumulators declared"
	}
	return ""
}

// onlyCopies reports whether every apply op of x is one over copies, which
// reaches no streamed rule.
func (x *streamExec) onlyCopies() bool {
	ok := true
	x.eachApply(func(op *streamOp) { ok = ok && groundApplyOp(op) })
	return ok
}

// executeCopies runs i, an apply op over copies: the population is read
// whole, its items collected, then handed to the tree's dispatch.
func (i *applyTemplatesInstr) executeCopies(rt *runtime, out *outputBuilder, run *streamRun, op *streamOp) error {
	if run.used {
		return errStreamPlan
	}
	var seq xdm.Sequence
	err := op.pop.read(run, rt, op, func(it xdm.Item) error {
		seq = append(seq, it)
		return nil
	}, func(item func(xdm.Item) error, _ func() (bool, error)) error {
		run.used = true
		return op.split.loopAt(rt.ctx, run.c, run.d, run.groupAt(op), item, nil)
	})
	if err != nil {
		return err
	}
	off := run.off
	run.off = true
	err = i.applySelected(rt, out, seq)
	run.off = off
	return err
}

// groundLastWhy is why el, an xsl:for-each whose body was not planned (it
// runs on the tree's code over grounded copies or values: op.grounded), may
// not run streamed: its body may read last(). §19.8.9.14 makes last()
// motionless over a grounded population, so the analysis admits it, but the
// streamed loop hands its items one at a time with no context size
// (forEachInstr.Execute, size 0: XPDY0002 where the tree gives the count).
// Conservatively: "last(" anywhere in the body's attributes or text, in a
// named template it calls (which keeps the focus), or an attribute set it
// uses (evaluated with the instruction's focus).
func groundLastWhy(el *xdm.Node) string {
	if !isXSL(el, "for-each") {
		return ""
	}
	seen := map[*xdm.Node]bool{}
	var reads func(n *xdm.Node) bool
	reads = func(n *xdm.Node) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		for ch := range n.Children() {
			switch ch.Kind() {
			case xdm.KindText:
				if strings.Contains(ch.Value(), "last(") {
					return true
				}
			case xdm.KindElement:
				for a := range ch.Attrs() {
					if strings.Contains(a.Value(), "last(") || a.Name().Local == "use-attribute-sets" {
						return true
					}
				}
				if isXSL(ch, "call-template") {
					t := namedTemplate(ch)
					if t == nil || reads(t) {
						return true
					}
				}
				if reads(ch) {
					return true
				}
			}
		}
		return false
	}
	if reads(el) {
		return "loop body over a grounded population may read last()"
	}
	return ""
}

// namedTemplate is the xsl:template of el's module that el, an
// xsl:call-template, names by the same lexical name, or nil (another module,
// another prefix).
func namedTemplate(el *xdm.Node) *xdm.Node {
	name := el.AttrValue("name")
	root := documentElementOf(el)
	if root == nil || name == "" {
		return nil
	}
	for ch := range root.Children() {
		if ch.Kind() == xdm.KindElement && isXSL(ch, "template") && ch.AttrValue("name") == name {
			return ch
		}
	}
	return nil
}
