package xslt

// Simple content of a sequence that holds text nodes beside a consuming
// path of elements (lane L3: streamable-091, value-of select="$t,
// Address/State, $t", $t a text node).
//
// §5.7.2 drops zero-length text nodes, merges adjacent ones, then
// atomizes: so the value as a whole may not be read as data() of it (two
// $t with no State between them merge into one string, data() would give
// two). But a node that is not a text node is never merged: replacing it by
// its typed value changes nothing -- except where that value is empty, when
// the node still separated the text nodes around it. So the consuming item
// alone is read as values (data() of it, matchText), the outer keeps the
// sequence as it is, and a match whose typed value is empty is kept as its
// copy (keepEmpty), which the outer then atomizes to nothing in its place.

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// dataItems wraps in data() each item of e, a sequence made simple content,
// that gives no text node and is not motionless on the focus (the consuming
// one), and reports whether it did.
func dataItems(e xpath.Expr, doc bool) (xpath.Expr, bool) {
	seq, ok := e.(*xpath.SequenceExpr)
	if !ok {
		return e, false
	}
	f := vElem
	if doc {
		f = vNone
	}
	items := make([]xpath.Expr, len(seq.Items))
	done := false
	for i, it := range seq.Items {
		items[i] = it
		if !noTextNodes(it) || atomicValued(it) || (viewCheck{}).safe(it, f, false) {
			continue
		}
		items[i] = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "data"}, Args: []xpath.Expr{it}}
		done = true
	}
	if !done {
		return e, false
	}
	return &xpath.SequenceExpr{Items: items}, true
}

// keepItems readies s, the split of a sequence dataItems rewrote: its path
// is read as text (matchText: one value per match, or the typed value), and
// its outer takes the matches' values where data($items) was, as they are.
func (s *streamSplit) keepItems(sel *xpath.Compiled) string {
	if s.cond != nil || s.nests || s.mode != matchText || s.red != "" || s.tail != nil || s.group {
		return "a sequence of text nodes and a path not read as text"
	}
	seq, ok := s.outer.Expr().(*xpath.SequenceExpr)
	if !ok {
		return "a sequence of text nodes and a path not read as text"
	}
	items := make([]xpath.Expr, len(seq.Items))
	n := 0
	for i, it := range seq.Items {
		items[i] = it
		if f, ok := it.(*xpath.FuncCall); ok && f.Name.URI == fnNS && f.Name.Local == "data" &&
			len(f.Args) == 1 && isVarRef(f.Args[0], splitItems) {
			items[i] = f.Args[0]
			n++
		}
	}
	if n != 1 {
		return "a sequence of text nodes and a path not read as text"
	}
	s.outer = xpathleaf.WithExpr(sel, &xpath.SequenceExpr{Items: items}).(*xpath.Compiled)
	s.keepEmpty = true
	return ""
}

// splitDataItems is splitFor's split of sel, simple content, as a sequence
// whose consuming item alone is read as values (dataItems, keepItems), on a
// fresh parse of it; nil, and checks as they were, when that does not fit.
func (c *compiler) splitDataItems(sel *xpath.Compiled, ns xpath.NamespaceResolver, doc, group, key bool,
	checks []xpath.Expr) (*streamSplit, []xpath.Expr) {
	e := reparse(sel, ns)
	if e == nil {
		return nil, checks
	}
	e, ch := foreignSets(c.inlineCalls(e))
	m, ok := dataItems(e, doc)
	if !ok {
		return nil, checks
	}
	s, _ := splitStreamIn(m, sel, doc, group, key)
	if s == nil || s.keepItems(sel) != "" {
		return nil, checks
	}
	return s, ch
}
