package xslt

import (
	"bytes"
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// jsonErrDocs are JSON XML documents, valid and not: each kind of error,
// and several in one document, where the tree's order decides which is
// raised (xmlToJSON checks a map's or array's whole content for text before
// its members, and a member's key before its name).
var jsonErrDocs = []string{
	`<array><foo/></array>`,
	`<array><foo/>x</array>`,
	`<array><number>x</number><string>y</string></array>`,
	`<array><number>x</number>t</array>`,
	`<array><array><number>x</number></array>t</array>`,
	`<array><array><number>x</number>u</array>t</array>`,
	`<array><array><number>x</number>u</array></array>`,
	`<array><array/><array>u<foo/></array><number>x</number></array>`,
	`<map><null/></map>`,
	`<map><null/>t</map>`,
	`<map><null key="a"/><null key="a"/></map>`,
	`<map><null key="a"/><null key="a"/>x</map>`,
	`<map><null key="\u0041" escaped-key="1"/><null key="A"/></map>`,
	`<map><null key="a" escaped-key="maybe" bad="1"/></map>`,
	`<map><null key="\q" escaped-key="true"/></map>`,
	`<map><null key="\u00G1" escaped-key="true"/></map>`,
	`<map><null key="\u00" escaped-key="true"/></map>`,
	`<map><null key="a\" escaped-key="true"/></map>`,
	`<map><foo key="a" escaped-key="true"/></map>`,
	`<map><x:foo xmlns:x="urn:x"/></map>`,
	`<map><x:foo xmlns:x="urn:x" key="k"/></map>`,
	`<map><null key="a" bad="1"/>t</map>`,
	`<null><a>z</a>q</null>`,
	`<null><a/></null>`,
	`<null>q<a/></null>`,
	`<null> x </null>`,
	`<array><null><foo>t</foo></null> t</array>`,
	`<string escaped="maybe"><a/></string>`,
	`<string escaped="maybe">\q</string>`,
	`<string escaped="true">\q</string>`,
	`<string escaped="true">a\</string>`,
	`<string escaped="true">\u12</string>`,
	`<string escaped="true">\u12x4</string>`,
	`<string>a<null/>b</string>`,
	`<boolean>maybe</boolean>`,
	`<boolean> 1 </boolean>`,
	`<boolean>a<b/></boolean>`,
	`<number>INF</number>`,
	`<number>NaN</number>`,
	`<number>1e999</number>`,
	`<number> +005 </number>`,
	`<number>1<!--c-->2</number>`,
	`<array bad="1"><foo/></array>`,
	`<array j:x="1" xmlns:j="http://www.w3.org/2005/xpath-functions"><foo/></array>`,
	`<array o:x="1" xmlns:o="urn:o" key="k" escaped-key="no"><number escaped="no">1</number></array>`,
	`<x:array xmlns:x="urn:x"/>`,
	`<thing/>`,
	`<array><map><number key="a">1</number><string key="b"><x/></string></map><boolean>no</boolean> t</array>`,
	`<array><map><number key="a">1</number><string key="b"><x/></string></map><boolean>no</boolean></array>`,
	`<map><array key="B"><number>x</number></array><array key="B"/></map>`,
	`<map><array key="B"/><array key="C"><array key="B"><null key="B"/></array></array></map>`,
	`<map><array key="B"><string><x key="B"/></string></array></map>`,
	`<map><array key="B"><number>x</number></array></map>`,
	`<map><array key="B"><null/></array>t</map>`,
	`<array><foo key="B"><x/><x/></foo></array>`,
	`<array><bad:x xmlns:bad="urn:b"><map/><array/></bad:x></array>`,
	`<!--c--><map><string key="s">a/b"c&#9;` + "\u0085" + `</string><number key="n">2.50</number>` +
		`<array key="a"><number>1</number><!--x--><string escaped="true">\u00e9\n</string><map/><array/></array>` +
		`<map key="m"><string key="k\n" escaped-key="true">v</string><string key="k2">w</string></map>` + "\n</map><?p?>",
}

// jsonErrSource puts doc in the JSON namespace.
func jsonErrSource(doc string) string {
	i := strings.IndexAny(doc, " />")
	if strings.HasPrefix(doc, "<!--") {
		j := strings.Index(doc, "-->") + 3
		return doc[:j] + jsonErrSource(doc[j:])
	}
	return doc[:i] + ` xmlns="http://www.w3.org/2005/xpath-functions"` + doc[i:]
}

// jsonErrRandom is a small random tree of the representation with mistakes
// in it, made from r.
func jsonErrRandom(r *rand.Rand, depth int, inMap bool) string {
	names := []string{"map", "array", "array", "map", "null", "boolean", "number", "string", "foo"}
	if depth >= 3 {
		names = names[4:]
	}
	name := names[r.IntN(len(names))]
	var b strings.Builder
	b.WriteString("<" + name)
	if inMap && r.IntN(8) > 0 {
		keys := []string{"a", "B", "a", `\u0061`, `\q`, "c"}
		b.WriteString(` key="` + keys[r.IntN(len(keys))] + `"`)
		if r.IntN(4) == 0 {
			b.WriteString(` escaped-key="` + []string{"true", "0", "maybe"}[r.IntN(3)] + `"`)
		}
	} else if r.IntN(6) == 0 {
		b.WriteString(` key="B"`)
	}
	switch r.IntN(14) {
	case 0:
		b.WriteString(` bad="1"`)
	case 1:
		b.WriteString(` escaped="` + []string{"true", "maybe"}[r.IntN(2)] + `"`)
	}
	b.WriteString(">")
	text := func() {
		switch r.IntN(10) {
		case 0:
			b.WriteString("t")
		case 1, 2:
			b.WriteString(" ")
		case 3:
			b.WriteString("<!--c-->")
		}
	}
	switch name {
	case "map", "array", "foo":
		for range r.IntN(4) {
			text()
			b.WriteString(jsonErrRandom(r, depth+1, name == "map"))
		}
		text()
	default:
		vals := map[string][]string{
			"null":    {"", "", " ", "x"},
			"boolean": {"true", "0", " 1 ", "yes"},
			"number":  {"1", "-2.5e3", " +005 ", "x", "INF", "1e999"},
			"string":  {"s", `a\nb`, `\q`, `\u00e9`, "/"},
		}[name]
		b.WriteString(vals[r.IntN(len(vals))])
		if r.IntN(12) == 0 {
			b.WriteString("<k/>")
			text()
		}
	}
	b.WriteString("</" + name + ">")
	return b.String()
}

// TestStreamedJSONErrorsAreTrees: a streamed xml-to-json raises the tree's
// error -- its code, its message, and of several the one the tree raises
// first -- and writes the tree's JSON when there is none, in the main
// streamed transform (TransformReader: no tree to fall back on) and in a
// streamed xsl:source-document, for the document node (value-of to Output,
// a string), the document element, and a path, one match or several.
func TestStreamedJSONErrorsAreTrees(t *testing.T) {
	docs := append([]string(nil), jsonErrDocs...)
	r := rand.New(rand.NewPCG(9, 9))
	for range 300 {
		docs = append(docs, jsonErrRandom(r, 0, false))
	}
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<xsl:output method="text"/><xsl:template match="/">A<xsl:value-of select="xml-to-json(.)"/>B</xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(.)"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(., map{'indent': true()})"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><xsl:apply-templates/></xsl:template>` +
			`<xsl:template match="*"><j><xsl:value-of select="xml-to-json(.)"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(*)"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(//*[@key='B'], map{'indent': true()})"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(//*[@key='B']/*)"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(/*/*)"/></j></xsl:template>`, true},
		{`<xsl:template match="/"><j><xsl:value-of select="xml-to-json(/descendant::node()[../@key='B'][1])"/></j></xsl:template>`, false},
	}
	invalid, streamed := 0, 0
	for bi, b := range bodies {
		s := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
			`<xsl:mode streamable="yes"/>`+b.body+`</xsl:stylesheet>`)
		for di, d := range docs {
			src := jsonErrSource(d)
			doc, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("doc %d: %v", di, err)
			}
			var want, got bytes.Buffer
			_, terr := s.Transform(context.Background(), doc.Root, TransformOptions{Output: &want})
			before := streamtrace.Snapshot()
			_, serr := s.TransformReader(context.Background(), strings.NewReader(src), TransformOptions{Output: &got})
			st := streamtrace.Snapshot().Sub(before)
			if st.Runs[streamtrace.Main] > 0 {
				streamed++
			} else if b.stream {
				t.Errorf("body %d doc %d: not streamed", bi, di)
			}
			if st.Guards != 0 {
				t.Errorf("body %d doc %d: %d guard hits", bi, di, st.Guards)
			}
			switch {
			case (terr == nil) != (serr == nil) || terr != nil && terr.Error() != serr.Error():
				t.Errorf("body %d doc %d %s:\nstream %v\ntree   %v", bi, di, src, serr, terr)
			case terr == nil && got.String() != want.String():
				t.Errorf("body %d doc %d %s:\nstream %q\ntree   %q", bi, di, src, got.String(), want.String())
			case terr != nil:
				invalid++
			}
		}
	}
	t.Logf("%d runs, %d streamed, %d raising errors", len(bodies)*len(docs), streamed, invalid)
	if invalid < len(bodies)*len(docs)/3 {
		t.Errorf("only %d runs raise errors", invalid)
	}

	// In a streamed xsl:source-document: the container re-runs on the tree
	// after a streamed error, so the error is the tree's either way; the
	// stream must not raise one the tree does not.
	for di, d := range docs[:len(jsonErrDocs)] {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(jsonErrSource(d)), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, b := range []string{`<j><xsl:value-of select="xml-to-json(.)"/></j>`,
			`<j><xsl:value-of select="xml-to-json(//*[@key='B'])"/></j>`} {
			sheet := coreSheetWith("", b)
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
			if so != to || se != te {
				t.Errorf("source-document doc %d %s:\ncore %q %q\ntree %q %q", di, b, so, se, to, te)
			}
			if st.Guards != 0 {
				t.Errorf("source-document doc %d: %d guard hits", di, st.Guards)
			}
		}
	}
}
