package xsd

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
	"github.com/knroy/go-xml/v2/xdm"
)

// cloneFacts is nodeFacts plus what a renumbered record could get wrong:
// the position, the links walked backwards, and the tree-level properties.
func cloneFacts(n *xdm.Node) string {
	line, col, ok := n.Position()
	s := fmt.Sprintf("%s pos=%d:%d:%v parent=%v", nodeFacts(n), line, col, ok, n.Parent() != nil)
	if c := n.LastChild(); c != nil {
		k := 0
		for ; c != nil; c = c.PrevSibling() {
			k++
		}
		s += fmt.Sprintf(" back=%d", k)
	}
	if t := n.Tree(); t != nil {
		s += fmt.Sprintf(" tree=%q/%q", t.DocType, t.XMLVersion)
	}
	return s
}

func sameClone(t *testing.T, label string, a, b *xdm.Node) {
	t.Helper()
	if fa, fb := cloneFacts(a), cloneFacts(b); fa != fb {
		t.Fatalf("%s: nodes differ\n  per-node %s\n  bulk     %s", label, fa, fb)
	}
	if a.NumAttrs() != b.NumAttrs() {
		t.Fatalf("%s: %d attributes, want %d", label, b.NumAttrs(), a.NumAttrs())
	}
	for i := range a.NumAttrs() {
		sameClone(t, label, a.AttrAt(i), b.AttrAt(i))
	}
	ac, bc := a.FirstChild(), b.FirstChild()
	for ; ac != nil && bc != nil; ac, bc = ac.NextSibling(), bc.NextSibling() {
		sameClone(t, label, ac, bc)
	}
	if ac != nil || bc != nil {
		t.Fatalf("%s: children differ in number under %v", label, a.Name())
	}
}

// TestBulkTypedCopyMatchesPerNode checks the bulk clone ValidateCopy makes
// its copies with against the per-node copy it replaced, which is what a
// refusing Clone falls back to: the same typed tree, error text and
// positions, the dropped whitespace and defaulted attributes renumbered in,
// and no positions left on the result.
func TestBulkTypedCopyMatchesPerNode(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	fragment := func() *xdm.Node {
		r := xdm.NewNode(xdm.KindElement, xdm.QName{URI: "urn:t", Local: "q"}, "")
		r.SetBaseURI("http://ex/f/")
		r.AppendText(" local ")
		return r
	}
	cases := []struct {
		name, doc, target string
	}{
		{"valid document", copyDoc("100", `<n xsi:nil="true"/>`, "1"), ""},
		{"invalid document", copyDoc("1.5", `<n>x</n>`, "-9"), ""},
		// Nothing to drop or add: the result is the first copy itself.
		{"invalid document left unedited", `<q xmlns="urn:t">p:x</q>`, ""},
		{"element inside a document", copyDoc("100", `<n xsi:nil="true"/>`, "1"), "item"},
		{"constructed element", "", "fragment"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func() (*xdm.Node, error, *xdm.Node) {
				var target *xdm.Node
				if c.target == "fragment" {
					target = fragment()
				} else {
					target = pickCopyTarget(parseCopyDoc(t, c.doc).Root, c.target)
				}
				got, err := s.ValidateCopy(target, ValidateOptions{MaxErrors: -1})
				return got, err, target
			}
			bulk, errBulk, _ := run()
			clone := xdmclone.Clone
			calls := 0
			xdmclone.Clone = func(any, xdmclone.Options) func(any) any { calls++; return nil }
			perNode, errPer, _ := run()
			xdmclone.Clone = clone
			if calls == 0 {
				t.Fatal("ValidateCopy never asked for a bulk clone")
			}
			if errText(errPer) != errText(errBulk) {
				t.Fatalf("errors differ\n  per-node %s\n  bulk     %s", errText(errPer), errText(errBulk))
			}
			sameClone(t, c.name, perNode, bulk)
			if top := bulk; top.Parent() != nil {
				sameClone(t, c.name, perNode.Parent(), bulk.Parent())
			}
		})
	}
}
