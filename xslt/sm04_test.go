package xslt

import (
	"bytes"
	"context"
	"html"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

const sm04Head = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">`

// math.MinInt64 is its own negation: the speller recursed on it until the
// stack overflowed, and a start-at of it wrapped s-1 to math.MaxInt64.
func TestSM04SpellMinInt64(t *testing.T) {
	const want = "minus 9223372036854775808"
	if got := spellNumber(math.MinInt64, false); got != want {
		t.Errorf("spellNumber: %q", got)
	}
	if got := spellNumberDE(math.MinInt64); got != want {
		t.Errorf("spellNumberDE: %q", got)
	}
	got := rebaseNumbers([]*big.Int{big.NewInt(0)}, []int64{math.MinInt64})[0]
	if w, _ := new(big.Int).SetString("-9223372036854775809", 10); got.Cmp(w) != 0 {
		t.Errorf("rebaseNumbers: %s", got)
	}
}

// xsl:message text is kept until the transform returns: 200 messages of
// 1,000 bytes were held under a 100,000-byte bound.
func TestSM04MessageBytesCharged(t *testing.T) {
	sheet := func(n string) string {
		return sm04Head + `<xsl:variable name="s" select="string-join((1 to 100) ! 'xxxxxxxxxx')"/>` +
			`<xsl:template match="/"><xsl:for-each select="1 to ` + n + `"><xsl:message select="$s"/></xsl:for-each><done/></xsl:template></xsl:stylesheet>`
	}
	opts := TransformOptions{MaxBytes: 100000}
	if _, err := optsRun(t, sheet("200"), `<r/>`, opts); !refused(err, "bytes") {
		t.Fatalf("want the byte refusal, got %v", err)
	}
	if got, err := optsRun(t, sheet("20"), `<r/>`, opts); err != nil || got != "<done/>" {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}

// A constructor holding xsl:on-empty checked the item bound only after every
// sibling had run: 50 x 1,000 items were built under a bound of 10,000 and
// the trailing error() was reached.
func TestSM04ConditionalSequenceItemsChecked(t *testing.T) {
	sheet := sm04Head + `<xsl:variable name="v" select="1 to 1000"/><xsl:template match="/">` +
		`<xsl:variable name="big" as="item()*">` + strings.Repeat(`<xsl:sequence select="$v"/>`, 50) +
		`<xsl:sequence select="error(QName('', 'LATE'))"/><xsl:on-empty select="'empty'"/></xsl:variable>` +
		`<out><xsl:value-of select="count($big)"/></out></xsl:template></xsl:stylesheet>`
	if _, err := optsRun(t, sheet, `<r/>`, TransformOptions{MaxItems: 10000}); !refused(err, "items") {
		t.Fatalf("want the item refusal, got %v", err)
	}
}

// Each level="any" instruction kept a whole-tree table for the rest of the
// transform, uncharged. Under a bound one table fits and a second does not:
// the second instruction then walks per execution, retaining nothing, and the
// answers are unchanged.
func TestSM04NumberAnyTablesBounded(t *testing.T) {
	const n = 300
	sheet := sm04Head + `<xsl:template match="/"><out><xsl:for-each select="r/e">` +
		`<xsl:number level="any" count="e"/>.<xsl:number level="any" count="e" from="r"/>,</xsl:for-each></out></xsl:template></xsl:stylesheet>`
	sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := xdm.ParseString(`<r>`+strings.Repeat(`<e/>`, n)+`</r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	calls := func(maxItems int) int64 {
		ctx := &errCountCtx{Context: context.Background(), limit: 1 << 40}
		res, err := st.Transform(ctx, d.Root, TransformOptions{MaxItems: maxItems})
		if err != nil {
			t.Fatalf("MaxItems %d: %v", maxItems, err)
		}
		if out := res.String(); !strings.Contains(out, "<out>1.1,2.2,") || !strings.Contains(out, ",300.300,</out>") {
			t.Fatalf("MaxItems %d: %q", maxItems, out)
		}
		return ctx.calls
	}
	if c := calls(0); c > 20*n {
		t.Fatalf("both tables under the default bound: %d Err calls", c)
	}
	if c := calls(400); c < n*n/4 {
		t.Fatalf("second table kept past the bound: only %d Err calls", c)
	}
}

// inherit-namespaces="no" undeclared K prefixes on each of M children, one
// binding at a time and uncharged: K x M bindings outside the byte bound.
func TestSM04InheritNoUndeclarationsCharged(t *testing.T) {
	sheet := func(k, m string) string {
		return sm04Head + `<xsl:template match="/"><xsl:element name="r" inherit-namespaces="no">` +
			`<xsl:for-each select="1 to ` + k + `"><xsl:namespace name="p{.}" select="'urn:x' || ."/></xsl:for-each>` +
			`<xsl:for-each select="1 to ` + m + `"><xsl:element name="c"/></xsl:for-each>` +
			`</xsl:element></xsl:template></xsl:stylesheet>`
	}
	opts := TransformOptions{MaxBytes: 60000}
	if _, err := optsRun(t, sheet("60", "60"), `<a/>`, opts); !refused(err, "bytes") {
		t.Fatalf("want the byte refusal, got %v", err)
	}
	got, err := optsRun(t, sheet("2", "2"), `<a/>`, opts)
	if err != nil || got != `<r xmlns:p1="urn:x1" xmlns:p2="urn:x2"><c/><c/></r>` {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}

// fn:snapshot copies every ancestor's attributes per item, and fn:copy-of and
// xsl:copy-of a subtree's values, charged to the node budget only: 50
// snapshots under a root with 80 KB of attributes held 4 MB under 1 MB.
func TestSM04CopiedValueBytesCharged(t *testing.T) {
	var src strings.Builder
	src.WriteString(`<r`)
	for i := range 20 {
		src.WriteString(` a` + string(rune('a'+i)) + `="` + strings.Repeat("x", 4000) + `"`)
	}
	src.WriteString(`>` + strings.Repeat(`<l/>`, 50) + `</r>`)
	opts := TransformOptions{MaxBytes: 1 << 20}
	for _, sel := range []string{`//l/snapshot()`, `let $r := /r return (1 to 50) ! copy-of($r)`} {
		sheet := sm04Head + `<xsl:template match="/"><xsl:variable name="v" select="` + sel + `"/><out n="{count($v)}"/></xsl:template></xsl:stylesheet>`
		if _, err := optsRun(t, sheet, src.String(), opts); !refused(err, "bytes") {
			t.Errorf("%s: want the byte refusal, got %v", sel, err)
		}
	}
	sheet := sm04Head + `<xsl:template match="/"><out><xsl:for-each select="1 to 50"><xsl:copy-of select="$r"/></xsl:for-each></out></xsl:template>` +
		`<xsl:variable name="r" select="/r"/></xsl:stylesheet>`
	if _, err := optsRun(t, sheet, src.String(), opts); !refused(err, "bytes") {
		t.Errorf("xsl:copy-of: want the byte refusal, got %v", err)
	}
	if got, err := optsRun(t, sm04Head+`<xsl:template match="/"><xsl:variable name="v" select="(//l)[1]/snapshot()"/><out n="{count($v)}"/></xsl:template></xsl:stylesheet>`,
		src.String(), opts); err != nil || got != `<out n="1"/>` {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}

// fn:transform's stylesheet-text was parsed with a fresh entity allowance per
// call; stylesheet-location and fn:parse-xml share the evaluation's. Four
// calls of 300,000 expanded bytes pass the default 1 MB only together.
func TestSM04StylesheetTextEntityBudgetShared(t *testing.T) {
	fr, err := NewFileResolver(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fr.AllowDOCTYPE = true
	nested := `<!DOCTYPE xsl:stylesheet [<!ENTITY a "` + strings.Repeat("x", 1000) + `"><!ENTITY b "` + strings.Repeat("&a;", 10) +
		`"><!ENTITY c "` + strings.Repeat("&b;", 6) + `">]>` +
		`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template name="xsl:initial-template"><o>` +
		strings.Repeat("&c;", 5) + `</o></xsl:template></xsl:stylesheet>`
	run := func(calls string) error {
		sheet := sm04Head + `<xsl:variable name="t" as="xs:string"><xsl:text>` + html.EscapeString(nested) + `</xsl:text></xsl:variable>` +
			`<xsl:template name="xsl:initial-template"><r><xsl:value-of select="for $i in 1 to ` + calls +
			` return string-length(string(transform(map{'stylesheet-text': $t, 'cache': false(), 'initial-template': QName('http://www.w3.org/1999/XSL/Transform','initial-template')})?output))"/></r></xsl:template></xsl:stylesheet>`
		sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(sd.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.Transform(context.Background(), nil, TransformOptions{Documents: fr, InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
		return err
	}
	if err := run("1"); err != nil {
		t.Fatalf("one call: %v", err)
	}
	if err := run("4"); err == nil || !strings.Contains(err.Error(), "entity expansion") {
		t.Fatalf("want the shared entity refusal, got %v", err)
	}
}

// Raw text is for script and style serialized as HTML elements only: an
// svg:style or an island's x:script is foreign content an HTML parser reads
// as ordinary text, so its "<" was markup.
func TestSM04ForeignScriptStyleEscaped(t *testing.T) {
	d, err := xdm.ParseString(`<html><body><svg:svg xmlns:svg="http://www.w3.org/2000/svg"><svg:style>&lt;img src=x onerror=alert(1)&gt;</svg:style></svg:svg>`+
		`<x:script xmlns:x="urn:x">&lt;b&gt;</x:script><script>&lt;i&gt;</script></body></html>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"5", "4.01"} {
		var b bytes.Buffer
		if err := Serialize(&b, xdm.One(d.Root), OutputSettings{Method: "html", HTMLVersion: v}, nil); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		out := b.String()
		if strings.Contains(out, "<img") || strings.Contains(out, "<b>") ||
			!strings.Contains(out, "&lt;img") || !strings.Contains(out, "<script><i></script>") {
			t.Errorf("html %s: %s", v, out)
		}
	}
}

// The streamed merge snapshotted each record as it was read and held a
// group's items, neither charged: 300 equal-key records formed one group of
// 300 under an item bound of 100, and 300 distinct-key records were copied
// twice each under a node bound of 1,000. The tree refuses both.
func TestSM04StreamedMergeCharged(t *testing.T) {
	dir := t.TempDir()
	var same, distinct strings.Builder
	for i := range 300 {
		same.WriteString(`<r k="1"><v>x</v></r>`)
		distinct.WriteString(`<r k="` + strconv.Itoa(i) + `"><v>x</v></r>`)
	}
	for name, doc := range map[string]string{"same.xml": same.String(), "distinct.xml": distinct.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`<a>`+doc+`</a>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		doc, action string
		opts        TransformOptions
		what        string
	}{
		{"same.xml", `<n/>`, TransformOptions{MaxItems: 100}, "items"},
		{"distinct.xml", `<n/>`, TransformOptions{MaxNodes: 1000}, "nodes"},
		{"distinct.xml", `<n/>`, TransformOptions{}, ""},
	} {
		sd, err := xdm.ParseString(sm04Head+`<xsl:template name="main"><out><xsl:merge>`+
			`<xsl:merge-source name="A" for-each-source="'`+c.doc+`'" streamable="yes" select="a/r"><xsl:merge-key select="xs:integer(@k)"/></xsl:merge-source>`+
			`<xsl:merge-action>`+c.action+`</xsl:merge-action></xsl:merge></out></xsl:template></xsl:stylesheet>`,
			xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(sd.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// A fresh resolver each time: one that has read the document holds
		// its tree, and the merge is then not streamed.
		docs, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		c.opts.InitialTemplate, c.opts.Documents = "main", docs
		before := streamtrace.Snapshot()
		_, err = st.Transform(context.Background(), nil, c.opts)
		if streamtrace.Snapshot().Sub(before).Runs[streamtrace.Merge] == 0 {
			t.Fatalf("%s: the merge did not stream (%v)", c.what, err)
		}
		if c.what == "" {
			if err != nil {
				t.Fatalf("control: %v", err)
			}
			continue
		}
		if !refused(err, c.what) {
			t.Errorf("%s: want the refusal, got %v", c.what, err)
		}
	}
}
