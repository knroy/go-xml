package xdm

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A parsed tree's document order is the order of its records: every node,
// its namespace nodes and attributes before its children, compares after the
// one before it.

func collectOrders(n *Node, out []*Node) []*Node {
	out = append(out, n)
	for ns := range n.NamespaceNodes() {
		out = append(out, ns)
	}
	for _, a := range attrsOf(n) {
		out = append(out, a)
	}
	for _, c := range kids(n) {
		out = collectOrders(c, out)
	}
	return out
}

// checkParseOrder parses src with and without whitespace stripping and checks
// each tree's order. It reports whether src parsed.
func checkParseOrder(t *testing.T, name, src string) bool {
	t.Helper()
	parsed := false
	for _, o := range []ParseOptions{
		{AllowDOCTYPE: true},
		{AllowDOCTYPE: true, StripSpace: func(QName) bool { return true }},
	} {
		tr, err := ParseString(src, o)
		if err != nil {
			continue
		}
		parsed = true
		got := collectOrders(tr.Root, nil)
		for i := 1; i < len(got); i++ {
			if got[i-1].Compare(got[i]) >= 0 || got[i].Compare(got[i-1]) <= 0 {
				t.Fatalf("%s (strip %v): node %d does not follow node %d",
					name, o.StripSpace != nil, i, i-1)
			}
		}
	}
	return parsed
}

func TestParseNumberingMatchesFinalize(t *testing.T) {
	cases := map[string]string{
		"inherited namespaces reserve slots": `<a xmlns="u" xmlns:p="v" x="1"><p:b y="2"><c/>t</p:b><!--c--><?pi d?></a>`,
		"undeclaring":                        `<a xmlns:p="v" xmlns="u"><b xmlns="" a="1"><c xmlns:p="w"/></b><d/></a>`,
		"shadowing restored for a sibling":   `<a xmlns:p="v"><b xmlns:p="w" xmlns:q="x"><c/></b><d q="1"/></a>`,
		"cdata and text join":                "<a>x<![CDATA[y]]>z<b/> <c> </c></a>",
		"element-only DTD content":           "<!DOCTYPE a [<!ELEMENT a (b)*><!ELEMENT b EMPTY>]><a>\n <b/>\n <b/>\n</a>",
		"entity with markup re-parses":       `<!DOCTYPE a [<!ENTITY e "<b xmlns:p='v'>t</b>">]><a>&e;<c/></a>`,
		"prolog and epilog":                  "<?pi a?><!--c--><a/><!--d--><?pi b?>",
	}
	for name, src := range cases {
		if !checkParseOrder(t, name, src) {
			t.Fatalf("%s: did not parse", name)
		}
	}

	// A sample of the conformance suites, when they are present: stylesheets
	// carry the most namespace declarations, and the QT3 sources carry DTDs.
	const perRoot = 600
	for _, root := range []string{"../testdata/xslt30-test/tests", "../testdata/qt3tests"} {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		n := 0
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".xml") && !strings.HasSuffix(p, ".xsl") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil || len(data) > 1<<20 {
				return nil
			}
			if checkParseOrder(t, p, string(data)) {
				n++
			}
			if n == perRoot {
				return filepath.SkipAll
			}
			return nil
		})
		t.Logf("%s: %d documents checked", root, n)
	}
}
