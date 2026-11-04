package xslt

import "github.com/knroy/go-xml/v2/xpath"

// disjointDepthPaths reports whether two striding operands of a union,
// intersect or except are paths that select nodes at one depth below one
// origin: both absolute, or both relative to the context item, and each
// nothing but the same number of child steps. Two nodes at the same depth
// never contain one another, so the merged sequence is striding, not the
// crawling §19.8.8.4 gives two striding operands in general for
// "author | author/name".
//
// Only the trusting walk (a.trust set) reads it: a refusal path keeps
// §19.8.8.4's crawling answer, so no XTSE3430 changes. It is what lets
// sx-union-315's for-each over
// (/BOOKLIST/BOOKS/ITEM/PRICE union /BOOKLIST/BOOKS/ITEM/QUANTITY) with
// ".+1 || ' '" in its body have a verdict (stringConcat withholds one over a
// crawling focus).
func (a *analyzer) disjointDepthPaths(left, right xpath.Expr) bool {
	if a.trust == nil {
		return false
	}
	l, ok1 := left.(*xpath.PathExpr)
	r, ok2 := right.(*xpath.PathExpr)
	if !ok1 || !ok2 || l.Root != r.Root || len(l.Steps) == 0 || len(l.Steps) != len(r.Steps) {
		return false
	}
	for _, p := range [2]*xpath.PathExpr{l, r} {
		for _, e := range p.Steps {
			if st, ok := e.(*xpath.Step); !ok || st.Axis != xpath.AxisChild {
				return false
			}
		}
	}
	return true
}
