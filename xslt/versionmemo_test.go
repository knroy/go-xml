package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// With the memo on, the version walks answer what they answer with it off,
// for every node of a stylesheet that states versions at several levels,
// and the answers are remembered. The static phase turns it off and hands
// back an empty one.
func TestVersionMemo(t *testing.T) {
	doc := parseDoc(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template match="a" version="1.0"><x><y xsl:version="2.0"><z/></y></x></xsl:template>
		<xsl:template match="b"><x><xsl:value-of select="1"/></x></xsl:template>
	</xsl:stylesheet>`)
	type answer struct {
		holder *xdm.Node
		v      float64
		module *xdm.Node
	}
	ask := func(n *xdm.Node) answer {
		h, v := versionHolder(n)
		return answer{h, v, moduleElement(n)}
	}
	var nodes []*xdm.Node
	nodes = append(nodes, doc)
	for d := range doc.Descendants() {
		nodes = append(nodes, d)
	}
	want := map[*xdm.Node]answer{}
	for _, n := range nodes {
		want[n] = ask(n)
	}
	setVersionMemo(true)
	defer setVersionMemo(false)
	// Twice: the first pass fills the memo, the second answers from it.
	for pass := 0; pass < 2; pass++ {
		for i := len(nodes) - 1; i >= 0; i-- {
			if got := ask(nodes[i]); got != want[nodes[i]] {
				t.Fatalf("pass %d, %s: got %+v, want %+v", pass,
					describeNode(nodes[i]), got, want[nodes[i]])
			}
		}
	}
	if len(versionMemo.holders) != len(nodes) {
		t.Errorf("memo holds %d nodes, want %d", len(versionMemo.holders), len(nodes))
	}
	resume := suspendVersionMemo()
	ask(nodes[len(nodes)-1])
	if versionMemo.holders != nil {
		t.Error("the memo was written while suspended")
	}
	resume()
	if !versionMemo.on || versionMemo.holders != nil {
		t.Error("resume did not give back an empty memo")
	}
}
