package xslt

// Populations (lane G2): what an xsl:for-each or xsl:iterate hands its
// body, beyond a bare path of the stream.
//
// A loop's select is normalised into the split's walk (streamPop): one
// stream part -- a path of the stream, its matches handed over as the bare
// loop hands them -- with motionless parts around it (($extra, P),
// (100, 101, P), (P, $v/@a)), windows over its matches (head, tail,
// subsequence, remove, unordered, one-or-more, insert-before), a function
// of each match that makes it a complete node or a value (copy-of(P),
// data(P/@a)), attributes as the matches' tail (P/@a), or a motionless
// choice between two populations (if ($c) then () else P). The motionless
// parts and the windows' arguments are evaluated first, as the tree
// evaluates the whole select before the body; then the parts are handed
// over in order. A select that gives only values (if (P) then 0 else 1,
// data(P/@a), 101) is read off the cursor as a select is, and the loop runs
// over its values (values).

import (
	"math"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// leafEmptySelect reports whether el, an xsl:apply-templates in the body of
// a rule run on a text, comment, processing-instruction or attribute node
// (focus vLeaf: a complete node, off the cursor), selects nothing there: its
// select is absent (child::node()) or one child or attribute step with no
// predicate. Such a node has neither children nor attributes, so the
// instruction runs as the tree runs it, over the empty sequence -- the
// identity template's <xsl:apply-templates select="@*"/> and
// <xsl:apply-templates/> on a text node.
func leafEmptySelect(el *xdm.Node) bool {
	src := el.AttrValue("select")
	if el.Attr("", "select") == nil {
		return true
	}
	st, ok := oneStep(parseAt(el, src)).(*xpath.Step)
	return ok && len(st.Predicates) == 0 && (st.Axis == xpath.AxisChild || st.Axis == xpath.AxisAttribute)
}

// oneStep is e, or the step a relative path of one step is ("node()" parses
// as such a path).
func oneStep(e xpath.Expr) xpath.Expr {
	if pe, ok := e.(*xpath.PathExpr); ok && !pe.Root && len(pe.Steps) == 1 {
		return pe.Steps[0]
	}
	return e
}

// streamPop is a loop's population: parts in order, one of them (at) the
// stream part; or a choice between then and els on cond; or values.
type streamPop struct {
	cond      *xpath.Compiled
	then, els *streamPop
	parts     []*xpath.Compiled // nil at the stream part
	at        int               // index of the stream part, -1 for none
	wins      []popWin          // over the stream part's matches, innermost first
	fn        *xpath.Compiled   // applied to each match ($splitItems), or nil
	ground    bool              // the matches are handed as grounded copies
	off       bool              // the items are not the matches' views: the body runs off the cursor
	values    bool              // the whole select is read as a select's, its values looped over
	oneOrMore *xpath.Compiled   // one-or-more(()): raised when nothing was handed
	// cut, for insert-before($t, n, P), is where $t (parts 0 and 2) is cut.
	cut *popWin
	// split, in a branch of a choice whose branches both read the stream,
	// is the branch's own (nil: the op's); union is F union () for
	// F union P, F's nodes merged into the matches by order (streampop_l6.go).
	split *streamSplit
	union *xpath.Compiled
}

// popWin is a window over the stream part's matches: of kind head, tail,
// sub (positions from a to a+b, b absent: on), remove (all but position a),
// or ins (b's items before position a). check is the function called on ()
// with the window's arguments: its errors are the tree's argument errors.
// The bounds are the tree's own (xpath/fn_seq.go): subsequence's arguments
// as doubles (number()) rounded as its roundHalfEven does (floor(f + 0.5),
// whatever its name); remove's and insert-before's
// position clamped as clampPosition does -- truncated when it fits an
// int64, before the first when negative, past the end otherwise.
type popWin struct {
	kind       string
	a, b       *xpath.Compiled
	anum, bnum *xpath.Compiled // number(a), number(b)
	check      *xpath.Compiled
}

// popBuilder builds a population from a select's expression.
type popBuilder struct {
	c          *compiler
	sel        *xpath.Compiled
	ns         xpath.NamespaceResolver
	doc        bool
	group, key bool
	focus      vfocus
	split      *streamSplit // the stream part's, once found
	why        string
	el         *xdm.Node // the instruction (streampop_l6.go: fresh operands)
}

func (pb *popBuilder) part(x xpath.Expr) *xpath.Compiled {
	return xpathleaf.WithExpr(pb.sel, x).(*xpath.Compiled)
}

// motionless reports whether x reads of the loop's focus only what it has.
func (pb *popBuilder) motionless(x xpath.Expr) bool {
	return (viewCheck{key: pb.key}).safe(x, pb.focus, true)
}

// popFor normalises the select of a loop op whose split is not a bare path,
// or says why not.
func (c *compiler) popFor(op *streamOp, ns xpath.NamespaceResolver) (*streamPop, *streamSplit, string) {
	e := reparse(op.sel, ns)
	if e == nil {
		return nil, nil, "select does not parse again"
	}
	pb := &popBuilder{c: c, sel: op.sel, ns: ns, doc: op.doc, group: op.inGroup, key: op.key, focus: vElem, el: op.el}
	if op.doc {
		pb.focus = vNone
	}
	if p := pb.pop(withChecks(foreignSets(c.inlineCalls(e)))); p != nil && pb.split != nil {
		return p, pb.split, ""
	}
	if pb.why == "" {
		pb.why = "loop select is not a bare path"
	}
	if s := op.split; s.valuesOnly() {
		return &streamPop{values: true, off: true}, s, ""
	}
	return nil, nil, pb.why
}

// valuesOnly reports whether what s gives holds no node of the stream: a
// fold, atomized items or string values, in every branch of a choice.
func (s *streamSplit) valuesOnly() bool {
	if s.cond != nil {
		return s.then.valuesOnly() && s.els.valuesOnly()
	}
	return s.steps == nil && !s.ctxItem || s.red != "" && s.red != "nth" || s.atomize || s.tailText != ""
}

// pop builds the population e, or returns nil.
func (pb *popBuilder) pop(e xpath.Expr) *streamPop {
	if y := flatArray(e); y != nil { // streampop_l6.go
		return pb.pop(y)
	}
	switch x := e.(type) {
	case *xpath.IfExpr:
		if !pb.motionless(x.Cond) {
			return nil
		}
		if p := pb.choiceOfStreams(x); p != nil { // streampop_l6.go
			return p
		}
		t, f := pb.pop(x.Then), pb.pop(x.Else)
		if t == nil || f == nil {
			return nil
		}
		return &streamPop{cond: pb.part(x.Cond), then: t, els: f, off: t.off || f.off}
	case *xpath.BinaryOp:
		if p := pb.unionPop(x); p != nil { // streampop_l6.go
			return p
		}
	case *xpath.SequenceExpr:
		p := &streamPop{at: -1}
		for _, it := range x.Items {
			if pb.motionless(it) {
				p.parts = append(p.parts, pb.part(it))
				continue
			}
			if p.at >= 0 || !pb.stream(p, it) {
				return nil
			}
			p.at = len(p.parts)
			p.parts = append(p.parts, nil)
		}
		return p
	case *xpath.FuncCall:
		if isFn(x, "insert-before", 3) && pb.motionless(x.Args[0]) && pb.motionless(x.Args[1]) && !pb.motionless(x.Args[2]) {
			// insert-before($t, n, P): $t's items before n, P, the rest.
			p := &streamPop{at: 1, parts: []*xpath.Compiled{nil, nil, nil}}
			if !pb.stream(p, x.Args[2]) {
				return nil
			}
			n := x.Args[1]
			// Its argument errors first: insert-before itself, over nothing;
			// then $t, cut where insert-before would put P (read).
			p.wins = append(p.wins, popWin{kind: "check", check: pb.part(&xpath.FuncCall{Name: x.Name,
				Args: []xpath.Expr{x.Args[0], n, &xpath.SequenceExpr{}}})})
			p.cut = &popWin{kind: "ins", a: pb.part(n), anum: pb.part(fnCall("number", n)), b: pb.part(&xpath.SequenceExpr{}),
				check: pb.part(&xpath.SequenceExpr{})}
			p.parts[0] = pb.part(x.Args[0])
			p.parts[2] = p.parts[0]
			return p
		}
	}
	if pb.motionless(e) {
		return &streamPop{at: -1, parts: []*xpath.Compiled{pb.part(e)}}
	}
	p := &streamPop{at: 0, parts: []*xpath.Compiled{nil}}
	if !pb.stream(p, e) {
		return nil
	}
	return p
}

func isFn(x *xpath.FuncCall, name string, n int) bool {
	return x.Name.URI == fnNS && x.Name.Local == name && len(x.Args) == n
}

// stream makes e p's stream part: windows and a function over a path.
func (pb *popBuilder) stream(p *streamPop, e xpath.Expr) bool {
	if x, ok := e.(*xpath.FuncCall); ok && x.Name.URI == fnNS {
		args := x.Args
		win := func(kind string, a, b xpath.Expr) bool {
			for _, v := range []xpath.Expr{a, b} {
				if v != nil && !pb.motionless(v) {
					return false
				}
			}
			if !pb.stream(p, args[0]) {
				return false
			}
			w := popWin{kind: kind}
			ck := []xpath.Expr{&xpath.SequenceExpr{}}
			if a != nil {
				w.a = pb.part(a)
				ck = append(ck, a)
			}
			if b != nil {
				w.b = pb.part(b)
				ck = append(ck, b)
			}
			if kind == "ins" {
				ck = []xpath.Expr{&xpath.SequenceExpr{}, a, b}
			}
			if a != nil {
				w.anum = pb.part(fnCall("number", a))
			}
			if b != nil && kind == "sub" {
				w.bnum = pb.part(fnCall("number", b))
			}
			w.check = pb.part(&xpath.FuncCall{Name: x.Name, Args: ck})
			p.wins = append(p.wins, w)
			return true
		}
		switch {
		case isFn(x, "head", 1):
			return win("head", nil, nil)
		case isFn(x, "tail", 1):
			return win("tail", nil, nil)
		case isFn(x, "subsequence", 2):
			return win("sub", args[1], nil)
		case isFn(x, "subsequence", 3):
			return win("sub", args[1], args[2])
		case isFn(x, "remove", 2):
			return win("remove", args[1], nil)
		case isFn(x, "insert-before", 3) && pb.motionless(args[2]):
			return win("ins", args[1], args[2])
		case isFn(x, "unordered", 1):
			return pb.stream(p, args[0])
		case isFn(x, "one-or-more", 1) && p.oneOrMore == nil:
			p.oneOrMore = pb.part(&xpath.FuncCall{Name: x.Name, Args: []xpath.Expr{&xpath.SequenceExpr{}}})
			return pb.stream(p, args[0])
		case isFn(x, "snapshot", 1) && p.fn == nil && len(p.wins) == 0:
			return pb.snapshot(p, x) // streampop_snapshot.go
		case (isFn(x, "copy-of", 1) || isFn(x, "data", 1)) && p.fn == nil && len(p.wins) == 0:
			p.fn = pb.part(&xpath.FuncCall{Name: x.Name, Args: []xpath.Expr{&xpath.VarRef{Name: splitItems}}})
			if !pb.stream(p, args[0]) {
				return false
			}
			return x.Name.Local == "data" && p.off || x.Name.Local == "copy-of" && p.ground
		}
	}
	if pb.split != nil {
		return false
	}
	if x, ok := snapshotTail(e); ok { // streampop_snapshot.go
		return pb.snapshot(p, x)
	}
	if x, ok := copyOfTail(e); ok { // streamsplit_l6.go
		return pb.stream(p, x)
	}
	if x := rangeWindow(e); x != nil { // streampop_snapshot.go
		return pb.stream(p, x)
	}
	// The expression itself, taken apart (splitFor would parse the whole
	// select again from its source).
	s, why := splitStreamIn(e, pb.sel, pb.doc, pb.group, pb.key)
	if s == nil {
		pb.why = "split: " + why
		return false
	}
	switch {
	case s.bare() && p.fn != nil && s.leafOnly && snapshotOf(p.fn): // streampop_snapshot.go
		p.off = true
	case s.bare() && p.fn != nil:
		// copy-of(P), snapshot(P): each match grounded, then copied.
		_, elem := testKinds(s.steps[len(s.steps)-1].test)
		if !elem || s.leaves {
			return false
		}
		p.ground, p.off = true, true
	case s.bare():
	case s.cond == nil && !s.ctxItem && s.red == "" && s.mode == matchView && s.tail != nil && s.attr == nil &&
		isVarRef(s.outer.Expr(), splitItems) && endsInAttribute(s.tail.Expr()) && ownAttrTail(s.tail.Expr()):
		p.off = true // P/@a: the attributes of each match's view
	default:
		return false
	}
	pb.split = s
	return true
}

// ownAttrTail reports whether a tail is one attribute step of the match.
func ownAttrTail(e xpath.Expr) bool {
	st, ok := oneStep(e).(*xpath.Step)
	return ok && st.Axis == xpath.AxisAttribute
}

// readyPop readies a loop op with a population that runs its body off the
// cursor (p.off): the split keeps the mode it was made with, or grounds
// each match; ok says it is readied.
func (x *streamExec) readyPop(op *streamOp, ctxD, k int, kinds *[streamtrace.NumKinds]int64) (string, bool) {
	p := op.pop
	if p == nil || !p.off {
		return "", false
	}
	if p.values {
		if why := x.readySelect(op, ctxD); why != "" {
			return why, true
		}
		kinds[streamtrace.KindForEach]++
		return "", true
	}
	if p.ground {
		if k < 1 {
			return "loop body: neither streamed nor grounded (a match may be the document node)", true
		}
		op.split.mode = matchGround
		kinds[streamtrace.KindGround]++
	}
	kinds[streamtrace.KindForEach]++
	return "", true
}

// popRun is a window's state in one run: the matches it has seen, its
// bounds, and for an insertion its items and whether they went out.
type popRun struct {
	w      *popWin
	n      float64
	lo, hi float64
	ins    xdm.Sequence
	done   bool
}

// ready evaluates the window's arguments: the check first, whose errors are
// the function's own.
func (r *popRun) ready(w *popWin, ctx *xpath.Context) error {
	r.w, r.lo, r.hi = w, math.Inf(-1), math.Inf(1)
	if _, err := w.check.Eval(ctx); err != nil {
		return err
	}
	one := func(e *xpath.Compiled) (*xdm.Atomic, error) {
		seq, err := e.Eval(ctx)
		if err != nil {
			return nil, err
		}
		a, ok := firstAtomic(xdm.Atomize(seq))
		if !ok {
			return nil, errStreamPlan // the check raised what the tree raises
		}
		return a, nil
	}
	switch w.kind {
	case "sub":
		a, err := one(w.anum)
		if err != nil {
			return err
		}
		r.lo = treeRound(a.Float64())
		if w.bnum != nil {
			b, err := one(w.bnum)
			if err != nil {
				return err
			}
			r.hi = r.lo + treeRound(b.Float64())
		}
		if math.IsNaN(r.lo) || math.IsNaN(r.hi) {
			r.lo, r.hi = math.Inf(1), math.Inf(1) // nothing
		}
	case "remove", "ins":
		a, err := one(w.a)
		if err != nil {
			return err
		}
		if a.Type == xdm.TypeUntypedAtomic {
			if a, err = one(w.anum); err != nil {
				return err
			}
		}
		// clampPosition's rules (xpath/fn_seq.go).
		switch {
		case a.FitsInt64():
			r.lo = float64(a.Int64())
		case a.Rat() != nil && a.Rat().Sign() < 0:
			r.lo = 0
		default:
			r.lo = math.Inf(1)
		}
		if w.kind == "ins" {
			r.lo = math.Max(r.lo, 1)
			r.ins, err = w.b.Eval(ctx)
			return err
		}
	}
	return nil
}

func firstAtomic(seq xdm.Sequence) (*xdm.Atomic, bool) {
	if len(seq) != 1 {
		return nil, false
	}
	a, ok := seq[0].(*xdm.Atomic)
	return a, ok
}

// pass reports whether the next match passes the window; an insertion at
// its position is handed to each first.
func (r *popRun) pass(each func(xdm.Item) error) (bool, error) {
	r.n++
	switch r.w.kind {
	case "head":
		return r.n == 1, nil
	case "tail":
		return r.n > 1, nil
	case "sub":
		return r.n >= r.lo && r.n < r.hi, nil // NaN bounds: never
	case "remove":
		return r.n != r.lo, nil
	}
	if !r.done && r.n == r.lo {
		r.done = true
		for _, it := range r.ins {
			if err := each(it); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// end hands an insertion that no position reached, after the matches.
func (r *popRun) end(each func(xdm.Item) error) error {
	if r.w.kind != "ins" || r.done {
		return nil
	}
	r.done = true
	for _, it := range r.ins {
		if err := each(it); err != nil {
			return err
		}
	}
	return nil
}

// read hands the population's items to each, as a loop feed's read does:
// the stream part's matches through stream (the loop's own walk, which asks
// gate whether a match is handed at all, at its start tag), every other
// item with the body off the cursor.
func (p *streamPop) read(run *streamRun, rt *runtime, op *streamOp, each func(xdm.Item) error,
	stream func(item func(xdm.Item) error, gate func() (bool, error)) error) error {

	offEach := func(it xdm.Item) error {
		off := run.off
		run.off = true
		err := each(it)
		run.off = off
		return err
	}
	if p.values {
		seq, err := run.evalOp(rt, op)
		if err != nil {
			return err
		}
		for _, it := range seq {
			if err := offEach(it); err != nil {
				return err
			}
		}
		return nil
	}
	for p.cond != nil {
		seq, err := p.cond.Eval(rt.ctx)
		if err != nil {
			return err
		}
		ok, err := xpath.EffectiveBooleanValue(seq)
		if err != nil {
			return err
		}
		if ok {
			p = p.then
		} else {
			p = p.els
		}
	}
	if p.split != nil { // streampop_l6.go
		prev := run.popSplit
		run.popSplit = p.split
		defer func() { run.popSplit = prev }()
	}
	// The motionless parts and the windows' arguments first, as the tree
	// evaluates the whole select before the body.
	vals := make([]xdm.Sequence, len(p.parts))
	for i, e := range p.parts {
		if e == nil {
			continue
		}
		seq, err := e.Eval(rt.ctx)
		if err != nil {
			return err
		}
		vals[i] = seq
	}
	wins := make([]popRun, len(p.wins))
	for i := range p.wins {
		if err := wins[i].ready(&p.wins[i], rt.ctx); err != nil {
			return err
		}
	}
	if p.cut != nil {
		var c popRun
		if err := c.ready(p.cut, rt.ctx); err != nil {
			return err
		}
		t := vals[0]
		at := int(min(c.lo, float64(len(t)+1))) // c.lo >= 1
		vals[0], vals[2] = t[:at-1], t[at-1:]
	}
	handed := false
	merge, err := p.unionMerge(run, rt, offEach) // streampop_l6.go
	if err != nil {
		return err
	}
	gate := func() (bool, error) {
		if err := merge.atCursor(); err != nil {
			return false, err
		}
		for j := range wins {
			ok, err := wins[j].pass(offEach)
			if err != nil || !ok {
				return false, err
			}
		}
		handed = true
		return true, nil
	}
	item := func(it xdm.Item) error {
		if err := merge.before(it); err != nil {
			return err
		}
		if ok, err := gate(); err != nil || !ok {
			return err
		}
		if p.fn == nil {
			return offEach(it)
		}
		seq, err := p.fn.Eval(rt.ctx.WithVar(splitItems, xdm.One(it)))
		if err != nil {
			return err
		}
		for _, v := range seq {
			if err := offEach(v); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range p.parts {
		if i != p.at {
			for _, it := range vals[i] {
				handed = true
				if err := offEach(it); err != nil {
					return err
				}
			}
			continue
		}
		if err := stream(item, gate); err != nil {
			return err
		}
		if err := merge.rest(); err != nil {
			return err
		}
		for j := range wins {
			if err := wins[j].end(offEach); err != nil {
				return err
			}
		}
	}
	if !handed && p.oneOrMore != nil {
		_, err := p.oneOrMore.Eval(rt.ctx)
		return err
	}
	return nil
}

// popFeed is the feed of a loop op with a population: the loop feed's
// reading of its split (streamexec.go), the matches gated by the windows.
func (run *streamRun) popFeed(instr Instruction, rt *runtime, op *streamOp, g *groupAt) *burstFeed {
	return &burstFeed{instr: instr, read: func(each func(xdm.Item) error) error {
		if run.used {
			return errStreamPlan
		}
		return op.pop.read(run, rt, op, each, func(item func(xdm.Item) error, gate func() (bool, error)) error {
			run.used = true
			d := run.d
			s := run.splitOf(op) // streampop_l6.go
			if s.mode == matchBody {
				return s.loopAt(rt.ctx, run.c, d, g, nil, func(v *xdm.Node, md int) (bool, error) {
					if ok, err := gate(); err != nil || !ok {
						return false, err
					}
					if v.Kind() != xdm.KindElement {
						// A kept leaf's copy, complete: off the cursor, as
						// the bare loop hands it (streamleaf.go, G4).
						return true, run.offEach(each, v)
					}
					run.d, run.used = md, false
					err := each(v)
					read := run.used
					run.d, run.used = d, true
					return read, err
				})
			}
			return s.loopAt(rt.ctx, run.c, d, g, item, nil)
		})
	}}
}

// windowsOnly reports whether p is its stream part's matches through
// windows and nothing else: what an xsl:apply-templates may select
// (streamapply.go dispatches only matches of the stream).
func (p *streamPop) windowsOnly() bool {
	if p.cond != nil || p.values || p.off || p.fn != nil || p.at != 0 || len(p.parts) != 1 {
		return false
	}
	for _, w := range p.wins {
		if w.kind == "ins" || w.kind == "check" {
			return false // an insertion hands items that are not matches
		}
	}
	return true
}

// gates readies p's windows (their argument errors first) and returns the
// gate each match passes, and the check to run once the matches are read.
func (p *streamPop) gates(ctx *xpath.Context) (gate func() (bool, error), done func() error, err error) {
	wins := make([]popRun, len(p.wins))
	for i := range p.wins {
		if err := wins[i].ready(&p.wins[i], ctx); err != nil {
			return nil, nil, err
		}
	}
	handed := false
	gate = func() (bool, error) {
		for j := range wins {
			if ok, err := wins[j].pass(nil); err != nil || !ok {
				return false, err
			}
		}
		handed = true
		return true, nil
	}
	done = func() error {
		if !handed && p.oneOrMore != nil {
			_, err := p.oneOrMore.Eval(ctx)
			return err
		}
		return nil
	}
	return gate, done, nil
}

// treeRound is xpath's roundHalfEven, which subsequence uses: floor(f + 0.5),
// NaN and infinities as they are.
func treeRound(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	return math.Floor(f + 0.5)
}
