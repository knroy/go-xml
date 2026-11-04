package xslt

// has-children of the element a streamed rule runs on (lane L6).
//
// §19.8.9 makes fn:has-children motionless: whether an element has a child
// is known at its start tag once the next event is seen. A rule's
// motionless parts (xsl:attribute select="has-children(.)") and its
// pattern's predicates (*[has-children()]) run on the tree's code over the
// element's view, which has no content: has-children of it is a posture
// fault. Here the stream answers it: in a mode whose rules read it
// (Stylesheet.kidsModes), the dispatch of an element peeks at the event after
// its start tag (Cursor.Peek) before the pattern is matched, and records the
// answer on the view (xdm/streamkids.go), which fn:has-children reads. After
// a Peek the element can no longer be skipped, grounded or read as text: the
// link admits the mode only when no rule of it does either at its element
// (kidsLinkWhy), and a body that read nothing has the element read through
// to its end (kidsDispatch).

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// topKids reports whether e calls has-children() or has-children(.) of its
// own focus, and never has-children of anything else (an argument other
// than ".", a call inside a step's predicate or a map's right side, whose
// focus is another node).
func topKids(e xpath.Expr) bool {
	found, bad := false, false
	kidsWalk(e, true, &found, &bad)
	return found && !bad
}

func kidsWalk(e xpath.Expr, top bool, found, bad *bool) {
	switch x := e.(type) {
	case nil, *xpath.Literal, *xpath.VarRef, *xpath.ContextItem:
	case *xpath.FuncCall:
		if x.Name.URI == fnNS && x.Name.Local == "has-children" {
			if top && (len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0])) {
				*found = true
			} else {
				*bad = true
			}
		}
		for _, a := range x.Args {
			kidsWalk(a, top, found, bad)
		}
	case *xpath.Step:
		for _, p := range x.Predicates {
			kidsWalk(p, false, found, bad)
		}
	case *xpath.PathExpr:
		for i, s := range x.Steps {
			kidsWalk(s, top && i == 0 && !x.Root, found, bad)
		}
	case *xpath.FilterExpr:
		kidsWalk(x.Base, top, found, bad)
		for _, p := range x.Predicates {
			kidsWalk(p, false, found, bad)
		}
	case *xpath.SimpleMap:
		kidsWalk(x.Left, top, found, bad)
		kidsWalk(x.Right, false, found, bad)
	case *xpath.BinaryOp:
		kidsWalk(x.Left, top, found, bad)
		kidsWalk(x.Right, top, found, bad)
	case *xpath.StringConcat:
		kidsWalk(x.Left, top, found, bad)
		kidsWalk(x.Right, top, found, bad)
	case *xpath.UnaryOp:
		kidsWalk(x.Operand, top, found, bad)
	case *xpath.CastExpr:
		kidsWalk(x.Operand, top, found, bad)
	case *xpath.InstanceOfExpr:
		kidsWalk(x.Operand, top, found, bad)
	case *xpath.TreatExpr:
		kidsWalk(x.Operand, top, found, bad)
	case *xpath.IfExpr:
		kidsWalk(x.Cond, top, found, bad)
		kidsWalk(x.Then, top, found, bad)
		kidsWalk(x.Else, top, found, bad)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			kidsWalk(it, top, found, bad)
		}
	case *xpath.LetExpr:
		for _, b := range bindingExprs(x.Bindings) {
			kidsWalk(b, top, found, bad)
		}
		kidsWalk(x.Return, top, found, bad)
	case *xpath.ForExpr:
		for _, b := range bindingExprs(x.Bindings) {
			kidsWalk(b, top, found, bad)
		}
		kidsWalk(x.Return, top, found, bad)
	case *xpath.QuantifiedExpr:
		for _, b := range bindingExprs(x.Bindings) {
			kidsWalk(b, top, found, bad)
		}
		kidsWalk(x.Test, top, found, bad)
	default:
		if strings.Contains(e.String(), "has-children(") {
			*bad = true
		}
	}
}

// patternKids reports whether a predicate of the last step of an
// alternative of p reads has-children of the candidate (topKids).
func patternKids(p *Pattern) bool {
	for _, a := range p.alts {
		if len(a.steps) == 0 {
			continue
		}
		for _, pr := range a.steps[len(a.steps)-1].preds {
			if topKids(pr) {
				return true
			}
		}
	}
	return false
}

// kidsAt reports whether e, written on el, a motionless instruction of a
// rule's body at its element's view (focus f), reads has-children of that
// element: el's focus is the rule's own (ruleFocus), and every call is of
// the focus (topKids).
func (b *streamBuilder) kidsAt(el *xdm.Node, e xpath.Expr, f vfocus) bool {
	return e != nil && f == vElem && b.x.rule && b.x.class == classElem && !b.noIdent && ruleFocus(el) && topKids(e)
}

// ruleFocus reports whether the focus of el's expressions is the item of the
// template rule el is in: no instruction between them sets another.
func ruleFocus(el *xdm.Node) bool {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if n.Name().URI != xdm.NSXSL {
			continue
		}
		switch n.Name().Local {
		case "template":
			return true
		case "for-each", "for-each-group", "iterate", "analyze-string", "source-document", "merge",
			"merge-source", "function", "evaluate", "accumulator", "key", "sort", "number":
			return false
		}
	}
	return false
}

// kidsLinkWhy readies mode for has-children by a Peek, or says why not:
// after a Peek no rule of it may skip, ground or read as text the element
// it is handed -- no built-in rule that does (deep-copy, deep-skip), no body
// on a grounded copy, no consumer of the context itself (string(.),
// copy-of(.)), no fork, no apply-templates of the context.
func (s *Stylesheet) kidsLinkWhy(mode string) string {
	if s.kidsModes[mode] {
		return ""
	}
	label := "has-children by a Peek in mode " + mode + ": "
	switch s.modeNoMatch[mode] {
	case "deep-copy", "deep-skip":
		return label + "the built-in rule reads the element whole"
	}
	for _, t := range s.templates {
		if t.Match == nil || !t.matchesMode(mode) || t.stream == nil {
			continue
		}
		x := t.stream.x[classElem]
		if x == nil {
			continue
		}
		if x.groundRule {
			return label + "rule " + t.Match.String() + " runs on a grounded copy"
		}
		if why := kidsOpsWhy(x.top); why != "" {
			return label + "rule " + t.Match.String() + ": " + why
		}
	}
	if s.kidsModes == nil {
		s.kidsModes = map[string]bool{}
	}
	s.kidsModes[mode] = true
	return ""
}

// kidsOpsWhy says why one of ops reads its context in a way a Peek forbids.
func kidsOpsWhy(ops []*streamOp) string {
	for _, op := range ops {
		switch {
		case op.fork:
			return elName(op.el) + " forks the cursor"
		case op.apply == applySelf:
			return "apply-templates of the context"
		case op.split != nil && op.split.readsContextWhole():
			return elName(op.el) + " reads the context whole"
		}
		if why := kidsOpsWhy(op.inner); why != "" {
			return why
		}
	}
	return ""
}

// readsContextWhole reports whether s grounds or reads as text the context
// itself, in any branch.
func (s *streamSplit) readsContextWhole() bool {
	if s.cond != nil {
		return s.then.readsContextWhole() || s.els.readsContextWhole()
	}
	return s.ctxItem
}

// kidsDispatch peeks past the start tag of node, the element at hand on the
// cursor, records has-children on its view, and dispatches it; it reports
// whether it did (false: node is not the cursor's view, or has its answer).
// A body that read nothing has the element read to its end, which a Skip
// cannot do after a Peek.
func (run *streamRun) kidsDispatch(f focuser, node *xdm.Node, pos, size int, mode string,
	params, tunnels paramValues, out *outputBuilder) (bool, error) {

	if _, known := xdmburst.ShallowKids(node); known || run.used || run.off {
		return false, nil
	}
	if v, err := run.c.View(); err != nil || v != node {
		return false, nil
	}
	ev, err := run.c.Peek()
	if err != nil {
		return true, err
	}
	xdmburst.SetShallowKids(node, ev.Kind != xdmstream.EndElement && ev.Kind != xdmstream.EndDocument)
	if err := run.dispatchAt(f, node, pos, size, mode, params, tunnels, out); err != nil {
		return true, err
	}
	if run.used {
		return true, nil
	}
	run.used = true
	d := run.d
	filt := &xdmstream.Filter{Accept: acceptNone, Depth: d}
	for {
		ev, err := run.c.NextTag(filt)
		switch {
		case err != nil:
			return true, err
		case ev.Kind == xdmstream.EndDocument || ev.Kind == xdmstream.EndElement && ev.Depth == d:
			return true, nil
		}
	}
}
