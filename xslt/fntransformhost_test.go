package xslt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// fn:transform for a caller with no transform of its own -- an XQuery query,
// a bare xpath.Eval -- runs through the processor this package's init
// registers with xpath. These cases drive it the way such a caller does:
// a Context over xpath.Builtins, so the stub, not registerTransformFunc, is
// what the call reaches.

// plainContext is a bare XPath caller's Context, binding each name/value pair
// in vars as an xs:string variable.
func plainContext(docs xpath.DocumentResolver, vars ...string) *xpath.Context {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = xpath.XPath31
	ctx.Docs = docs
	for i := 0; i < len(vars); i += 2 {
		ctx.Vars[vars[i]] = xdm.One(xdm.NewString(vars[i+1]))
	}
	return ctx
}

// str is the string a serialized fn:transform output holds.
func str(seq xdm.Sequence) string {
	if len(seq) != 1 {
		return ""
	}
	return seq[0].(*xdm.Atomic).String()
}

func TestTransformFromPlainXPath(t *testing.T) {
	got, err := xpath.Eval(`transform(map{
		'stylesheet-text': '<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"><xsl:template name="xsl:initial-template"><r>ok</r></xsl:template></xsl:stylesheet>',
		'delivery-format': 'serialized'})?output`, plainContext(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := str(got); !strings.Contains(s, "<r>ok</r>") {
		t.Errorf("output = %q, want <r>ok</r>", s)
	}
}

// stylesheet-location, source-location and the nested stylesheet's own doc()
// resolve through the caller's resolver and nothing else: with none every
// one is refused, and with a FileResolver only what is under its roots reads.
func TestTransformFromPlainXPathIsSandboxed(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "s.xsl"), `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:param name="d" select="''"/>
  <xsl:template match="/"><got><xsl:value-of select="/r"/><xsl:value-of select="if ($d) then doc($d) else ()"/></got></xsl:template>
</xsl:stylesheet>`)
	write(filepath.Join(dir, "in.xml"), `<r>near</r>`)
	write(filepath.Join(other, "s.xsl"), `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"/>`)
	write(filepath.Join(other, "far.xml"), `<r>far</r>`)
	res, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	sheet, in := fileuri.Of(filepath.Join(dir, "s.xsl")), fileuri.Of(filepath.Join(dir, "in.xml"))
	call := func(docs xpath.DocumentResolver, xsl, src, d string) (string, error) {
		got, err := xpath.Eval(`transform(map{
			'stylesheet-location': $xsl, 'source-location': $src,
			'stylesheet-params': map{QName('','d'): $d},
			'delivery-format': 'serialized'})?output`,
			plainContext(docs, "xsl", xsl, "src", src, "d", d), nil)
		return str(got), err
	}

	if got, err := call(res, sheet, in, ""); err != nil || !strings.Contains(got, "<got>near</got>") {
		t.Fatalf("inside the roots: %q, %v", got, err)
	}
	refused := map[string][3]string{
		"no resolver":                 {sheet, in, ""},
		"stylesheet-location outside": {fileuri.Of(filepath.Join(other, "s.xsl")), in, ""},
		"source-location outside":     {sheet, fileuri.Of(filepath.Join(other, "far.xml")), ""},
		"doc() outside":               {sheet, in, fileuri.Of(filepath.Join(other, "far.xml"))},
	}
	for name, a := range refused {
		docs := xpath.DocumentResolver(res)
		if name == "no resolver" {
			docs = nil
		}
		got, err := call(docs, a[0], a[1], a[2])
		if err == nil || strings.Contains(got, "far") {
			t.Errorf("%s: read %q, want a refusal", name, got)
			continue
		}
		if name != "doc() outside" && xdm.ErrorCode(err) != "FOXT0002" {
			t.Errorf("%s: code %q, want FOXT0002 (error: %v)", name, xdm.ErrorCode(err), err)
		}
	}
}

// A stylesheet transforming itself, entered from plain XPath, is bound by the
// caller's MaxDepth exactly as one entered through Transform is.
func TestSelfCallingTransformFromPlainXPathIsRefused(t *testing.T) {
	ctx := plainContext(nil, "s", selfCallingSheet)
	ctx.MaxDepth = 5
	_, err := xpath.Eval(`transform(map{'stylesheet-text': $s,
		'initial-template': QName('','go'),
		'stylesheet-params': map{QName('','sheet'): $s}})`, ctx, nil)
	if xdm.ErrorCode(err) != "XPDY0001" || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("err = %v, want the XPDY0001 nesting refusal", err)
	}
}

// The recursion bound survives a hop through another host language: a
// stylesheet calling fn:load-xquery-module into a query that calls
// fn:transform again. The loader here stands in for xquery's, building a fresh
// Context for the query that inherits only the caller's depth and bound --
// which is all a query can see. The nested runtime's Context must therefore
// carry the count; started at zero, every hop restarted it and the cycle ran
// until the guard below, or the Go stack, gave out.
func TestTransformRecursionThroughAnotherHostIsBounded(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="xsl:initial-template"><xsl:sequence select="load-xquery-module('urn:q')"/></xsl:template>
</xsl:stylesheet>`
	const query = `transform(map{'stylesheet-text': $s})`
	calls := 0
	xpath.RegisterXQueryModuleLoader(func(ctx *xpath.Context, _ string, _ *xdm.MapItem) (xdm.Sequence, error) {
		if calls++; calls > 200 {
			return nil, errors.New("fn:transform recursion through a query was not bounded")
		}
		q := plainContext(nil, "s", sheet)
		q.Depth, q.MaxDepth = ctx.Depth, ctx.MaxDepth
		return xpath.Eval(query, q, nil)
	})
	defer xpath.RegisterXQueryModuleLoader(nil)

	ctx := plainContext(nil, "s", sheet)
	ctx.MaxDepth = 50
	_, err := xpath.Eval(query, ctx, nil)
	if xdm.ErrorCode(err) != "XPDY0001" || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("err = %v, want a depth refusal", err)
	}
}

// A use-when under a 2.0 processor that calls fn:transform is refused with
// FOXT0004, as before the processor was registered. Reaching it instead would
// Compile under the compileMu the outer Compile holds, and hang.
func TestStaticTransformUnderA20ProcessorIsRefused(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template match="/" use-when="exists(transform(map{'stylesheet-text': '&lt;x/>'}))"><a/></xsl:template>
</xsl:stylesheet>`
	d, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Compile(d.Root, CompileOptions{MaxVersion: 2.0})
		done <- err
	}()
	select {
	case err := <-done:
		if !strings.Contains(err.Error(), "FOXT0004") {
			t.Errorf("err = %v, want FOXT0004", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Compile deadlocked on fn:transform in a use-when")
	}
}

// serialization-params overrides the unnamed xsl:output for the serialized
// principal result, merging list parameters and character maps as one
// xsl:output merges with another (F&O 3.1 14.7.1; QT3 fn-transform-29..80).
func TestTransformSerializationParams(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
<xsl:output cdata-section-elements="a" use-character-maps="m" standalone="yes"/>
<xsl:character-map name="m"><xsl:output-character character="-" string="(hyphen)"/><xsl:output-character character="*" string="(asterisk)"/></xsl:character-map>
<xsl:template name="xsl:initial-template"><html><a>x</a><b>y</b><br/><c>-*</c></html></xsl:template>
</xsl:stylesheet>`
	run := func(params string) (string, error) {
		got, err := xpath.Eval(`transform(map{'stylesheet-text': $s, 'delivery-format': 'serialized',
			'serialization-params': `+params+`})?output`, plainContext(nil, "s", sheet), nil)
		return str(got), err
	}
	for _, tc := range []struct{ params, want, not string }{
		{`map{}`, `<br>`, `CDATA`},
		{`map{'method': 'xml'}`, `<br/>`, ``},
		{`map{'method': 'xml', 'cdata-section-elements': QName('', 'b')}`, `<![CDATA[y]]>`, `<a>x</a>`},
		{`map{'method': 'xml', 'cdata-section-elements': ()}`, `<a>x</a>`, `CDATA`},
		{`map{'use-character-maps': map{'*': '(star)'}}`, `(hyphen)(star)`, `(asterisk)`},
		{`map{'method': 'xml', 'indent': true()}`, "\n", ``},
		{`map{'method': 'xml', 'omit-xml-declaration': false(), 'standalone': ()}`, `<?xml`, `standalone`},
	} {
		got, err := run(tc.params)
		if err != nil {
			t.Errorf("%s: %v", tc.params, err)
			continue
		}
		if !strings.Contains(got, tc.want) || (tc.not != "" && strings.Contains(got, tc.not)) {
			t.Errorf("%s: output = %q, want %q and not %q", tc.params, got, tc.want, tc.not)
		}
	}
	for _, tc := range []struct{ params, code string }{
		{`'indent'`, "XPTY0004"},
		{`map{'indent': 'yes'}`, "XPTY0004"},
		{`map{'cdata-section-elements': 'a'}`, "XPTY0004"},
		{`map{'use-character-maps': map{'ab': 'x'}}`, "SEPM0016"},
		{`map{'method': 'nonsense'}`, "SEPM0016"},
	} {
		if _, err := run(tc.params); xdm.ErrorCode(err) != tc.code {
			t.Errorf("%s: err = %v, want %s", tc.params, err, tc.code)
		}
	}
}

// An empty principal result beside xsl:result-document output is no result
// tree at all, as XSLT 2.0 had it; fn-transform-43 parses every entry.
func TestTransformOmitsEmptyPrincipal(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
<xsl:template name="main"><xsl:result-document href="s.xml"><s/></xsl:result-document></xsl:template>
</xsl:stylesheet>`
	got, err := xpath.Eval(`transform(map{'stylesheet-text': $s, 'initial-template': QName('', 'main'),
		'base-output-uri': 'http://example.com/out.xml', 'delivery-format': 'serialized'}) => Q{http://www.w3.org/2005/xpath-functions/map}keys()`,
		plainContext(nil, "s", sheet), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || str(got) != "http://example.com/s.xml" {
		t.Errorf("keys = %v, want only the secondary http://example.com/s.xml", got)
	}
}
