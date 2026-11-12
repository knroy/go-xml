package xslt

// Lane MM7: a path over snapshots of the stream's matches (sf-snapshot-0101c:
// snapshot(//*:identifier)/self::*:identifier/ancestor::node()[last()]).
//
// fn:snapshot copies each node with copies of its ancestors, their
// attributes and namespaces (XSLT 3.0 §18.4 fn:snapshot, "the result has
// ... the copied ancestors"), so a path from the snapshots may climb to
// their roots: what it reaches is the snapshots' own trees. The split
// grounds P's matches, the outer runs snapshot($items)/... on the tree's
// code: the snapshots are made from the grounded copies, whose ancestors
// carry the originals' names, attributes and namespaces, as the tree's are
// made from its nodes. What is held is the grounded matches, at the
// document node under the cap of streamhold.go, below it by the memory
// rule's (s.memory); the snapshots themselves the tree makes too.

import "github.com/knroy/go-xml/v2/xpath"

// snapshotHead takes e, snapshot(P)/S1/.../Sn with P the consuming path,
// as the call's split, the steps run on its value by the outer. The steps
// are axis steps whose predicates read no focus but their own (no
// current(), which would be the instruction's item, a node of the stream).
func (w *splitter) snapshotHead(e xpath.Expr) (done, ok bool) {
	pe, isPath := e.(*xpath.PathExpr)
	if !isPath || pe.Root || len(pe.Steps) < 2 {
		return false, false
	}
	f, isCall := pe.Steps[0].(*xpath.FuncCall)
	if !isCall || !isFn(f, "snapshot", 1) {
		return false, false
	}
	for _, st := range pe.Steps[1:] {
		s, isStep := st.(*xpath.Step)
		if !isStep {
			return false, false
		}
		for _, p := range s.Predicates {
			if callsCurrentFn(p) || readsGroup(p) {
				return false, false
			}
		}
	}
	return true, w.slot(pe.Steps[0], func(y xpath.Expr) { pe.Steps[0] = y }, useKeep)
}

// callsCurrentFn reports whether e calls fn:current().
func callsCurrentFn(e xpath.Expr) bool {
	found := false
	walkExprCalls(e, func(x *xpath.FuncCall) { found = found || isCurrentCall(x) })
	return found
}
