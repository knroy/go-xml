package xdm

import "github.com/knroy/go-xml/v2/internal/xdmtrim"

func init() {
	xdmtrim.TextBlocks = func(n any) int { return len(n.(*Node).tree.text.blocks) }
	xdmtrim.CompactText = func(n any) { n.(*Node).tree.compactText() }
}

// compactText stores the value of each of t's records in a new store and
// drops the old one: the values of records taken back are left behind. The
// old blocks are not written to again, so a string read from them stays
// what it was.
func (t *Tree) compactText() {
	old := t.text
	t.text = textStore{}
	for i := uint32(0); i < t.n; i++ {
		r := t.rec(i)
		if r.flags&fSide != 0 || r.v1 == 0 {
			continue
		}
		switch NodeKind(r.kind) {
		case KindAttribute, KindText, KindComment, KindPI:
			r.v0, r.v1 = t.text.add(old.str(r.v0, r.v1))
		}
	}
}
