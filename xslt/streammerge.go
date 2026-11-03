package xslt

// Streamed xsl:merge (full streaming, component C10; DESIGN section 1.7,
// "Merge"; XSLT 3.0 §15.4).
//
// A merge source with for-each-source and streamable="yes" (explicit or
// defaulted) reads each document its URIs name on a cursor of its own, all
// of them open at once: its select, a path of child steps (§15.4 makes it
// striding), is split for the cursor and each match grounded, then made the
// snapshot §15.4 says the select is wrapped in (snapshotItem, as the tree
// makes it). The other sources are collected as the tree collects them.
// Each input sequence (a source's, per anchor) gives up its items one at a
// time, and the merge takes, again and again, the least head by the merge
// keys -- ties to the earlier input, which is the tree's stable sort with its
// (source, anchor, position) tie-break -- and with it every head equal to it,
// input by input: the current merge group, the action run on it at once.
// What is held is one head per input and one group.
//
// Keys, collations, the sort order, XTDE2220 (an input out of order),
// XTTE2230 (keys that cannot be compared, within an input, between heads, or
// an untyped key of one source against a typed one of another) and the
// errors of loading and of the keys are the tree's code. The tree reports
// all of them before any action runs, the stream as it meets them: so on
// any error the merge runs again on the tree, whose answer it is -- the
// tree's error, exactly (what the stream wrote goes with the failed
// transformation), as a streamed xsl:source-document is re-run
// (executeStream). A merge the tree completes where the stream failed after
// writing is a streaming defect: an internal error, counted as a guard hit.
//
// Refused when the stylesheet compiles (mergeStreams), the merge then
// running on the tree: a streamed source whose select the split cannot
// read, or with use-accumulators, validation, or sort-before-merge="yes"
// (which holds the input); an action that may ask for last() -- the tree
// gives it the number of groups, which a stream knows only at its end --
// directly or through xsl:call-template or xsl:evaluate, which keep the
// focus; and an action whose effects a re-run would repeat (xsl:message,
// xsl:result-document, xsl:assert, fn:trace). At run time: a document the
// transformation has read already, a resolver that cannot stream, and the
// and the items are read by coroutines, which hand xpath's EvalLock back and
// forth with the merge as a fork's prongs do (streamfork.go).

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// mergeSplit splits sel, the select of n, a streamed merge source, for a
// cursor over each document: nil when it cannot be.
func (c *compiler) mergeSplit(n *xdm.Node, sel *xpath.Compiled, ns xpath.NamespaceResolver) *streamSplit {
	s, why := c.splitFor(sel, ns, true, false, false, false)
	if why != "" || !s.bare() || s.mode != matchGround || s.minDepth() < 1 || s.nests {
		return nil
	}
	return s
}

// mergeStreams reports whether i may read its streamable sources each on a
// cursor of its own (see the file comment).
func mergeStreams(i *mergeInstr, action *xdm.Node) (bool, []int) {
	any := false
	for _, src := range i.sources {
		if !src.streamed || src.sourceURIs == nil {
			if any {
				// Collected after a streamed one: its nodes would be made
				// before that one's, where the tree makes them after
				// (mergeInput.take).
				return false, nil
			}
			continue
		}
		if src.split == nil || src.accums != nil || !src.validation.isDefault() || src.sortBeforeMerge {
			return false, nil
		}
		any = true
	}
	text, ok := mergeKeysOneType(i)
	if !any || !ok || !mergeActionOK(action) {
		return false, nil
	}
	return true, text
}

// mergeKeysOneType reports whether every key position holds values of one
// type across all sources, decided from how each key is written. The tree
// orders all items by one stable sort, whose comparator is no consistent
// order across types (an untyped value against a number, NaN against
// anything) and which raises XTTE2230 only for the pairs it happens to
// compare: a merge of heads cannot follow it there (found by the fuzzer).
// The key positions whose data-type is an attribute value template are
// returned: it must resolve to "text" (runStream checks, before anything is
// read), which compares every value as a string.
func mergeKeysOneType(i *mergeInstr) ([]int, bool) {
	var text []int
	for k := range i.sources[0].keyElems {
		class := ""
		for _, src := range i.sources {
			c := mergeKeyClass(src.keyElems[k], src.streamed && src.sourceURIs != nil)
			if c == "" || class != "" && c != class {
				return nil, false
			}
			class = c
		}
		if class == "avt" {
			text = append(text, k)
		}
	}
	return text, true
}

// mergeStringFns are the fn functions whose value is one xs:string.
var mergeStringFns = map[string]bool{"string": true, "translate": true, "concat": true, "substring": true,
	"substring-before": true, "substring-after": true, "lower-case": true, "upper-case": true,
	"normalize-space": true, "string-join": true, "name": true, "local-name": true}

// mergeKeyClass is the type of the values the merge key el gives: a
// constructor function's (fn:dateTime's), "string" for a function that
// makes a string or a path into a streamed
// document (never validated: untyped, ordered as strings), or "" when it
// cannot be told.
func mergeKeyClass(el *xdm.Node, streamed bool) string {
	if el.Attr("", "select") == nil {
		return ""
	}
	if a := el.Attr("", "data-type"); a != nil {
		switch v := strings.TrimSpace(a.Value()); {
		case v == "text":
			return "text"
		case strings.ContainsAny(v, "{}"):
			return "avt"
		}
		return ""
	}
	e := parseAt(el, el.AttrValue("select"))
	switch x := e.(type) {
	case *xpath.FuncCall:
		switch {
		case x.Name.URI == xdm.NSXS && (x.Name.Local == "integer" || x.Name.Local == "decimal" ||
			x.Name.Local == "date" || x.Name.Local == "dateTime" || x.Name.Local == "time"):
			return "xs:" + x.Name.Local
		case x.Name.URI == xdm.NSXS && x.Name.Local == "string":
			return "string"
		case x.Name.URI == fnNS && x.Name.Local == "dateTime":
			return "xs:dateTime"
		case x.Name.URI == fnNS && mergeStringFns[x.Name.Local]:
			return "string"
		}
	}
	if streamed && nodesOnly(e) {
		return "string"
	}
	return ""
}

// nodesOnly reports, conservatively, whether e returns only elements or
// attributes (of a streamed document: untyped).
func nodesOnly(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step, *xpath.PathExpr:
		return noTextNodes(e)
	case *xpath.FilterExpr:
		return nodesOnly(x.Base)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !nodesOnly(it) {
				return false
			}
		}
		return len(x.Items) > 0
	}
	return false
}

// mergeActionOK checks the merge action: nothing in it asks for the context
// size or has an effect a re-run would repeat. The check is on the source
// text: a mention is enough.
func mergeActionOK(action *xdm.Node) bool {
	ok := true
	mentions := func(v string) bool {
		return strings.Contains(v, "last(") || strings.Contains(v, "trace(")
	}
	walkElements(action, func(el *xdm.Node) bool {
		if isXSL(el, "message") || isXSL(el, "result-document") || isXSL(el, "assert") ||
			isXSL(el, "call-template") || isXSL(el, "evaluate") {
			ok = false
		}
		for a := range el.Attrs() {
			if mentions(a.Value()) {
				ok = false
			}
		}
		for ch := range el.Children() {
			if ch.Kind() == xdm.KindText && mentions(ch.Value()) {
				ok = false
			}
		}
		return ok
	})
	return ok
}

// mergeInput is one input sequence of a streamed merge: a source's, for one
// anchor.
type mergeInput struct {
	src, anchor int
	// next gives the next item of a streamed input; entries are a collected
	// input's, with their keys.
	next    func() (xdm.Item, bool)
	err     error
	entries []mergeEntry
	pos     int
	head    *mergeEntry
	prev    mergeEntry
	// first holds the input's first key value of each kind per key
	// position, for XTTE2230 across sources (checkMergeKeysComparable).
	typed, untyped []*xdm.Atomic
}

// errMergeTree sends a merge to the tree before anything was read.
var errMergeTree = errors.New("streaming: the merge runs on the tree")

// errMergeStop ends the walk of an input the merge no longer reads.
var errMergeStop = errors.New("streaming: merge input stopped")

// executeStream runs i on cursors, and reports whether it did.
func (i *mergeInstr) executeStream(rt *runtime, out *outputBuilder) (bool, error) {
	if noBursts || streamtrace.Off() {
		return false, nil
	}
	sig, mark := signature(out), rt.localsMark()
	undoSecondary := rt.secondaryMark() // streamresult.go
	n0, b0 := xpathleaf.Charged(rt.ctx)
	var unread []func()
	err := i.runStream(rt, out, &unread)
	if err == errMergeTree {
		for _, u := range unread {
			u()
		}
		return false, nil
	}
	if err == nil {
		return true, nil
	}
	streamtrace.Rerun()
	if traceRerun != nil {
		traceRerun(err)
	}
	rt.localsPop(mark)
	if n1, b1 := xpathleaf.Charged(rt.ctx); n1 > n0 || b1 > b0 {
		xpathleaf.Refund(rt.ctx, max(n1-n0, 0), max(b1-b0, 0))
	}
	clean := signature(out) == sig
	var pe *streamtrace.PostureError
	if errors.As(err, &pe) && !clean {
		return true, fmt.Errorf("streaming: internal error after output was written: %w", err)
	}
	for _, u := range unread {
		u()
	}
	undoSecondary()
	terr := i.executeTree(rt, out)
	if terr == nil && !clean {
		streamtrace.Guard()
		return true, fmt.Errorf("streaming: internal error: the streamed merge failed where the tree did not: %w", err)
	}
	return true, terr
}

// mergeDoc is a document a streamed input reads.
type mergeDoc struct {
	in    *mergeInput
	split *streamSplit
	br    burstResolver
	href  string
	base  string
	strip func(*xdm.Node) bool
	ctx   *xpath.Context // the select's focus: the anchor's position
}

// runStream is the streamed merge. A posture fault is returned as its error.
func (i *mergeInstr) runStream(rt *runtime, out *outputBuilder, unread *[]func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			pe, ok := p.(*streamtrace.PostureError)
			if !ok {
				panic(p)
			}
			err = pe
		}
	}()
	rt = rt.clearCurrentRule()
	resolved, colls, err := i.resolveKeys(rt)
	if err != nil {
		return err
	}
	for _, k := range i.textKeys {
		for s := range resolved {
			if resolved[s][k].dataType != "text" {
				return errMergeTree
			}
		}
	}
	var inputs []*mergeInput
	var docs []*mergeDoc
	for s, src := range i.sources {
		if !src.streamed || src.sourceURIs == nil {
			got, err := src.collect(rt, s, resolved[s], colls[s])
			if err != nil {
				return err
			}
			for len(got) > 0 {
				n := 1
				for n < len(got) && got[n].anchor == got[0].anchor {
					n++
				}
				inputs = append(inputs, &mergeInput{src: s, anchor: got[0].anchor, entries: got[:n]})
				got = got[n:]
			}
			continue
		}
		uris, err := src.sourceURIStrings(rt)
		if err != nil {
			return err
		}
		base := src.baseURI
		if base == "" {
			base = rt.ctx.StaticBaseURI()
		}
		for a, u := range uris {
			d, err := mergeDocOf(rt, u, base, unread)
			if err != nil {
				return err
			}
			d.in = &mergeInput{src: s, anchor: a}
			d.split = src.split
			root := xdm.NewTree().Root
			d.ctx = rt.withCurrent(root, a+1, len(uris)).ctx
			inputs = append(inputs, d.in)
			docs = append(docs, d)
		}
	}
	streamtrace.Run(streamtrace.Merge)
	var open func(j int) error
	open = func(j int) error {
		if j == len(docs) {
			return i.merge(rt, out, inputs, resolved, colls)
		}
		d := docs[j]
		return d.br.streamDoc(rt.ctx, d.href, d.base, func(c *xdmstream.Cursor) error {
			if d.strip != nil {
				c.StripWhitespace(d.strip)
			}
			c.Handoff(func() func() { return xpathleaf.LockYield(rt.evalLock) })
			in := d.in
			// The coroutine and the merge take turns evaluating: each lets
			// the EvalLock go as its turn ends (see LockYield).
			lock := func() func() { return xpathleaf.LockYield(rt.evalLock) }
			next, stop := iter.Pull(func(yield func(xdm.Item) bool) {
				defer lock()
				err := d.split.loopAt(d.ctx, c, 0, nil, func(it xdm.Item) error {
					back := lock()
					ok := yield(it)
					back()
					if !ok {
						return errMergeStop
					}
					return nil
				}, nil)
				if err != nil && err != errMergeStop {
					in.err = err
				}
			})
			in.next = func() (xdm.Item, bool) {
				back := lock()
				defer back()
				return next()
			}
			defer func() {
				back := lock()
				stop()
				back()
			}()
			return open(j + 1)
		})
	}
	return open(0)
}

// mergeDocOf finds how to stream href: through the resolvers Transform
// wraps around the caller's, as executeStream looks through them (the
// document recorded as read, for XTDE1500, and its whitespace stripped);
// errMergeTree when it cannot be streamed.
func mergeDocOf(rt *runtime, href, base string, unread *[]func()) (*mergeDoc, error) {
	if validSourceDocumentURI(href) != nil || strings.ContainsRune(href, '#') {
		return nil, errMergeTree
	}
	pkg := packageOf(rt.ctx)
	var (
		rd    *readDocResolver
		br    burstResolver
		strip func(*xdm.Node) bool
	)
	for d := rt.ctx.Env().Docs; br == nil; {
		switch v := d.(type) {
		case *readDocResolver:
			if _, seen := v.docs[docKey{href, base, pkg}]; seen {
				return nil, errMergeTree
			}
			rd, d = v, v.inner
		case *stripSpaceResolver:
			strip, d = v.sheet.whitespaceStripper(pkg), v.inner
		case burstResolver:
			br = v
		default:
			return nil, errMergeTree
		}
	}
	docURI, ok := br.wantsBursts(href, base)
	if !ok {
		return nil, errMergeTree
	}
	if rd != nil && docURI != "" {
		if !rd.read[docURI] {
			*unread = append(*unread, func() { delete(rd.read, docURI) })
		}
		rd.read[docURI] = true
		if rd.written[docURI] {
			return nil, fmt.Errorf(
				"XTDE1500: this transformation reads %q, which xsl:result-document "+
					"has already written", docURI)
		}
	}
	return &mergeDoc{br: br, href: href, base: base, strip: strip}, nil
}

// advance moves in to its next entry (head nil at its end), checking that
// it follows the one before in merge key order (XTDE2220).
func (in *mergeInput) advance(rt *runtime, keys []*sortKey, colls []xpath.Collation) error {
	in.head = nil
	var e mergeEntry
	switch {
	case in.next == nil:
		if in.pos == len(in.entries) {
			return nil
		}
		e = in.entries[in.pos]
	default:
		it, ok := in.next()
		if !ok {
			return in.err
		}
		var err error
		if e, err = mergeEntryOf(rt, snapshotItem(it), keys, colls, in.src, in.anchor, in.pos); err != nil {
			return err
		}
		if in.pos > 0 {
			if err := checkMergeInputSorted([]mergeEntry{in.prev, e}, keys); err != nil {
				return err
			}
		}
		in.prev = e
	}
	in.pos++
	if in.typed == nil {
		in.typed, in.untyped = make([]*xdm.Atomic, len(keys)), make([]*xdm.Atomic, len(keys))
	}
	for k, a := range e.atoms {
		switch {
		case a == nil:
		case isUntyped(a) && in.untyped[k] == nil:
			in.untyped[k] = a
		case !isUntyped(a) && in.typed[k] == nil:
			in.typed[k] = a
		}
	}
	in.head = &e
	return nil
}

// take returns the item of in's head for the current merge group. The
// order of nodes in different trees is the order the trees were made in,
// and the tree makes every snapshot of source 1 before any of source 2,
// anchor by anchor: so a streamed input's item, read as a snapshot when it
// became the head (its keys are computed on it), is made again here, as the
// group is formed input by input in that order. (A collected input, made
// before any streamed one, is the tree's as it is: mergeStreams.)
func (in *mergeInput) take() xdm.Item {
	if in.next == nil {
		return in.head.item
	}
	return snapshotItem(in.head.item)
}

// merge runs the action on each group of inputs, taken least key first.
func (i *mergeInstr) merge(rt *runtime, out *outputBuilder, inputs []*mergeInput,
	resolved [][]*sortKey, colls [][]xpath.Collation) error {

	for _, in := range inputs {
		if err := in.advance(rt, resolved[in.src], colls[in.src]); err != nil {
			return err
		}
	}
	order := resolved[0]
	// less compares two heads as the tree's sort does.
	less := func(a, b *mergeEntry) (bool, error) {
		for k := range a.keys {
			if k >= len(b.keys) {
				break
			}
			cmp := compareSortValues(a.keys[k], b.keys[k])
			if cmp == sortIncomparable {
				return false, fmt.Errorf(
					"XTTE2230: merge key values %s and %s cannot be "+
						"compared with the le operator",
					a.keys[k].typeName(), b.keys[k].typeName())
			}
			if cmp == 0 {
				continue
			}
			if order[k].order == "descending" {
				cmp = -cmp
			}
			return cmp < 0, nil
		}
		return false, nil
	}
	names := make([]string, len(i.sources))
	named := make([]bool, len(i.sources))
	nameSeq := make(xdm.Sequence, 0, len(i.sources))
	for s, src := range i.sources {
		names[s], named[s] = src.name, src.named
		if src.named {
			nameSeq = append(nameSeq, xdm.NewString(src.name))
		}
	}
	for g := 0; ; g++ {
		var m *mergeInput
		for _, in := range inputs {
			if in.head == nil {
				continue
			}
			if m == nil {
				m = in
				continue
			}
			l, err := less(in.head, m.head)
			if err != nil {
				return err
			}
			if l {
				m = in
			}
		}
		if m == nil {
			break
		}
		first := *m.head
		binding := &mergeGroupBinding{names: names, named: named, items: make([]xdm.Sequence, len(i.sources))}
		for _, in := range inputs {
			for in.head != nil && mergeKeysEqual(first.keys, in.head.keys) {
				binding.items[in.src] = append(binding.items[in.src], in.take())
				if err := in.advance(rt, resolved[in.src], colls[in.src]); err != nil {
					return err
				}
			}
		}
		all := binding.all()
		sub := rt.withCurrent(all[0], g+1, 0)
		sub = sub.withVar(currentMergeGroupVar, xdm.One(&xdm.Opaque{Value: binding}))
		sub = sub.withVar(currentMergeKeyVar, mergeKeySequence(first.keys))
		sub = sub.withVar(currentMergeSourcesVar, nameSeq)
		if err := execSequence(i.action, sub, out); err != nil {
			return err
		}
	}
	// XTTE2230 across sources, on the samples the tree would take: per
	// source, the first of each kind in anchor order.
	var samples []mergeEntry
	for _, in := range inputs {
		for k := range in.typed {
			for _, a := range []*xdm.Atomic{in.typed[k], in.untyped[k]} {
				if a != nil {
					atoms := make([]*xdm.Atomic, len(in.typed))
					atoms[k] = a
					samples = append(samples, mergeEntry{atoms: atoms, src: in.src, anchor: in.anchor})
				}
			}
		}
	}
	return checkMergeKeysComparable(samples, len(i.sources))
}
