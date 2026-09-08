package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xslt"
)

// json-node-output-method="html" serializes the nodes nested in the JSON with
// the HTML method, and that method declares the output encoding inside <head>.
//
// The spelling is the http-equiv one the full serializer already writes, not
// the HTML5 meta/@charset: output-0716 requires the serialization to match
// "<head>...<meta http-equiv=\"Content-Type\"" and output-0702 the same with a
// content attribute, and neither regex admits a charset attribute in its
// place. Writing @charset here also left the two serializers disagreeing about
// the same element.
func TestJSONNodeHTMLWritesContentTypeMeta(t *testing.T) {
	sheet := compileFor(t, `<t:transform
	   xmlns:t="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <t:output method="json" json-node-output-method="html"/>
	  <t:template name="t:initial-template">
	    <t:variable name="e1" as="element()"><head><title>A document</title></head></t:variable>
	    <t:sequence select="[$e1]"/>
	  </t:template>
	</t:transform>`)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	got := res.String()
	if !strings.Contains(got, `<meta http-equiv=\"Content-Type\" content=\"text\/html; charset=UTF-8\">`) {
		t.Fatalf("got %s, want the http-equiv content-type meta", got)
	}
	if strings.Contains(got, "meta charset") {
		t.Fatalf("got %s, want no HTML5 meta/@charset", got)
	}
}

// The same meta belongs in a <head> that is in the XHTML namespace. Under the
// html method every element is HTML by definition, so the namespace does not
// decide -- output-0702 builds its <head> under a default xmlns of
// http://www.w3.org/1999/xhtml and asks to see the meta, which a test on no
// namespace alone left out.
func TestJSONNodeHTMLWritesMetaInXHTMLNamespace(t *testing.T) {
	sheet := compileFor(t, `<t:transform xmlns="http://www.w3.org/1999/xhtml"
	   xmlns:t="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <t:output method="json" indent="no" json-node-output-method="html"/>
	  <t:template name="t:initial-template">
	    <t:map>
	      <t:map-entry key="'doc1'">
	        <html><head><title>Document 1</title></head><body><p>Content 1</p></body></html>
	      </t:map-entry>
	    </t:map>
	  </t:template>
	</t:transform>`)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	got := res.String()
	if !strings.Contains(got, `<head><meta http-equiv=\"Content-Type\"`) {
		t.Fatalf("got %s, want the meta inside the XHTML head", got)
	}
}

// A <head> in some other vocabulary is not a place to describe a media type,
// so widening the namespace test must not have widened it to everything.
func TestJSONNodeHTMLLeavesAlienHeadAlone(t *testing.T) {
	sheet := compileFor(t, `<t:transform
	   xmlns:t="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <t:output method="json" json-node-output-method="html"/>
	  <t:template name="t:initial-template">
	    <t:variable name="e1" as="element()">
	      <head xmlns="urn:not-html"><title>x</title></head>
	    </t:variable>
	    <t:sequence select="[$e1]"/>
	  </t:template>
	</t:transform>`)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	if got := res.String(); strings.Contains(got, "http-equiv") {
		t.Fatalf("got %s, want no meta in an alien head", got)
	}
}
