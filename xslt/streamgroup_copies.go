package xslt

// current-group()!copy-of() (lane MM4, si-group-064): each member of a
// group copied, as the tail of the group's members' path. The split grounds
// a member, its tail copies the copy, and the ground is let go: what is held
// is the copies, which the tree-mode run makes too beside its document, and
// one member's ground at a time. A map over the group with another R stays
// refused (mapPathOf).

import "github.com/knroy/go-xml/v2/xpath"

// groupCopyMap reports whether r, mapped over current-group(), is
// copy-of() or snapshot() of the member: a copy made of each.
func groupCopyMap(r xpath.Expr) bool {
	f, ok := r.(*xpath.FuncCall)
	if !ok || f.Name.URI != fnNS || f.Name.Local != "copy-of" && f.Name.Local != "snapshot" {
		return false
	}
	return len(f.Args) == 0 || len(f.Args) == 1 && isContextItem(f.Args[0])
}
