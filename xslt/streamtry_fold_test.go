package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamTryFoldOrder: under xsl:try, selects whose fold cannot fail
// once their items have passed the predicates and tail (numeric items, or
// untyped ones a predicate converted to a number), whose second numeric
// comparison cannot fail after the first, and whose simple map's right side
// runs after its left, stream; the catch sees the tree's first error, code
// and description, wherever the records fail.
func TestStreamTryFoldOrder(t *testing.T) {
	sels := []string{
		`avg(doc/rec/@v[. > 0])`,
		`avg(doc/rec/(@v * 2))`,
		`sum(doc/rec/@v/data()[. < 1000][. > 0])`,
		`avg(doc/rec/sub/text()[. < 1000][. > 0])`,
		`avg(doc/rec/@v ! xs:NMTOKENS(.) ! xs:decimal(.))`,
		`min(doc/rec/abs(@v))`,
		`max(doc/rec/number(@v)[. > 0])`,
		`sum(doc/rec/@v[xs:decimal(.) gt 0])`,
		// The fold can fail on an item that passed (strings): refused, so
		// the catch still sees the predicate's error the tree raises first.
		`sum(doc/rec/@v[xs:decimal(.) ne 0]/string(.))`,
	}
	refused := len(sels) - 1
	vals := [][]string{{"1", "2", "3"}, {"1", "x", "3"}, {"x", "1", "y"}, {"5", "1e1", "-2"}, {"1 2", "3", "z"},
		{"2000", "1", ""}, {"0.5", "1.5", "a b"}}
	n := 0
	for vi, vs := range vals {
		var b strings.Builder
		b.WriteString("<doc>")
		for _, v := range vs {
			fmt.Fprintf(&b, `<rec v="%s"><sub>%s</sub></rec>`, v, v)
		}
		b.WriteString("</doc>")
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		for si, sel := range sels {
			body := `<xsl:try><v><xsl:value-of select="` + strings.ReplaceAll(strings.ReplaceAll(sel, "<", "&lt;"), ">", "&gt;") + `"/></v>` +
				`<xsl:catch xmlns:err="http://www.w3.org/2005/xqt-errors"><e c="{$err:code}" d="{$err:description}"/></xsl:catch></xsl:try>`
			sheet := coreSheet(body)
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
			if so != to || se != te {
				t.Errorf("values %d select %d %s:\ncore %q %q\ntree %q %q", vi, si, sel, so, se, to, te)
			}
			if st.Guards != 0 || st.Reruns != 0 {
				t.Errorf("values %d select %d: guards %d, re-runs %d", vi, si, st.Guards, st.Reruns)
			}
			if si == refused {
				if st.Runs[streamtrace.Core] != 0 {
					t.Errorf("values %d select %d %s: streamed", vi, si, sel)
				}
				continue
			}
			if st.Runs[streamtrace.Core] == 0 {
				t.Errorf("values %d select %d %s: not streamed", vi, si, sel)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("no cases")
	}
}
