package xslt

import (
	"strings"
	"testing"
)

// TestIncludeContentTypeNoKeepsStylesheetMeta: the html and xhtml methods
// discard the head's own content-type meta only "if a meta element has been
// added" (Serialization 3.1 §7.4.13, §6.1.14). With include-content-type="no"
// nothing is added, so both of the stylesheet's metas -- the HTML5 charset
// spelling and the http-equiv one -- must survive. They were dropped, which
// left the output with no encoding declaration at all and is why XRechnung's
// HTML stage never agreed with Saxon.
func TestIncludeContentTypeNoKeepsStylesheetMeta(t *testing.T) {
	for _, method := range []string{"html", "xhtml"} {
		for _, version := range []string{"", ` html-version="5"`} {
			for _, ict := range []string{"yes", "no"} {
				ns := ""
				if method == "xhtml" {
					ns = ` xmlns="http://www.w3.org/1999/xhtml"`
				}
				sheet := `<xsl:stylesheet version="3.0" ` +
					`xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` + ns + `>` +
					`<xsl:output method="` + method + `" encoding="UTF-8" ` +
					`omit-xml-declaration="yes" include-content-type="` + ict + `"` +
					version + `/>` +
					`<xsl:template match="/"><html><head><meta charset="UTF-8"/>` +
					`<meta http-equiv="Content-Type" content="text/html; charset=ISO-8859-1"/>` +
					`<title>t</title></head><body/></html></xsl:template></xsl:stylesheet>`
				out := run(t, sheet, `<r/>`)
				name := method + version + " include-content-type=" + ict
				ownCharset := strings.Contains(out, `<meta charset="UTF-8"`)
				ownEquiv := strings.Contains(out, `charset=ISO-8859-1"`)
				added := strings.Contains(out, `content="text/html; charset=UTF-8"`)
				if ict == "no" {
					if !ownCharset || !ownEquiv || added {
						t.Errorf("%s: want both stylesheet metas and none added, got:\n%s", name, out)
					}
				} else if ownCharset || ownEquiv || !added {
					t.Errorf("%s: want only the added meta, got:\n%s", name, out)
				}
			}
		}
	}
}

// TestHTMLIndentLeavesInlineElementsAlone: under indent="yes" the html and
// xhtml methods "MUST NOT" add whitespace adjacent to an inline element
// (Serialization 3.1 §7.4.3, §6.1.4). <p><b>bold</b><i>it</i></p> came out
// with each child on its own line, which renders "bold it" with a space the
// document never had. The expected layouts are Saxon 12's for the same input.
func TestHTMLIndentLeavesInlineElementsAlone(t *testing.T) {
	body := `<html><body><div><p><b>bold</b><I>it</I></p>` +
		`<style>s{}</style><script>var a;</script></div>` +
		`<span><div>x</div><div>y</div></span>` +
		`<ul><li>a</li><li>b</li></ul></body></html>`
	for _, method := range []string{"html", "xhtml"} {
		out := run(t, htmlIndentSheet(method, "", body), `<r/>`)
		for _, want := range []string{
			// Nothing between inline siblings or before </p> after one.
			"<p><b>bold</b><I>it</I></p>",
			// <style> is not inline, <script> is, and so is the end tag
			// after it.
			"\n      <style>s{}</style><script>var a;</script></div>",
			// Inside an inline element the first block child is indented,
			// its end tag is not.
			"<span>\n      <div>x</div>\n      <div>y</div></span>",
			// Block content indents as before.
			"<ul>\n      <li>a</li>\n      <li>b</li>\n    </ul>",
		} {
			if method == "xhtml" && strings.Contains(want, "<I>") {
				// XHTML is XML: <I> is not <i>, so it is not inline there.
				want = "<p><b>bold</b><I>it</I>\n      </p>"
			}
			if !strings.Contains(out, want) {
				t.Errorf("method=%s: want %q in output, got:\n%s", method, want, out)
			}
		}
	}
}

// TestAddedMetaReplacesOnlyHeadChildren: §7.4.13 discards "any existing meta
// element child of the head element" with http-equiv="Content-Type" once the
// method adds its own. Every such meta anywhere under <head> was dropped, so a
// <noscript> inside head lost its content. The charset spelling follows the
// same rule. Saxon 12.10 writes the same.
func TestAddedMetaReplacesOnlyHeadChildren(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output method="html" indent="no" media-type="text/foo"/><xsl:template match="/">` +
		`<html><head><meta charset="x"/><meta http-equiv="content-type" content="x"/>` +
		`<noscript><meta http-equiv="Content-Type" content="y"/><meta charset="y"/></noscript>` +
		`<title>t</title></head></html></xsl:template></xsl:stylesheet>`
	out := run(t, sheet, `<r/>`)
	want := `<head><meta http-equiv="Content-Type" content="text/foo; charset=UTF-8">` +
		`<noscript><meta http-equiv="Content-Type" content="y"><meta charset="y"></noscript>` +
		`<title>t</title></head>`
	if !strings.Contains(out, want) {
		t.Errorf("want %s in:\n%s", want, out)
	}
}
