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
