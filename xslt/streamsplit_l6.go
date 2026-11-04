package xslt

// Lane L6: shapes and populations the planner left on the tree.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
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

// groundContextSplit splits e, the select of an xsl:value-of or
// xsl:attribute (simple content: what e returns is atomized) that the split
// refused, by grounding the context element whole and evaluating e on its
// copy: `$splitItems ! e`. e must read nothing a grounded copy lacks
// (burstSafe; no fn:position, which the map would answer with 1), so it
// reads the context's subtree and its ancestors' names and attributes, as
// on the tree: descendant-or-self::* (streamable-032), nested matches, two
// paths of the context. The copy is let go once its values are made
// (ctxOut: class "output"); it costs at most the context's subtree, which
// the tree holds too. Not at the document node (the copy would be the
// document) nor in a group.
func groundContextSplit(e xpath.Expr, base *xpath.Compiled, doc, group bool) *streamSplit {
	// current() would be the instruction's own item, not the copy: refused.
	if doc || group || focusFree(e) || !burstSafe(e, true) || strings.Contains(e.String(), "current(") {
		return nil
	}
	s, _ := splitStreamIn(&xpath.ContextItem{}, base, false, false, false)
	if s == nil || !s.ctxItem || s.atomize || s.cond != nil {
		return nil
	}
	s.outer = xpathleaf.WithExpr(base, &xpath.SimpleMap{Left: &xpath.VarRef{Name: splitItems}, Right: e}).(*xpath.Compiled)
	s.ctxOut = true
	return s
}

// motionlessIterate checks el, an xsl:iterate the analysis found
// motionless, as motionlessLoop checks a motionless xsl:for-each: its select
// reads only what the focus f's view has (ancestor::*, @*), or no focus at
// all (a variable's items); its parameters' defaults read no focus
// (iterateParamOK, as loopOp's); xsl:on-completion has no focus; each
// instruction of the body reads of its item only what a view (or, over
// attributes and values, a complete parentless node: leafBody) has. The
// tree's code runs it, over the views and their attributes.
func (b *streamBuilder) motionlessIterate(el *xdm.Node, f vfocus) bool {
	sel := el.Attr("", "select")
	if sel == nil {
		return b.refuse("motionless xsl:iterate")
	}
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && !streamAttrsOK[n.Local] {
			return b.refuse("attribute " + n.Local + " of xsl:iterate")
		}
	}
	e := parseAt(el, sel.Value())
	if e == nil {
		return b.refuse("motionless xsl:iterate")
	}
	var body []*xdm.Node
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}"):
			return b.refuse("text with a brace")
		case ch.Kind() != xdm.KindElement:
		case isXSL(ch, "param"):
			if !iterateParamOK(ch) {
				return b.refuse("xsl:iterate parameter default reads the focus")
			}
		case isXSL(ch, "on-completion"):
			if !b.attrs(ch, vNone) || !b.content(ch, vNone) {
				return false
			}
		default:
			body = append(body, ch)
		}
	}
	if foreign(e) {
		return true // a variable's items: never a node of the stream
	}
	if !(viewCheck{up: b.up}).safe(e, f, true) {
		return b.refuse("motionless xsl:iterate over " + sel.Value())
	}
	if b.leafBody(el, body) {
		return true
	}
	for _, ch := range body {
		if !fallbackIgnored(ch) && !b.motionless(ch, vElem) {
			return false
		}
	}
	return true
}

// foreignSort reports whether ch, an xsl:sort of el, a motionless
// xsl:for-each, sorts items that are not nodes of the stream (el's select
// is foreign: a variable's items), its keys read off those items, and its
// other attributes (order, lang, collation, ...) motionless at el's focus f.
func (b *streamBuilder) foreignSort(el, ch *xdm.Node, f vfocus) bool {
	e := parseAt(el, el.AttrValue("select"))
	if e == nil || !foreign(e) {
		return false
	}
	for a := range ch.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != "select" && !b.avt(ch, a.Value(), f) {
			return false
		}
	}
	return true
}

// viewKeyOK admits key(name, value, $top) where $top is a global variable
// (globalVar): the index is of $top's document, never the stream's (a global
// cannot reach a streamed container's nodes; one reading the principal
// input's context item is refused before planning: MP). Its name and value
// are atomized, read off the focus as any operand. key() with two arguments,
// which searches the stream's document, stays refused (burstFnBanned).
// vc.globals is the element the expression is written on (nil: off).
func viewKeyOK(vc viewCheck, x *xpath.FuncCall, f vfocus) bool {
	if vc.globals == nil || !isFn(x, "key", 3) {
		return false
	}
	v, ok := x.Args[2].(*xpath.VarRef)
	if !ok || !globalVar(vc.globals, v.Name) {
		return false
	}
	return vc.safe(x.Args[0], f, false) && vc.safe(x.Args[1], f, false)
}

// globalVar reports whether $q, read in an expression written on el, is a
// global variable or parameter of el's module: no xsl:variable or xsl:param
// of that name binds it before el in a construct holding it, and the module
// declares one at the top level. A prefixed name is not resolved: false.
func globalVar(el *xdm.Node, q xdm.QName) bool {
	if q.URI != "" {
		return false
	}
	binds := func(n *xdm.Node) bool {
		return n.Kind() == xdm.KindElement && (isXSL(n, "variable") || isXSL(n, "param")) && n.AttrValue("name") == q.Local
	}
	for n := el; ; n = n.Parent() {
		p := n.Parent()
		if p == nil || p.Kind() != xdm.KindElement {
			return false
		}
		if gp := p.Parent(); gp == nil || gp.Kind() != xdm.KindElement {
			// n is a top-level declaration of p, the stylesheet.
			for ch := range p.Children() {
				if binds(ch) {
					return true
				}
			}
			return false
		}
		for s := n.PrevSibling(); s != nil; s = s.PrevSibling() {
			if binds(s) {
				return false
			}
		}
	}
}

// docAttrSelect reports whether el, a motionless xsl:apply-templates at the
// document node, selects its attributes: one attribute step with no
// predicate (@*, as the rule match="." of si-apply-templates-010 has). A
// document node has none, so the instruction runs as the tree runs it, over
// the empty sequence, on the stand-in (as leafEmptySelect on a leaf).
func docAttrSelect(el *xdm.Node) bool {
	if el.Attr("", "select") == nil {
		return false
	}
	st, ok := oneStep(parseAt(el, el.AttrValue("select"))).(*xpath.Step)
	return ok && len(st.Predicates) == 0 && st.Axis == xpath.AxisAttribute
}

// leafAttrSetOp reports whether x, a set operator, has as operands two
// attribute steps of the focus with no predicate (@* except @length). At a
// leaf (focus vLeaf: a text, comment or PI node or an attribute, which have
// none; a value, where the tree raises the same XPTY0020; a variable's
// complete node) both are read whole, and identity is the tree's: the
// operator reads nothing noIdent guards (streamable-063's rule node() on a
// text node).
func leafAttrSetOp(x *xpath.BinaryOp) bool {
	if x.Op != "union" && x.Op != "intersect" && x.Op != "except" {
		return false
	}
	attr := func(e xpath.Expr) bool {
		st, ok := oneStep(e).(*xpath.Step)
		return ok && st.Axis == xpath.AxisAttribute && len(st.Predicates) == 0
	}
	return attr(x.Left) && attr(x.Right)
}
