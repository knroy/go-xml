package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestIntersectExceptDefaultPriority pins the default priority of an
// IntersectExceptExprP.
//
// Section 6.5: "If the top-level pattern is an IntersectExceptExprP
// containing two or more PathExprP operands separated by intersect or except
// operators, then the priority of the pattern is that of the first
// PathExprP." The whole form used to score 0.5 — the value every other
// general pattern gets — so "a except b" outranked rules the spec makes the
// winner, and "* except b" outranked a plain name test.
//
// The W3C suite matches such patterns (match-273 and friends) but never puts
// one in a conflict, so a wrong priority is invisible there.
func TestIntersectExceptDefaultPriority(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xsl:template match="a except b"/>
	<xsl:template match="a intersect b"/>
	<xsl:template match="* except b"/>
	<xsl:template match="a/c except b"/>
	<xsl:template match="element(e,xs:string) except b"/>
	</xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := map[string]float64{
		// the first operand is a name test
		"a except b":    0,
		"a intersect b": 0,
		// the first operand is "*", which scores -0.5
		"* except b": -0.5,
		// the first operand is a two-step path, which scores 0.5
		"a/c except b": 0.5,
		// the first operand names both a name and a type, which scores 0.25
		"element(e,xs:string) except b": 0.25,
	}
	seen := 0
	for _, tm := range st.templates {
		if tm.Match == nil {
			continue
		}
		w, ok := want[tm.Match.String()]
		if !ok {
			continue
		}
		seen++
		if tm.Priority != w {
			t.Errorf("pattern %q: default priority %v, want %v",
				tm.Match.String(), tm.Priority, w)
		}
	}
	if seen != len(want) {
		t.Errorf("matched %d patterns, want %d", seen, len(want))
	}
}

// TestIntersectExceptPriorityConflict is the same rule seen through conflict
// resolution: "a except b" has priority 0, so an explicit priority="0.25"
// rule beats it. While the form scored 0.5 the except rule won instead.
func TestIntersectExceptPriorityConflict(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:output method="text"/>
	<xsl:template match="/"><xsl:apply-templates select="//a"/></xsl:template>
	<xsl:template match="a except b">EXCEPT</xsl:template>
	<xsl:template match="a" priority="0.25">EXPLICIT</xsl:template>
	</xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src, err := xdm.ParseString(`<r><a/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := st.Transform(context.Background(), src.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if got := b.String(); got != "EXPLICIT" {
		t.Errorf("got %q, want %q: priority=0.25 outranks the default "+
			"priority 0 of \"a except b\"", got, "EXPLICIT")
	}
}
