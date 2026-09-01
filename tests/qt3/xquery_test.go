package qt3

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// TestQT3XQuery measures the XQuery half of the same suite.
//
// It is a separate test from TestQT3 rather than a fourth target inside it,
// because the XPath figures are a regression check that must not move while
// XQuery is implemented, and a failure here should not be reported as a
// failure there.
func TestQT3XQuery(t *testing.T) {
	root := SuiteRoot()
	if root == "" {
		t.Skip("set GOXSLT_QT3 to a checkout of w3c/qt3tests")
	}
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(false)

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}
	for name, val := range map[string]string{
		"QTTEST":      "42",
		"QTTEST2":     "other",
		"QTTESTEMPTY": "",
	} {
		t.Setenv(name, val)
	}
	runSuite(t, root, cat, XQuery31)
}
