package xslt

import (
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// A built-in rule on the parse's own record (lane J4).
//
// An element whose rule choice needs nothing but its name -- the mode's
// rule index (ruleindex.go) holds no rule that may match it or any of its
// attributes -- gets the built-in rule, whatever a view would say. Its
// view is then made for nothing: the shallow-copy, shallow-skip,
// deep-skip and text-only-copy rules read the element's name, namespaces,
// attributes and base URI, all of which the parse's record has, complete,
// at the start tag (the cursor's StartElement event), and read them only
// there, before the children are read. So the dispatch runs on that
// record, live: nothing of it is kept, the attributes are matched (to no
// rule) and copied by the output builder as the leaves already are.

// liveBuiltIn dispatches mode to el, the parse's record of the element at
// depth md, the cursor at its start tag, when no rule of mode may match el
// or its attributes and the mode's built-in rule reads only what el has;
// ok false (nothing done) otherwise. read is element's.
func (run *streamRun) liveBuiltIn(focus focuser, pos int, el *xdm.Node, md int, mode string,
	params, tunnels paramValues, out *outputBuilder) (read, ok bool, err error) {

	s := focus.disp.sheet
	switch s.modeNoMatch[mode] {
	case "shallow-copy", "copy", "shallow-skip", "deep-skip", "text-only-copy", "":
	default:
		return false, false, nil // deep-copy grounds, fail stops: the view's way
	}
	if l, ok := s.candidates(el, mode); !ok || len(l) > 0 {
		return false, false, nil
	}
	for i := range el.NumAttrs() {
		if l, ok := s.candidates(el.AttrAt(i), mode); !ok || len(l) > 0 {
			return false, false, nil
		}
	}
	// A skip or text-only-copy rule reads its runtime only to count the depth
	// and as the base of its children's focus (applyBuiltInChild): disp's
	// does, unless a warning names el.
	rt := focus.disp
	if nm := s.modeNoMatch[mode]; s.modeWarnNoMatch[mode] || nm == "shallow-copy" || nm == "copy" {
		rt = focus.at(el, pos, 0)
	}
	if err := s.checkModeTyped(el, mode); err != nil {
		return false, true, err
	}
	s.warnNoMatch(rt, el, mode)
	d, grp := run.d, run.grp
	run.d, run.used, run.grp = md, false, nil
	err = run.builtIn(rt, el, mode, params, tunnels, out)
	read = run.used
	run.d, run.used, run.grp = d, true, grp
	return read, true, err
}

// applyFeed is the feed of instr, the apply op op, made once per run: the
// feed is the same each time the op runs.
func (run *streamRun) applyFeed(instr Instruction, op *streamOp) *burstFeed {
	f := run.applyFeeds[op]
	if f == nil {
		if run.applyFeeds == nil {
			run.applyFeeds = map[*streamOp]*burstFeed{}
		}
		f = &burstFeed{instr: instr, run: run, apply: op}
		run.applyFeeds[op] = f
	}
	return f
}

// kidFilter is the filter children reads the children of an element at
// depth d with, made once per run and depth. Never changed once made.
func (run *streamRun) kidFilter(d int) *xdmstream.Filter {
	for len(run.kidFilters) <= d {
		run.kidFilters = append(run.kidFilters, nil)
	}
	if run.kidFilters[d] == nil {
		run.kidFilters[d] = &xdmstream.Filter{Leaves: true, Depth: d}
	}
	return run.kidFilters[d]
}

// dispatchAt is dispatch for node at position pos of a selection of size
// size (0: not known), f making its runtime: node is given one, the
// built-in rule's or its rule's, the focus and the selection in one copy
// of f's runtime (dispatch's rt and withSelection's copy were two).
func (run *streamRun) dispatchAt(f focuser, node *xdm.Node, pos, size int, mode string,
	params, tunnels paramValues, out *outputBuilder) error {

	s := f.disp.sheet
	if s.kidsModes[mode] && node.Kind() == xdm.KindElement {
		if done, err := run.kidsDispatch(f, node, pos, size, mode, params, tunnels, out); done { // streamkids_l6.go
			return err
		}
	}
	if err := s.checkModeTyped(node, mode); err != nil {
		return err
	}
	ctx := f.focusCtx(node, pos, size)
	t, next, err := s.findTemplateFrom(node, mode, ctx, 0)
	if err != nil {
		return err
	}
	if t == nil {
		rt := f.runtimeIn(ctx)
		s.warnNoMatch(rt, node, mode)
		return run.builtIn(rt, node, mode, params, tunnels, out)
	}
	sub := f.ruleRuntime(ctx, node, t, next, mode, params, tunnels)
	if err := s.warnMultipleMatch(sub, node, mode, t, next, sub.ctx); err != nil {
		return err
	}
	if err := s.failMultipleMatch(node, mode, t, next, sub.ctx); err != nil {
		return err
	}
	if !onCursor(node) {
		// Its body was checked to read only what the node has.
		return run.offCursor(func() error { return runTemplate(sub, t, params, tunnels, out) })
	}
	return run.rule(sub, t, node, params, tunnels, out)
}

// focusCtx is the context of f.at(n, pos, size): f's own when that is f's
// runtime unchanged (a built-in rule's in a mode with no rules).
func (f focuser) focusCtx(n *xdm.Node, pos, size int) *xpath.Context {
	if mr := f.disp.sheet.rules[f.mode]; f.builtIn && mr != nil && mr.empty {
		return f.disp.ctx
	}
	return withFocusCurrent(f.disp.ctx, n, pos, size)
}

// runtimeIn is f.at's runtime made of its context ctx (focusCtx).
func (f focuser) runtimeIn(ctx *xpath.Context) *runtime {
	if ctx == f.disp.ctx {
		return f.disp
	}
	rt := *f.disp
	rt.ctx = ctx
	return &rt
}

// ruleRuntime is the runtime the body of t, selected for node, runs in:
// f's runtime with ctx, node's focus in it (focusCtx), and t's selection
// (withSelection), in one copy. A rule's focus is always withCurrent's:
// under a built-in rule too, as a mode with rules has builtInFocus copy.
func (f focuser) ruleRuntime(ctx *xpath.Context, node *xdm.Node, t *Template, next int, mode string,
	params, tunnels paramValues) *runtime {

	sub := f.disp.withSel(selection{template: t, next: next, mode: mode,
		params: params, tunnels: tunnels, item: node})
	sub.ctx = ctx
	return sub
}

// bareMatch reports whether every rule of mode that may match n has a
// pattern with no predicate: one that is matched on n's kind, name and
// ancestors alone, with no context and no current() (Pattern.matches), so
// the focus it is matched in is not read.
func bareMatch(s *Stylesheet, n *xdm.Node, mode string) bool {
	l, ok := s.candidates(n, mode)
	if !ok {
		return false
	}
	for _, i := range l {
		if !s.templates[i].Match.predicateFree() {
			return false
		}
	}
	return true
}
