package xslt

import (
	"context"
	"strings"
	"testing"
)

// TestSortLangUnsupportedFallsBack pins section 13.1.3's rule for an
// xsl:sort/@lang naming a language the implementation has no collation for.
//
// The spec draws a line between two failure modes (xslt-lcwd30.xml:18473-18482):
// the effective value "must either be a string in the value space of
// xs:language, or a zero-length string" -- breaking that is an error -- but
// "[i]f a language is requested that is not supported, the processor may use a
// fallback language ...; failing this, the processor behaves as if the lang
// attribute were omitted."
//
// An unsupported language was being refused outright with XTSE0020, so a
// conformant stylesheet sorting with lang="art-lojban" or lang="qqq" failed to
// compile instead of sorting by the default collation. xsl:number/@lang always
// had this right (12.3, xslt-lcwd30.xml:18063-18068); xsl:sort/@lang did not.
func TestSortLangUnsupportedFallsBack(t *testing.T) {
	// Sorted by codepoint, the fallback these must reach.
	const want = "<out>A B a b</out>"

	for _, tc := range []struct {
		name string
		lang string
		err  string // non-empty when the value is outside xs:language
	}{
		// Legal xs:language, no collation data: must fall back, not fail.
		{name: "unassigned code", lang: "qqq"},
		{name: "registered collective", lang: "art-lojban"},
		{name: "private use", lang: "x-private"},
		// Legal xs:language that BCP 47 cannot parse. It is still inside the
		// value space, so it takes the fallback rather than the error path --
		// this is the case that separates xs:language membership from BCP 47
		// well-formedness.
		{name: "valid xs:language not valid BCP47", lang: "xx-YY-ZZ"},
		// Outside the value space: a space is not permitted in any subtag,
		// and a 9-character subtag exceeds the length limit. These keep the
		// error, so the fix does not simply stop validating.
		{name: "space is not a subtag char", lang: "not a lang!", err: "XTSE0020"},
		{name: "subtag too long", lang: "abcdefghi", err: "XTSE0020"},
		{name: "digit in first subtag", lang: "1234", err: "XTSE0020"},
		// A supported language still builds its collator, so the fallback is
		// not swallowing every @lang.
		{name: "supported language", lang: "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:template name="main"><out>
			    <xsl:perform-sort select="('b','A','a','B')">
			      <xsl:sort select="." lang="` + tc.lang + `"/>
			    </xsl:perform-sort>
			  </out></xsl:template>
			</xsl:stylesheet>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err == nil {
				_, err = sheet.Transform(context.Background(), nil,
					TransformOptions{InitialTemplate: "main"})
			}
			if tc.err != "" {
				if err == nil {
					t.Fatalf("lang=%q: want error %s, got none", tc.lang, tc.err)
				}
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("lang=%q: got %v, want %s", tc.lang, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("lang=%q must be accepted (13.1.3 falls back to the "+
					"default collation), got %v", tc.lang, err)
			}
		})
	}

	// The unsupported languages do not merely compile -- they sort, and they
	// sort the way an omitted @lang would. Asserting the output is what
	// separates "the error was removed" from "the fallback was implemented".
	for _, lang := range []string{"qqq", "art-lojban", "xx-YY-ZZ", ""} {
		attr := ""
		if lang != "" {
			attr = ` lang="` + lang + `"`
		}
		src := `<xsl:stylesheet version="3.0"
		    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		  <xsl:template name="main"><out>
		    <xsl:perform-sort select="('b','A','a','B')">
		      <xsl:sort select="."` + attr + `/>
		    </xsl:perform-sort>
		  </out></xsl:template>
		</xsl:stylesheet>`
		sheet, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			t.Fatalf("lang=%q: compile: %v", lang, err)
		}
		res, err := sheet.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if err != nil {
			t.Fatalf("lang=%q: transform: %v", lang, err)
		}
		if got := res.String(); !strings.Contains(got, want) {
			t.Errorf("lang=%q sorted %q, want the omitted-@lang order %q",
				lang, got, want)
		}
	}
}

// A computed @lang takes the AVT path, which reports XTDE0030 rather than the
// static XTSE0020. The unsupported-language fallback has to apply there too.
func TestSortComputedLangUnsupportedFallsBack(t *testing.T) {
	for _, tc := range []struct {
		lang string
		err  string
	}{
		{lang: "qqq"},
		{lang: "xx-YY-ZZ"},
		{lang: "not a lang!", err: "XTDE0030"},
	} {
		src := `<xsl:stylesheet version="3.0"
		    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		  <xsl:template name="main"><out>
		    <xsl:variable name="L" select="'` + tc.lang + `'"/>
		    <xsl:perform-sort select="('b','A','a','B')">
		      <xsl:sort select="." lang="{$L}"/>
		    </xsl:perform-sort>
		  </out></xsl:template>
		</xsl:stylesheet>`
		sheet, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			t.Fatalf("lang=%q: compile: %v", tc.lang, err)
		}
		res, err := sheet.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("computed lang=%q: got %v, want %s", tc.lang, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("computed lang=%q must fall back, got %v", tc.lang, err)
		}
		if got := res.String(); !strings.Contains(got, "<out>A B a b</out>") {
			t.Errorf("computed lang=%q sorted %q, want A B a b", tc.lang, got)
		}
	}
}
