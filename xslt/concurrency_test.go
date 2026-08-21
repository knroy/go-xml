package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A compiled Stylesheet is documented as safe to share across goroutines, so
// that a server can compile a rule set once and validate concurrently. These
// tests exercise the paths where that promise is easiest to break: the caches,
// the collator, and anything the runtime writes back into shared state.
//
// They are worth having under -race even though they look redundant with the
// existing tests, because a data race only manifests when two goroutines touch
// the same word — a sequential test cannot see it however many times it runs.

// stress runs fn concurrently and reports the first mismatch.
func stress(t *testing.T, workers, iterations int, fn func() (string, error)) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan string, workers*iterations)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				got, err := fn()
				if err != nil {
					errs <- err.Error()
					return
				}
				_ = got
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
		return
	}
}

// One compiled stylesheet and one *shared* parsed document across goroutines.
// TestConcurrentTransforms already covers a document parsed per goroutine;
// sharing the tree is the stronger claim, since it means evaluation must not
// write into the source.
func TestConcurrentSortAndSharedDocument(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." data-type="number"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	doc := `<r><n>3</n><n>1</n><n>2</n></r>`
	want := "<out><i>1</i><i>2</i><i>3</i></out>"

	s, dtree := compileFor(t, sheet, doc)
	stress(t, 8, 50, func() (string, error) {
		res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
		if err != nil {
			return "", err
		}
		got := strings.TrimSpace(res.String())
		if got != want {
			return "", fmt.Errorf("got %q, want %q", got, want)
		}
		return got, nil
	})
}

// xsl:key builds an index lazily and caches it on the runtime. If that cache
// were shared between transforms rather than per-runtime, this would race.
func TestConcurrentKeyIndex(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:key name="byid" match="item" use="@id"/>
		<xsl:template match="/"><out><xsl:value-of select="key('byid','b')/text()"/></out></xsl:template>
	</xsl:stylesheet>`
	doc := `<r><item id="a">A</item><item id="b">B</item></r>`

	s, dtree := compileFor(t, sheet, doc)
	stress(t, 8, 50, func() (string, error) {
		res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
		if err != nil {
			return "", err
		}
		if got := strings.TrimSpace(res.String()); got != "<out>B</out>" {
			return "", fmt.Errorf("got %q, want <out>B</out>", got)
		}
		return "", nil
	})
}

// The regex cache is process-wide. Concurrent transforms compiling distinct
// patterns exercise both the store path and the clear-when-full path.
func TestConcurrentRegexCache(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//p"><xsl:value-of select="matches('abc', @re)"/></xsl:for-each>
	</out></xsl:template>`)
	var b strings.Builder
	b.WriteString("<r>")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, `<p re="^a%db$"/>`, i)
	}
	b.WriteString("</r>")

	s, dtree := compileFor(t, sheet, b.String())
	stress(t, 8, 10, func() (string, error) {
		_, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
		return "", err
	})
}

// xsl:message writes into a slice held through a pointer on the runtime, which
// is copied on every focus change. Concurrent transforms must not see each
// other's messages.
func TestConcurrentMessagesAreIsolated(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:message>one</xsl:message><xsl:message>two</xsl:message><out/>
	</xsl:template>`)
	s, dtree := compileFor(t, sheet, `<r/>`)
	stress(t, 8, 50, func() (string, error) {
		res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
		if err != nil {
			return "", err
		}
		if len(res.Messages) != 2 {
			return "", fmt.Errorf("got %d messages, want 2 — state leaked between transforms",
				len(res.Messages))
		}
		return "", nil
	})
}

// xsl:result-document collects into a slice on the runtime, the same shape as
// messages and the same failure mode if it were shared.
func TestConcurrentResultDocumentsAreIsolated(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:result-document href="a.xml"><a/></xsl:result-document>
		<main/>
	</xsl:template>`)
	s, dtree := compileFor(t, sheet, `<r/>`)
	stress(t, 8, 50, func() (string, error) {
		res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
		if err != nil {
			return "", err
		}
		if len(res.Secondary) != 1 {
			return "", fmt.Errorf("got %d secondary results, want 1", len(res.Secondary))
		}
		return "", nil
	})
}

// compileFor compiles a stylesheet and parses a document once, for reuse
// across goroutines.
func compileFor(t *testing.T, sheet, doc string) (*Stylesheet, *xdm.Tree) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dtree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s, dtree
}

// A node placed into output must be copied, not adopted.
//
// AppendChild rewrites a node's Parent and tree pointers, and Finalize
// renumbers its document order, so adopting a source node in place mutates the
// *source document*. That is a wrong answer even single-threaded — evaluating
// an unused variable was enough to reorder the input — and a data race when
// two goroutines transform a shared parsed tree.
//
// xsl:copy-of always deep-copied; xsl:sequence and xsl:perform-sort did not.
func TestOutputDoesNotAdoptSourceNodes(t *testing.T) {
	const src = `<r><a/><b/><c/></r>`
	const hdr = `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`
	order := `<xsl:value-of select="string-join(/r/*/name(),',')"/>`

	for _, c := range []struct{ name, body string }{
		{"control", ``},
		{"sequence", `<xsl:variable name="v"><xsl:sequence select="/r/b"/></xsl:variable>`},
		{"copy-of", `<xsl:variable name="v"><xsl:copy-of select="/r/b"/></xsl:variable>`},
		{"perform-sort", `<xsl:variable name="v"><xsl:perform-sort select="/r/*"><xsl:sort select="name()"/></xsl:perform-sort></xsl:variable>`},
	} {
		sheet := hdr + `<xsl:template match="/">` + c.body +
			`<o>` + order + `</o></xsl:template></xsl:stylesheet>`
		if got := run(t, sheet, src); got != `<o>a,b,c</o>` {
			t.Errorf("%s: document order became %s", c.name, got)
		}
	}

	// The copied nodes still carry their content, so the fix must not have
	// hollowed out the output.
	sheet := hdr + `<xsl:template match="/"><o><xsl:sequence select="/r/b"/></o></xsl:template></xsl:stylesheet>`
	if got := run(t, sheet, src); got != `<o><b/></o>` {
		t.Errorf("xsl:sequence output = %s, want <o><b/></o>", got)
	}
}

// A resolver retains parsed documents, and the cache is bounded.
//
// It is keyed by path under fixed roots, so it cannot grow without limit from
// one stylesheet — but a stylesheet chooses which documents to fetch, and a
// directory of many files would otherwise have it retain every one for the
// life of the process.
func TestResolverCacheIsBounded(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < resolverCacheMax+20; i++ {
		name := filepath.Join(dir, fmt.Sprintf("d%d.xml", i))
		if err := os.WriteFile(name, []byte(`<a/>`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ResolveDocument(name, ""); err != nil {
			t.Fatalf("resolving %s: %v", name, err)
		}
	}
	r.mu.Lock()
	n := len(r.cache)
	r.mu.Unlock()
	if n > resolverCacheMax {
		t.Errorf("cache holds %d documents, over the %d bound", n, resolverCacheMax)
	}
	// And it still caches: a second resolve of the same path must not fail.
	name := filepath.Join(dir, "d0.xml")
	if _, err := r.ResolveDocument(name, ""); err != nil {
		t.Errorf("re-resolving after the cache cleared: %v", err)
	}
}
