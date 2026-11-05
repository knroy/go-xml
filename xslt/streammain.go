package xslt

// Streamed principal input (full streaming, component C7; DESIGN section
// 1.7, "Streamed main input").
//
// TransformReader streams the document it reads when the transform starts
// by applying templates to it in a mode declared streamable (section 6.6.4):
// the document node's stand-in is dispatched in that mode on a cursor over
// the reader, exactly as an apply op of a streamed xsl:source-document
// hands on its context (streamapply.go), and the rules and built-in rules
// read the rest as the cursor reaches it.
//
// What a streamed xsl:source-document does on a failure, re-run on the
// tree, is not open here: the reader is read once. So everything that would
// send a run to the tree is refused when the stylesheet compiles, per mode
// (planMainStreams, linkMain), and the run itself has nothing to fall back
// to:
//
//   - the global context item is the tree's document node, which a global
//     variable may read in any order (section 5.3.3 makes it grounded): a
//     global whose value may depend on it, an xsl:global-context-item with
//     a type, and
//     an accumulator are refused, as are input-type-annotations="strip"
//     and a stylesheet of more than one module (streamtrust.go);
//   - a node of the stream at the top of the principal result (a built-in
//     rule's copy of a leaf or an attribute, a deep-copy's grounded element,
//     an attribute rule's): Result.Nodes would hold a copy where the tree's
//     holds the source's own node, which a source-document run sends to the
//     tree; refused by linkMain, and caught at run time if it slips through.
//     So is one at the top of an xsl:result-document's body, which
//     SecondaryResult.Nodes holds as it is (keptStreamNode): a built-in's
//     attribute or deep copy there (a leaf's copy is a new node, as in a
//     container), and an xsl:sequence (resultKeeps);
//   - a select at the document node that gives up (errSplitGiveUp), which a
//     source-document run evaluates on its tree: refused by linkMain.
//
// The errors of the run are the tree's as they come: the run evaluates what
// the tree does, in the same order, with the same charges. One order is
// not the tree's: xdm.Parse reports a malformed document before the
// transform starts, the stream when it gets there. So on any error the rest
// of the document is read, and a parse error found is the result, as
// Parse + Transform has it. A posture fault, or a run the plan does not
// cover, is a defect of the analysis or of the plans (0 in the
// differentials): it is reported as an internal error, there being no tree
// to give the answer instead (docs/known-gaps.md).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

// traceMain, when set, is told why each streamable mode of a stylesheet
// cannot stream a principal input ("" when it can), for the coverage tests.
var traceMain func(mode, why string)

// planMainStreams records in s.mainStreams the modes of the stylesheet a
// principal input can be streamed in, once it has compiled.
func (c *compiler) planMainStreams() {
	s := c.sheet
	if s.source == nil {
		return
	}
	els := s.source.ChildElements()
	if len(els) == 0 {
		return
	}
	rs := c.rulesPlanFor(els[0])
	if rs == nil {
		return
	}
	why := s.mainRefusal()
	for _, m := range slices.Sorted(maps.Keys(rs.modes)) {
		w := why
		if w == "" {
			w = s.mainAccumRefusal(m)
		}
		if w == "" {
			w = s.linkMain(m, rs)
		}
		if w == "" {
			if s.mainStreams == nil {
				s.mainStreams = map[string]bool{}
			}
			s.mainStreams[m] = true
		}
		if traceMain != nil {
			traceMain(m, w)
		}
	}
}

// mainRefusal says why nothing of the stylesheet outside its rules may read
// a streamed principal input, or "".
func (s *Stylesheet) mainRefusal() string {
	switch {
	case s.globalContextItem != nil && s.globalContextItem.decl.as != nil:
		// With no type, the declaration's check reads nothing of the
		// document node, the stand-in's answer is the tree's; the globals
		// that might read it are refused below.
		return "xsl:global-context-item"
	case s.stripTypeAnnotations:
		return "input-type-annotations"
	}
	// The globals read no focus: TransformReader makes the global context
	// item absent for a streamable initial mode (streamglobal_l7.go).
	return ""
}

// mayGiveUp reports whether a select of x, a plan at the document node,
// may give up (errSplitGiveUp) for the tree to answer.
func (x *streamExec) mayGiveUp() bool {
	for _, op := range x.top {
		if op.apply == applyNone && !op.loop && op.split != nil && op.split.givesUp() && !rootEvents(op) && !rootGround(op) { // streamtee_docnode.go
			return true
		}
	}
	return false
}

// givesUp reports whether the split may give up: a min or max over values
// of several types, or a copy that would ground the document element.
func (s *streamSplit) givesUp() bool {
	if s.cond != nil {
		return s.then.givesUp() || s.els.givesUp()
	}
	return s.noRoot || s.hold && !s.oneMatch() || s.fold != nil && s.red != "sum" && s.red != "avg" && !s.oneTake()
}

// atTop reports whether what the instruction el writes may be an item of
// the result of the template rule it is in: no constructor holds it.
func atTop(el *xdm.Node) bool { return topOf(el) == "template" }

// atResultTop reports whether what el writes may be an item of the body of
// an xsl:result-document, which SecondaryResult.Nodes holds as it is.
func atResultTop(el *xdm.Node) bool { return topOf(el) == "result-document" }

// topOf names the sequence what el writes may be an item of: "template"
// (the rule's result), "result-document" (its body), or "" when a
// constructor holds it.
func topOf(el *xdm.Node) string {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if n.Name().URI != xdm.NSXSL {
			return "" // a literal result element
		}
		switch l := n.Name().Local; l {
		case "template", "result-document":
			return l
		case "element", "copy", "document", "attribute", "comment", "processing-instruction", "namespace",
			"value-of", "message", "variable", "param", "with-param":
			return ""
		}
	}
	return "template"
}

// resultKeeps says why a node of the stream may be an item of the body of
// an xsl:result-document in el, a template rule, or "": an instruction there
// that returns the items it selects as they are. A container re-runs on its
// tree when one is (keptStreamNode); a principal input has none, so
// linkMain refuses it. The built-in rules' copies are linkMain's top.
func resultKeeps(el *xdm.Node) string {
	for d := range el.Descendants() {
		if d.Kind() != xdm.KindElement || d.Name().URI != xdm.NSXSL {
			continue
		}
		switch d.Name().Local {
		case "sequence", "on-empty", "on-non-empty", "break", "on-completion":
			if atResultTop(d) {
				return elName(d) + " in xsl:result-document may return a node of the stream"
			}
		}
	}
	return ""
}

// streamsMain reports whether TransformReader streams its document under
// opts: an apply-templates of the document in a mode planMainStreams took.
func (s *Stylesheet) streamsMain(opts TransformOptions) bool {
	if s.mainStreams == nil || noBursts || streamtrace.Off() || opts.InitialTemplate != "" ||
		opts.InitialFunction.Local != "" || opts.InitialMatchSelection != nil {
		return false
	}
	m, err := s.initialModeOf(opts)
	return err == nil && s.mainStreams[m] && !s.dynParamSupplied(&opts) // streamtee_dyncall.go
}

// mainInput is a principal input TransformReader streams: the cursor over
// it, and the stand-in for its document node, which the transform takes for
// its source.
type mainInput struct {
	c    *xdmstream.Cursor
	root *xdm.Node
	acc  *streamAccums // its accumulators (streamaccum.go)
	// typed says it is validated as it is read (streamcopy_l3.go).
	typed bool
}

// transformStream is TransformReader streaming r.
func (s *Stylesheet) transformStream(ctx context.Context, r io.Reader, opts TransformOptions) (*Result, error) {
	po := opts.SourceParseOptions
	main := &mainInput{c: xdmstream.Open(r, po), root: xdm.NewTree().Root}
	defer main.c.Close()
	main.root.SetBaseURI(po.BaseURI)
	main.root.SetDocumentURI(po.DocumentURI)
	if len(s.strip) > 0 {
		main.c.StripWhitespace(s.whitespaceStripper(0))
	}
	valid := s.validateMain(opts.SourceValidation, main.c) // streamvalidin.go
	main.typed = opts.SourceValidation != 0 && s.schema != nil
	res, err := s.transform(ctx, nil, opts, main)
	if err == nil {
		if err := valid(); err != nil {
			return nil, err
		}
		return res, nil
	}
	// Parse + Transform reports a malformed document first. A cancelled
	// transform is not made to read on.
	if ctx == nil || ctx.Err() == nil {
		if perr := main.drain(); perr != nil {
			return nil, perr
		}
		if verr := valid(); verr != nil {
			return nil, verr
		}
		if aerr := main.acc.pending(); aerr != nil {
			return nil, aerr
		}
	}
	return nil, err
}

// drain reads the rest of the document, and returns its parse error.
func (m *mainInput) drain() error {
	for {
		ev, err := m.c.NextTag(&drainAll)
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case ev.Kind == xdmstream.EndDocument:
			return nil
		}
	}
}

// errMainKept is a node of the stream at the top of the principal result,
// which linkMain refuses.
var errMainKept = errors.New("a node of the stream at the top of the principal result")

// run is the initial apply-templates of the document node in mode, on the
// cursor, into out, the principal result. It reads the document to its
// end.
func (m *mainInput) run(rt *runtime, mode string, params, tunnels paramValues, out *outputBuilder) (err error) {
	defer func() {
		if p := recover(); p != nil {
			pe, ok := p.(*streamtrace.PostureError)
			if !ok {
				panic(p)
			}
			err = pe
		}
		if err != nil && isStreamDefect(err) {
			if !errors.As(err, new(*streamtrace.PostureError)) {
				streamtrace.Guard() // a posture fault counts itself
			}
			err = fmt.Errorf("streaming: internal error: %w", err)
		}
	}()
	m.c.Handoff(func() func() { return xpathleaf.LockYield(rt.evalLock) }) // streamfork.go
	streamtrace.Run(streamtrace.Main)
	streamtrace.Node(streamtrace.KindApply, 1)
	x := newStreamExec()
	x.class = classDoc
	run := &streamRun{x: x, c: m.c, root: m.root, held: out, typed: m.typed}
	noteInitialAccumulators(rt, mode, m.root)
	m.acc = run.observeAccums(rt, rt.treeAccums[m.root], false)
	defer m.acc.stop()
	sub := *rt
	sub.bursts = &burstFeed{run: run}
	if err := run.dispatch(&sub, m.root, mode, params, tunnels, out); err != nil {
		return err
	}
	if run.kept || keepsStreamNode(out.Sequence()) {
		return errMainKept
	}
	if err := m.drain(); err != nil {
		return err
	}
	return m.acc.pending()
}

// isStreamDefect reports whether err is a fault of the plans, not an error
// of the document or the stylesheet: a tree-mode run would not raise it.
func isStreamDefect(err error) bool {
	var pe *streamtrace.PostureError
	return errors.As(err, &pe) || errors.Is(err, errStreamPlan) || errors.Is(err, errSplitGiveUp) ||
		errors.Is(err, errMainKept)
}
