package xslt

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamFuzzG6 is the differential fuzzer for what gap lane G6 streams:
// xsl:result-document in a streamed body (its secondary results compared
// too, and a re-run after one), groupings over copies (the copies' order
// across their trees), and the pattern ".". Each case runs on the tree and
// streamed; outputs, secondary results and error codes must agree.
// GOXSLT_STREAM_FUZZ sets the count (default 300), GOXSLT_STREAM_FUZZ_SEED
// the first seed.
func TestStreamFuzzG6(t *testing.T) {
	n, seed := 300, int64(1)
	if v := os.Getenv("GOXSLT_STREAM_FUZZ"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("GOXSLT_STREAM_FUZZ_SEED"); v != "" {
		seed, _ = strconv.ParseInt(v, 10, 64)
	}
	dir := t.TempDir()
	diffs, streamed, reruns, compileErr := 0, 0, 0, 0
	for s := seed; s < seed+int64(n); s++ {
		r := rand.New(rand.NewSource(s))
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(g6Doc(r)), 0o644); err != nil {
			t.Fatal(err)
		}
		body := g6Body(r, 2)
		want, _ := resultDocRun(t, body, dir, false)
		if strings.HasPrefix(want, "COMPILE") {
			compileErr++
			continue
		}
		got, tr := resultDocRun(t, body, dir, true)
		if got != want || tr.Guards != 0 {
			diffs++
			if diffs <= 5 {
				t.Errorf("seed %d: guards %d\nbody %s\nstreamed %q\ntree     %q", s, tr.Guards, body, got, want)
			}
		}
		if tr.Runs[streamtrace.Core] > 0 {
			streamed++
		}
		if tr.Reruns > 0 {
			reruns++
		}
	}
	t.Logf("G6FUZZ cases %d, not compiled %d, differences %d, streamed %d, re-runs %d", n, compileErr, diffs, streamed, reruns)
}

// g6Doc is a record document: runs of keys, values (some not numbers),
// nested records, text.
func g6Doc(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 1 + r.Intn(8) {
		v := strconv.Itoa(r.Intn(20) - 3)
		if r.Intn(12) == 0 {
			v = "x"
		}
		fmt.Fprintf(&b, `<rec k="%c" v="%s" n="%d">`, 'a'+rune(r.Intn(3)), v, i)
		if r.Intn(3) == 0 {
			fmt.Fprintf(&b, `<sub x="%d">t%d</sub>`, r.Intn(5), i)
		}
		if r.Intn(4) == 0 {
			b.WriteString(`<rec k="z" v="1"/>`)
		}
		b.WriteString("</rec>")
		if r.Intn(5) == 0 {
			b.WriteString("<other/>")
		}
	}
	b.WriteString("</doc>")
	return b.String()
}

// g6Body is a container body.
func g6Body(r *rand.Rand, depth int) string {
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	inner := pick(
		`<xsl:copy-of select="doc/rec"/>`,
		`<xsl:value-of select="sum(doc/rec/@n)"/>`,
		`<xsl:for-each select="doc/rec"><r k="{@k}"><xsl:copy-of select="sub"/></r></xsl:for-each>`,
		`<xsl:for-each-group select="doc/rec" group-ending-with="."><g n="{position()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group>`,
		`<xsl:for-each-group select="doc/*" group-starting-with="."><g><xsl:value-of select="count(current-group())"/></g></xsl:for-each-group>`,
		`<xsl:for-each-group select="doc/rec/copy-of()" group-adjacent="@k"><g k="{current-grouping-key()}" n="{current-group()/@n}"/></xsl:for-each-group>`,
		`<xsl:for-each-group select="doc/rec/copy-of()" group-by="@k"><g k="{current-grouping-key()}" n="{current-group()/@n}" s="{current-group()/sub/@x}"/></xsl:for-each-group>`,
		`<xsl:for-each-group select="copy-of(doc/rec)" group-by="@k"><xsl:sort select="current-grouping-key()"/><g n="{count(current-group())}"><xsl:copy-of select="current-group()[1]"/></g></xsl:for-each-group>`,
		`<xsl:for-each-group select="doc/rec/copy-of()" group-adjacent="string(number(@v) idiv 5)"><g d="{current-group()/@k}" v="{current-group()/@v}"/></xsl:for-each-group>`,
		`<xsl:value-of select="sum(doc/rec/@v)"/>`, // FORG0001 on "x": a re-run
	)
	if depth == 0 || r.Intn(3) == 0 {
		return inner
	}
	href := pick(`r.xml`, `r{count(doc/rec)}.xml`, `same.xml`)
	attrs := pick(``, ` method="text"`, ` indent="yes"`, ` omit-xml-declaration="yes"`)
	switch r.Intn(4) {
	case 0: // the result document consumes
		return `<xsl:result-document href="` + href + `"` + attrs + `><out>` + g6Body(r, depth-1) + `</out></xsl:result-document>`
	case 1: // a motionless one, then a consumer
		return `<xsl:result-document href="m.xml"` + attrs + `><m/></xsl:result-document>` + g6Body(r, depth-1)
	case 2: // one per record
		return `<xsl:for-each select="doc/rec"><xsl:result-document href="` + pick(`r{position()}.xml`, `same.xml`) + `"` + attrs +
			`><xsl:copy-of select="."/></xsl:result-document></xsl:for-each>`
	}
	return `<w>` + g6Body(r, depth-1) + `</w>`
}
