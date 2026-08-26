package qt3

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// TestEntityResolverConfinement checks that the suite's entity resolver cannot
// be walked out of the checkout. It is a security property, not a conformance
// one: a case is data, and a system identifier is written by that data.
func TestEntityResolverConfinement(t *testing.T) {
	root := SuiteRoot()
	if root == "" {
		t.Skip("set GOXSLT_QT3")
	}
	res := suiteEntityResolver{text: suiteTextResolver{root: root, dir: "fn"}}
	base := "file://" + root + "/fn/"
	for _, sysID := range []string{
		"../../../../../../etc/passwd",
		"parse-xml/../../../../go.mod",
		"/etc/passwd",
		"file:///etc/passwd",
		"http://example.com/x",
	} {
		rc, _, err := res.ResolveEntity(sysID, "", base)
		if err == nil {
			if rc != nil {
				rc.Close()
			}
			t.Errorf("ResolveEntity(%q) was allowed; it must be refused", sysID)
		}
	}
	// A default context must still refuse every external entity.
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = xpath.XPath30
	doc := `<!DOCTYPE a [<!ENTITY x SYSTEM "/etc/passwd">]><a>&x;</a>`
	if _, err := xpath.Eval(`parse-xml('`+doc+`')`, ctx, nil); err == nil {
		t.Error("parse-xml resolved an external entity with no resolver configured")
	} else if strings.Contains(err.Error(), "root:") {
		t.Errorf("entity content leaked into the error: %v", err)
	}
}
