package xslt

// Copies of the document and of the document element, read off the cursor
// (lane L3; DESIGN section 1.6: "copy-of . streams events to the output
// instead of grounding").
//
// A grounded copy of the document element is the whole document, held twice
// while it is cloned (DESIGN section 2.1, rule 3), so the plans refused it or
// gave it up to the tree. Here the copy is made from the events as they
// come instead, with what xsl:copy does for each element (the streamed
// shallow-copy rule's calls, streamapply.go): its name, its in-scope
// namespaces, its attributes, then its children in order. Nothing of the
// source is held beyond the open elements' views:
//
//   - into an open element (a literal result element, xsl:copy of an
//     element, ...), the copy is the output itself: what copyItem's
//     in-place path makes, written as it is read (copyEvents);
//   - anywhere else -- a variable, a validated copy, a function's value --
//     the document node is built once, in place, as the copy the tree's
//     copy-of would have made of its document (docCopyEval): the tree holds
//     the source and that copy, the stream the copy alone.
//
// Only an untyped source, and a copy that keeps nothing but names and
// values, is written as events: copy-namespaces="no", copy-accumulators,
// validation other than strip (or preserve of an untyped source) and @type
// go through the built document, which the instruction then treats as the
// tree treats its document node.

import (
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
	"github.com/knroy/go-xml/v2/xpath"
)

// copyEvents writes a copy of the context the cursor c is at to out: at
// depth d >= 1, the element whose start tag the cursor has just returned,
// read to its end tag; at d == 0, the document's children, read to its end.
// doc, when set, is called with the view of the document element, at its
// start tag. strip says the copy keeps, of each node's typing, only what
// validation="strip" keeps (is-id, is-idrefs: a DTD's ID attributes);
// otherwise it keeps all of it, as AppendCopy does.
func copyEvents(rt *runtime, out *outputBuilder, c *xdmstream.Cursor, d int, strip bool, doc func(v *xdm.Node)) error {
	stack := []*outputBuilder{out}
	// The in-scope bindings of the open elements, an entry only where an
	// element declares one: each copied element's come from its parent's
	// and its own declarations, not from a walk up its ancestors, which made
	// a deep copy quadratic.
	var scopes []copyScope
	start := func(top *outputBuilder, depth int) error {
		v, err := c.View()
		if err != nil {
			return err
		}
		var all []nsBinding
		switch {
		case len(scopes) == 0:
			all = scopeBindings(v, nil)
			scopes = append(scopes, copyScope{depth, all})
		case v.NumNamespaceDecls() > 0:
			all = overlayScope(scopes[len(scopes)-1].all, v)
			scopes = append(scopes, copyScope{depth, all})
		default:
			all = scopes[len(scopes)-1].all
		}
		sub, err := copyStart(rt, top, v, strip, all)
		if err != nil {
			return err
		}
		stack = append(stack, sub)
		return nil
	}
	if d > 0 {
		if err := start(out, d); err != nil {
			return err
		}
	} else if err := rt.ctx.ChargeNodes(1); err != nil {
		return err // the document node, as countSubtree counts it
	}
	for {
		ev, err := c.Next()
		if err != nil {
			return err
		}
		top := stack[len(stack)-1]
		switch ev.Kind {
		case xdmstream.EndDocument:
			return top.Refused()
		case xdmstream.EndElement:
			stack = stack[:len(stack)-1]
			if n := len(scopes); n > 0 && scopes[n-1].depth == ev.Depth {
				scopes = scopes[:n-1]
			}
			if err := top.Refused(); err != nil || ev.Depth == d {
				return err
			}
		case xdmstream.StartElement:
			if err := rt.ctx.Err(); err != nil {
				return err
			}
			if doc != nil && ev.Depth == 1 {
				v, err := c.View()
				if err != nil {
					return err
				}
				doc(v)
			}
			if err := start(top, ev.Depth); err != nil {
				return err
			}
		case xdmstream.Text:
			if len(stack) > 1 { // a document node has no text children
				top.AppendText(ev.Node.Value())
			}
		case xdmstream.Comment, xdmstream.PI:
			if err := rt.ctx.ChargeNodes(1); err != nil {
				return err
			}
			// A fresh node, so the copy shares none of the parse's text.
			n := xdmbuild.NewComment(ev.Node.Value())
			if ev.Kind == xdmstream.PI {
				n = xdmbuild.NewPI(ev.Node.Name().Local, ev.Node.Value())
			}
			top.AppendNode(n)
		}
	}
}

// copyScope is the in-scope bindings (scopeBindings) of the open element at
// depth, one that declares a namespace.
type copyScope struct {
	depth int
	all   []nsBinding
}

// overlayScope is scopeBindings of v given its parent's, parent: v's own
// declarations, the last of a prefix winning, over the inherited ones.
func overlayScope(parent []nsBinding, v *xdm.Node) []nsBinding {
	all := slices.Clone(parent)
	for prefix, uri := range v.DeclaredNamespaces() {
		if prefix == "xml" {
			continue
		}
		if k := slices.IndexFunc(all, func(b nsBinding) bool { return b.prefix == prefix }); k >= 0 {
			all[k].uri = uri
		} else {
			all = append(all, nsBinding{prefix, uri})
		}
	}
	all = slices.DeleteFunc(all, func(b nsBinding) bool { return b.uri == "" })
	slices.SortFunc(all, func(a, b nsBinding) int { return strings.Compare(a.prefix, b.prefix) })
	return all
}

// copyStart opens, in out, the copy of v, the element whose start tag the
// cursor is at, as xsl:copy of an element opens it (copyInstr), with its
// attributes; all is v's in-scope bindings. The builder of its content is
// returned.
func copyStart(rt *runtime, out *outputBuilder, v *xdm.Node, strip bool, all []nsBinding) (*outputBuilder, error) {
	sub := out.StartElement(v.Name())
	el := sub.Open()
	if out.Open() == nil && el.BaseURI() == "" {
		el.SetBaseURI(v.BaseURI())
	}
	copyTyping(el, v, strip)
	var arr [32]nsBinding
	addBindings(rt, sub, copyOfBindings(v, all, arr[:0]))
	for a := range v.Attrs() {
		if err := sub.AddAttributeWithTyping(a.Name(), a.Value(), copiedTyping(a, strip)); err != nil {
			return nil, err
		}
		if !strip && a.TypeEnv() != nil {
			if ca := el.Attr(a.Name().URI, a.Name().Local); ca != nil {
				ca.SetTypeEnv(a.TypeEnv())
			}
		}
	}
	return sub, nil
}

// copyOfBindings appends to buf v's in-scope bindings in the order the
// tree's copy-of gives its copy (copyItem: deepCopy, then
// inheritNamespaces): v's own declarations as written, then the inherited
// ones, sorted. xsl:copy's sorted order would serialise them otherwise
// (si-result-document-107). all is v's in-scope bindings (scopeBindings).
func copyOfBindings(v *xdm.Node, all, buf []nsBinding) []nsBinding {
	for p := range v.DeclaredNamespaces() {
		for _, b := range all {
			if b.prefix == p { // not xml, nor an undeclaration
				buf = append(buf, b)
			}
		}
	}
	own := len(buf)
	for _, b := range all {
		if !slices.Contains(buf[:own], b) {
			buf = append(buf, b)
		}
	}
	return buf
}

// copiedTyping is the typing a copy of n has: all of n's, or with strip
// what stripping keeps (CopyTypingStrippedFrom).
func copiedTyping(n *xdm.Node, strip bool) xdm.Typing {
	t := xdm.TypingOf(n)
	if strip {
		return xdm.Typing{IsID: t.IsID, IsIDREFS: t.IsIDREFS}
	}
	return t
}

// copyTyping gives el, the copy of v, its typing (copiedTyping) and, unless
// strip, its type environment.
func copyTyping(el, v *xdm.Node, strip bool) {
	if t := copiedTyping(v, strip); t != (xdm.Typing{}) {
		el.ApplyTyping(t)
	}
	if !strip && v.TypeEnv() != nil {
		el.SetTypeEnv(v.TypeEnv())
	}
}

// stripsTyping reports whether instr, an eventCopier, keeps only what
// validation="strip" keeps of the copies' typing.
func stripsTyping(instr Instruction) bool {
	i, ok := instr.(*copyOfInstr)
	return ok && i.validation.mode == validateStrip
}

// eventCopier reports whether instr, the instruction of a select op, makes
// of a node handed to it what copyEvents writes: xsl:copy-of with the
// default copy-namespaces, no copy-accumulators, and a validation that keeps
// nothing (strip, or preserve of an untyped source); or xsl:sequence of an
// untyped source, whose node becomes content as a copy (inElement). typed
// says the source is validated as it is read.
func eventCopier(instr Instruction, typed bool) bool {
	switch i := instr.(type) {
	case *copyOfInstr:
		v := i.validation
		return !i.noNamespaces && !i.copyAccumulators && v.typeName == nil &&
			(v.mode == validateStrip || v.mode == validatePreserve && !v.constructsElement && !typed)
	case *sequenceInstr:
		return !typed
	}
	return false
}

// --- Planning ------------------------------------------------------------------

// docCopySplit is the split of e at the document node when e copies the
// document whole -- copy-of(.), snapshot(.), or "." itself where self says
// the instruction makes a copy of what it selects (copy-of, or a sequence in
// an element's content) -- or reads its string value: data(.), string(.).
// It returns nil for any other e.
func docCopySplit(e xpath.Expr, base *xpath.Compiled, self bool) *streamSplit {
	part := func(x xpath.Expr) *xpath.Compiled { return xpathleaf.WithExpr(base, x).(*xpath.Compiled) }
	items := &xpath.VarRef{Name: splitItems}
	if _, ok := e.(*xpath.ContextItem); ok {
		if !self {
			return nil
		}
		return &streamSplit{doc: true, ctxItem: true, docCopy: true, outerVar: splitItems, outer: part(items)}
	}
	f, ok := e.(*xpath.FuncCall)
	if !ok || f.Name.URI != fnNS || len(f.Args) > 1 {
		return nil
	}
	if len(f.Args) == 1 {
		if _, ok := f.Args[0].(*xpath.ContextItem); !ok {
			return nil
		}
	}
	switch f.Name.Local {
	case "copy-of", "snapshot":
		// The copy of a document node is a document node, as the one built.
		return &streamSplit{doc: true, ctxItem: true, docCopy: true, outerVar: splitItems, outer: part(items)}
	case "data", "string":
		if len(f.Args) == 0 {
			return nil // focus-dependent at the document node: refused as before
		}
		return &streamSplit{doc: true, ctxItem: true, atomize: true, docText: true, outerVar: splitItems,
			outer: part(&xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{items}})}
	}
	return nil
}

// splitForDoc is splitFor for the select of a select op or of a variable or
// parameter: at the document node, a copy of the document or its string
// value has a split of its own (docCopySplit); loops, applies and groups
// keep splitFor's refusal of the context item.
func (c *compiler) splitForDoc(sel *xpath.Compiled, ns xpath.NamespaceResolver, doc, simple, group, key bool) (*streamSplit, string) {
	if doc && !group && !sel.CompatMode() {
		if e := reparse(sel, ns); e != nil {
			if _, ok := e.(*xpath.ContextItem); ok && simple {
				// Simple content of the document node: its string value
				// (splitFor's data() of the context item).
				e = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "data"}, Args: []xpath.Expr{e}}
			}
			if s := docCopySplit(e, sel, false); s != nil {
				return s, ""
			}
			if s := c.wholeDocSplit(sel, ns); s != nil { // streamcopy_whole.go
				return s, ""
			}
		}
	}
	return c.splitFor(sel, ns, doc, simple, group, key)
}

// --- Running -------------------------------------------------------------------

// docString reads the string value of the document, its text events, to
// the document's end: what the tree's string value holds, and no more.
func docString(c *xdmstream.Cursor) (string, error) {
	var sb strings.Builder
	f := xdmstream.Filter{Accept: acceptNone, Leaves: true}
	for {
		ev, err := c.NextTag(&f)
		if err != nil {
			return "", err
		}
		switch ev.Kind {
		case xdmstream.EndDocument:
			return sb.String(), nil
		case xdmstream.Text:
			sb.WriteString(ev.Node.Value())
		}
	}
}

// buildDoc reads the document off the cursor into a new document node, the
// copy copyDocumentNode makes of a tree's: its children, DOCTYPE and base
// URI, no document URI.
func buildDoc(rt *runtime, c *xdmstream.Cursor) (*xdm.Node, error) {
	b, root := xdmbuild.InPlaceDocument(xsltPolicy{nodes: rt.nodeBudget()})
	err := copyEvents(rt, b, c, 0, false, func(v *xdm.Node) {
		top := v
		for top.Parent() != nil {
			top = top.Parent()
		}
		root.Tree().CopyDTDFrom(top.Tree())
		root.SetBaseURI(top.BaseURI())
	})
	if err != nil {
		return nil, err
	}
	root.Tree().Finalize()
	return root, nil
}

// docCopyEval is evalOp for op, whose split copies the document whole
// (docCopy): the copy is built, and is what the select's items are.
func (run *streamRun) docCopyEval(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	if run.used || (run.d == 0) != op.split.doc {
		return nil, errStreamPlan
	}
	run.used = true
	octx := rt.ctx.WithFocus(xdm.NewTree().Root, 1, 1)
	build := func() (*xdm.Node, error) { return buildDoc(rt, run.c) }
	if run.d > 0 {
		// copy-of(.) of the context element (streamcopy_l3apply.go).
		v, verr := run.c.View()
		if verr != nil {
			return nil, verr
		}
		octx = rt.ctx.WithFocus(v, 1, 1)
		build = func() (*xdm.Node, error) { return buildElem(rt, run) }
	}
	// The copy answers accumulators with the original's values
	// (streamaccum_ground_l4.go); with none recorded it would answer with
	// its own tree's.
	doc, ok, err := recordCopy(rt, run.c, run.d, build)
	if !ok {
		if len(rt.sheet.accumulators) > 0 {
			return nil, errStreamPlan
		}
		doc, err = build()
	}
	if err != nil {
		return nil, err
	}
	if op.split.wholeSole != nil && doc.FirstChild() != nil && doc.FirstChild().NextSibling() == nil {
		return op.split.wholeSole.Eval(octx.WithVar(splitItems, xdm.One(doc))) // streamcopy_whole.go
	}
	return op.split.outer.Eval(octx.WithVar(splitItems, xdm.One(doc)))
}

// docCopyFeed is the feed of op, whose split copies the document whole, for
// instr run in rt: into an open element, an instruction that copies as
// copyEvents does writes the events there; otherwise the document is built
// and handed over.
func (run *streamRun) docCopyFeed(instr Instruction, rt *runtime, op *streamOp) *burstFeed {
	return &burstFeed{instr: instr,
		eval: func(rt *runtime) (xdm.Sequence, error) { return run.docCopyEval(rt, op) },
		read: func(func(xdm.Item) error) error { return errStreamPlan },
		readOut: func(out *outputBuilder, each func(xdm.Item) error) error {
			// The copy itself, not a select run on it (streamcopy_whole.go).
			plain := isVarRef(op.split.outer.Expr(), splitItems)
			if plain && out.Open() != nil && out.Open().Kind() == xdm.KindElement && eventCopier(op.instr, run.typed) {
				if run.used || run.d != 0 {
					return errStreamPlan
				}
				run.used = true
				return copyEvents(rt, out, run.c, 0, stripsTyping(op.instr), nil)
			}
			seq, err := run.docCopyEval(rt, op)
			if err != nil {
				return err
			}
			ci, _ := op.instr.(*copyOfInstr)
			for _, it := range seq {
				if n, ok := it.(*xdm.Node); ok && plain && ci != nil && n.Kind() == xdm.KindDocument {
					err = ci.copyBuilt(rt, out, n)
				} else {
					err = each(it)
				}
				if err != nil {
					return err
				}
			}
			return nil
		}}
}

// copyBuilt is copyItem for doc, a document node built off the cursor that
// nothing else holds: copyItem's branch for a document node without the
// copy it makes first (copyDocumentNode), the built node being that copy.
func (i *copyOfInstr) copyBuilt(rt *runtime, out *outputBuilder, doc *xdm.Node) error {
	if i.noNamespaces {
		for ch := range doc.Children() {
			if ch.Kind() == xdm.KindElement {
				stripNamespaces(ch)
			}
		}
	}
	c, err := i.validation.assess(rt, doc)
	if err != nil {
		return err
	}
	out.AppendNode(c)
	return nil
}

// copiesReadOut is the feed's readOut of op, a copy-of or sequence handing
// its items over as they are read (op.copies): as read does, but a match
// that is the document element, which the split gives up on (noRoot), is
// written as events into out, when the instruction copies as copyEvents
// does and out is where its copy goes.
func (run *streamRun) copiesReadOut(rt *runtime, op *streamOp, g *groupAt, read func(func(xdm.Item) error) error) func(*outputBuilder, func(xdm.Item) error) error {
	return func(out *outputBuilder, each func(xdm.Item) error) error {
		s := op.split
		if !s.noRoot || run.d != 0 || s.nests || s.tail != nil || s.tailText != "" || s.group ||
			!eventCopier(op.instr, run.typed) {
			return read(each)
		}
		if run.used {
			return errStreamPlan
		}
		run.used = true
		root := func() error {
			v, err := run.c.View()
			if err != nil {
				return err
			}
			if out.Open() == nil && v.Attr(xdm.NSXML, "base") != nil {
				// A parentless copy resolves its xml:base against the
				// instruction's base URI (rebaseDetached): the tree's.
				return errSplitGiveUp
			}
			if _, ok := op.instr.(*sequenceInstr); ok && out.Open() == nil {
				return errSplitGiveUp // the node itself, not a copy, at the top
			}
			return copyEvents(rt, out, run.c, 1, stripsTyping(op.instr), nil)
		}
		if err := s.runChecks(rt.ctx); err != nil {
			return err
		}
		r := &splitRun{s: s, ctx: rt.ctx, c: run.c, each: each, rootCopy: root}
		err := r.run(nil, r.at(g, run.d))
		if err == errSplitGiveUp && run.d == 0 && run.i != nil {
			// As read: from the tree, nothing of the document element
			// having been written yet.
			items, err := run.fallback(rt, op.sel)
			if err != nil {
				return err
			}
			for _, it := range xdm.Flatten(items) {
				if err := each(it); err != nil {
					return err
				}
			}
			return nil
		}
		return err
	}
}
