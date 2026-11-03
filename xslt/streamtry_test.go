package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// tryDocs fail at record k in several ways: a value that is not a number
// (FORG0001 in a cast, FORG0006 in avg), a zero divisor (FOAR0001), and a
// malformed tail after an error the try catches.
var tryDocs = map[string]string{
	"ok":    `<doc><rec k="a" v="1"><sub x="1">t1</sub></rec><rec k="b" v="2"><sub x="2"/></rec><rec k="c" v="3"/></doc>`,
	"nan3":  `<doc><rec k="a" v="1"><sub x="1">t1</sub></rec><rec k="b" v="2"/><rec k="c" v="x"><sub x="z"/></rec><rec k="d" v="4"/></doc>`,
	"nan1":  `<doc><rec k="a" v="x"/><rec k="b" v="2"><sub x="0"/></rec></doc>`,
	"zero":  `<doc><rec k="a" v="1"><sub x="0"/></rec><rec k="b" v="0"><sub x="1"/></rec></doc>`,
	"bad":   `<doc><rec k="a" v="x"/><rec k="b" v="2"><sub x="1"/></rec><rec k="c"></doc>`,
	"empty": `<doc/>`,
	"wide":  "<doc>" + strings.Repeat(`<rec k="a" v="1"><sub x="1">s</sub></rec>`, 700) + `<rec k="z" v="q"/></doc>`,
}

// errOut writes the error a catch sees.
const errOut = `<xsl:value-of select="$err:code, $err:description" separator="|"/>`

// tryBodies are container bodies holding an xsl:try; stream says the core
// must take the container.
var tryBodies = []struct {
	body   string
	stream bool
}{
	// A reducer, a select of the try, a copy-of: the body's one consumer.
	{`<o><xsl:try><xsl:value-of select="avg(/doc/rec/@v)"/><xsl:catch>` + errOut + `</xsl:catch></xsl:try></o>`, true},
	{`<o><xsl:try select="sum(doc/rec/@v)"><xsl:catch select="'caught', $err:code"/></xsl:try></o>`, true},
	{`<o><xsl:try><xsl:copy-of select="doc/rec[xs:integer(@v) gt 0]"/><xsl:catch errors="*:FORG0001">` + errOut + `</xsl:catch></xsl:try></o>`, true},
	{`<o><xsl:try><xsl:value-of select="(doc/rec/@v) treat as xs:integer+"/><xsl:catch errors="*:XPDY0050">t</xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:try><xsl:value-of select="round(avg(doc/rec[position() lt 3]/@v), 2)"/><xsl:catch>c</xsl:catch></xsl:try></o>`, true},
	// treat as tests the items, not their values: an atomized P would
	// fail it (XPDY0050) where the tree does not.
	{`<o><xsl:value-of select="sum((doc/rec/@v) treat as attribute()+)"/></o>`, false},
	{`<o><xsl:try><xsl:value-of select="sum((doc/rec/@v) treat as attribute()+)"/><xsl:catch errors="*:XPDY0050">t</xsl:catch></xsl:try></o>`, false},
	// A catch that does not match: the error goes on (re-run on the tree).
	{`<o><xsl:try><xsl:value-of select="sum(doc/rec/@v)"/><xsl:catch errors="*:XTDE0000">no</xsl:catch></xsl:try></o>`, true},
	// A try in a loop's body, per match; the catch writes the match's view.
	{`<o><xsl:for-each select="doc/rec"><xsl:try><r k="{@k}"><xsl:value-of select="sum(sub/@x) div 1"/></r><xsl:catch><e k="{@k}">` + errOut + `</e></xsl:catch></xsl:try></xsl:for-each></o>`, true},
	{`<o><xsl:for-each select="doc/rec"><xsl:try><r><xsl:copy-of select="sub[xs:integer(@x) ge 0]"/></r><xsl:catch><e k="{@k}"/></xsl:catch></xsl:try><n k="{@k}"/></xsl:for-each></o>`, true},
	{`<o><xsl:for-each select="doc/rec"><xsl:try><r><xsl:value-of select="1 div xs:integer(@v)"/></r><xsl:catch errors="*:FOAR0001 *:FORG0001">z</xsl:catch></xsl:try></xsl:for-each></o>`, true},
	// A try around a loop whose bodies fail at record k.
	{`<o><xsl:try><xsl:for-each select="doc/rec"><r><xsl:value-of select="xs:integer(@v) * 2"/></r></xsl:for-each><xsl:catch>` + errOut + `</xsl:catch></xsl:try></o>`, true},
	{`<o><xsl:try><xsl:for-each select="doc/rec"><r><xsl:value-of select="sum(sub/@x)"/></r></xsl:for-each><xsl:catch>c</xsl:catch></xsl:try></o>`, true},
	// Nested tries, the inner one rethrowing.
	{`<o><xsl:try><xsl:for-each select="doc/rec"><xsl:try><r><xsl:value-of select="xs:integer(@v)"/></r><xsl:catch errors="*:FOAR0001">in</xsl:catch></xsl:try></xsl:for-each><xsl:catch>out</xsl:catch></xsl:try></o>`, true},
	// A motionless try (sTree on the view).
	{`<o><xsl:for-each select="doc/rec"><xsl:try><r v="{xs:integer(@v)}"/><xsl:catch><e/></xsl:catch></xsl:try></xsl:for-each></o>`, true},
	// Refused: parts that may fail in another order than the tree's (a
	// predicate and a fold; a tail and a fold), a loop whose select may
	// fail, apply-templates, a consuming catch, rollback-output="no".
	{`<o><xsl:try><xsl:value-of select="sum(doc/rec[xs:integer(@v) gt 0]/@v)"/><xsl:catch>c</xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:try><xsl:value-of select="avg(doc/rec/(@v * 2))"/><xsl:catch>c</xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:try><xsl:for-each select="doc/rec[xs:integer(@v) gt 0]"><r/></xsl:for-each><xsl:catch>c</xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:try><xsl:apply-templates select="doc/rec"/><xsl:catch>c</xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:try><xsl:value-of select="sum(doc/rec/@v)"/><xsl:catch><xsl:value-of select="count(doc/rec)"/></xsl:catch></xsl:try></o>`, false},
	{`<o><xsl:for-each select="doc/rec"><xsl:try><r><xsl:value-of select="1 div xs:integer(@v)"/></r><xsl:catch><xsl:value-of select="count(*)"/></xsl:catch></xsl:try></xsl:for-each></o>`, false},
	{`<o><xsl:try rollback-output="no"><xsl:copy-of select="doc/rec[xs:integer(@v) gt 0]"/><xsl:catch>c</xsl:catch></xsl:try></o>`, false},
}

// TestStreamTryMatchesTree: every try body over every document, under every
// whitespace declaration, gives on the core the tree's output or error --
// the catch seeing the tree's error code and description -- and the ones
// that must stream ran on the core, a catch taking an error without a re-run.
func TestStreamTryMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed, caught := 0, 0
	traceTry = func() { caught++ }
	defer func() { traceTry = nil }()
	for name, doc := range tryDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range tryBodies {
			body := strings.Replace(c.body, "<o>", `<o xmlns:err="http://www.w3.org/2005/xqt-errors">`, 1)
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 || caught == 0 {
		t.Errorf("%d streamed without a re-run, %d errors caught on the cursor", streamed, caught)
	}
}

// TestStreamTryErrors: a streamed principal input (TransformReader, no tree
// to re-run on) whose try catches an error at record k sees the tree's
// error, skips the rest of the context, and still meets a later parse
// error, which no try inside the body can catch.
func TestStreamTryErrors(t *testing.T) {
	sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="doc"><out xmlns:err="http://www.w3.org/2005/xqt-errors">` +
		`<xsl:try><xsl:for-each select="rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each>` +
		`<xsl:catch>` + errOut + `</xsl:catch></xsl:try><after/></out></xsl:template>`)
	docs := []string{
		`<doc><rec v="1"/><rec v="x"><sub/></rec><rec v="3"/></doc>`,
		`<doc><rec v="1"/><rec v="x"><sub/></rec><rec v="3"></doc>`,
		`<doc><rec v="1"/><rec v="2"/></doc>`,
		`<doc><rec v="x"/>` + strings.Repeat(`<rec v="1"><sub>s</sub></rec>`, 300) + `</doc>`,
	}
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{BaseURI: "file:///f/doc.xml"}}
	for j, doc := range docs {
		so, st := mainRun(sheet, doc, true, opts)
		to, _ := mainRun(sheet, doc, false, opts)
		if so != to {
			t.Errorf("doc %d:\nstreamed %s\ntree     %s", j, so, to)
		}
		if st.Guards != 0 || st.Runs[streamtrace.Main] == 0 {
			t.Errorf("doc %d: trace %+v", j, st)
		}
	}
}
