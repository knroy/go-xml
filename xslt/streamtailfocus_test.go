package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// tailFocusCheck runs each body streamed and on the tree over doc and
// wants the same output, with no rerun or guard hit.
func tailFocusCheck(t *testing.T, doc string, bodies []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, body := range bodies {
		got, tr := resultDocRun(t, body, dir, true)
		want, _ := resultDocRun(t, body, dir, false)
		if got != want {
			t.Errorf("%s\n stream %q\n tree   %q", body, got, want)
		}
		if tr.Reruns != 0 || tr.Guards != 0 {
			t.Errorf("%s: reruns %d, guards %d", body, tr.Reruns, tr.Guards)
		}
	}
}

// TestStreamTailFocusPosition: a path's tail runs once per match, where
// position() is 1; P/position() and P ! position() under a map count over
// P's sequence, as the tree does (A1-F1).
func TestStreamTailFocusPosition(t *testing.T) {
	tailFocusCheck(t, `<doc><rec k="a"><sub/></rec><other/><rec k="b"/><rec k="c"/></doc>`, []string{
		`<v><xsl:value-of select="(doc/rec/position()) ! string()" separator="|"/></v>`,
		`<v><xsl:value-of select="(doc/rec ! position()) ! string()" separator="|"/></v>`,
		`<v><xsl:value-of select="(doc/rec/(@k || position())) ! string()" separator="|"/></v>`,
		`<v><xsl:value-of select="(doc/rec/@k/position()) ! string()" separator="|"/></v>`,
		`<v><xsl:value-of select="(doc/*/position()) ! string()" separator="|"/></v>`,
		`<v><xsl:value-of select="sum((//rec/position()) ! xs:decimal(.))"/></v>`,
		`<v><xsl:value-of select="(doc/rec[position() lt 3]/sub[1]) ! name()" separator="|"/></v>`,
	})
}

// TestStreamIterateLast: xsl:iterate over copies of the stream reading
// last() streams with its population's size, or is refused at plan time:
// never XPDY0002 or a rerun (A1-F4).
func TestStreamIterateLast(t *testing.T) {
	tailFocusCheck(t, `<doc><rec k="a"><sub/></rec><other/><rec k="b"/></doc>`, []string{
		`<xsl:iterate select="copy-of(doc/rec)"><e p="{position()}" l="{last()}" k="{@k}"/></xsl:iterate>`,
		`<xsl:iterate select="doc/rec/copy-of(.)"><e l="{last()}"/></xsl:iterate>`,
		`<xsl:iterate select="snapshot(doc/rec)"><e l="{last()}"/></xsl:iterate>`,
		`<xsl:iterate select="copy-of(doc/rec)"><xsl:param name="n" select="0"/><xsl:on-completion><done n="{$n}"/></xsl:on-completion>` +
			`<xsl:next-iteration><xsl:with-param name="n" select="$n + last()"/></xsl:next-iteration></xsl:iterate>`,
	})
}
