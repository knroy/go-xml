package xslt

// Lane MM7: a dynamic call given copies of the stream (use-package-152:
// $fallback(snapshot(.)) in the xml-to-json package's rule for elements it
// does not know, $fallback a tunnel parameter read from an options map).
//
// The function item is known only at run time. But every function item
// that can reach the call was made somewhere: on the tree's code, where no
// node of the stream exists, or in a streamed plan, where the view check
// admits a function item only when it captures no focus (funcItemSafe,
// streamsplit_fns.go: no named reference to a focus-dependent function, an
// inline function's body read with no focus) and a partial application's
// bound arguments only as any argument (viewCheck.call). So whatever item
// $f holds, it reads of the stream nothing but its arguments. Given only
// grounded copies (copy-of(P), snapshot(P) of the consuming path P, which
// the split makes of P's matches as the tree makes them of its nodes) and
// atomic values, the call reads no node of the stream: the outer makes it,
// the tree's own code, on the copies (XSLT 3.0 §19.8.8.11: the base
// expression has usage inspection, the arguments a usage their type
// determines, which a grounded operand satisfies whatever it is).
//
// Memory: snapshot(.) of a rule's match grounds the match, which may be
// the document element: the stream holds that copy and the snapshot made
// from it, the tree its document and the same snapshot (memory, with
// dynCopies and itemsOnlyCopied, as streamfn_copies.go reasons for
// holdAt).
//
// Each other argument must give no node of the stream: atomic values, or
// a variable's items. The target is motionless: a variable, a lookup. A
// call given no copy is left to the view check, which takes only a known
// lookup (a parameter without a map type may be given any function).

import "github.com/knroy/go-xml/v2/xpath"

// dynCopiesCall is walk's case for a dynamic call whose arguments are
// copies of the consuming path or values.
func (w *splitter) dynCopiesCall(e xpath.Expr) (done, ok bool) {
	x, isDyn := e.(*xpath.DynamicCall)
	if !isDyn || !(viewCheck{key: w.key}).safe(x.Target, w.focus, false) || readsGroup(x.Target) || !copiedArg(x.Args) {
		return false, false
	}
	for k := range x.Args {
		orig, before := x.Args[k], w.pe
		var inner xpath.Expr
		if c, isCall := orig.(*xpath.FuncCall); isCall && c.Name.URI == fnNS && len(c.Args) == 1 &&
			(c.Name.Local == "copy-of" || c.Name.Local == "snapshot") {
			inner = c.Args[0]
		}
		if !w.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }, useKeep) {
			return true, false
		}
		switch {
		case w.pe == before && (atomicValued(orig) || foreign(orig)):
		case before == nil && inner != nil && w.pe == inner:
		default:
			return true, w.refuse("dynamic call given more than copies and values")
		}
	}
	w.dynCopies = true
	return true, true
}

// copiedArg reports whether one of args is copy-of(P) or snapshot(P).
func copiedArg(args []xpath.Expr) bool {
	for _, a := range args {
		if c, ok := a.(*xpath.FuncCall); ok && (isFn(c, "copy-of", 1) || isFn(c, "snapshot", 1)) {
			return true
		}
	}
	return false
}
