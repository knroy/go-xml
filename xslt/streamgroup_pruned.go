package xslt

// Grouping populations of attributes, leaves or node() children, held as a
// pruned copy of the document built from the events (lane L5, round 2:
// si-group-024 `transaction/@date` in a rule for the document element,
// si-group-033 `*/text()` in a rule for the document element in main mode,
// si-group-037 `node()` with apply-templates in the body).
//
// select="P/@a", "P/text()" or "P/node()", P a path of child steps to
// elements. P's matches are walked as popWalk walks a population; for each,
// the copy of its ancestors (shallow: name, namespaces, attributes, base URI)
// is made once, shared by all the matches below them, and the match itself
// is copied -- shallow for an attribute population, whole (copyEvents) for
// one of its children. The population is then the copies' attributes, or
// children, that the last step's test takes, in document order: a pruned
// copy of the document holding each node of the tree the members and their
// ancestors are, once. The tree's grouping runs over them; the body runs as
// the tree runs it, nothing of the stream intercepted (executeOff).
//
// Memory: every node built is a copy of a distinct node of the document
// (ancestors once, members' parents and their content once), so never more
// than the tree's records, at any depth and with no tree to fall back to.
//
// What the body (and any template rule it applies) sees differs from the
// tree only outside what was copied: siblings of the matches' ancestors,
// the document node's URIs, node identity as generate-id spells it. So the
// grouping and the body must read members by name, value, attributes,
// content, and upward by ancestors' names and attributes only
// (prunedExprOK); rules applied are checked alike, patterns without
// positions.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// prunedPop is the shape of a pruned population: k child steps P, then the
// last step's test, of attributes (attr) or of P's matches' children.
type prunedPop struct {
	k    int
	test xpath.NodeTest
	attr bool
}

// prunedPopulation reports the shape of el's select, an xsl:for-each-group
// not in a streamed group's body, when it is a pruned population and
// nothing el and what it applies evaluate reads beyond the copy.
func (b *streamBuilder) prunedPopulation(el *xdm.Node) *prunedPop {
	if b.grp {
		return nil
	}
	pe, ok := parseAt(el, el.AttrValue("select")).(*xpath.PathExpr)
	if !ok || pe.Root || len(pe.Steps) < 1 {
		return nil
	}
	var p prunedPop
	for _, x := range pe.Steps[:len(pe.Steps)-1] {
		st, ok := x.(*xpath.Step)
		if !ok || st.Axis != xpath.AxisChild {
			return nil
		}
		if leaf, elem := testKinds(st.Test); leaf || !elem {
			return nil
		}
		p.k++
	}
	last, ok := pe.Steps[len(pe.Steps)-1].(*xpath.Step)
	if !ok || len(last.Predicates) > 0 {
		return nil
	}
	switch leaf, _ := testKinds(last.Test); {
	case last.Axis == xpath.AxisAttribute:
		p.attr = true
	case last.Axis != xpath.AxisChild || !leaf:
		return nil // elements alone are popWalk's
	}
	if p.k == 0 && p.attr {
		return nil // the context's own attributes: motionless, not a population read
	}
	p.test = last.Test
	if !prunedInstrOK(el, true, p.k) {
		return nil
	}
	return &p
}

// prunedReady checks op's split against its pruned shape once compiled: P
// is the split's first k steps, child steps the walk takes. It clears
// op.pruned when not.
func prunedReady(op *streamOp) bool {
	p, s := op.pruned, op.split
	if p == nil {
		return false
	}
	n := p.k
	if !p.attr {
		n++
	}
	ok := s != nil && s.cond == nil && !s.group && !s.ctxItem && !s.outermost && !s.innermost &&
		!s.hasBranches() && len(s.steps) == n
	for j := 0; ok && j < p.k; j++ {
		st := s.steps[j]
		_, elem := testKinds(st.test)
		ok = st.axis == xpath.AxisChild && elem
		for _, pr := range st.preds {
			ok = ok && !pr.kids
		}
	}
	if !ok {
		op.pruned = nil
	}
	return ok
}

// prunedFeed is the feed of op, a pruned population's grouping, for instr:
// eval builds the population, and the grouping runs off the stream.
func (run *streamRun) prunedFeed(instr Instruction, rt *runtime, op *streamOp) *burstFeed {
	return &burstFeed{instr: instr, offBody: true,
		eval: func(rt *runtime) (xdm.Sequence, error) { return run.prunedEval(rt, op) }}
}

// executeOff runs i, whose feed f builds its population, as the tree runs
// it over that population, with a feed that knows only it: nothing in the
// body, nor in a rule it applies, reads the stream.
func (i *forEachGroupInstr) executeOff(rt *runtime, out *outputBuilder, f *burstFeed) error {
	seq, err := f.eval(rt)
	if err != nil {
		return err
	}
	sub := *rt
	sub.bursts = &burstFeed{instr: i, eval: func(*runtime) (xdm.Sequence, error) { return seq, nil }}
	return i.Execute(&sub, out)
}

// prunedAnc is an ancestor copied: the original's identity, and the
// builder of its copy's content.
type prunedAnc struct {
	id xdm.IdentityKey
	b  *outputBuilder
}

// prunedEval walks op's population and returns the members, copies in the
// pruned copy of the document.
func (run *streamRun) prunedEval(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	if run.used {
		return nil, errStreamPlan
	}
	run.used = true
	p := op.pruned
	ps := *op.split
	ps.steps = ps.steps[:p.k]
	w := &popWalk{s: &ps, d: run.d}
	w.r = splitRun{s: &ps, ctx: rt.ctx, c: run.c}
	w.filt = xdmstream.Filter{Accept: op.split.accept, Depth: run.d}
	if p.k > 0 {
		w.insts = []*splitInst{ps.inst(0)}
	}
	root := newOutputBuilder(rt)
	var stack []prunedAnc
	var copies []*xdm.Node
	read, first := true, true
	for {
		var ev xdmstream.Event
		if p.k == 0 {
			// node() or text() of the context: the context, at its start
			// tag, is the one element whose children are the members.
			if !first {
				break
			}
			if run.d == 0 {
				return nil, errStreamPlan
			}
			ev = run.c.At()
		} else {
			e, ok, err := w.next(read)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			ev = e
		}
		first = false
		if err := rt.ctx.Err(); err != nil {
			return nil, err
		}
		v, err := run.c.View()
		if err != nil {
			return nil, err
		}
		var anc []*xdm.Node
		for a := v.Parent(); a != nil && a.Kind() == xdm.KindElement; a = a.Parent() {
			anc = append(anc, a)
		}
		for i, j := 0, len(anc)-1; i < j; i, j = i+1, j-1 {
			anc[i], anc[j] = anc[j], anc[i]
		}
		k := 0
		for k < len(stack) && k < len(anc) && stack[k].id == anc[k].Identity() {
			k++
		}
		stack = stack[:k]
		for ; k < len(anc); k++ {
			parent := root
			if k > 0 {
				parent = stack[k-1].b
			}
			sub, err := copyStartOf(rt, parent, anc[k])
			if err != nil {
				return nil, err
			}
			stack = append(stack, prunedAnc{id: anc[k].Identity(), b: sub})
		}
		parent := root
		if len(stack) > 0 {
			parent = stack[len(stack)-1].b
		}
		if p.attr {
			sub, err := copyStartOf(rt, parent, v)
			if err != nil {
				return nil, err
			}
			copies, read = append(copies, sub.Open()), false
			continue
		}
		if err := copyEvents(rt, parent, run.c, ev.Depth, false, nil); err != nil {
			return nil, err
		}
		var el *xdm.Node
		if o := parent.Open(); o != nil {
			el = o.LastChild()
		} else if seq := parent.Sequence(); len(seq) > 0 {
			el, _ = seq[len(seq)-1].(*xdm.Node)
		}
		if el == nil {
			return nil, errStreamPlan
		}
		copies, read = append(copies, el), true
	}
	if err := root.Refused(); err != nil {
		return nil, err
	}
	var members xdm.Sequence
	for _, el := range copies {
		if p.attr {
			for a := range el.Attrs() {
				if p.test.Matches(a, xdm.KindAttribute) {
					members = append(members, a)
				}
			}
			continue
		}
		for ch := el.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if p.test.Matches(ch, xdm.KindElement) {
				members = append(members, ch)
			}
		}
	}
	return members, nil
}

// copyStartOf opens in out a copy of v, an element (a view or one of its
// ancestors' copies), as copyStart does of the cursor's.
func copyStartOf(rt *runtime, out *outputBuilder, v *xdm.Node) (*outputBuilder, error) {
	sub := out.StartElement(v.Name())
	el := sub.Open()
	if out.Open() == nil && el.BaseURI() == "" {
		el.SetBaseURI(v.BaseURI())
	}
	copyTyping(el, v, false)
	copyNamespacesTo(rt, sub, v)
	for a := range v.Attrs() {
		if err := sub.AddAttributeWithTyping(a.Name(), a.Value(), copiedTyping(a, false)); err != nil {
			return nil, err
		}
	}
	return sub, nil
}

// prunedInstrs are the instructions a body over pruned copies may not hold.
var prunedInstrs = map[string]bool{"call-template": true, "source-document": true, "evaluate": true,
	"number": true, "stream": true, "merge": true, "fork": true, "result-document": true}

// prunedFns read identity, a document, or what the copy left out.
var prunedFns = map[string]bool{"generate-id": true, "root": true, "path": true, "base-uri": true,
	"document-uri": true, "id": true, "idref": true, "element-with-id": true, "key": true,
	"unparsed-entity-uri": true, "unparsed-entity-public-id": true, "lang": true,
	"accumulator-before": true, "accumulator-after": true, "function-lookup": true, "doc": true,
	"document": true, "collection": true, "uri-collection": true, "transform": true,
	"load-xquery-module": true, "has-children": true, "innermost": true, "outermost": true,
	"current-merge-group": true, "current-merge-key": true}

// prunedInstrOK reports whether n and everything under it reads only what
// the pruned copy has, climbing at most up parent steps (prunedExprOK); top
// says n is the grouping itself, whose select is the population's own. A
// template applied is checked too: every rule of the stylesheet module,
// which must import or include nothing.
func prunedInstrOK(n *xdm.Node, top bool, up int) bool {
	rules := false
	ok := true
	var check func(n *xdm.Node, top bool)
	check = func(n *xdm.Node, top bool) {
		if !ok {
			return
		}
		switch n.Kind() {
		case xdm.KindText:
			if strings.ContainsAny(n.Value(), "{}") {
				ok = false // a text value template: not scanned
			}
			return
		case xdm.KindElement:
		default:
			return
		}
		xsl := n.Name().URI == xdm.NSXSL
		local := n.Name().Local
		switch {
		case xsl && prunedInstrs[local]:
			ok = false
			return
		case xsl && (local == "apply-templates" || local == "next-match" || local == "apply-imports"):
			rules = true
		case xsl && local == "sequence" && n.Attr("", "select") != nil && !leafAtomic(n) && !inElement(n):
			ok = false // it could hand a member on as the node itself
			return
		}
		for a := range n.Attrs() {
			if a.Name().URI != "" && !(xsl && a.Name().URI == xdm.NSXSL) {
				continue
			}
			if top && a.Name().Local == "select" {
				continue
			}
			var srcs []string
			pattern := xsl && patternAttrs[a.Name().Local]
			if xsl && (exprAttrs[a.Name().Local] || pattern) {
				srcs = []string{a.Value()}
			} else if strings.ContainsAny(a.Value(), "{}") {
				exprs, valid := avtExpressions(a.Value())
				if !valid {
					ok = false
					return
				}
				srcs = exprs
			}
			for _, src := range srcs {
				e := parseAt(n, src)
				if e == nil || !prunedExprOK(e, up) || pattern && positionalPattern(e) {
					ok = false
					return
				}
			}
		}
		for ch := range n.Children() {
			check(ch, false)
		}
	}
	check(n, top)
	if !ok || !rules {
		return ok
	}
	// The rules a body's apply-templates may reach: all of this module's.
	sheet := n
	for sheet.Parent() != nil && sheet.Parent().Kind() == xdm.KindElement {
		sheet = sheet.Parent()
	}
	for ch := range sheet.Children() {
		if ch.Kind() != xdm.KindElement || ch.Name().URI != xdm.NSXSL {
			continue
		}
		switch ch.Name().Local {
		case "import", "include", "use-package":
			return false
		case "template":
			if ch.Attr("", "match") != nil {
				check(ch, false)
			}
		}
	}
	return ok
}

// patternAttrs are the attributes of XSLT instructions that hold a pattern.
var patternAttrs = map[string]bool{"match": true, "group-starting-with": true, "group-ending-with": true}

// positionalPattern reports whether a pattern has a predicate that reads a
// position (a number, position(), last()): among the pruned copy's
// siblings it would count other nodes.
func positionalPattern(e xpath.Expr) bool {
	found := false
	walkExprNodes(e, func(v any) {
		switch x := v.(type) {
		case *xpath.Step:
			for _, p := range x.Predicates {
				if _, isLit := p.(*xpath.Literal); isLit || strings.Contains(p.String(), "position(") ||
					strings.Contains(p.String(), "last(") {
					found = true
				}
			}
		case *xpath.FilterExpr:
			found = true
		}
	})
	return found
}

// prunedExprOK reports whether e reads of nodes only what the pruned copy
// has the tree's way: downward and attributes; upward (parent, ancestor) to
// elements and their attributes and names, never down again; no root path,
// sibling or following axis, identity or set operator, no function in
// prunedFns, no stylesheet or dynamic function. At most up parent steps in
// all: a member's ancestors are copied up to the document element, whose
// copy has no document node above it, and a member is at least up+1 below
// the document node (P's k steps and the member's own).
func prunedExprOK(e xpath.Expr, up int) bool {
	ok := true
	parents := 0
	walkExprNodes(e, func(v any) {
		switch x := v.(type) {
		case *xpath.PathExpr:
			if x.Root {
				ok = false
			}
			up := false
			for _, st := range x.Steps {
				s, isStep := st.(*xpath.Step)
				if !isStep {
					continue
				}
				switch s.Axis {
				case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf:
					up = true
				case xpath.AxisAttribute, xpath.AxisNamespace, xpath.AxisSelf:
				default:
					if up {
						ok = false // down again from an ancestor: its other content is not copied
					}
				}
			}
		case *xpath.Step:
			switch x.Axis {
			case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf, xpath.AxisSelf,
				xpath.AxisAttribute, xpath.AxisNamespace:
			case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf:
				// The copy's top has no document node above it.
				if x.Axis == xpath.AxisParent {
					parents++
				}
				if leaf, elem := testKinds(x.Test); leaf || !elem {
					if kt, isKind := x.Test.(*xpath.KindTest); !isKind || !kt.Any || x.Axis != xpath.AxisParent {
						ok = false
					}
				}
			default:
				ok = false
			}
		case *xpath.BinaryOp:
			switch x.Op {
			case "is", "<<", ">>", "union", "intersect", "except":
				ok = false
			}
		case *xpath.FuncCall:
			switch x.Name.URI {
			case fnNS:
				if prunedFns[x.Name.Local] {
					ok = false
				}
			case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
			default:
				ok = false
			}
		case *xpath.DynamicCall, *xpath.NamedFunctionRef, *xpath.InlineFunctionExpr:
			ok = false
		}
	})
	return ok && parents <= up
}
