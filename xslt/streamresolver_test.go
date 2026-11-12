package xslt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// memDocs is an in-memory resolver: docs[uri] is each document's text, or gen
// makes it as it is read. Its document URI is "http://mem/" + uri.
type memDocs struct {
	docs                    map[string]string
	gen                     func() io.Reader
	opens, closes, resolves int
	// onRead, if set, is called before each Read of an opened document.
	onRead func()
}

func (m *memDocs) text(uri string) io.Reader {
	if m.gen != nil {
		return m.gen()
	}
	return strings.NewReader(m.docs[uri])
}

func (m *memDocs) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	m.resolves++
	u := "http://mem/" + uri
	return xdm.Parse(m.text(uri), xdm.ParseOptions{BaseURI: u, DocumentURI: u})
}

// plainDocs is memDocs without OpenDocument.
type plainDocs struct{ m *memDocs }

func (p plainDocs) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	return p.m.ResolveDocument(uri, base)
}

func (m *memDocs) OpenDocument(uri, base string) (io.ReadCloser, string, error) {
	m.opens++
	return &memReader{m: m, r: m.text(uri)}, "http://mem/" + uri, nil
}

type memReader struct {
	m *memDocs
	r io.Reader
}

func (r *memReader) Read(p []byte) (int, error) {
	if r.m.onRead != nil {
		r.m.onRead()
	}
	return r.r.Read(p)
}

func (r *memReader) Close() error { r.m.closes++; return nil }

// memRecords is a reader of <a> holding n records, made as it is read: the
// document is never held whole.
func memRecords(n int) io.Reader {
	i := -1
	return io.MultiReader(strings.NewReader("<a>"), readerFunc(func(p []byte) (int, error) {
		if i++; i >= n {
			return 0, io.EOF
		}
		return copy(p, fmt.Sprintf("<r v=\"%d\"><name>record %d</name></r>\n", i%10, i)), nil
	}), strings.NewReader("</a>"))
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// memRun runs the template "main" of src over docs, reporting whether
// xsl:source-document streamed.
func memRun(t *testing.T, ctx context.Context, src string, docs xpath.DocumentResolver) (string, bool, error) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: "http://mem/s.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	traceBursts = func(string) { ran = true }
	defer func() { traceBursts = nil }()
	res, err := sheet.Transform(ctx, nil, TransformOptions{InitialTemplate: "main", Documents: docs})
	if err != nil {
		return "", ran, err
	}
	return res.String(), ran, nil
}

const memTotal = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><xsl:source-document streamable="yes" href="big.xml">
	<u><xsl:value-of select="document-uri(.)"/></u>
	<xsl:iterate select="a/r"><xsl:param name="t" select="0"/>
	<xsl:on-completion><total><xsl:value-of select="$t"/></total></xsl:on-completion>
	<xsl:next-iteration><xsl:with-param name="t" select="$t + @v"/></xsl:next-iteration>
	</xsl:iterate></xsl:source-document></xsl:template></xsl:stylesheet>`

// A StreamingDocumentResolver's document streams from its reader: the tree
// is never asked for, and the document has the URI OpenDocument gave it.
func TestStreamingResolverStreams(t *testing.T) {
	m := &memDocs{gen: func() io.Reader { return memRecords(20000) }}
	out, ran, err := memRun(t, context.Background(), memTotal, m)
	if err != nil || !ran || m.resolves != 0 || m.opens != 1 || m.closes != 1 {
		t.Fatalf("got %q, %v; streamed %v, resolves %d, opens %d, closes %d", out, err, ran, m.resolves, m.opens, m.closes)
	}
	if !strings.HasSuffix(out, "<u>http://mem/big.xml</u><total>90000</total>") {
		t.Fatalf("got %q", out)
	}
}

// A resolver without OpenDocument is read as a tree, with the same result.
func TestStreamingResolverAbsentIsTree(t *testing.T) {
	m := &memDocs{gen: func() io.Reader { return memRecords(100) }}
	out, ran, err := memRun(t, context.Background(), memTotal, plainDocs{m})
	if err != nil || ran || m.resolves != 1 || !strings.HasSuffix(out, "<u>http://mem/big.xml</u><total>450</total>") {
		t.Fatalf("got %q, %v; streamed %v, resolves %d", out, err, ran, m.resolves)
	}
}

// The reader is closed on every path: a malformed document (which re-runs on
// the tree), a cancelled context, and a streamed merge, whole or failing.
func TestStreamingResolverCloses(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		m := &memDocs{docs: map[string]string{"big.xml": "<a><r v='1'/></b>"}}
		_, ran, err := memRun(t, context.Background(), memTotal, m)
		if err == nil || !strings.Contains(err.Error(), "FODC0002") || !ran || m.opens != 1 || m.closes != 1 {
			t.Fatalf("%v; streamed %v, opens %d, closes %d", err, ran, m.opens, m.closes)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := &memDocs{gen: func() io.Reader { return memRecords(20000) }}
		reads := 0
		m.onRead = func() {
			if reads++; reads == 3 {
				cancel()
			}
		}
		_, ran, err := memRun(t, ctx, memTotal, m)
		if err != context.Canceled || !ran || m.resolves != 0 || m.opens != 1 || m.closes != 1 {
			t.Fatalf("%v; streamed %v, resolves %d, opens %d, closes %d", err, ran, m.resolves, m.opens, m.closes)
		}
	})
	const merge = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><out><xsl:merge>
	<xsl:merge-source for-each-source="'a.xml', 'b.xml'" select="a/r" streamable="yes"><xsl:merge-key select="@v"/></xsl:merge-source>
	<xsl:merge-action><xsl:value-of select="current-merge-key()"/></xsl:merge-action>
	</xsl:merge></out></xsl:template></xsl:stylesheet>`
	t.Run("merge", func(t *testing.T) {
		m := &memDocs{docs: map[string]string{"a.xml": "<a><r v='1'/><r v='3'/></a>", "b.xml": "<a><r v='2'/></a>"}}
		out, _, err := memRun(t, context.Background(), merge, m)
		if err != nil || !strings.HasSuffix(out, "<out>123</out>") || m.resolves != 0 || m.opens != 2 || m.closes != 2 {
			t.Fatalf("got %q, %v; resolves %d, opens %d, closes %d", out, err, m.resolves, m.opens, m.closes)
		}
	})
	t.Run("merge error", func(t *testing.T) {
		m := &memDocs{docs: map[string]string{"a.xml": "<a><r v='1'/></b>", "b.xml": "<a><r v='2'/></a>"}}
		_, _, err := memRun(t, context.Background(), merge, m)
		if err == nil || m.opens == 0 || m.opens != m.closes {
			t.Fatalf("%v; opens %d, closes %d", err, m.opens, m.closes)
		}
	})
}

// XTDE1500 holds over the URI OpenDocument gives: a streamed read then a
// write of it, and a write then a streamed read, fail as fn:doc's do.
func TestStreamingResolverXTDE1500(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"read then write", `<xsl:source-document streamable="yes" href="a.xml"><xsl:value-of select="count(a/r)"/></xsl:source-document>
			<xsl:result-document href="http://mem/a.xml"><x/></xsl:result-document>`},
		{"write then read", `<xsl:result-document href="http://mem/a.xml"><x/></xsl:result-document>
			<xsl:source-document streamable="yes" href="a.xml"><xsl:value-of select="count(a/r)"/></xsl:source-document>`},
		{"doc then write", `<xsl:value-of select="count(doc('a.xml')/a/r)"/>
			<xsl:result-document href="http://mem/a.xml"><x/></xsl:result-document>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &memDocs{docs: map[string]string{"a.xml": "<a><r/><r/></a>"}}
			src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:template name="main">` + tc.body + `</xsl:template></xsl:stylesheet>`
			_, _, err := memRun(t, context.Background(), src, m)
			if err == nil || !strings.Contains(err.Error(), "XTDE1500") || m.opens != m.closes {
				t.Fatalf("%v; opens %d, closes %d", err, m.opens, m.closes)
			}
		})
	}
	// The streamed read is a read: it is the one that ran.
	m := &memDocs{docs: map[string]string{"a.xml": "<a><r/><r/></a>"}}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><xsl:source-document streamable="yes" href="a.xml"><xsl:value-of select="count(a/r)"/></xsl:source-document>
	<xsl:result-document href="http://mem/a.xml"><x/></xsl:result-document></xsl:template></xsl:stylesheet>`
	if _, ran, _ := memRun(t, context.Background(), src, m); !ran || m.resolves != 0 {
		t.Fatalf("streamed %v, resolves %d", ran, m.resolves)
	}
}

// FileResolver.OpenDocument is confined as ResolveDocument is: a file inside
// a root opens with its file: URI, and one outside, or a link out of the
// root, is refused at the open (os.Root).
func TestFileResolverOpenDocumentConfined(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	for name, dir := range map[string]string{"in.xml": root, "secret.xml": outside} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("<a/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewFileResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	var _ StreamingDocumentResolver = r
	rc, u, err := r.OpenDocument("in.xml", fileURIOf(filepath.Join(root, "s.xsl")))
	if err != nil || u != fileURIOf(filepath.Join(root, "in.xml")) {
		t.Fatalf("inside: %q, %v", u, err)
	}
	rc.Close()
	if _, _, err := r.OpenDocument(filepath.Join(outside, "secret.xml"), ""); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("outside: %v, want a refusal", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.xml"), filepath.Join(root, "link.xml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rc, _, err := r.OpenDocument("link.xml", fileURIOf(filepath.Join(root, "s.xsl"))); err == nil {
		rc.Close()
		t.Fatal("followed a link out of the root")
	}
}
