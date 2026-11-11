package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSM11XIncludeChainChargedBeforeRecursing: each included document is
// charged to the pass budget while it is live, so a chain is refused after a
// couple of fetches past the budget, not after every level was parsed.
func TestSM11XIncludeChainChargedBeforeRecursing(t *testing.T) {
	const xi = `xmlns:xi="http://www.w3.org/2001/XInclude"`
	const k = 20
	files := map[string]string{}
	for i := 1; i <= k; i++ {
		inc := ""
		if i < k {
			inc = fmt.Sprintf(`<xi:include %s href="mem:d%d.xml"/>`, xi, i+1)
		}
		files[fmt.Sprintf("mem:d%d.xml", i)] = "<r>" + strings.Repeat("<a/>", 400) + inc + "</r>"
	}
	r := &mapResolver{files: files}
	tree, err := ParseString(`<top><xi:include `+xi+` href="mem:d1.xml"/></top>`,
		ParseOptions{BaseURI: "mem:top.xml"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProcessXInclude(tree, XIncludeOptions{Resolver: r, Parse: ParseOptions{MaxNodes: 1000}})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("err = %v, want ErrResourceLimit", err)
	}
	if len(r.reads) > 3 {
		t.Fatalf("%d documents read before the 1000-node pass budget refused", len(r.reads))
	}
}
