package xslt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// secWriters is a SecondaryOutput that keeps each result's bytes.
type secWriters struct {
	bufs  map[string]*secBuf
	order []string
	fail  string // the URI suffix whose open fails
	cfail string // the URI suffix whose Close fails
}

type secBuf struct {
	bytes.Buffer
	closed int
	cfail  bool
}

func (b *secBuf) Close() error {
	b.closed++
	if b.cfail {
		return errors.New("disk full")
	}
	return nil
}

func (s *secWriters) open(href string) (io.WriteCloser, error) {
	if s.fail != "" && strings.HasSuffix(href, s.fail) {
		return nil, errors.New("no such directory")
	}
	if s.bufs == nil {
		s.bufs = map[string]*secBuf{}
	}
	if s.bufs[href] != nil {
		return nil, errors.New("opened twice: " + href)
	}
	b := &secBuf{cfail: s.cfail != "" && strings.HasSuffix(href, s.cfail)}
	s.bufs[href] = b
	s.order = append(s.order, href)
	return b, nil
}

// render is the results by URI, sorted.
func (s *secWriters) render(t *testing.T) string {
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range s.bufs {
			if !yield(k) {
				return
			}
		}
	})
	var b strings.Builder
	for _, k := range keys {
		if s.bufs[k].closed != 1 {
			t.Errorf("%s closed %d times", k, s.bufs[k].closed)
		}
		b.WriteString("--- " + filepath.Base(k) + "\n" + s.bufs[k].String() + "\n")
	}
	return b.String()
}

// treeRender is res.Secondary rendered as render renders the writers.
func treeRender(res *Result) (string, error) {
	m := map[string]string{}
	for _, sr := range res.Secondary {
		var b bytes.Buffer
		if err := sr.Serialize(&b, nil); err != nil {
			return "", err
		}
		m[resultKey(sr.Href, sr.BaseURI)] = b.String()
	}
	var b strings.Builder
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}) {
		b.WriteString("--- " + filepath.Base(k) + "\n" + m[k] + "\n")
	}
	return b.String(), nil
}

// secRun runs sheet over src (main: through TransformReader; documents in dir) and renders the
// principal and secondary results: from Result (w nil) or from w.
func secRun(t *testing.T, sheet *Stylesheet, src string, main bool, w *secWriters, dir string) (string, error) {
	// A resolver of its own: one that has parsed a document hands the tree
	// over, and a streamed container then runs on it.
	docs, _ := NewFileResolver(dir)
	opts := TransformOptions{Documents: docs, MaxNodes: -1}
	if w != nil {
		opts.SecondaryOutput = w.open
	}
	var res *Result
	var err error
	if main {
		res, err = sheet.TransformReader(context.Background(), strings.NewReader(src), opts)
	} else {
		var doc *xdm.Tree
		if doc, err = xdm.ParseString(src, xdm.ParseOptions{}); err == nil {
			res, err = sheet.Transform(context.Background(), doc.Root, opts)
		}
	}
	if err != nil {
		return "", err
	}
	sec := ""
	if w == nil {
		if sec, err = treeRender(res); err != nil {
			return "", err
		}
	} else {
		if len(res.Secondary) != 0 {
			t.Errorf("Result.Secondary holds %d results under SecondaryOutput", len(res.Secondary))
		}
		sec = w.render(t)
	}
	return res.String() + "\n" + sec, nil
}

func secSheet(t *testing.T, dir, decls, body string) *Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		decls+body+`</xsl:stylesheet>`, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const secDoc = `<doc><rec k="a" v="1"><sub>s</sub></rec><rec k="b" v="2"/><rec k="c" v="x"/></doc>`

const secMain = `<xsl:mode streamable="yes"/>`

// TestSecondaryOutputMatchesTree: what each SecondaryOutput writer receives is
// what SecondaryResult.Serialize writes for the run without it, error for
// error; each writer is opened once and closed once; and the shapes that can
// stream are written as they are made.
func TestSecondaryOutputMatchesTree(t *testing.T) {
	// The resolver's URIs are of the real path (macOS: /private/var), the
	// stylesheet's base URI is not resolved.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(secDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, decls, body string
		opens             bool // the secondary results are written as made
	}{
		{"per-record", secMain, `<xsl:template match="/"><out><xsl:for-each select="doc/rec"><xsl:result-document href="r{position()}.xml"><r k="{@k}"><xsl:copy-of select="sub"/></r></xsl:result-document></xsl:for-each></out></xsl:template>`, true},
		{"copy-of", secMain, `<xsl:template match="/"><xsl:for-each select="doc/rec"><xsl:result-document href="c{position()}.xml"><w><xsl:copy-of select="."/></w></xsl:result-document></xsl:for-each></xsl:template>`, true},
		{"group", secMain, `<xsl:template match="/"><xsl:for-each-group select="doc/rec" group-adjacent="@v = 'x'"><xsl:result-document href="g{position()}.xml"><g><xsl:copy-of select="current-group()"/></g></xsl:result-document></xsl:for-each-group></xsl:template>`, true},
		{"indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><xsl:result-document href="i.xml"><a><b><c/></b>t<d/></a></xsl:result-document><xsl:result-document href="j.xml"><a><b><c/></b><d/></a></xsl:result-document></xsl:template>`, true},
		{"text", ``, `<xsl:template match="/"><xsl:result-document href="t.txt" method="text"><xsl:for-each select="//rec"><xsl:value-of select="@k"/>;</xsl:for-each></xsl:result-document></xsl:template>`, false},
		{"html", ``, `<xsl:template match="/"><xsl:result-document href="h.html" method="html"><html><head><title>t</title></head><body><p>x<br/></p></body></html></xsl:result-document></xsl:template>`, true},
		{"format", `<xsl:output name="f" omit-xml-declaration="yes" encoding="ISO-8859-1"/><xsl:character-map name="m"><xsl:output-character character="a" string="A"/></xsl:character-map>`, `<xsl:template match="/"><xsl:result-document href="f.xml" format="f" use-character-maps="m"><a>a&#xe9;&#x4e00;</a></xsl:result-document></xsl:template>`, true},
		{"atomics", ``, `<xsl:template match="/"><xsl:result-document href="a.xml"><xsl:sequence select="1, 2"/><e/><xsl:sequence select="3"/></xsl:result-document></xsl:template>`, false},
		{"separator", ``, `<xsl:template match="/"><xsl:result-document href="s.xml" item-separator="|"><xsl:sequence select="1, 2"/><e/></xsl:result-document></xsl:template>`, false},
		{"doctype", ``, `<xsl:template match="/"><xsl:result-document href="d.xml" doctype-system="d.dtd"><a><b/></a></xsl:result-document></xsl:template>`, false},
		{"adaptive", ``, `<xsl:template match="/"><xsl:result-document href="m.txt" method="adaptive"><xsl:sequence select="map{1:2}, 3"/></xsl:result-document></xsl:template>`, false},
		{"base-uri", ``, `<xsl:template match="/"><xsl:result-document href=""><x><y/></x></xsl:result-document></xsl:template>`, true},
		{"nested", ``, `<xsl:template match="/"><xsl:result-document href="o.xml"><o><xsl:result-document href="n.xml"><n/></xsl:result-document><p/></o></xsl:result-document></xsl:template>`, true},
		{"same-href", ``, `<xsl:template match="/"><xsl:for-each select="//rec"><xsl:result-document href="same.xml"><x><y/></x></xsl:result-document></xsl:for-each></xsl:template>`, true},
		{"same-uri", ``, `<xsl:template match="/"><xsl:result-document href="u.xml"><x><y/></x></xsl:result-document><xsl:result-document href="./u.xml"><y/></xsl:result-document></xsl:template>`, true},
		{"nested-same", ``, `<xsl:template match="/"><xsl:result-document href="o.xml"><o><xsl:result-document href="o.xml"><n/></xsl:result-document></o></xsl:result-document></xsl:template>`, false},
		{"principal-too", ``, `<xsl:template match="/"><xsl:result-document><x><y/></x></xsl:result-document><p/></xsl:template>`, true},
		{"read-then-write", ``, `<xsl:template match="/"><xsl:value-of select="count(doc('doc.xml')//rec)"/><xsl:result-document href="doc.xml"><x><y/></x></xsl:result-document></xsl:template>`, false},
		{"write-then-read", ``, `<xsl:template match="/"><xsl:result-document href="doc.xml"><x><y/></x></xsl:result-document><xsl:value-of select="count(doc('doc.xml')//rec)"/></xsl:template>`, true},
		{"body-error", ``, `<xsl:template match="/"><xsl:result-document href="e.xml"><a><b/><xsl:value-of select="xs:integer('x')" xmlns:xs="http://www.w3.org/2001/XMLSchema"/></a></xsl:result-document></xsl:template>`, true},
		{"comment-error", ``, `<xsl:template match="/"><xsl:result-document href="ce.xml"><a><b/><xsl:comment>bad--</xsl:comment></a></xsl:result-document></xsl:template>`, true},
		{"serialize-error", ``, `<xsl:template match="/"><xsl:result-document href="se.xml" doctype-system="a&quot;b'c"><x><y/></x></xsl:result-document></xsl:template>`, false},
		// A caught error inside a result-document: the tree has no result.
		{"try-inside", ``, `<xsl:template match="/"><xsl:try><xsl:result-document href="ti.xml"><a><b/><xsl:sequence select="error()"/></a></xsl:result-document><xsl:catch><caught/></xsl:catch></xsl:try><xsl:result-document href="after.xml"><after><z/></after></xsl:result-document></xsl:template>`, true},
		// A caught error after one completed: the tree keeps it.
		{"try-after", ``, `<xsl:template match="/"><xsl:try><xsl:result-document href="ta.xml"><a/></xsl:result-document><xsl:sequence select="error()"/><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template>`, false},
		// A try inside the body: the result-document itself goes on.
		{"try-body", ``, `<xsl:template match="/"><xsl:result-document href="tb.xml"><a><b/><xsl:try><c><xsl:sequence select="error()"/></c><xsl:catch><caught/></xsl:catch></xsl:try></a></xsl:result-document></xsl:template>`, true},
		// Streamed containers: a re-run on the tree takes back the failed
		// run's results, which are held until the container ends.
		{"container", ``, `<xsl:template match="/"><xsl:source-document href="doc.xml" streamable="yes"><xsl:for-each select="doc/rec"><xsl:result-document href="sd{position()}.xml"><r k="{@k}"/></xsl:result-document></xsl:for-each></xsl:source-document><xsl:result-document href="next.xml"><n/></xsl:result-document></xsl:template>`, false},
		{"container-rerun", ``, `<xsl:template match="/"><xsl:try><xsl:source-document href="doc.xml" streamable="yes"><xsl:result-document href="rr.xml"><a/></xsl:result-document><xsl:value-of select="sum(doc/rec/@v)"/></xsl:source-document><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template>`, false},
		{"container-kept", ``, `<xsl:template match="/"><xsl:source-document href="doc.xml" streamable="yes"><xsl:for-each select="doc/rec"><xsl:result-document href="k{position()}.xml"><xsl:sequence select="."/></xsl:result-document></xsl:for-each></xsl:source-document></xsl:template>`, false},
		{"merge", ``, `<xsl:template match="/"><xsl:merge><xsl:merge-source for-each-source="'doc.xml'" select="doc/rec" streamable="yes"><xsl:merge-key select="string(@k)"/></xsl:merge-source><xsl:merge-action><xsl:result-document href="mg{current-merge-key()}.xml"><m><xsl:value-of select="current-merge-key()"/></m></xsl:result-document></xsl:merge-action></xsl:merge></xsl:template>`, false},
	} {
		sheet := secSheet(t, dir, c.decls, c.body)
		for _, main := range []bool{false, true} {
			if main && c.decls != secMain {
				continue
			}
			want, werr := secRun(t, sheet, secDoc, main, nil, dir)
			opened := 0
			traceOutOpen = func() { opened++ }
			w := &secWriters{}
			before := streamtrace.Snapshot()
			got, gerr := secRun(t, sheet, secDoc, main, w, dir)
			tr := streamtrace.Snapshot().Sub(before)
			traceOutOpen = nil
			if want := main || strings.HasPrefix(c.name, "container") || c.name == "merge"; (tr.Streamed() > 0) != want || tr.Guards != 0 {
				t.Errorf("%s (main %v): streamed %d, guards %d", c.name, main, tr.Streamed(), tr.Guards)
			}
			switch {
			case (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error():
				// A serialization error is Serialize's without SecondaryOutput.
				if !(c.name == "serialize-error" && werr == nil && gerr != nil && strings.Contains(gerr.Error(), "SEPM0016")) {
					t.Errorf("%s (main %v): error %v, want %v", c.name, main, gerr, werr)
				}
			case werr == nil && got != want:
				t.Errorf("%s (main %v):\n got %q\nwant %q", c.name, main, got, want)
			}
			if gerr != nil {
				for k, b := range w.bufs {
					if b.closed != 1 {
						t.Errorf("%s (main %v): %s closed %d times after an error", c.name, main, k, b.closed)
					}
				}
			}
			if (opened > 0) != c.opens {
				t.Errorf("%s (main %v): %d opened before their end, want written as made %v", c.name, main, opened, c.opens)
			}
		}
	}
}

// TestSecondaryOutputHeldUntilDone: what cannot be taken back is not written
// early. A result-document whose body fails under a catching xsl:try was
// never opened; a streamed container's results are written once it ends.
func TestSecondaryOutputHeldUntilDone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(secDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	sheet := secSheet(t, dir, ``, `<xsl:template match="/"><xsl:try><xsl:result-document href="ti.xml"><a><xsl:sequence select="error()"/></a></xsl:result-document><xsl:catch/></xsl:try></xsl:template>`)
	w := &secWriters{}
	if _, err := secRun(t, sheet, secDoc, false, w, dir); err != nil || len(w.bufs) != 0 {
		t.Errorf("try: err %v, opened %v", err, w.order)
	}
	before := streamtrace.Snapshot()
	sheet = secSheet(t, dir, ``, `<xsl:template match="/"><xsl:source-document href="doc.xml" streamable="yes"><xsl:result-document href="a.xml"><a><b/></a></xsl:result-document><xsl:value-of select="sum(doc/rec/@v)"/></xsl:source-document></xsl:template>`)
	w = &secWriters{}
	_, err := secRun(t, sheet, secDoc, false, w, dir)
	tr := streamtrace.Snapshot().Sub(before)
	if err == nil || xdm.ErrorCode(err) != "FORG0001" || len(w.bufs) != 0 || tr.Reruns == 0 {
		t.Errorf("re-run: err %v, opened %v, re-runs %d", err, w.order, tr.Reruns)
	}
}

// TestSecondaryOutputErrors: the function's error and Close's are the
// transform's; a nested fn:transform does not write through it; written
// nodes are handed back to MaxNodes, as under Output, and held ones are not.
func TestSecondaryOutputErrors(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		body string
		held bool
	}{
		{`<xsl:template match="/"><xsl:for-each select="1 to 1000"><xsl:result-document href="c{.}.xml"><r><a/><b/></r></xsl:result-document></xsl:for-each></xsl:template>`, false},
		{`<xsl:template match="/"><xsl:for-each select="1 to 1000"><xsl:result-document href="c{.}.xml" item-separator=" "><r><a/><b/></r></xsl:result-document></xsl:for-each></xsl:template>`, true},
	} {
		capped := secSheet(t, dir, ``, c.body)
		doc, _ := xdm.ParseString(`<d/>`, xdm.ParseOptions{})
		for _, sec := range []bool{false, true} {
			opts := TransformOptions{MaxNodes: 2000}
			if sec {
				opts.SecondaryOutput = (&secWriters{}).open
			}
			_, err := capped.Transform(context.Background(), doc.Root, opts)
			if refused := err != nil && strings.Contains(err.Error(), "XPDY0130"); refused != (!sec || c.held) {
				t.Errorf("MaxNodes 2000, SecondaryOutput %v, held %v: %v", sec, c.held, err)
			}
		}
	}
	sheet := secSheet(t, dir, ``, `<xsl:template match="/"><xsl:result-document href="ok.xml"><a/></xsl:result-document><xsl:result-document href="bad.xml"><b/></xsl:result-document></xsl:template>`)
	w := &secWriters{fail: "bad.xml"}
	_, err := secRun(t, sheet, secDoc, false, w, dir)
	if err == nil || !strings.Contains(err.Error(), "SecondaryOutput(") || !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("open error: %v", err)
	}
	w = &secWriters{cfail: "ok.xml"}
	if _, err = secRun(t, sheet, secDoc, false, w, dir); err == nil || !strings.Contains(err.Error(), "disk full") || len(w.bufs) != 1 {
		t.Errorf("close error: %v, opened %v", err, w.order)
	}
	// Held (try) and written later: the same errors.
	held := secSheet(t, dir, ``, `<xsl:template match="/"><xsl:try><xsl:result-document href="bad.xml"><b/></xsl:result-document><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`)
	w = &secWriters{fail: "bad.xml"}
	if _, err = secRun(t, held, secDoc, false, w, dir); err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("held open error: %v (not caught by xsl:try)", err)
	}
	nested := secSheet(t, dir, ``, `<xsl:template match="/"><out><xsl:sequence select="count(map:keys(transform(map{'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;&gt;&lt;xsl:template match=&quot;/&quot;&gt;&lt;xsl:result-document href=&quot;inner.xml&quot;&gt;&lt;i/&gt;&lt;/xsl:result-document&gt;&lt;/xsl:template&gt;&lt;/xsl:stylesheet&gt;', 'source-node': .})))" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/></out></xsl:template>`)
	w = &secWriters{}
	want, werr := secRun(t, nested, secDoc, false, nil, dir)
	got, gerr := secRun(t, nested, secDoc, false, w, dir)
	if werr != nil || gerr != nil || got != want || len(w.bufs) != 0 {
		t.Errorf("nested transform: %q (%v), want %q (%v), opened %v", got, gerr, want, werr, w.order)
	}
}
