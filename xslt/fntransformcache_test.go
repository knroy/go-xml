package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/internal/fileuri"
	"github.com/knroy/go-xml/xdm"
)

// cacheInner reports its static parameter, so a stale cache hit across
// different static-params would show in the output.
const cacheInner = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:param name="s" static="yes" select="'none'"/>
  <xsl:template name="xsl:initial-template"><xsl:value-of select="$s"/></xsl:template>
</xsl:stylesheet>`

// cacheOuter calls fn:transform on $inner once per value of SEQ, passing the
// value as the static parameter s, with cache set to CACHE.
const cacheOuter = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="inner" as="xs:string"/>
  <xsl:output method="text"/>
  <xsl:template name="go">
    <xsl:value-of select="for $v in SEQ return string(transform(map{
      'stylesheet-text': $inner,
      'static-params': map{QName('','s'): $v},
      'cache': CACHE,
      'delivery-format': 'raw'})?output)"/>
  </xsl:template>
</xsl:stylesheet>`

func compileCacheOuter(t *testing.T, seq, cache string) *Stylesheet {
	t.Helper()
	src := strings.NewReplacer("SEQ", seq, "CACHE", cache).Replace(cacheOuter)
	sd, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func runCacheOuter(st *Stylesheet, inner string) (string, error) {
	res, err := st.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "go",
		Params:          map[string]xdm.Sequence{"inner": {xdm.NewString(inner)}},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = res.Serialize(&b)
	return b.String(), err
}

func cacheState(st *Stylesheet) (compiles, entries int) {
	st.nested.mu.Lock()
	defer st.nested.mu.Unlock()
	return st.nested.compiles, len(st.nested.byKey)
}

func TestTransformCache(t *testing.T) {
	for _, tc := range []struct {
		name, seq, cache, want string
		compiles, entries      int
	}{
		// Three calls, one compilation, carried over to a second run below.
		{"hit", "('a','a','a')", "true()", "a a a", 1, 1},
		// cache=false stores nothing, so every call compiles afresh.
		{"cache false", "('a','a')", "false()", "a a", 0, 0},
		// A different static parameter is a different stylesheet.
		{"static params", "('a','b','a')", "true()", "a b a", 2, 2},
		// xs:untypedAtomic 'a' is not xs:string 'a' to the key.
		{"static param type", "('a', xs:untypedAtomic('a'))", "true()", "a a", 2, 2},
		// Twenty distinct stylesheets, at most nestedCacheSize kept.
		{"bound", "(1 to 20)", "true()", "1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20", 20, nestedCacheSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := compileCacheOuter(t, tc.seq, tc.cache)
			got, err := runCacheOuter(st, cacheInner)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("output %q, want %q", got, tc.want)
			}
			c, e := cacheState(st)
			if c != tc.compiles || e != tc.entries {
				t.Errorf("compiles=%d entries=%d, want %d and %d", c, e, tc.compiles, tc.entries)
			}
		})
	}

	t.Run("shared across transforms", func(t *testing.T) {
		st := compileCacheOuter(t, "('a','b')", "true()")
		for range 3 {
			if _, err := runCacheOuter(st, cacheInner); err != nil {
				t.Fatal(err)
			}
		}
		if c, _ := cacheState(st); c != 2 {
			t.Errorf("compiles=%d over three transforms, want 2", c)
		}
	})

	// stylesheet-location through a resolver, DocBook's shape. Transform wraps
	// the caller's resolver afresh for every run, so keying on the wrapper
	// rather than on the resolver beneath it would never hit.
	t.Run("location across transforms", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "inner.xsl")
		if err := os.WriteFile(path, []byte(cacheInner), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		loc := fileuri.Of(path)
		src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		  <xsl:output method="text"/>
		  <xsl:template name="go">
		    <xsl:value-of select="string(transform(map{'stylesheet-location': '` + loc + `',
		      'delivery-format': 'raw'})?output)"/>
		  </xsl:template>
		</xsl:stylesheet>`
		sd, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(sd.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			res, err := st.Transform(context.Background(), nil, TransformOptions{
				InitialTemplate: "go", Documents: r})
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			if err := res.Serialize(&b); err != nil || b.String() != "none" {
				t.Fatalf("output %q, %v; want \"none\"", b.String(), err)
			}
		}
		if c, e := cacheState(st); c != 1 || e != 1 {
			t.Errorf("compiles=%d entries=%d over three transforms, want 1 and 1", c, e)
		}
	})

	// A compile failure is never stored: each call recompiles and reports
	// the stylesheet's own static error.
	t.Run("error not cached", func(t *testing.T) {
		st := compileCacheOuter(t, "('a','a')", "true()")
		bad := strings.Replace(cacheInner, "<xsl:value-of", "<xsl:no-such-instruction/><xsl:value-of", 1)
		for range 2 {
			_, err := runCacheOuter(st, bad)
			if err == nil || !strings.Contains(err.Error(), "xsl:no-such-instruction is not an XSLT 3.0 element (XTSE0010)") {
				t.Fatalf("err = %v, want the nested XTSE0010", err)
			}
		}
		if c, e := cacheState(st); c != 0 || e != 0 {
			t.Errorf("compiles=%d entries=%d after failures, want 0 and 0", c, e)
		}
	})

	// One compiled outer stylesheet, many concurrent transforms (go test -race).
	t.Run("concurrent", func(t *testing.T) {
		st := compileCacheOuter(t, "('a','b','c','a')", "true()")
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := runCacheOuter(st, cacheInner)
				if err == nil && got != "a b c a" {
					err = fmt.Errorf("output %q", got)
				}
				if err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if _, e := cacheState(st); e != 3 {
			t.Errorf("entries=%d, want 3", e)
		}
	})
}
