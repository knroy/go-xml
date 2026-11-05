package xslt

// A grouping by key (or with a sort) over a streamed population of elements
// whose members may be the document element's children (lane L3:
// si-fork-803..807, `xsl:fork/xsl:for-each-group select="city"
// group-by="@country"` in a rule for cities).
//
// §19.8.4.19: the groups of group-by are known only at the population's
// end, so every member is held. Held as grounded copies that are the
// originals to identity and order (streamgroup_held.go) they cost more than
// the tree's records (a batch per few members, and an ordinal per node), so
// the memory rule refused them below the document node, and at it capped
// them. Here, when nothing the grouping and its body evaluate can tell a
// member from a plain copy of it -- no identity, no axis out of a member,
// no root, no base or document URI of its document, no template or function
// that could do either -- each member is copied off the cursor as events,
// as copy-of copies it (copyEvents: names, in-scope namespaces, attributes,
// typing, base URI, content), into one fragment shared by all the members:
// the members' own records once, which the tree holds besides the rest of
// the document. The tree's grouping then runs over those copies, in
// document order, as over any grounded population.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// eventPopulation reports whether op, a held grouping population (heldPop),
// may be copied as events: its split is a path of elements that cannot
// nest, read whole, and nothing in the instruction reads more of a member
// than a copy has (copyFreeBody). It then marks the split to hand each
// match over at its start tag (matchBody).
func eventPopulation(op *streamOp) bool {
	s := op.split
	if !op.heldPop || s == nil || s.cond != nil || s.nests || s.tail != nil || s.tailText != "" ||
		s.red != "" || s.mode != matchGround || s.keepLeaves || s.group || s.ctxItem || s.steps == nil ||
		!isVarRef(s.outer.Expr(), splitItems) || !copyFreeBody(op.el) {
		return false
	}
	_, elem := testKinds(s.steps[len(s.steps)-1].test)
	if !elem {
		return false
	}
	s.mode, s.rawBody, op.eventPop = matchBody, true, true
	op.keep = prunedMembers(op.el) // streamcopy_l3keep.go
	return true
}

// eventPopEval reads op's population off the cursor, a copy of each member
// as it is met, and returns the copies (evalOp for an eventPop op).
func (run *streamRun) eventPopEval(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	g := run.groupAt(op)
	if run.used || op.inGroup && g == nil {
		return nil, errStreamPlan
	}
	run.used = true
	// The copies stand for the members the tree reads in its source, which
	// the parse budget counted: they are not charged to MaxNodes, the
	// result's budget, which the tree never charges for them. They are
	// children of one document node, which holds the base URI they share:
	// nothing in the body reaches a member's parent (copyFreeBody), and a
	// parentless copy would carry a base URI of its own.
	var (
		doc     *xdm.Node
		members xdm.Sequence
		buf     []string
	)
	err := op.split.loopAt(rt.ctx, run.c, run.d, g, nil, func(v *xdm.Node, md int) (bool, error) {
		if v.Kind() != xdm.KindElement {
			return false, errStreamPlan // eventPopulation admits elements only
		}
		if doc == nil {
			doc = xdm.NewFragment().NewRoot(xdm.KindDocument, xdm.QName{}, "")
			doc.SetBaseURI(run.c.At().Node.BaseURI())
		}
		m, err := copyMember(rt, doc, run.c, md, op.keep, &buf)
		if err != nil {
			return false, err
		}
		members = append(members, m)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// copyMember copies the element whose start tag c is at, depth d, as the
// last child of doc, read off the parse's own nodes (Event.Node) as its
// events pass, as copy-of copies a node (copyItem: xdm.Copy, then
// inheritNamespaces): each node's name, base URI, typing, declared
// namespaces as written, attributes and content, and on the copy's root the
// bindings it inherited, sorted. It is written straight into doc's records:
// no builder, no view per node. With keep, the root keeps only those
// attributes and children, the others skipped on the cursor unread. buf is
// scratch for the namespace pairs.
func copyMember(rt *runtime, doc *xdm.Node, c *xdmstream.Cursor, d int, keep *memberKeep, buf *[]string) (*xdm.Node, error) {
	if err := rt.ctx.Err(); err != nil {
		return nil, err
	}
	src := c.At().Node
	root := doc.AppendElementInheriting(src.Name())
	shallowMember(root, src, keep, buf)
	var arr [32]nsBinding
	pairs := (*buf)[:0]
	for _, b := range scopeBindings(src, arr[:0]) {
		if !declares(root, b.prefix) {
			pairs = append(pairs, b.prefix, b.uri)
		}
	}
	root.AddNamespaces(pairs...)
	*buf = pairs
	if keep != nil && len(keep.kids) == 0 {
		return root, c.Skip()
	}
	cur := root
	var text *xdm.Node // the text node last appended to cur, if nothing followed it
	for {
		ev, err := c.Next()
		if err != nil {
			return nil, err
		}
		if keep != nil && ev.Depth == d+1 && ev.Kind != xdmstream.EndElement {
			text = nil
			if ev.Kind != xdmstream.StartElement {
				continue // a leaf of the member's own: not read
			}
			if !hasName(keep.kids, ev.Node.Name()) {
				if err := c.Skip(); err != nil {
					return nil, err
				}
				continue
			}
		}
		switch ev.Kind {
		case xdmstream.StartElement:
			if err := rt.ctx.Err(); err != nil {
				return nil, err
			}
			cur, text = cur.AppendElementInheriting(ev.Node.Name()), nil
			shallowMember(cur, ev.Node, nil, buf)
		case xdmstream.EndElement:
			if ev.Depth == d {
				return root, nil
			}
			cur, text = cur.Parent(), nil
		case xdmstream.Text:
			if text != nil {
				text.AppendValue(ev.Node.Value())
			} else {
				text = cur.AppendText(ev.Node.Value())
			}
		case xdmstream.Comment:
			cur.AppendComment(ev.Node.Value())
			text = nil
		case xdmstream.PI:
			cur.AppendPI(ev.Node.Name().Local, ev.Node.Value())
			text = nil
		default:
			return nil, errStreamPlan // the document ended inside the member
		}
	}
}

// shallowMember gives el, a copy of src just made, what xdm.Copy gives a
// copied element besides its content: src's base URI, typing, declared
// namespaces and attributes (with keep, only the ones it names), with their
// typing.
func shallowMember(el, src *xdm.Node, keep *memberKeep, buf *[]string) {
	el.SetBaseURI(src.BaseURI())
	copyTyping(el, src, false)
	pairs := (*buf)[:0]
	for p, uri := range src.DeclaredNamespaces() {
		pairs = append(pairs, p, uri)
	}
	el.AddNamespaces(pairs...)
	*buf = pairs
	for a := range src.Attrs() {
		if keep != nil && !hasName(keep.attrs, a.Name()) {
			continue
		}
		ca := el.AppendAttr(a.Name(), a.Value())
		ca.CopyTypingFrom(a)
		if a.TypeEnv() != nil {
			ca.SetTypeEnv(a.TypeEnv())
		}
	}
}

// copyFreeInstrs are the instructions a body of copies may not hold: they
// reach templates, functions, other documents, or a member's position among
// its siblings.
var copyFreeInstrs = map[string]bool{"apply-templates": true, "call-template": true, "next-match": true,
	"apply-imports": true, "source-document": true, "evaluate": true, "number": true, "stream": true}

// copyFreeFns are the functions that read a node's identity, its document,
// or what lies outside it.
var copyFreeFns = map[string]bool{"generate-id": true, "root": true, "path": true, "base-uri": true,
	"document-uri": true, "id": true, "idref": true, "element-with-id": true, "key": true,
	"unparsed-entity-uri": true, "unparsed-entity-public-id": true, "lang": true, "snapshot": true,
	"accumulator-before": true, "accumulator-after": true, "function-lookup": true, "doc": true,
	"document": true, "collection": true, "uri-collection": true, "transform": true, "load-xquery-module": true}

// copyFreeBody reports whether nothing in el, an xsl:for-each-group with
// its attributes and content, can tell a member from its copy.
func copyFreeBody(el *xdm.Node) bool {
	ok := true
	var check func(n *xdm.Node)
	check = func(n *xdm.Node) {
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
		if xsl && copyFreeInstrs[n.Name().Local] {
			ok = false
			return
		}
		if xsl && n.Name().Local == "sequence" && n.Attr("", "select") != nil && !leafAtomic(n) &&
			!nodeFreeSelect(n) { // streamcopy_nodefree.go
			ok = false // it could hand a member on as the node itself
			return
		}
		if !nodeExprs(n, func(_ *xdm.Node, e xpath.Expr) bool { return copyFreeExpr(e) }) {
			ok = false
			return
		}
		for ch := range n.Children() {
			check(ch)
		}
	}
	check(el)
	return ok
}

// nodeExprs calls fn on each expression n, an element of a stylesheet,
// holds in its attributes -- an XSLT instruction's expression attributes
// (exprAttrs), and attribute value templates -- with the attribute; it
// reports false when one cannot be parsed or fn returns false.
func nodeExprs(n *xdm.Node, fn func(a *xdm.Node, e xpath.Expr) bool) bool {
	xsl := n.Name().URI == xdm.NSXSL
	for a := range n.Attrs() {
		if a.Name().URI != "" && !(xsl && a.Name().URI == xdm.NSXSL) {
			continue
		}
		var srcs []string
		if xsl && exprAttrs[a.Name().Local] {
			srcs = []string{a.Value()}
		} else if strings.ContainsAny(a.Value(), "{}") {
			exprs, valid := avtExpressions(a.Value())
			if !valid {
				return false
			}
			srcs = exprs
		}
		for _, src := range srcs {
			if e := parseAt(n, src); e == nil || !fn(a, e) {
				return false
			}
		}
	}
	return true
}

// exprAttrs are the attributes of XSLT instructions that hold an expression.
var exprAttrs = map[string]bool{"select": true, "test": true, "group-by": true, "group-adjacent": true,
	"use-when": true, "value": true, "count": true, "from": true, "start-at": true, "key": true}

// copyFreeExpr reports whether e reads nothing of a member that its copy
// does not have: no axis out of a node but down and to its attributes, no
// root path, no identity comparison or set operation, no function in
// copyFreeFns, no stylesheet or dynamic function.
func copyFreeExpr(e xpath.Expr) bool {
	ok := true
	walkExprNodes(e, func(v any) {
		switch x := v.(type) {
		case *xpath.Step:
			switch x.Axis {
			case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf, xpath.AxisSelf,
				xpath.AxisAttribute, xpath.AxisNamespace:
			default:
				ok = false
			}
		case *xpath.PathExpr:
			if x.Root {
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
				if copyFreeFns[x.Name.Local] {
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
	return ok
}
