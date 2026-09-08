package xslt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// A stylesheet function is the third entry point, beside a named template and
// an apply-templates: XSLT 3.0 section 2.3.5 "Function Call Invocation".
//
// The arity is inferred from the length of the parameter list, so naming the
// function is only half of saying which one -- see initialFunction.
func TestInitialFunction(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:square" as="xs:integer" visibility="public">
	    <xsl:param name="n" as="xs:integer"/>
	    <xsl:sequence select="$n * $n"/>
	  </xsl:function>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction:       xdm.QName{URI: "urn:functions", Local: "square"},
		InitialFunctionParams: []xdm.Sequence{xdm.One(xdm.NewInteger(12))},
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	// The raw result is the integer 144, not a text node holding "144":
	// 2.3.5 returns the function's value, and turning it into a tree is the
	// caller's option under 2.3.6 rather than something the engine does.
	if len(res.Nodes) != 1 {
		t.Fatalf("got %d items, want 1", len(res.Nodes))
	}
	a, ok := res.Nodes[0].(*xdm.Atomic)
	if !ok {
		t.Fatalf("got %s, want an atomic value", res.Nodes[0].TypeName())
	}
	if a.String() != "144" {
		t.Fatalf("got %q, want 144", a.String())
	}
}

// A source document is not required. The initial function is evaluated with an
// absent focus, so "no selection and no source" is not a defect of this
// invocation -- which is the exemption issue #4 turned on.
func TestInitialFunctionNeedsNoSource(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:greet" as="xs:string" visibility="public">
	    <xsl:sequence select="'hello'"/>
	  </xsl:function>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	// No parameters at all, so the arity is zero and the nullary function is
	// the one selected.
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction: xdm.QName{URI: "urn:functions", Local: "greet"},
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].(*xdm.Atomic).String() != "hello" {
		t.Fatalf("got %v, want hello", res.Nodes)
	}
}

// XTDE0041 covers both halves of 2.3.5's sentence: a name that matches nothing,
// and an arity that matches nothing.
func TestInitialFunctionUnknownIsXTDE0041(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:square" as="xs:integer" visibility="public">
	    <xsl:param name="n" as="xs:integer"/>
	    <xsl:sequence select="$n * $n"/>
	  </xsl:function>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	for _, tc := range []struct {
		name  string
		fname xdm.QName
		args  []xdm.Sequence
	}{
		{"no such name", xdm.QName{URI: "urn:functions", Local: "nonsuch"},
			[]xdm.Sequence{xdm.One(xdm.NewInteger(1))}},
		{"wrong namespace", xdm.QName{URI: "urn:other", Local: "square"},
			[]xdm.Sequence{xdm.One(xdm.NewInteger(1))}},
		{"arity too large", xdm.QName{URI: "urn:functions", Local: "square"},
			[]xdm.Sequence{xdm.One(xdm.NewInteger(1)), xdm.One(xdm.NewInteger(2))}},
		{"arity too small", xdm.QName{URI: "urn:functions", Local: "square"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
				InitialFunction:       tc.fname,
				InitialFunctionParams: tc.args,
			})
			if err == nil {
				t.Fatal("got nil, want XTDE0041")
			}
			if !strings.Contains(err.Error(), "XTDE0041") {
				t.Fatalf("got %v, want XTDE0041", err)
			}
		})
	}
}

// A builtin is not a stylesheet function, so it is not an entry point. Lookup
// chains to the parent library and would have found fn:concat; Declares is the
// question that distinguishes them.
func TestInitialFunctionRejectsABuiltin(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   version="3.0">
	  <xsl:template name="xsl:initial-template"><o/></xsl:template>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction: xdm.QName{
			URI: "http://www.w3.org/2005/xpath-functions", Local: "concat"},
		InitialFunctionParams: []xdm.Sequence{
			xdm.One(xdm.NewString("a")), xdm.One(xdm.NewString("b"))},
	})
	if err == nil {
		t.Fatal("got nil, want XTDE0041 for a builtin")
	}
	if !strings.Contains(err.Error(), "XTDE0041") {
		t.Fatalf("got %v, want XTDE0041", err)
	}
}

// A function that is not public may not be an entry point, and the rule bites
// in a plain xsl:stylesheet rather than only inside an xsl:package -- which is
// where the initial-function rule parts company with the initial-template one.
// initial-function-905 is this case: my:private carries no visibility
// attribute, the stylesheet is not a package, and XTDE0041 is still due.
func TestInitialFunctionRequiresPublic(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:hidden" as="xs:string">
	    <xsl:sequence select="'secret'"/>
	  </xsl:function>
	  <xsl:function name="f:shown" as="xs:string" visibility="public">
	    <xsl:sequence select="'open'"/>
	  </xsl:function>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction: xdm.QName{URI: "urn:functions", Local: "hidden"},
	})
	if err == nil {
		t.Fatal("got nil, want XTDE0041 for a function that is not public")
	}
	if !strings.Contains(err.Error(), "XTDE0041") {
		t.Fatalf("got %v, want XTDE0041", err)
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Fatalf("got %v, want the message to say the function is not public", err)
	}

	// The public one beside it still works, so the rule is discriminating
	// rather than refusing everything.
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction: xdm.QName{URI: "urn:functions", Local: "shown"},
	})
	if err != nil {
		t.Fatalf("public function: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].(*xdm.Atomic).String() != "open" {
		t.Fatalf("got %v, want open", res.Nodes)
	}
}

// The declared parameter type converts the argument, and a value that will not
// convert is the function-parameter error rather than a silent coercion.
func TestInitialFunctionConvertsArguments(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:square" as="xs:integer" visibility="public">
	    <xsl:param name="n" as="xs:integer"/>
	    <xsl:sequence select="$n * $n"/>
	  </xsl:function>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialFunction: xdm.QName{URI: "urn:functions", Local: "square"},
		InitialFunctionParams: []xdm.Sequence{
			xdm.One(xdm.NewString("not a number"))},
	})
	if err == nil {
		t.Fatal("got nil, want a type error")
	}
	// The suite's initial-function-102b pins this as XPTY0004 rather than
	// FORG0001, after the 2018-09-25 amendment.
	if !strings.Contains(err.Error(), "XPTY0004") &&
		!strings.Contains(err.Error(), "XTTE0790") {
		t.Fatalf("got %v, want a conversion error", err)
	}
}

// An initial function is an alternative to the other entry points, not an
// addition to them.
func TestInitialFunctionExcludesOtherEntryPoints(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:greet" as="xs:string" visibility="public">
	    <xsl:sequence select="'hello'"/>
	  </xsl:function>
	  <xsl:template name="start"><o/></xsl:template>
	  <xsl:template match="/" mode="m"><o/></xsl:template>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	for _, tc := range []struct {
		name string
		opts xslt.TransformOptions
	}{
		{"with a template", xslt.TransformOptions{
			InitialFunction: xdm.QName{URI: "urn:functions", Local: "greet"},
			InitialTemplate: "start",
		}},
		{"with a mode", xslt.TransformOptions{
			InitialFunction: xdm.QName{URI: "urn:functions", Local: "greet"},
			InitialMode:     "m",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sheet.Transform(context.Background(), nil, tc.opts)
			if err == nil {
				t.Fatal("got nil, want XTDE0047")
			}
			if !strings.Contains(err.Error(), "XTDE0047") {
				t.Fatalf("got %v, want XTDE0047", err)
			}
		})
	}
}

// fn:transform reaches the same entry point through its options map, which is
// the path issue #4 reported: initial-function and function-params were read
// by nobody, so the nested transform got no entry point at all.
func TestFnTransformInitialFunction(t *testing.T) {
	dir := t.TempDir()
	inner := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:mf="http://example.com/mf" version="3.0">
	  <xsl:function name="mf:evaluate" as="item()*" visibility="public">
	    <xsl:param name="context" as="item()?"/>
	    <xsl:param name="expr" as="xs:string"/>
	    <xsl:evaluate xpath="$expr" context-item="$context"/>
	  </xsl:function>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(inner), 0o644); err != nil {
		t.Fatal(err)
	}

	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:output method="text"/>
	  <xsl:template name="xsl:initial-template">
	    <xsl:variable name="c" as="element()"><a><b>17</b></a></xsl:variable>
	    <xsl:value-of select="transform(map{
	      'stylesheet-location': 'inner.xsl',
	      'initial-function': QName('http://example.com/mf','evaluate'),
	      'function-params': [$c, 'b * 2'],
	      'delivery-format': 'raw'})?output"/>
	  </xsl:template>
	</xsl:stylesheet>`

	base := "file://" + dir + "/outer.xsl"
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{dir}},
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	if got := xslt.SerializeAsXML(res); !strings.Contains(got, "34") {
		t.Fatalf("got %q, want 34", got)
	}
}

// The option is a QName, and its namespace must survive being read. Reading it
// through String() yields the lexical form and silently drops the URI, which
// would look up a function in no namespace and report XTDE0041 for a function
// that is right there.
func TestFnTransformInitialFunctionKeepsTheNamespace(t *testing.T) {
	dir := t.TempDir()
	// Two functions of the same local name in different namespaces. Only a
	// reader that keeps the URI can tell them apart.
	inner := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:a="urn:a" xmlns:b="urn:b" version="3.0">
	  <xsl:function name="a:pick" as="xs:string" visibility="public">
	    <xsl:sequence select="'FROM-A'"/>
	  </xsl:function>
	  <xsl:function name="b:pick" as="xs:string" visibility="public">
	    <xsl:sequence select="'FROM-B'"/>
	  </xsl:function>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(inner), 0o644); err != nil {
		t.Fatal(err)
	}

	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:output method="text"/>
	  <xsl:template name="xsl:initial-template">
	    <xsl:value-of select="transform(map{
	      'stylesheet-location': 'inner.xsl',
	      'initial-function': QName('urn:b','pick'),
	      'function-params': [],
	      'delivery-format': 'raw'})?output"/>
	  </xsl:template>
	</xsl:stylesheet>`

	base := "file://" + dir + "/outer.xsl"
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{dir}},
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	got := xslt.SerializeAsXML(res)
	if !strings.Contains(got, "FROM-B") {
		t.Fatalf("got %q, want FROM-B (the namespace was dropped)", got)
	}
}

// function-params without initial-function names the arguments of no function.
func TestFnTransformFunctionParamsWithoutFunction(t *testing.T) {
	dir := t.TempDir()
	inner := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:template name="xsl:initial-template"><o/></xsl:template>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(inner), 0o644); err != nil {
		t.Fatal(err)
	}
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template name="xsl:initial-template">
	    <xsl:sequence select="transform(map{
	      'stylesheet-location': 'inner.xsl',
	      'function-params': [1,2]})?output"/>
	  </xsl:template>
	</xsl:stylesheet>`

	base := "file://" + dir + "/outer.xsl"
	tree, _ := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{dir}},
	})
	if err == nil {
		t.Fatal("got nil, want FOXT0002")
	}
	if !strings.Contains(err.Error(), "FOXT0002") {
		t.Fatalf("got %v, want FOXT0002", err)
	}
	if !strings.Contains(err.Error(), "function-params") {
		t.Fatalf("got %v, want the message to name function-params", err)
	}
}

// XTDE0044 raised inside fn:transform must say WHICH stylesheet lacked an
// entry point. Before issue #4 it read as though it were about the stylesheet
// the author was looking at, which sent them hunting in the wrong file.
func TestFnTransformNamesTheNestedStylesheet(t *testing.T) {
	dir := t.TempDir()
	// An inner stylesheet with no entry point at all: no initial-template,
	// and it will be invoked with no source and no selection.
	inner := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:f="urn:functions" version="3.0">
	  <xsl:function name="f:unused" visibility="public">
	    <xsl:sequence select="1"/>
	  </xsl:function>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "noentry.xsl"), []byte(inner), 0o644); err != nil {
		t.Fatal(err)
	}
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template name="xsl:initial-template">
	    <xsl:sequence select="transform(map{
	      'stylesheet-location': 'noentry.xsl'})?output"/>
	  </xsl:template>
	</xsl:stylesheet>`

	base := "file://" + dir + "/outer.xsl"
	tree, _ := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{dir}},
	})
	if err == nil {
		t.Fatal("got nil, want an entry-point error")
	}
	if !strings.Contains(err.Error(), "XTDE0044") {
		t.Fatalf("got %v, want XTDE0044", err)
	}
	// The identifying half: without it the message is the one from issue #4.
	if !strings.Contains(err.Error(), "fn:transform") ||
		!strings.Contains(err.Error(), "noentry.xsl") {
		t.Fatalf("got %v, want the message to name the nested stylesheet", err)
	}
}

// compileFor compiles a stylesheet source for these tests.
func compileFor(t *testing.T, src string) *xslt.Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	return sheet
}
