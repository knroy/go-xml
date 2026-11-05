package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStreamXMLToJSONNestedMatches: xml-to-json of a path whose matches may
// nest (sf-xml-to-json-004's shape) streams, the writer matching below the
// first match: a second match, nested or not, raises the tree's XPTY0004,
// one match gives the tree's JSON.
func TestStreamXMLToJSONNestedMatches(t *testing.T) {
	const fn = `xmlns="http://www.w3.org/2005/xpath-functions"`
	docs := map[string]string{
		"one":    `<map ` + fn + `><array key="B"><map><number key="d">1</number></map></array></map>`,
		"nested": `<map ` + fn + `><array key="B"><map><array key="B"><null/></array></map></array></map>`,
		"deep":   `<map ` + fn + `><array key="B"><map><map key="x"><array key="B"><null/></array></map></map></array></map>`,
		"twice":  `<map ` + fn + `><array key="B"><null/><null/></array></map>`,
		"none":   `<map ` + fn + `><array key="C"><null/></array></map>`,
	}
	bodies := []string{
		`<j><xsl:value-of select="xml-to-json(//*[@key='B']/*, map{'indent': true()})"/></j>`,
		`<j><xsl:value-of select="xml-to-json(//*[@key='B']//*)"/></j>`,
	}
	for name, doc := range docs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for bi, b := range bodies {
			sheet := coreSheetWith("", b)
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
			if so != to || se != te {
				t.Errorf("%s body %d:\ncore %q %q\ntree %q %q", name, bi, so, se, to, te)
			}
			if st.Runs[0] == 0 {
				t.Errorf("%s body %d: not streamed", name, bi)
			}
			if st.Guards != 0 || st.Reruns != 0 && te == "" {
				t.Errorf("%s body %d: guards %d, re-runs %d where the tree gives %q", name, bi, st.Guards, st.Reruns, te)
			}
			if wantErr := name == "nested" || name == "deep" || name == "twice" || bi == 1 && name == "one"; wantErr != strings.Contains(te, "XPTY0004") {
				t.Errorf("%s body %d: tree error %q", name, bi, te)
			}
		}
	}
}
