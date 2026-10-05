package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The accumulators applicable to the document of the initial match selection
// are the initial mode's use-accumulators list, and an absent list is empty.
//
// 18.2.2: "For a document containing nodes supplied in the initial match
// selection, the accumulators that are applicable are those determined by the
// xsl:mode declaration of the initial mode." The attribute's default is "an
// empty list", so a mode declared with <xsl:mode on-no-match="shallow-copy"/>
// and nothing else makes no accumulator applicable, and reading one is
// XTDE3362. Issue #16: go-xml ran the stylesheet below and used the value;
// Saxon reports "Accumulator chapter-pos is not applicable to the current
// document". A declared mode without the attribute used to be permissive.
func TestInitialModeUseAccumulators(t *testing.T) {
	const doc = `<doc><chapter/><chapter/></doc>`
	sheet := func(decls, body string) string {
		return `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:m="urn:m"
			exclude-result-prefixes="xs m" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:accumulator name="chapter-pos" as="xs:integer" initial-value="0">
			<xsl:accumulator-rule match="chapter" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:accumulator name="other" as="xs:integer" initial-value="7">
			<xsl:accumulator-rule match="chapter" select="$value"/>
		</xsl:accumulator>
		` + decls + body + `
		</xsl:transform>`
	}
	const readPos = `<xsl:template match="chapter"><c n="{accumulator-before('chapter-pos')}"/></xsl:template>`
	run := func(t *testing.T, src string, opts TransformOptions) (string, error) {
		t.Helper()
		stree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(stree.Root, CompileOptions{})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		dtree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Transform(context.Background(), dtree.Root, opts)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(res.String()), nil
	}
	const both = `<doc><c n="1"/><c n="2"/></doc>`

	cases := []struct {
		name, src string
		opts      TransformOptions
		want      string // "" means XTDE3362
	}{
		{"declared mode without use-accumulators (issue #16)",
			sheet(`<xsl:mode on-no-match="shallow-copy"/>`, readPos), TransformOptions{}, ""},
		{"no xsl:mode at all",
			sheet(``, readPos), TransformOptions{}, ""},
		{"use-accumulators names it",
			sheet(`<xsl:mode on-no-match="shallow-copy" use-accumulators="chapter-pos"/>`, readPos),
			TransformOptions{}, both},
		{"use-accumulators #all",
			sheet(`<xsl:mode on-no-match="shallow-copy" use-accumulators="#all"/>`, readPos),
			TransformOptions{}, both},
		{"named initial mode lists it",
			sheet(`<xsl:mode name="m:m" on-no-match="shallow-copy" use-accumulators="chapter-pos"/>`,
				`<xsl:template match="chapter" mode="m:m"><c n="{accumulator-before('chapter-pos')}"/></xsl:template>`),
			TransformOptions{InitialMode: "m:m"}, both},
		{"only the unnamed mode lists it, initial mode is named",
			sheet(`<xsl:mode use-accumulators="chapter-pos"/><xsl:mode name="m:m" on-no-match="shallow-copy"/>`,
				`<xsl:template match="chapter" mode="m:m"><c n="{accumulator-before('chapter-pos')}"/></xsl:template>`),
			TransformOptions{InitialMode: "m:m"}, ""},
		// A later mode's list neither grants nor withholds: the initial mode
		// lists chapter-pos, mode m lists only "other", and a template in m
		// still reads chapter-pos.
		{"a non-initial mode's list does not withhold",
			sheet(`<xsl:mode use-accumulators="chapter-pos"/><xsl:mode name="m:m" use-accumulators="other"/>`,
				`<xsl:template match="doc"><doc><xsl:apply-templates mode="m:m"/></doc></xsl:template>
				<xsl:template match="chapter" mode="m:m"><c n="{accumulator-before('chapter-pos')}"/></xsl:template>`),
			TransformOptions{}, both},
		{"a non-initial mode's list does not grant",
			sheet(`<xsl:mode/><xsl:mode name="m:m" use-accumulators="chapter-pos"/>`,
				`<xsl:template match="doc"><xsl:apply-templates mode="m:m"/></xsl:template>
				<xsl:template match="chapter" mode="m:m"><c n="{accumulator-before('chapter-pos')}"/></xsl:template>`),
			TransformOptions{}, ""},
		// The copy is applicable iff the original is (18.2.2), so the
		// snapshot follows the initial mode's list.
		{"snapshot inherits: listed",
			sheet(`<xsl:mode on-no-match="shallow-copy" use-accumulators="chapter-pos"/>`,
				`<xsl:template match="doc"><doc><xsl:for-each select="(snapshot() => root())//chapter"><c n="{accumulator-before('chapter-pos')}"/></xsl:for-each></doc></xsl:template>`),
			TransformOptions{}, both},
		{"snapshot inherits: not listed",
			sheet(`<xsl:mode on-no-match="shallow-copy"/>`,
				`<xsl:template match="doc"><doc><xsl:for-each select="(snapshot() => root())//chapter"><c n="{accumulator-before('chapter-pos')}"/></xsl:for-each></doc></xsl:template>`),
			TransformOptions{}, ""},
		// The rule is about the initial match selection only. Under
		// call-template invocation there is none, so the global context
		// item's document falls under 18.2.2's opening default: "An
		// accumulator is applicable to a tree unless otherwise specified".
		{"initial template over the global context item",
			sheet(``, `<xsl:template name="xsl:initial-template"><doc><xsl:for-each select="//chapter"><c n="{accumulator-before('chapter-pos')}"/></xsl:for-each></doc></xsl:template>`),
			TransformOptions{InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL}, both},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := run(t, c.src, c.opts)
			if c.want == "" {
				if err == nil || !strings.Contains(err.Error(), "XTDE3362") {
					t.Fatalf("want XTDE3362, got %q, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// fn:transform's nested run is a transformation of its own, so its own initial
// mode decides for its source-node -- even when the outer run's initial mode
// had made every accumulator applicable to that same document.
func TestInitialModeUseAccumulatorsFnTransform(t *testing.T) {
	outer := func(innerMode string) string {
		return `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:mode use-accumulators="#all"/>
		<xsl:variable name="inner" as="xs:string"><![CDATA[<xsl:transform
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:xs="http://www.w3.org/2001/XMLSchema"
			exclude-result-prefixes="xs" version="3.0">
			<xsl:accumulator name="chapter-pos" as="xs:integer" initial-value="0">
				<xsl:accumulator-rule match="chapter" select="$value + 1"/>
			</xsl:accumulator>
			` + innerMode + `
			<xsl:template match="/"><n><xsl:value-of select="//chapter[2]/accumulator-before('chapter-pos')"/></n></xsl:template>
		</xsl:transform>]]></xsl:variable>
		<xsl:template match="/">
			<xsl:sequence select="transform(map{'stylesheet-text': $inner, 'source-node': /})?output"/>
		</xsl:template>
		</xsl:transform>`
	}
	const doc = `<doc><chapter/><chapter/></doc>`

	res, err := transformResult(t, outer(`<xsl:mode use-accumulators="#all"/>`), doc)
	if err != nil {
		t.Fatalf("inner mode lists #all: %v", err)
	}
	if got, want := strings.TrimSpace(res.String()), "<n>2</n>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	_, err = transformResult(t, outer(``), doc)
	if err == nil || !strings.Contains(err.Error(), "XTDE3362") {
		t.Fatalf("inner run has no xsl:mode: want XTDE3362, got %v", err)
	}
}
