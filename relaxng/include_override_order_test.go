package relaxng

import (
	"fmt"
	"strings"
	"testing"
)

// An <include> overriding several definitions the included grammar lacks
// names the first in document order on every compile. Twenty such
// overrides; ranging the override set named any of them.
func TestIncludeMissingOverrideReportedInDocumentOrder(t *testing.T) {
	docs := map[string]string{"inc.rng": `<grammar` + rngNS + `>
		<start><element name="doc"><empty/></element></start></grammar>`}
	var defs strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&defs, `<define name="d%d"><empty/></define>`, i)
	}
	src := `<grammar` + rngNS + `><include href="inc.rng">` + defs.String() + `</include></grammar>`
	const want = `overrides "d0", which it does not define`
	for i := 0; i < 20; i++ {
		_, err := compileWith(t, src, Options{Resolver: &mapResolver{docs: docs}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
