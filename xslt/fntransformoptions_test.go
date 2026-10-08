package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// optionsInner says which entry point ran.
const optionsInner = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="xsl:initial-template">init</xsl:template>
  <xsl:template name="main">main</xsl:template>
  <xsl:template match="/">doc</xsl:template>
  <xsl:template match="/" mode="m">m</xsl:template>
</xsl:stylesheet>`

// fn:transform's typed options go through the function conversion rules
// (F&O 3.1 1.5.4): arrays flatten, nodes and untypedAtomic convert, and any
// other mismatch is XPTY0004 -- untypedAtomic to xs:QName is XPTY0117.
func TestTransformOptionConversion(t *testing.T) {
	const outer = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:map="http://www.w3.org/2005/xpath-functions/map">
	  <xsl:param name="inner" as="xs:string"/>
	  <xsl:output method="text"/>
	  <xsl:template name="go">
	    <xsl:value-of select="let $m := map:merge((
	        map{'stylesheet-text': $inner, 'delivery-format': 'raw'}, OPTS),
	        map{'duplicates': 'use-last'})
	      return string-join(transform(if (map:contains($m, 'stylesheet-node'))
	        then map:remove($m, 'stylesheet-text') else $m)?*)"/>
	  </xsl:template>
	</xsl:stylesheet>`
	src := "map{'source-node': parse-xml('&lt;d/&gt;')}"
	for _, tc := range []struct{ opts, want, code string }{
		{"map{'cache': [true()]}", "init", ""},
		{"map{'cache': []}", "", "XPTY0004"},
		{"map{'enable-assertions': xs:untypedAtomic('true')}", "init", ""},
		{"map{'enable-messages': 'yes'}", "", "XPTY0004"},
		{"map{'enable-trace': 1}", "", "XPTY0004"},
		{"map{'enable-trace': map{}}", "", "FOTY0013"},
		{"map{'xslt-version': [3.0]}", "init", ""},
		{"map{'xslt-version': 3.0e0}", "", "XPTY0004"},
		{"map{'delivery-format': ['raw']}", "init", ""},
		{"map{'delivery-format': ('raw', 'raw')}", "", "XPTY0004"},
		{"map{'base-output-uri': xs:anyURI('http://example.com/')}", "init", ""},
		{"map{'base-output-uri': 1}", "", "XPTY0004"},
		{"map{'initial-template': [QName('', 'main')]}", "main", ""},
		{"map{'initial-template': 'main'}", "", "XPTY0004"},
		{"map{'initial-template': xs:untypedAtomic('main')}", "", "XPTY0117"},
		{"map{'initial-mode': QName('', 'm')}, " + src, "m", ""},
		{"map{'initial-mode': 'm'}, " + src, "", "XPTY0004"},
		{"map{'vendor-options': map{QName('urn:v', 'k'): 1}}", "init", ""},
		{"map{'vendor-options': 'x'}", "", "XPTY0004"},
		{"map{'stylesheet-node': 'x'}", "", "XPTY0004"},
	} {
		sd, err := xdm.ParseString(strings.Replace(outer, "OPTS", tc.opts, 1), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(sd.Root, CompileOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.opts, err)
		}
		got, err := runCacheOuter(st, optionsInner)
		if code := xdm.ErrorCode(err); code != tc.code {
			t.Errorf("%s: err = %v, want code %q", tc.opts, err, tc.code)
			continue
		}
		if tc.code == "" && got != tc.want {
			t.Errorf("%s: output %q, want %q", tc.opts, got, tc.want)
		}
	}
}
