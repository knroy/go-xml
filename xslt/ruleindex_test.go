package xslt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// ruleIndexLow is imported, so every rule in it ranks below the main module.
const ruleIndexLow = `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p">
  <xsl:template match="a" priority="9">[L-a9 <xsl:next-match/>]</xsl:template>
  <xsl:template match="*">[L-* <xsl:next-match/>]</xsl:template>
  <xsl:template match="p:a | @id | text()">[L-union <xsl:next-match/>]</xsl:template>
  <xsl:template match="node()" mode="#all">[L-node <xsl:next-match/>]</xsl:template>
  <xsl:template match="@*" mode="#all">[L-@* <xsl:next-match/>]</xsl:template>
</xsl:stylesheet>`

// ruleIndexMain mixes precedence, priority, union branches with their own
// default priorities, predicate and call patterns, and every node kind, and
// lets every rule continue with xsl:next-match so each chain walks the whole
// ordering from every position.
const ruleIndexMain = `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p">
  <xsl:import href="low.xsl"/>
  <xsl:output method="xml" omit-xml-declaration="yes"/>
  <xsl:key name="k" match="b" use="@k"/>
  <xsl:variable name="bs" select="//b"/>
  <xsl:mode name="f" on-multiple-match="fail"/>
  <xsl:template match="/">
    <out>
      <xsl:apply-templates select="//node() | //@*"/>
      <m><xsl:apply-templates select="//node() | //@*" mode="m"/></m>
      <ns><xsl:apply-templates select="/doc/namespace::*" mode="m"/></ns>
      <f><xsl:apply-templates select="//c" mode="f"/></f>
    </out>
  </xsl:template>
  <xsl:template match="doc/a">[doc/a <xsl:next-match/>]</xsl:template>
  <xsl:template match="a">[a <xsl:next-match/>]</xsl:template>
  <xsl:template match="a[@id]" priority="0.5">[a@id <xsl:next-match/>]</xsl:template>
  <xsl:template match="b[1]">[b1 <xsl:next-match/>]</xsl:template>
  <xsl:template match="a | b | text() | p:*">[union <xsl:next-match/>]</xsl:template>
  <xsl:template match="a | comment()" priority="0">[u0 <xsl:next-match/>]</xsl:template>
  <xsl:template match="*:a">[*:a <xsl:next-match/>]</xsl:template>
  <xsl:template match="p:a">[p:a <xsl:next-match/>]</xsl:template>
  <xsl:template match="@id">[@id <xsl:next-match/>]</xsl:template>
  <xsl:template match="@*">[@* <xsl:next-match/>]</xsl:template>
  <xsl:template match="attribute(k)">[attr(k) <xsl:next-match/>]</xsl:template>
  <xsl:template match="element(b)">[elem(b) <xsl:next-match/>]</xsl:template>
  <xsl:template match="processing-instruction(pi)">[pi <xsl:next-match/>]</xsl:template>
  <xsl:template match="processing-instruction()">[pi() <xsl:next-match/>]</xsl:template>
  <xsl:template match="comment()">[comment <xsl:next-match/>]</xsl:template>
  <xsl:template match="text()[normalize-space()]">[text <xsl:next-match/>]</xsl:template>
  <xsl:template match="document-node(element(doc))">[docnode <xsl:next-match/>]</xsl:template>
  <xsl:template match="key('k', 'x')">[key <xsl:next-match/>]</xsl:template>
  <xsl:template match="id('i1')">[id <xsl:next-match/>]</xsl:template>
  <xsl:template match="$bs">[$bs <xsl:next-match/>]</xsl:template>
  <xsl:template match=".[self::c]">[.c <xsl:next-match/>]</xsl:template>
  <xsl:template match="doc//c" priority="-1">[doc//c <xsl:next-match/>]</xsl:template>
  <xsl:template match="a" mode="m">[m-a <xsl:next-match/>]</xsl:template>
  <xsl:template match="b" mode="m #default">[m-b <xsl:next-match/>]</xsl:template>
  <xsl:template match="namespace-node()" mode="m">[m-ns <xsl:next-match/>]</xsl:template>
  <xsl:template match="c" mode="f">[f-c]</xsl:template>
  <xsl:template match="c[@t]" mode="f" priority="0">[f-c@t]</xsl:template>
  <xsl:template match="text()" mode="#all" priority="-2">[all-text <xsl:next-match/>]</xsl:template>
</xsl:stylesheet>`

const ruleIndexSource = `<?pi x?><!--top--><doc xmlns:p="urn:p" xmlns:q="urn:q">
  <a id="i1">t1</a><a/><b k="x">t2</b><b k="y"/><p:a id="i2"/><q:a/>
  <?pi y?><?other z?><!--c-->
  <c/><c u="1"/><e><a k="z"><b/></a></e>
</doc>`

// runRuleIndexSheet runs ruleIndexMain with the rule index on or off.
func runRuleIndexSheet(t *testing.T, indexed bool, source string) (string, error) {
	t.Helper()
	stree, err := xdm.ParseString(ruleIndexMain, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{
		Resolver: memModules{"low.xsl": ruleIndexLow}})
	if err != nil {
		t.Fatal(err)
	}
	if s.rules == nil {
		t.Fatal("the stylesheet was compiled without a rule index")
	}
	if !indexed {
		s.rules = nil
	}
	dtree, err := xdm.ParseString(source, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := res.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String(), nil
}

// TestRuleIndexMatchesLinearScan checks that dispatch through the rule index
// picks the same rule, and continues next-match chains in the same order, as
// the linear scan over every template.
func TestRuleIndexMatchesLinearScan(t *testing.T) {
	want, err := runRuleIndexSheet(t, false, ruleIndexSource)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runRuleIndexSheet(t, true, ruleIndexSource)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("indexed dispatch differs from the linear scan\n got: %s\nwant: %s", got, want)
	}
	// Sanity: the chains really run through both modules and all kinds.
	for _, s := range []string{"[id [a@id [doc/a [u0 [union [a [*:a [L-a9 ", "[pi [pi() ",
		"[key ", "[$bs ", "[m-ns ", "[attr(k) ", "[L-union ", "[f-c]"} {
		if !strings.Contains(got, s) {
			t.Errorf("output lacks %q:\n%s", s, got)
		}
	}
}

// TestRuleIndexMultipleMatchFails checks XTDE0540 survives the index: the
// tie in mode f must still be found and reported.
func TestRuleIndexMultipleMatchFails(t *testing.T) {
	src := strings.Replace(ruleIndexSource, `<c u="1"/>`, `<c t="1"/>`, 1)
	_, errLinear := runRuleIndexSheet(t, false, src)
	_, errIndexed := runRuleIndexSheet(t, true, src)
	// c[@t] at priority 0 and c at default priority 0 tie.
	if errIndexed == nil || !strings.Contains(errIndexed.Error(), "XTDE0540") {
		t.Fatalf("indexed: got %v, want XTDE0540", errIndexed)
	}
	if errLinear == nil || errLinear.Error() != errIndexed.Error() {
		t.Errorf("errors differ:\n indexed: %v\n  linear: %v", errIndexed, errLinear)
	}
}

// TestRuleIndexCoversEveryCandidate checks the index's invariant directly: a
// rule whose node test admits a node (Pattern.mayMatch) is in the list the
// index serves for that node, in every mode.
func TestRuleIndexCoversEveryCandidate(t *testing.T) {
	stree, _ := xdm.ParseString(ruleIndexMain, xdm.ParseOptions{})
	s, err := Compile(stree.Root, CompileOptions{
		Resolver: memModules{"low.xsl": ruleIndexLow}})
	if err != nil {
		t.Fatal(err)
	}
	dtree, _ := xdm.ParseString(ruleIndexSource, xdm.ParseOptions{})
	var nodes []*xdm.Node
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		nodes = append(nodes, n)
		for i := range n.NumAttrs() {
			nodes = append(nodes, n.AttrAt(i))
		}
		for ns := range n.NamespaceNodes() {
			nodes = append(nodes, ns)
		}
		for c := range n.Children() {
			walk(c)
		}
	}
	walk(dtree.Root)
	for mode := range s.rules {
		for _, n := range nodes {
			cand, ok := s.candidates(n, mode)
			if !ok {
				t.Fatalf("no index for %v in mode %q", n.Kind(), mode)
			}
			in := map[int]bool{}
			for _, i := range cand {
				in[i] = true
			}
			for i, r := range s.templates {
				if r.matchesMode(mode) && r.Match.mayMatch(n) && !in[i] {
					t.Errorf("mode %q: rule %q may match %s %q but is not a candidate",
						mode, r.Match.src, kindName(n.Kind()), n.Name().Local)
				}
			}
		}
	}
}

func kindName(k xdm.NodeKind) string {
	return [...]string{"document", "element", "attribute", "text", "comment",
		"pi", "namespace"}[k]
}
