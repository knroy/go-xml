package xslt

// Lane L2: a copy the stylesheet asks for, of matches that may be the
// document element (su-shallow-descent-003: f:odd-children(copy-of(/BOOKLIST), 2)).
//
// holdAt (streamhold.go) gives up at once at a match that may be the
// document element: its grounded copy is the tree, which a tree-mode run
// holds anyway, so streaming it holds as much for nothing. When the outer
// reads the split's items only through copy-of() or snapshot() of them, the
// tree-mode run holds the document AND that copy; the stream holds the
// grounded copy (capped by holdOn at the tree's records, as every held
// split is) and the outer's copy of it. So the copy is admitted, the cap
// still giving up past the tree's records.

import (
	"reflect"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// itemsOnlyCopied reports whether s's outer reads $items (or $value) only
// as the one argument of fn:copy-of or fn:snapshot.
func (s *streamSplit) itemsOnlyCopied() bool {
	if s.cond != nil || s.outer == nil {
		return false
	}
	e := s.outer.Expr()
	copied, bare := 0, 0
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if v.Kind() != reflect.Interface {
			return
		}
		switch x := v.Interface().(type) {
		case *xpath.FuncCall:
			if x.Name.URI == fnNS && len(x.Args) == 1 && (x.Name.Local == "copy-of" || x.Name.Local == "snapshot") {
				if r, ok := x.Args[0].(*xpath.VarRef); ok && isSplitItems(r) {
					copied++
				}
			}
		case *xpath.VarRef:
			if isSplitItems(x) {
				bare++
			}
		}
	})
	return copied > 0 && copied == bare
}

// sheetCopiesPop reports whether the loop el's select, split as s, is a
// call on a stylesheet function marked burst (streamsplit_calls.go) over
// the split's grounded matches: its value holds no node of the stream (the
// copies' nodes, or nodes it makes), so the loop runs over it off the cursor
// as over values (su-shallow-descent-001: for-each select="f(/BOOKLIST/BOOKS)").
// The call itself is the tree's code over the copies: a parameter's type
// raises the tree's XPTY0004 for two matches or none. The body runs on
// nodes of the copies, so it must read of them only what a copy has
// (burstBody: no ancestors' content, siblings beyond the match, root, key,
// identity across matches).
func sheetCopiesPop(s *streamSplit, el *xdm.Node) bool {
	if s.cond != nil || s.outer == nil || s.mode != matchGround || !burstBody(el) {
		return false
	}
	x, ok := s.outer.Expr().(*xpath.FuncCall)
	if !ok {
		return false
	}
	sc, marked := sheetCallOf(x)
	if !marked || !sc.burst || sc.values || sc.climb || sc.up {
		return false
	}
	items := 0
	for _, a := range x.Args {
		if r, ok := a.(*xpath.VarRef); ok && isSplitItems(r) {
			items++
			continue
		}
		if !foreign(a) || refsSplitItems(a) {
			return false
		}
	}
	return items == 1
}
