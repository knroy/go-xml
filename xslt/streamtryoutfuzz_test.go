package xslt

// The xsl:try grammar of TestStreamOutFuzz (streamtry_out.go): on its own
// random stream, a share of the cases get the body of one or more of their
// templates wrapped in tries -- rollback-output yes or no, nested, with
// items before and after the body (text, atomic values, a zero-length text
// node, comments, an attribute, an element), an error raised before or after
// the body or not at all, catches that take it, take everything, or miss it,
// writing nothing, text, an element or $err:code -- and a random context
// around the try (an element with or without content before it, text before
// it). Output must still write what Result.Serialize writes, error for error.
//
// GOXSLT_STREAM_FUZZ_TRYOUT=0 leaves them out.

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
)

// fuzzTryOutShape wraps template bodies of sheet in tries, for a share of
// the seeds.
func fuzzTryOutShape(seed int64, sheet string) string {
	if os.Getenv("GOXSLT_STREAM_FUZZ_TRYOUT") == "0" {
		return sheet
	}
	r := rand.New(rand.NewSource(seed*40503 + 977))
	if r.Intn(100) >= 35 {
		return sheet
	}
	var b strings.Builder
	rest := sheet
	for {
		i := strings.Index(rest, "<xsl:template ")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], ">")
		k := strings.Index(rest[i:], "</xsl:template>")
		if j < 0 || k < 0 || j > k || rest[i+j-1] == '/' {
			b.WriteString(rest[:i+1])
			rest = rest[i+1:]
			continue
		}
		head, body := rest[:i+j+1], rest[i+j+1:i+k]
		b.WriteString(head)
		// Params come first in a template body: leave them out of the try.
		params := ""
		for strings.HasPrefix(strings.TrimSpace(body), "<xsl:param") {
			body = strings.TrimSpace(body)
			e := strings.Index(body, "/>")
			if f := strings.Index(body, "</xsl:param>"); e < 0 || f >= 0 && f < e {
				if f < 0 {
					break
				}
				e = f + len("</xsl:param>") - 2
			}
			params += body[:e+2]
			body = body[e+2:]
		}
		b.WriteString(params)
		if r.Intn(100) < 60 {
			body = fuzzTryAround(r, body, 0)
		}
		b.WriteString(body)
		rest = rest[i+k:]
	}
	b.WriteString(rest)
	return b.String()
}

// fuzzTryAround returns body inside a try, in a random context.
func fuzzTryAround(r *rand.Rand, body string, depth int) string {
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	items := func() string {
		var s strings.Builder
		for range r.Intn(3) {
			s.WriteString(pick(`<xsl:text>t</xsl:text>`, `<xsl:sequence select="1, 2"/>`, `<xsl:text/>`,
				`<xsl:comment>c</xsl:comment>`, `<i n="1"/>`, `<xsl:attribute name="at" select="'v'"/>`,
				`<xsl:value-of select="'v', 3"/>`, `<j><k/>jt</j>`))
		}
		return s.String()
	}
	fail := func() string {
		switch r.Intn(4) {
		case 0:
			return `<xsl:sequence select="error(QName('http://www.w3.org/2005/xqt-errors', 'FOAR0001'))"/>`
		case 1:
			return `<xsl:value-of select="xs:integer('x')" xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`
		}
		return ""
	}
	inner := body
	if depth == 0 && r.Intn(4) == 0 {
		inner = fuzzTryAround(r, body, 1)
	}
	inner = fuzzTryElement(r, inner) // streamtryoutfuzz_element_test.go
	var t strings.Builder
	t.WriteString(`<xsl:try rollback-output="` + pick("yes", "yes", "no") + `" xmlns:err="http://www.w3.org/2005/xqt-errors">`)
	t.WriteString(items())
	if r.Intn(5) == 0 {
		t.WriteString(fail())
	}
	t.WriteString(inner)
	t.WriteString(items())
	t.WriteString(fail())
	for range 1 + r.Intn(2) {
		t.WriteString(`<xsl:catch errors="` + pick("*", "*", "err:FOAR0001", "err:FORG0001", "err:XPTY0004", "*:FOAR0001") + `">`)
		t.WriteString(pick(``, `<caught/>`, `caught`, `<c code="{$err:code}"/>`, `<xsl:value-of select="$err:code"/>`,
			`<c><d/>`+strconv.Itoa(r.Intn(9))+`</c><e/>`))
		t.WriteString(`</xsl:catch>`)
	}
	t.WriteString(`</xsl:try>`)
	switch r.Intn(5) {
	case 0:
		return `<w>` + t.String() + `</w>`
	case 1:
		return `<w><p/>` + t.String() + `<q/></w>`
	case 2:
		return `<w>x` + t.String() + `</w>`
	}
	return t.String()
}
