package xslt

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// TestSecondaryOutputFuzz is the differential fuzzer for
// TransformOptions.SecondaryOutput: random record documents (g6Doc) x
// stylesheets writing xsl:result-document in a streamed xsl:source-document
// (G6's bodies, which re-run on "x"), under a streamed principal input, and
// on the tree; under xsl:try with errors inside the result and after it,
// nested, with colliding URIs (XTDE1490). Each case runs without
// SecondaryOutput and with it: the principal result, every secondary result
// by URI (writer bytes against SecondaryResult.Serialize) and the error text
// must agree, and every writer is opened once and closed once.
// GOXSLT_STREAM_FUZZ sets the count (default 300), GOXSLT_STREAM_FUZZ_SEED
// the first seed.
func TestSecondaryOutputFuzz(t *testing.T) {
	n, seed := 300, int64(1)
	if v := os.Getenv("GOXSLT_STREAM_FUZZ"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("GOXSLT_STREAM_FUZZ_SEED"); v != "" {
		seed, _ = strconv.ParseInt(v, 10, 64)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	diffs, compileErr, streamed, reruns, written, errs, mains := 0, 0, 0, 0, 0, 0, 0
	for s := seed; s < seed+int64(n); s++ {
		r := rand.New(rand.NewSource(s))
		doc := g6Doc(r)
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		main := r.Intn(3) == 0
		sheet := h3Sheet(r, main)
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
		if err != nil {
			t.Fatal(err)
		}
		s1, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			compileErr++
			continue
		}
		if main {
			mains++
		}
		want, _ := h3Run(t, s1, dir, doc, main, nil)
		w := &secWriters{}
		before := streamtrace.Snapshot()
		got, gerr := h3Run(t, s1, dir, doc, main, w)
		tr := streamtrace.Snapshot().Sub(before)
		if got != want || tr.Guards != 0 {
			diffs++
			if diffs <= 5 {
				t.Errorf("seed %d: guards %d\nsheet %s\nSecondaryOutput %q\nResult.Secondary %q", s, tr.Guards, sheet, got, want)
			}
		}
		if tr.Streamed() > 0 {
			streamed++
		}
		if tr.Reruns > 0 {
			reruns++
		}
		if gerr {
			errs++
		}
		written += len(w.bufs)
	}
	t.Logf("SECFUZZ cases %d, not compiled %d, differences %d, streamed %d (principal input %d), re-runs %d, errors %d, results written %d",
		n, compileErr, diffs, streamed, mains, reruns, errs, written)
}

// h3Run renders one run: the principal result, then each secondary result
// by URI (from w, or from Result.Secondary when w is nil), or the error, and
// whether it failed.
func h3Run(t *testing.T, sheet *Stylesheet, dir, doc string, main bool, w *secWriters) (string, bool) {
	docs, _ := NewFileResolver(dir)
	opts := TransformOptions{Documents: docs}
	if w != nil {
		opts.SecondaryOutput = w.open
	}
	var res *Result
	var err error
	if main {
		res, err = sheet.TransformReader(context.Background(), strings.NewReader(doc), opts)
	} else {
		opts.InitialTemplate = "main"
		res, err = sheet.Transform(context.Background(), nil, opts)
	}
	sec := ""
	if err == nil {
		if w == nil {
			sec, err = treeRender(res)
		} else {
			if len(res.Secondary) != 0 {
				t.Errorf("Result.Secondary holds %d results under SecondaryOutput", len(res.Secondary))
			}
			sec = w.render(t)
		}
	} else if w != nil {
		for k, b := range w.bufs {
			if b.closed != 1 {
				t.Errorf("%s closed %d times after an error", k, b.closed)
			}
		}
	}
	if err != nil {
		return "ERROR " + err.Error(), true
	}
	return res.String() + "\n" + sec, false
}

// h3Sheet is a stylesheet: a named template main around a streamed
// xsl:source-document, with result documents on the tree before and after
// it, or (main) a streamable mode's rule for the document node.
func h3Sheet(r *rand.Rand, main bool) string {
	head := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">`
	if main {
		return head + `<xsl:mode streamable="yes"/><xsl:template match="/"><top>` + h3Body(r, 2, true) +
			`</top></xsl:template></xsl:stylesheet>`
	}
	return head + `<xsl:template name="main"><top>` + h3Tree(r) +
		`<xsl:source-document streamable="yes" href="doc.xml">` + h3Body(r, 2, true) +
		`</xsl:source-document>` + h3Tree(r) + `</top></xsl:template></xsl:stylesheet>`
}

// h3Tree is tree-mode content around the container: result documents, under
// xsl:try or not, failing inside or after.
func h3Tree(r *rand.Rand) string {
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	rd := `<xsl:result-document href="` + pick("t.xml", "u.xml", "same.xml") + `"` + pick(``, ` indent="yes"`, ` method="text"`) +
		`><a><b/>` + pick(``, `<xsl:sequence select="error()"/>`, `<c>t</c>`) + `</a></xsl:result-document>`
	switch r.Intn(5) {
	case 0:
		return ``
	case 1:
		return rd
	case 2:
		return `<xsl:try>` + rd + pick(``, `<xsl:sequence select="error()"/>`) + `<xsl:catch><caught/></xsl:catch></xsl:try>`
	case 3: // nested
		return `<xsl:result-document href="` + pick("o.xml", "same.xml") + `"><o><p/>` + rd + `<q/></o></xsl:result-document>`
	}
	return `<xsl:for-each select="1 to 3"><xsl:result-document href="` + pick("n{.}.xml", "same.xml") + `"><n><v><xsl:value-of select="."/></v></n></xsl:result-document></xsl:for-each>`
}

// h3Body is a streamed body: G6's, or result documents that consume, per
// record, under xsl:try, nested.
func h3Body(r *rand.Rand, depth int, top bool) string {
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	attrs := pick(``, ` method="text"`, ` indent="yes"`, ` omit-xml-declaration="yes"`)
	if depth == 0 || r.Intn(4) == 0 {
		return g6Body(r, depth)
	}
	switch r.Intn(5) {
	case 0: // one per record, URIs from its key (repeated keys collide)
		return `<xsl:for-each select="doc/rec"><xsl:result-document href="` + pick(`k{@k}.xml`, `r{position()}.xml`) + `"` + attrs +
			`><r n="{@n}"><xsl:copy-of select="sub"/><v><xsl:value-of select="@v"/></v></r></xsl:result-document></xsl:for-each>`
	case 1: // a group per result
		return `<xsl:for-each-group select="doc/rec" group-adjacent="@k"><xsl:result-document href="g{position()}.xml"` + attrs +
			`><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:result-document></xsl:for-each-group>`
	case 2: // fails inside, after output (FORG0001 on "x")
		return `<xsl:result-document href="e.xml"` + attrs + `><a><b/><xsl:value-of select="sum(doc/rec/@v)"/></a></xsl:result-document>`
	case 3: // under xsl:try
		return `<xsl:try>` + h3Body(r, depth-1, false) + `<xsl:catch><caught/></xsl:catch></xsl:try>`
	}
	if !top {
		return g6Body(r, depth)
	}
	return `<xsl:result-document href="` + pick(`w.xml`, `k{count(doc/rec)}.xml`) + `"` + attrs + `><w>` + h3Body(r, depth-1, false) + `</w></xsl:result-document>`
}
