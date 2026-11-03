package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStreamHold: a select at the document node that collects nodes runs on
// the core, the tree's output and error exactly, holding its matches; where
// the held copies would cost more records than the tree (every element a
// match, no text between) or a match is the document element, it gives up
// and the tree answers (streamhold.go).
func TestStreamHold(t *testing.T) {
	recs := "<doc>" + strings.Repeat(`<rec k="a"><a/><b/><p>1</p><c/><d/></rec>`, 300) + "</doc>"
	bare := "<doc>" + strings.Repeat(`<t/>`, 3000) + "</doc>"
	for _, c := range []struct {
		doc, sel string
		fallback bool
	}{
		{recs, `count(remove(/doc/rec/p, 3))`, false},
		{recs, `sum(insert-before(/doc/rec/p, 2, 5))`, false},
		{recs, `(doc/rec/p) treat as element(p)+`, false},
		{recs, `deep-equal(/doc/rec/p, ())`, false},
		{recs, `exactly-one(/doc/rec/p)`, false}, // FORG0005, the tree's
		{recs, `count((doc/rec/@k, 1))`, false},
		{bare, `string-join(remove(/doc/t, 3) ! name())`, true}, // a copy costs more than <t/>
		{bare, `remove(/*, 1) ! name()`, true},                  // the document element
		// Counted, not held (streamfold.go): no copy, so no give-up.
		{bare, `count(remove(/doc/t, 3))`, false},
		{bare, `count(remove(/*, 1))`, false},
		{bare, `boolean((/doc/t, 1))`, false},
		{bare, `boolean((1, /doc/t))`, false}, // FORG0006, the tree's
		{bare, `exists(subsequence(insert-before(1, 1, tail(/doc/t)), 2, 5))`, false},
		{bare, `count(exactly-one(/doc/t))`, false}, // FORG0005 with the tree's count
		{recs, `empty(unordered(/doc/rec/p))`, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		sheet := coreSheet(`<o><xsl:value-of select="` + c.sel + `"/></o>`)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		where := fmt.Sprintf("%s on %d bytes", c.sel, len(c.doc))
		if so != to || se != te {
			t.Errorf("%s:\ncore %q %q\ntree %q %q", where, so, se, to, te)
		}
		if st.Runs[0] == 0 || st.Guards != 0 {
			t.Errorf("%s: core runs %d, guards %d", where, st.Runs[0], st.Guards)
		}
		if fell := st.Fallbacks > 0; fell != c.fallback {
			t.Errorf("%s: fell back %v, want %v", where, fell, c.fallback)
		}
	}
}
