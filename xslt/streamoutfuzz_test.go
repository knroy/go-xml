package xslt

// TestStreamOutFuzz is the differential fuzzer for streamed principal output
// (C12): each case of the stream fuzzers' grammars (fuzzMake: a streamable
// xsl:source-document in a named template; fuzzMakeModes: modes and rules,
// applied to a document) gets random serialization parameters -- method,
// indent, omit-xml-declaration, a character map, cdata-section-elements,
// suppress-indentation, an encoding that needs character references, and now
// and then one that holds the result to the end (html-version 5, a doctype,
// standalone, an item separator, normalization) -- and a random shape around
// its output: a literal result element, an html page, an xsl:try that rolls
// back or not. It runs with TransformOptions.Output and without, and the
// bytes Output receives must be those Result.Serialize writes, and the error
// the same, text and all.
//
//	GOXSLT_STREAM_FUZZ=100000 GOXSLT_STREAM_FUZZ_SEED=1 go test ./xslt -run 'TestStreamOutFuzz$' -v -timeout 0
//
// A failing case prints its seed and the command that replays it alone.

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// fuzzOutDecls returns a random xsl:output, and the character map it uses.
func fuzzOutDecls(r *rand.Rand) (decls, method string) {
	p := func(pc int) bool { return r.Intn(100) < pc }
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	method = pick("", "", "xml", "xml", "xml", "html", "xhtml", "text")
	var b strings.Builder
	b.WriteString(`<xsl:output`)
	if method != "" {
		b.WriteString(` method="` + method + `"`)
	}
	if p(40) {
		b.WriteString(` indent="` + pick("yes", "no") + `"`)
	}
	if p(30) {
		b.WriteString(` omit-xml-declaration="yes"`)
	}
	if p(15) {
		b.WriteString(` cdata-section-elements="sub name x"`)
	}
	if p(10) {
		b.WriteString(` suppress-indentation="sub rec"`)
	}
	if p(10) {
		b.WriteString(` encoding="US-ASCII"`)
	}
	if p(5) {
		b.WriteString(` byte-order-mark="yes"`)
	}
	if p(5) {
		b.WriteString(` include-content-type="no"`)
	}
	charMap := p(25)
	if charMap {
		b.WriteString(` use-character-maps="m"`)
	}
	// Each of these holds the result to its end.
	switch r.Intn(40) {
	case 0:
		b.WriteString(` html-version="5"`)
	case 1:
		b.WriteString(` doctype-system="x.dtd"`)
	case 2:
		b.WriteString(` standalone="yes"`)
	case 3:
		b.WriteString(` item-separator="|"`)
	case 4:
		b.WriteString(` normalization-form="NFC"`)
	case 5:
		b.WriteString(` version="1.1"`)
	}
	b.WriteString("/>\n")
	if charMap {
		b.WriteString(`<xsl:character-map name="m">`)
		for _, m := range [][2]string{{"a", "[A]"}, {"&amp;", "&amp;amp;"}, {"&lt;", "&lt;LT&gt;"}, {"1", "one"},
			{"é", "e"}, {" ", "_"}} {
			if p(50) {
				b.WriteString(`<xsl:output-character character="` + m[0] + `" string="` + m[1] + `"/>`)
			}
		}
		b.WriteString("</xsl:character-map>\n")
	}
	return b.String(), method
}

// fuzzOutShape puts decls into sheet and a random shape around the output
// of its named template main, if it has one.
func fuzzOutShape(r *rand.Rand, sheet, decls, method string) string {
	i := strings.Index(sheet, ">\n")
	if i < 0 {
		return sheet
	}
	sheet = sheet[:i+2] + decls + sheet[i+2:]
	const open, end = `<xsl:template name="main">`, "\n</xsl:template>\n</xsl:stylesheet>\n"
	j, k := strings.Index(sheet, open), strings.LastIndex(sheet, end)
	if j < 0 || k < j {
		return sheet
	}
	body := sheet[j+len(open) : k]
	switch r.Intn(6) {
	case 0, 1:
		body = `<res n="1"><xsl:comment>top</xsl:comment>` + body + `<tail/></res>`
	case 2:
		if method == "html" || method == "xhtml" {
			body = `<html><head><title>t</title><meta http-equiv="Content-Type" content="x"/></head><body><p>` + body + `</p><script>a &lt; b</script></body></html>`
		} else {
			body = `<res xmlns="urn:r"><x xmlns="">` + body + `</x>t</res>`
		}
	case 3:
		body = `<res><xsl:try rollback-output="` + []string{"yes", "no"}[r.Intn(2)] + `"><in>` + body +
			`</in><xsl:sequence select="if (` + strconv.Itoa(r.Intn(3)) + ` = 0) then error() else ()"/>` +
			`<xsl:catch><caught/></xsl:catch></xsl:try></res>`
	case 4:
		body = `<xsl:sequence select="1, 'a'"/>` + body + `<xsl:sequence select="2"/>`
	}
	return sheet[:j+len(open)] + body + sheet[k:]
}

// fuzzOutRun runs sheet: the named template main over the documents in dir,
// or, main set, applying templates to doc through TransformReader; to
// Output, or (out false) through Result.Serialize. opened counts what the
// streamed result wrote the start of before its end.
func fuzzOutRun(sheet, doc, dir string, main, out bool, maxNodes int, maxBytes int64) (res string, opened int) {
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		return "SHEET: " + err.Error(), 0
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "COMPILE: " + err.Error(), 0
	}
	var buf bytes.Buffer
	var opts TransformOptions
	if out {
		opts.Output = &buf
		traceOutOpen = func() { opened++ }
		defer func() { traceOutOpen = nil }()
	}
	var r *Result
	if main {
		opts.SourceParseOptions = xdm.ParseOptions{MaxNodes: maxNodes, MaxBytes: maxBytes,
			BaseURI: fileURIOf(filepath.Join(dir, "doc.xml"))}
		r, err = s.TransformReader(context.Background(), strings.NewReader(doc), opts)
	} else {
		docs, _ := NewFileResolver(dir)
		docs.MaxNodes, docs.MaxBytes = maxNodes, maxBytes
		opts.InitialTemplate, opts.Documents = "main", docs
		r, err = s.Transform(context.Background(), nil, opts)
	}
	if err == nil && !out {
		err = r.Serialize(&buf)
	}
	if err != nil {
		return "error: " + err.Error(), opened
	}
	return buf.String(), opened
}

func TestStreamOutFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	n, seed0 := 1000, int64(1)
	if v := os.Getenv("GOXSLT_STREAM_FUZZ"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ: %v", err)
		}
	}
	if v := os.Getenv("GOXSLT_STREAM_FUZZ_SEED"); v != "" {
		var err error
		if seed0, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ_SEED: %v", err)
		}
	}
	dir := t.TempDir()
	tally := map[string]int{}
	for i := 0; i < n; i++ {
		seed := seed0 + int64(i)
		r := rand.New(rand.NewSource(seed*2654435761 + 11))
		var c fuzzCase
		main := r.Intn(3) == 0
		if main {
			c = fuzzMakeModes(seed)
		} else {
			c = fuzzMake(seed)
		}
		decls, method := fuzzOutDecls(r)
		sheet := fuzzOutShape(r, c.sheet, decls, method)
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		want, _ := fuzzOutRun(sheet, c.doc, dir, main, false, c.maxNodes, c.maxBytes)
		if strings.HasPrefix(want, "COMPILE") {
			// The grammars refuse shapes on purpose (XTSE3430); not
			// streamed, the stylesheet still writes a result.
			sheet = strings.ReplaceAll(sheet, ` streamable="yes"`, "")
			want, _ = fuzzOutRun(sheet, c.doc, dir, main, false, c.maxNodes, c.maxBytes)
			tally["not streamable"]++
		}
		got, opened := fuzzOutRun(sheet, c.doc, dir, main, true, c.maxNodes, c.maxBytes)
		tally["cases"]++
		if main {
			tally["applied to a principal input"]++
		}
		switch {
		case strings.HasPrefix(want, "COMPILE") || strings.HasPrefix(want, "SHEET"):
			tally["not compiled"]++
		case strings.HasPrefix(want, "error: "):
			tally["an error"]++
		default:
			tally["a result"]++
		}
		if opened > 0 {
			tally["written as it was made"]++
		}
		if got != want {
			tally["DIFFERENCES"]++
			if tally["DIFFERENCES"] <= 10 {
				t.Errorf("seed %d (main %v): Output differs\nreplay: GOXSLT_STREAM_FUZZ=1 GOXSLT_STREAM_FUZZ_SEED=%d go test ./xslt -run 'TestStreamOutFuzz$' -v\n"+
					"stylesheet:\n%s\ndocument:\n%s\nOutput    %q\nSerialize %q", seed, main, seed, sheet, c.doc, got, want)
			}
		}
	}
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("STREAMOUTFUZZ %-30s %d", k, tally[k])
	}
	t.Logf("STREAMOUTFUZZ seeds %d..%d", seed0, seed0+int64(n)-1)
}
