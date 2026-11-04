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
