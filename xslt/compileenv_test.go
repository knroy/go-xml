package xslt

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// overrideBase and overrideTop are a used package and a package overriding
// its template "t", calling the original from the override.
const overrideBase = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	name="urn:base" package-version="1.0.0" version="3.0">
	<xsl:template name="t" visibility="public"><base/></xsl:template>
	<xsl:template name="go" visibility="public"><xsl:call-template name="t"/></xsl:template>
</xsl:package>`

const overrideTop = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	name="urn:top" package-version="1.0.0" version="3.0">
	<xsl:use-package name="urn:base" package-version="1.0.0">
		<xsl:override>
			<xsl:template name="t" visibility="public"><over><xsl:call-template name="xsl:original"/></over></xsl:template>
		</xsl:override>
	</xsl:use-package>
	<xsl:template name="xsl:initial-template" visibility="public"><out><xsl:call-template name="go"/></out></xsl:template>
</xsl:package>`

// originalNames lists the generated xsl:original names a stylesheet holds.
func originalNames(s *Stylesheet) []string {
	var out []string
	for k := range s.named {
		if strings.Contains(k, originalNS) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// A compilation numbers its packages and generated names from zero, so the
// same stylesheet compiles to the same names whatever compiled before it.
func TestCompileNamesDoNotDependOnEarlierCompiles(t *testing.T) {
	var first []string
	for i := 0; i < 3; i++ {
		s, err := Compile(parseSheet(t, overrideTop), CompileOptions{
			PackageResolver: fixedPackages{"urn:base": overrideBase}})
		if err != nil {
			t.Fatal(err)
		}
		names := originalNames(s)
		if len(names) == 0 {
			t.Fatal("no generated xsl:original name; the test no longer reaches the override")
		}
		if i == 0 {
			first = names
		} else if !reflect.DeepEqual(names, first) {
			t.Fatalf("compile %d named the original %v, compile 0 %v", i, names, first)
		}
	}
}

// parallel runs fn(i) for i in [0, n) at once and waits for all of them.
func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// runMain runs a stylesheet's "main" (or initial) template and returns its
// serialised result.
func runMain(s *Stylesheet, template string) (string, error) {
	res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: template})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Concurrent compilations with different XPathVersion and MaxVersion each
// compile under their own: a pin one of them sets must not reach another.
func TestConcurrentCompilesKeepTheirOwnVersions(t *testing.T) {
	// A version="2.0" module: a 3.0 processor reads it with the 3.1 grammar
	// and accepts 3.0's visibility attribute, a 2.0 processor refuses the
	// attribute (XTSE0090), and pinning XPath 2.0 takes the map away.
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
   xmlns:my="urn:my" version="2.0">
  <xsl:function name="my:f" visibility="public"><xsl:sequence select="map{'k': 2}"/></xsl:function>
  <xsl:template name="main"><out><xsl:value-of select="my:f()?k"/></out></xsl:template>
</xsl:stylesheet>`
	xp20 := xpath.XPath20
	cases := []struct {
		opts CompileOptions
		want string // the error the compile must give, or "" for success
	}{
		{CompileOptions{}, ""},
		{CompileOptions{XPathVersion: &xp20}, "XPST0003"},
		{CompileOptions{MaxVersion: 2.0}, "XTSE0090"},
	}
	parallel(48, func(i int) {
		c := cases[i%len(cases)]
		s, err := Compile(parseSheet(t, sheet), c.opts)
		if c.want != "" {
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("case %d: got %v, want %s", i%len(cases), err, c.want)
			}
			return
		}
		if err != nil {
			t.Errorf("case %d: %v", i%len(cases), err)
			return
		}
		if out, err := runMain(s, "main"); err != nil || !strings.Contains(out, ">2</out>") {
			t.Errorf("case %d: got %q, %v", i%len(cases), out, err)
		}
	})
}

// Concurrent compilations of a package overriding a used package's template
// each compose their own copy.
func TestConcurrentOverrideCompiles(t *testing.T) {
	parallel(24, func(int) {
		s, err := Compile(parseSheet(t, overrideTop), CompileOptions{
			PackageResolver: fixedPackages{"urn:base": overrideBase}})
		if err != nil {
			t.Error(err)
			return
		}
		out, err := runMain(s, "")
		if err != nil || !strings.Contains(out, "><base/></over></out>") {
			t.Errorf("got %q, %v", out, err)
		}
	})
}

// Concurrent compilations importing different schemas each see their own: a
// type only the other's schema defines is XPST0051.
func TestConcurrentImportSchemaCompiles(t *testing.T) {
	sheet := func(ns string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
   xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:` + ns + `" version="3.0">
  <xsl:import-schema namespace="urn:` + ns + `">
    <xs:schema targetNamespace="urn:` + ns + `">
      <xs:simpleType name="` + ns + `-code"><xs:restriction base="xs:string"/></xs:simpleType>
    </xs:schema>
  </xsl:import-schema>
  <xsl:template name="main"><out><xsl:value-of select="'` + ns + `' cast as t:` + ns + `-code"/></out></xsl:template>
</xsl:stylesheet>`
	}
	parallel(32, func(i int) {
		ns := []string{"a", "b"}[i%2]
		s, err := Compile(parseSheet(t, sheet(ns)), CompileOptions{})
		if err != nil {
			t.Errorf("%s: %v", ns, err)
			return
		}
		if out, err := runMain(s, "main"); err != nil || !strings.Contains(out, ">"+ns+"</out>") {
			t.Errorf("%s: got %q, %v", ns, out, err)
		}
	})
}

// Concurrent compilations whose static phase calls fn:transform each nest
// their own compilation.
func TestConcurrentStaticTransformCompiles(t *testing.T) {
	const outer = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:param name="inner" static="yes" as="xs:string" xmlns:xs="http://www.w3.org/2001/XMLSchema"/>
  <xsl:variable name="v" static="yes" select="transform(map{'stylesheet-text': $inner,
    'initial-template': QName('', 'go'), 'delivery-format': 'raw'})?output"/>
  <xsl:template name="main"><out><xsl:value-of select="$v"/></out></xsl:template>
</xsl:stylesheet>`
	inner := func(n int) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template name="go"><xsl:sequence select="` + strconv.Itoa(n) + `"/></xsl:template>
</xsl:stylesheet>`
	}
	parallel(24, func(i int) {
		s, err := Compile(parseSheet(t, outer), CompileOptions{StaticParams: map[string]xdm.Sequence{
			"inner": xdm.One(xdm.NewString(inner(i)))}})
		if err != nil {
			t.Errorf("%d: %v", i, err)
			return
		}
		if out, err := runMain(s, "main"); err != nil || !strings.Contains(out, ">"+strconv.Itoa(i)+"</out>") {
			t.Errorf("%d: got %q, %v", i, out, err)
		}
	})
}

// Compilations run alongside streaming and non-streaming transforms of
// stylesheets already compiled; neither may see the other's state.
func TestCompileAlongsideTransforms(t *testing.T) {
	streaming, err := Compile(parseSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:mode streamable="yes"/>
  <xsl:template match="/"><out><xsl:value-of select="count(//x)"/></out></xsl:template>
</xsl:stylesheet>`), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Compile(parseSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
   xmlns:p="urn:p" version="3.0">
  <xsl:template match="/"><out><xsl:value-of select="in-scope-prefixes(*) => sort() => string-join(',')"/></out></xsl:template>
</xsl:stylesheet>`), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const doc = `<r xmlns:q="urn:q"><x/><x/><x/></r>`
	parallel(36, func(i int) {
		switch i % 3 {
		case 0:
			res, err := streaming.TransformReader(context.Background(), strings.NewReader(doc), TransformOptions{})
			var b strings.Builder
			if err == nil {
				err = res.Serialize(&b)
			}
			if err != nil || !strings.Contains(b.String(), ">3</out>") {
				t.Errorf("streaming: got %q, %v", b.String(), err)
			}
		case 1:
			tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			res, err := plain.Transform(context.Background(), tree.Root, TransformOptions{})
			var b strings.Builder
			if err == nil {
				err = res.Serialize(&b)
			}
			if err != nil || !strings.Contains(b.String(), ">q,xml</out>") {
				t.Errorf("plain: got %q, %v", b.String(), err)
			}
		default:
			s, err := Compile(parseSheet(t, overrideTop), CompileOptions{
				PackageResolver: fixedPackages{"urn:base": overrideBase}})
			if err != nil {
				t.Error(err)
				return
			}
			if out, err := runMain(s, ""); err != nil || !strings.Contains(out, "><base/></over></out>") {
				t.Errorf("compile: got %q, %v", out, err)
			}
		}
	})
}
