package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamXMLToJSONMatchesTree: fn:xml-to-json of the document node, the
// document element and a child path, written off the cursor
// (streamfn_json.go), against the tree on valid and invalid JSON XML. What
// the tree answers must stream; an invalid document raises the tree's error.
func TestStreamXMLToJSONMatchesTree(t *testing.T) {
	const fn = `xmlns="http://www.w3.org/2005/xpath-functions"`
	docs := map[string]struct {
		doc   string
		valid bool
	}{
		"all": {`<!--c--><map ` + fn + ` xmlns:o="urn:o" o:x="1"><string key="s">a/b"c&#9;` + "\u0085" + `</string>` +
			`<number key="n"> +005 </number><number key="d">2.50</number><boolean key="b">1</boolean><null key="z"/>` +
			`<array key="a"><number>1</number><!--x--><string escaped="true">\u00e9\n</string><map/><array/></array>` +
			`<map key="m"><string key="k\n" escaped-key="true">v</string><string key="k2">w</string></map>` +
			"\n</map>", true},
		"top":      {`<array ` + fn + ` key="ignored" escaped-key="maybe"><number escaped="maybe">1e3</number></array>`, true},
		"leaf":     {`<string ` + fn + `>x<?pi?>y</string>`, true},
		"dupkey":   {`<map ` + fn + `><null key="a"/><null key="a"/></map>`, false},
		"dupesc":   {`<map ` + fn + `><null key="\u0041" escaped-key="1"/><null key="A"/></map>`, false},
		"nokey":    {`<map ` + fn + `><null/></map>`, false},
		"text":     {`<array ` + fn + `><null/>x</array>`, false},
		"badnum":   {`<number ` + fn + `>INF</number>`, false},
		"kid":      {`<string ` + fn + `>a<null/></string>`, false},
		"ns":       {`<map xmlns="urn:x"/>`, false},
		"attr":     {`<map ` + fn + ` bad="1"/>`, false},
		"badesc":   {`<string ` + fn + ` escaped="true">\q</string>`, false},
		"badbool":  {`<boolean ` + fn + `>yes</boolean>`, false},
		"nullfull": {`<null ` + fn + `> x </null>`, false},
		"other":    {`<thing ` + fn + `/>`, false},
	}
	bodies := []string{
		`<j><xsl:value-of select="xml-to-json(.)"/></j>`,
		`<j><xsl:value-of select="xml-to-json(., map{'indent': true()})"/></j>`,
		`<j><xsl:value-of select="xml-to-json(*, map{'indent': false(), 'other': 1})"/></j>`,
		`<j><xsl:value-of select="xml-to-json(/*, map{'indent': true()})"/></j>`,
		// One match at most (sf-xml-to-json-005's shape): nested candidates.
		// Compared with the tree only: the split refuses a predicate reading
		// the parent of node() candidates (a text candidate is a copy with
		// no parent), so it does not stream.
		`<j><xsl:value-of select="xml-to-json(/descendant::node()[../@key='a'][1])"/></j>`,
	}
	for name, d := range docs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(d.doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, decl := range coreDecls {
			for bi, b := range bodies {
				sheet := coreSheetWith(decl, b)
				so, se, st := coreRun(sheet, dir, true, TransformOptions{})
				to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
				if so != to || se != te {
					t.Errorf("%s decl %d body %d:\ncore %q %q\ntree %q %q", name, di, bi, so, se, to, te)
				}
				if st.Guards != 0 {
					t.Errorf("%s decl %d body %d: %d guard hits", name, di, bi, st.Guards)
				}
				if st.Reruns != 0 && te == "" {
					t.Errorf("%s decl %d body %d: re-run where the tree succeeds", name, di, bi)
				}
				if bi < 4 && d.valid != (te == "") { // the whole document's JSON
					t.Errorf("%s decl %d body %d: tree error %q, valid %v", name, di, bi, te, d.valid)
				}
				if te == "" && st.Runs[0] == 0 && bi < 4 {
					t.Errorf("%s decl %d body %d: not streamed", name, di, bi)
				}
			}
		}
	}
}
