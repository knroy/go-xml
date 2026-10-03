package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
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
