package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// TestSM03ClimbSortHeld: what a climbSort tail holds stays within twice the
// distinct nodes its matches' tails give, however often they give them, and
// those are charged to the item budget; and the streamed answer of an
// out-of-order climb is the tree's.
func TestSM03ClimbSortHeld(t *testing.T) {
	doc, err := xdm.ParseString("<a><b><c/><d/></b><e/></a>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*xdm.Node
	for n := range doc.Root.Descendants() {
		nodes = append(nodes, n)
	}
	r := &splitRun{ctx: xpath.NewContext(nil, nil)}
	for i := range 100_000 {
		if err := r.holdClimbed(nodes[i%len(nodes)]); err != nil {
			t.Fatal(err)
		}
	}
	if cap(r.held) > 1024 {
		t.Errorf("held: len %d cap %d for %d distinct nodes", len(r.held), cap(r.held), len(nodes))
	}
	if got := sortClimbed(r.held); len(got) != len(nodes) {
		t.Errorf("%d distinct held, want %d", len(got), len(nodes))
	}
	r = &splitRun{ctx: xpath.NewContext(nil, nil).WithEnv(func(e *xpath.Env) { e.MaxItems = 300 })}
	tree, err := xdm.ParseString("<a>"+strings.Repeat("<x/>", 3000)+"</a>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = nil
	for n := range tree.Root.Descendants() {
		if err = r.holdClimbed(n); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "XPDY0130") {
		t.Errorf("3000 distinct nodes under MaxItems 300: %v, want XPDY0130 (len %d cap %d)", err, len(r.held), cap(r.held))
	}

	dir := t.TempDir()
	src := "<doc>" + strings.Repeat("<a>", 30) + strings.Repeat("<x/>", 300) + strings.Repeat("</a>", 30) + "</doc>"
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `<out><xsl:value-of select="count(//x/parent::*/ancestor-or-self::*)"/></out>`
	so, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
	to, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
	if so != to || se != "" || te != "" || !strings.Contains(so, "<out>31</out>") || st.Reruns != 0 {
		t.Errorf("stream %q %s\ntree %q %s (trace %+v)", so, se, to, te, st)
	}
}
