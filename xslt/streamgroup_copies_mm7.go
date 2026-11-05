package xslt

// Lane MM7: copies of the stream held as a value or a loop's population.
//
// copy-of(current-group()) as a parameter (si-group-040), and an
// xsl:for-each over copies whose body reads last() (si-group-065:
// current-group()!copy-of(), the group's members copied). The copies are
// the select's value, which a tree-mode run holds too, beside its document;
// the stream holds them and one grounded member at a time. A group's
// members may be every child of the document element: still the copies,
// never above the tree's.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// readsGroup reports whether e calls fn:current-group(). In a streamed
// group's body that is the consuming path (the group's members), though no
// focus is read: copy-of(current-group()) is not a copy of a variable's
// value, which the view check takes it for.
func readsGroup(e xpath.Expr) bool {
	cg := false
	walkExprCalls(e, func(x *xpath.FuncCall) { cg = cg || isCurrentGroup(x) })
	return cg
}

// groupParam reports whether ch, an xsl:with-param in a streamed group's
// body, reads the current group: a consuming parameter, read off the
// cursor at the instruction as an expression op whose split takes the
// group's members (foreignParams), not a motionless value.
func (b *streamBuilder) groupParam(ch *xdm.Node) bool {
	return b.grp && ch.Attr("", "select") != nil && readsGroup(parseAt(ch, ch.AttrValue("select")))
}

// copyOfMap reads P!copy-of() (or P!copy-of(.)) as copy-of(P): a copy of
// each item of P, in P's order, as fn:copy-of copies a sequence.
func copyOfMap(e xpath.Expr) (*xpath.FuncCall, bool) {
	m, ok := e.(*xpath.SimpleMap)
	if !ok {
		return nil, false
	}
	f, ok := m.Right.(*xpath.FuncCall)
	if !ok || !isFn(f, "copy-of", len(f.Args)) || !(len(f.Args) == 0 || isContextItem(f.Args[0])) {
		return nil, false
	}
	return &xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{m.Left}}, true
}

// sizedLoop reports whether op, an xsl:for-each whose body may read last()
// (groundLastWhy), runs over fresh copies of the stream's matches
// (copy-of(P), P/copy-of(), P!copy-of()): then it marks it sized, its
// population collected whole before the body runs, as the tree evaluates
// the select first and runs the body with its size.
func sizedLoop(op *streamOp) bool {
	p := op.pop
	if !isXSL(op.el, "for-each") || p == nil || p.cond != nil || !p.ground || p.fn == nil || p.post != nil {
		return false
	}
	if f, ok := p.fn.Expr().(*xpath.FuncCall); !ok || !isFn(f, "copy-of", 1) {
		return false
	}
	op.sized = true
	return true
}

// sizedFeed is f with its items collected before the first is handed on,
// off the cursor, the feed's size set to their number.
func (run *streamRun) sizedFeed(f *burstFeed) *burstFeed {
	read := f.read
	f.read = func(each func(xdm.Item) error) error {
		var seq xdm.Sequence
		if err := read(func(it xdm.Item) error {
			seq = append(seq, it)
			return nil
		}); err != nil {
			return err
		}
		f.size = len(seq)
		return run.offCursor(func() error {
			for _, it := range seq {
				if err := each(it); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return f
}
