package xslt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The runtime function library (key(), current(), the accumulators, ...) is
// built once per stylesheet and its functions find the transform through the
// context. These tests hold that to what a library per transform did.

func compileString(t *testing.T, src string) *Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func parseDoc(t *testing.T, src string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return tree.Root
}

// One stylesheet, many transforms at once over different documents: each
// key() call answers from its own transform's index.
func TestRuntimeLibrarySharedAcrossTransforms(t *testing.T) {
	s := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:key name="k" match="i" use="@id"/>
		<xsl:template match="/"><r><xsl:value-of select="key('k', 'a')"/></r></xsl:template>
	</xsl:stylesheet>`)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			doc := parseDoc(t, fmt.Sprintf(`<d><i id="a">v%d</i></d>`, n))
			for rep := 0; rep < 3; rep++ {
				res, err := s.Transform(context.Background(), doc, TransformOptions{})
				if err != nil {
					errs <- err
					return
				}
				if got, want := res.String(), fmt.Sprintf("<r>v%d</r>", n); got != want {
					errs <- fmt.Errorf("transform %d: got %q, want %q", n, got, want)
					return
				}
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A function item made from key() in one transform and called in a nested
// fn:transform keeps answering for the transform that made it: the nested
// stylesheet declares no key at all. Each of the three ways of making the
// item is checked -- a named reference, fn:function-lookup and a partial
// application -- since each makes it on its own path.
func TestKeyItemCrossesIntoNestedTransform(t *testing.T) {
	outer := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:param name="inner"/>
		<xsl:key name="k" match="i" use="@id"/>
		<xsl:template match="/">
			<xsl:sequence select="transform(map{
				'stylesheet-node': $inner,
				'source-node': /,
				'stylesheet-params': map{
					QName('', 'named'): key#2,
					QName('', 'looked'): function-lookup(
						QName('http://www.w3.org/2005/xpath-functions', 'key'), 2),
					QName('', 'partial'): key('k', ?)}
			})?output"/>
		</xsl:template>
	</xsl:stylesheet>`)
	inner := parseDoc(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:param name="named"/>
		<xsl:param name="looked"/>
		<xsl:param name="partial"/>
		<xsl:template match="/">
			<r n="{$named('k', 'b')}" l="{$looked('k', 'b')}" p="{$partial('b')}"/>
		</xsl:template>
	</xsl:stylesheet>`)
	doc := parseDoc(t, `<d><i id="a">A</i><i id="b">B</i></d>`)
	res, err := outer.Transform(context.Background(), doc, TransformOptions{
		Params: map[string]xdm.Sequence{"inner": xdm.One(inner)},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got, want := res.String(), `<r n="B" l="B" p="B"/>`; !strings.Contains(got, want) {
		t.Fatalf("got %q, want %s", got, want)
	}
}

// Package-scoped resolution holds on every transform of one compiled
// stylesheet, not only the first: the used package's private function stays
// reachable through its public wrapper and unreachable by name.
func TestPackageScopingSurvivesRepeatedTransforms(t *testing.T) {
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:p="urn:base" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:function name="p:pub" as="xs:string" visibility="public">
			<xsl:param name="in" as="xs:string"/>
			<xsl:sequence select="p:priv($in)"/>
		</xsl:function>
		<xsl:function name="p:priv" as="xs:string" visibility="private">
			<xsl:param name="in" as="xs:string"/>
			<xsl:sequence select="concat($in, $in)"/>
		</xsl:function>
	</xsl:package>`
	top := func(call string) *Stylesheet {
		tree, err := xdm.ParseString(`<xsl:package
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:p="urn:base" name="urn:top" package-version="1.0.0"
			expand-text="yes" version="3.0">
			<xsl:output omit-xml-declaration="yes"/>
			<xsl:use-package name="urn:base" package-version="1.0.0"/>
			<xsl:template name="xsl:initial-template" visibility="public">
				<res>{`+call+`}</res>
			</xsl:template>
		</xsl:package>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{
			PackageResolver: fixedPackages{"urn:base": base}})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return s
	}
	opts := TransformOptions{InitialTemplate: "initial-template",
		InitialTemplateURI: xdm.NSXSL}
	pub, priv := top("p:pub('x')"), top("p:priv('x')")
	for rep := 0; rep < 3; rep++ {
		res, err := pub.Transform(context.Background(), nil, opts)
		if err != nil {
			t.Fatalf("rep %d, public wrapper: %v", rep, err)
		}
		if got := res.String(); !strings.Contains(got, ">xx</res>") {
			t.Fatalf("rep %d, public wrapper: got %q", rep, got)
		}
		_, err = priv.Transform(context.Background(), nil, opts)
		if err == nil || !strings.Contains(err.Error(), "XPST0017") {
			t.Fatalf("rep %d, private by name: got %v, want XPST0017", rep, err)
		}
	}
}
