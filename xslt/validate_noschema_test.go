package xslt

import (
	"context"
	"strings"
	"testing"
)

// TestLaxValidationWithoutSchema pins the two halves of XTSE1660 on a
// processor that has imported no schema.
//
// The error is defined as firing when a package includes "an [xsl:]type
// attribute; or an [xsl:]validation or [xsl:]default-validation attribute
// with a value other than strip, preserve, or lax". lax is explicitly
// excluded, so validation="lax" with no schema must run: lax assessment
// consults a declaration only if one is available, and none is, so nothing is
// assessed and the constructed node comes out untyped.
//
// strict is the negative half, and it matters just as much: the spec says
// strict "indicates that the stylesheet is expecting to deal with typed data,
// and therefore cannot be processed without performing the validation", so it
// stays a static error.
//
// This is si-copy-024, si-element-024, si-lre-024, si-copy-of-024,
// si-document-024, si-result-document-024, stream-013 and non-stream-013 in
// miniature; all eight failed with XTSE1660 on a stylesheet the spec requires
// a non-schema-aware processor to accept.
func TestLaxValidationWithoutSchema(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		// wantErr is the error substring expected, or "" when the transform
		// must succeed and produce an untyped element.
		wantErr string
	}{
		{
			name: "copy lax",
			body: `<xsl:copy validation="lax"><xsl:value-of select="'x'"/></xsl:copy>`,
		},
		{
			name: "element lax",
			body: `<xsl:element name="e" validation="lax">x</xsl:element>`,
		},
		{
			name: "copy-of lax",
			body: `<xsl:copy-of select="/doc/*" validation="lax"/>`,
		},
		{
			name: "literal result element lax",
			body: `<e xsl:validation="lax">x</e>`,
		},
		{
			// Whitespace around the enumeration value is insignificant per
			// §3.2, which is how stream-013 spells it.
			name: "copy lax padded",
			body: `<xsl:copy validation=" lax "><xsl:value-of select="'x'"/></xsl:copy>`,
		},
		{
			name:    "copy strict",
			body:    `<xsl:copy validation="strict"><xsl:value-of select="'x'"/></xsl:copy>`,
			wantErr: "XTSE1660",
		},
		{
			name:    "element strict",
			body:    `<xsl:element name="e" validation="strict">x</xsl:element>`,
			wantErr: "XTSE1660",
		},
		{
			name:    "literal result element strict",
			body:    `<e xsl:validation="strict">x</e>`,
			wantErr: "XTSE1660",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `
			<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			   xmlns:xs="http://www.w3.org/2001/XMLSchema">
			  <xsl:template name="main">
			    <out>
			      <xsl:for-each select="parse-xml('&lt;doc>&lt;a>t&lt;/a>&lt;/doc>')/doc/a">
			        ` + tc.body + `
			      </xsl:for-each>
			    </out>
			    <xsl:value-of select="'|'"/>
			  </xsl:template>
			</xsl:stylesheet>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				if tc.wantErr != "" && strings.Contains(err.Error(), tc.wantErr) {
					return
				}
				t.Fatalf("compile: %v", err)
			}
			_, err = sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("validation=strict with no schema: got success, "+
						"want %s", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want one mentioning %s",
						err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validation=lax with no schema: %v", err)
			}
		})
	}
}

// TestLaxValidationWithoutSchemaLeavesUntyped is the assertion the eight
// suite cases actually make: not merely that the transform runs, but that
// what it constructs is untyped. A lax assessment that found no declaration
// annotated nothing, so "instance of element(*, xs:untyped)" must hold.
func TestLaxValidationWithoutSchemaLeavesUntyped(t *testing.T) {
	src := `
	<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:variable name="v" as="document-node()">
	    <xsl:document>
	      <out>
	        <xsl:for-each select="parse-xml('&lt;doc>&lt;a>t&lt;/a>&lt;/doc>')/doc/a">
	          <xsl:copy validation="lax"><xsl:value-of select="."/></xsl:copy>
	        </xsl:for-each>
	      </out>
	    </xsl:document>
	  </xsl:variable>
	  <xsl:template name="main">
	    <xsl:value-of select="$v/out/* instance of element(*, xs:untyped)"/>
	  </xsl:template>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := strings.TrimSpace(res.String()); !strings.HasSuffix(got, "true") {
		t.Errorf("instance of element(*, xs:untyped): got %q, want true", got)
	}
}
