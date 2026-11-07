package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// neweachtimeRun compiles main against modules and runs its initial template.
func neweachtimeRun(t *testing.T, main string, mods memModules) string {
	t.Helper()
	tree, err := xdm.ParseString(main, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{Resolver: mods})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return res.String()
}

// GitHub issue #13: a new-each-time="no" function is deterministic (10.3.7),
// so two calls with the same node must return the SAME node. The functions
// live in an imported module, as in the report. Every call built a fresh tree
// because a node argument was never cached, and "is" answered false.
func TestNewEachTimeNodeIdentity(t *testing.T) {
	for _, tc := range []struct{ net, want string }{
		{"no", "true true true true|true true true true"},
		{"false", "true true true true|true true true true"},
		{"yes", "false false false false|false false false false"},
		{"maybe", ""}, // either answer is allowed
	} {
		t.Run(tc.net, func(t *testing.T) {
			mods := memModules{"fns.xsl": `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			    xmlns:myFn="urn:fn">
			  <xsl:function as="element()" name="myFn:function-1032" new-each-time="` + tc.net + `">
			    <xsl:param as="element()" name="param01"/>
			    <type><xsl:copy-of select="$param01"/></type>
			  </xsl:function>
			  <xsl:function as="text()" name="myFn:element-check" new-each-time="` + tc.net + `">
			    <xsl:param as="element()" name="param01"/>
			    <xsl:value-of select="$param01/@type"/>
			  </xsl:function>
			</xsl:stylesheet>`}
			got := neweachtimeRun(t, `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			    xmlns:myFn="urn:fn" exclude-result-prefixes="myFn">
			  <xsl:import href="fns.xsl"/>
			  <xsl:output method="text"/>
			  <xsl:variable name="data">
			    <root><leaf type="a"/><leaf type="b"/><leaf type="c"/><leaf type="d"/></root>
			  </xsl:variable>
			  <xsl:template name="xsl:initial-template">
			    <xsl:value-of select="for-each-pair(
			        $data/root/leaf/myFn:function-1032(.),
			        $data/root/leaf/myFn:function-1032(.),
			        function($a, $b) { $a is $b })"/>
			    <xsl:text>|</xsl:text>
			    <xsl:value-of select="for-each-pair(
			        $data/root/leaf/myFn:element-check(.),
			        $data/root/leaf/myFn:element-check(.),
			        function($a, $b) { $a is $b })"/>
			  </xsl:template>
			</xsl:stylesheet>`, mods)
			got = strings.TrimSpace(got)
			if tc.want != "" && got != tc.want {
				t.Errorf("new-each-time=%q: got %q, want %q", tc.net, got, tc.want)
			}
		})
	}
}

// F&O 1.7.4's "identical" arguments are atomic values of precisely the same
// type, nodes by identity, and maps and arrays by content. 1 and 1.0 are
// therefore different argument lists: a cache keyed on "eq" alone handed
// f(1.0) the xs:integer f(1) returned.
func TestNewEachTimeIdenticalArguments(t *testing.T) {
	got := neweachtimeRun(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:f="urn:f" exclude-result-prefixes="f">
	  <xsl:output method="text"/>
	  <xsl:function name="f:id" new-each-time="no">
	    <xsl:param name="p"/>
	    <xsl:sequence select="$p"/>
	  </xsl:function>
	  <xsl:function name="f:wrap" new-each-time="no" as="element()">
	    <xsl:param name="p"/>
	    <w/>
	  </xsl:function>
	  <xsl:variable name="d"><a/><a/></xsl:variable>
	  <xsl:template name="xsl:initial-template">
	    <xsl:value-of select="
	      f:id(1) instance of xs:integer, f:id(1.0) instance of xs:decimal
	                                     and not(f:id(1.0) instance of xs:integer),
	      f:wrap(map{'k': $d/a[1]}) is f:wrap(map{'k': $d/a[1]}),
	      f:wrap(map{'k': $d/a[1]}) is f:wrap(map{'k': $d/a[2]}),
	      f:wrap([$d/a[1]]) is f:wrap([$d/a[1]]),
	      f:wrap($d/a[1]) is f:wrap($d/a[2])"
	      xmlns:xs="http://www.w3.org/2001/XMLSchema"/>
	  </xsl:template>
	</xsl:stylesheet>`, nil)
	if want := "true true true false true false"; strings.TrimSpace(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// §10.3.8 makes cache="yes" a hint whose use must still "respect the
// semantics", and §10.3.7 makes a function proactive by default: "when the
// function creates new nodes, it creates new nodes on each invocation". So the
// hint may reuse a result only where reuse is unobservable -- the function is
// deterministic or elidable, or the result holds no nodes. The hint alone
// returned the same constructed node from both calls. An atomic result is
// still memoised, which is what the hint is for (function-1031's fib).
func TestCacheHintKeepsProactiveNodesFresh(t *testing.T) {
	for _, tc := range []struct{ attrs, want string }{
		{`cache="yes"`, "false"},
		{`cache="yes" new-each-time="yes"`, "false"},
		{`cache="yes" new-each-time="no"`, "true"},
		{`cache="yes" new-each-time="maybe"`, ""}, // either identity is allowed
	} {
		t.Run(tc.attrs, func(t *testing.T) {
			tree, err := xdm.ParseString(`<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			    xmlns:xs="http://www.w3.org/2001/XMLSchema"
			    xmlns:f="urn:f" exclude-result-prefixes="f xs">
			  <xsl:output method="text"/>
			  <xsl:function name="f:make" as="element()" `+tc.attrs+`>
			    <xsl:param name="n" as="xs:integer"/>
			    <e n="{$n}"/>
			  </xsl:function>
			  <xsl:function name="f:num" as="xs:integer" `+tc.attrs+`>
			    <xsl:param name="n" as="xs:integer"/>
			    <xsl:message>computed</xsl:message>
			    <xsl:sequence select="$n"/>
			  </xsl:function>
			  <xsl:template name="xsl:initial-template">
			    <xsl:value-of select="f:make(1) is f:make(1)"/>
			    <xsl:sequence select="f:num(1) + f:num(1)"/>
			  </xsl:template>
			</xsl:stylesheet>`, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := s.Transform(context.Background(), nil, TransformOptions{
				InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := strings.TrimSpace(res.String()); tc.want != "" && !strings.HasPrefix(got, tc.want) {
				t.Errorf("%s: f:make(1) is f:make(1) gave %q, want %q", tc.attrs, got, tc.want)
			}
			if n := len(res.Messages); n != 1 {
				t.Errorf("%s: f:num(1) ran %d times, want 1: an atomic result is memoised", tc.attrs, n)
			}
		})
	}
}

// An array or map of atomic values holds no node, so reusing it is as
// unobservable as reusing an atomic value: cache="yes" on a proactive
// function memoises it. DocBook xslTNG's fcals:overhang, which returns
// array(xs:integer) and recurses on the preceding row, relies on that to
// keep its recursion shallow on table-cals.049.
func TestCacheHintMemoisesNodeFreeArrays(t *testing.T) {
	tree, err := xdm.ParseString(`<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    xmlns:f="urn:f" exclude-result-prefixes="f xs">
	  <xsl:output method="text"/>
	  <xsl:function name="f:arr" as="array(xs:integer)" cache="yes">
	    <xsl:param name="n" as="xs:integer"/>
	    <xsl:message>computed</xsl:message>
	    <xsl:sequence select="[$n, $n]"/>
	  </xsl:function>
	  <xsl:function name="f:map" as="map(*)" cache="yes">
	    <xsl:param name="n" as="xs:integer"/>
	    <xsl:message>computed</xsl:message>
	    <xsl:sequence select="map{'a': [$n], 'b': map{'c': $n}}"/>
	  </xsl:function>
	  <xsl:template name="xsl:initial-template">
	    <xsl:sequence select="f:arr(1)(1) + f:arr(1)(2) + f:map(1)?b?c + f:map(1)?b?c"/>
	  </xsl:template>
	</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := strings.TrimSpace(res.String()); got != "4" {
		t.Errorf("got %q, want 4", got)
	}
	if n := len(res.Messages); n != 2 {
		t.Errorf("the functions ran %d times, want 2: a node-free array or map is memoised", n)
	}
}
