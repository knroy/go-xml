package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

func runPP(t *testing.T, sheet, src string) (string, error) {
	t.Helper()
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse stylesheet: %v", err)
	}
	st, err := xslt.Compile(doc.Root, xslt.CompileOptions{})
	if err != nil {
		return "", err
	}
	in, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, terr := st.Transform(context.Background(), in.Root,
		xslt.TransformOptions{})
	if terr != nil {
		return "", terr
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// post-process is an F&O 3.1 option on fn:transform: "A function that is used
// to post-process each result document of the transformation (both the
// principal result and secondary results), in whatever form it would
// otherwise be delivered." It was accepted and ignored, so a stylesheet that
// asked for a second processing stage got a plausible result one
// transformation short, with nothing said. Reported as issue #5.
func TestTransformPostProcessRuns(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:template match="/" name="xsl:initial-template">
	    <xsl:value-of select="transform(map{
	      'source-node': .,
	      'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;&gt;&lt;xsl:template match=&quot;/&quot;&gt;&lt;a/&gt;&lt;/xsl:template&gt;&lt;/xsl:stylesheet&gt;',
	      'delivery-format': 'serialized',
	      'post-process': function($uri as xs:string, $r as item()*) as item()* {
	         concat('PP:', $uri)
	      }
	    })?output"/>
	  </xsl:template>
	</xsl:stylesheet>`
	got, err := runPP(t, sheet, `<r/>`)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	// The post-processor replaced the result entirely, so its return value
	// is what the map holds -- and it was given the key, which for a
	// principal result with no base output URI is "output".
	if !strings.Contains(got, "PP:output") {
		t.Errorf("got %q; post-process did not run, or was not given the "+
			"result key as its first argument", got)
	}
}

// An option name the processor does not know must be refused, not dropped.
// Silence is what let post-process go unimplemented and unnoticed: the output
// looked right, one stage short.
func TestTransformRefusesUnknownOption(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/" name="xsl:initial-template">
	    <xsl:sequence select="transform(map{
	      'source-node': .,
	      'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;/&gt;',
	      'post-proces': 1
	    })?output"/>
	  </xsl:template>
	</xsl:stylesheet>`
	_, err := runPP(t, sheet, `<r/>`)
	if err == nil {
		t.Fatal("a misspelled option was accepted and silently ignored")
	}
	if code := xdm.ErrorCode(err); code != "FOXT0002" {
		t.Errorf("code = %q, want FOXT0002", code)
	}
	if !strings.Contains(err.Error(), "post-proces") {
		t.Errorf("error %v does not name the option it refused", err)
	}
}

// The type is function(xs:string, item()*) as item()*, so arity is part of
// what makes the option valid rather than a detail of the body.
func TestTransformPostProcessArityIsChecked(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/" name="xsl:initial-template">
	    <xsl:sequence select="transform(map{
	      'source-node': .,
	      'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;/&gt;',
	      'post-process': function($only) { $only }
	    })?output"/>
	  </xsl:template>
	</xsl:stylesheet>`
	_, err := runPP(t, sheet, `<r/>`)
	if err == nil {
		t.Fatal("a one-argument post-process was accepted")
	}
	if code := xdm.ErrorCode(err); code != "FOXT0002" {
		t.Errorf("code = %q, want FOXT0002", code)
	}
}
