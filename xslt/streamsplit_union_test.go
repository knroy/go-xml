package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// unionBodies are container bodies whose select is a set operator over
// streamed paths (streamsplit_union.go); stream says the core must take them.
var unionBodies = []struct {
	body   string
	stream bool
}{
	{`<out><xsl:value-of select="count(//rec | //sub)"/></out>`, true},
	{`<out><xsl:value-of select="count(doc/rec union doc/other)"/></out>`, true},
	{`<out><xsl:value-of select="count(//rec union //rec/sub union //*)"/></out>`, true},
	{`<out><xsl:value-of select="count(//rec except //rec[@k = 'a'])"/></out>`, true},
	{`<out><xsl:value-of select="count(//sub except //rec/sub)"/></out>`, true},
	{`<out><xsl:value-of select="count(//* intersect //rec)"/></out>`, true},
	{`<out><xsl:value-of select="exists(//sub intersect /doc/rec/sub)"/></out>`, true},
	{`<out><xsl:value-of select="empty(//rec intersect //sub)"/></out>`, true},
	{`<out><xsl:value-of select="count((//rec | //w) except //w//rec)"/></out>`, true},
	{`<out><xsl:value-of select="count(/doc/rec[1] | /doc/rec[2]/sub)"/></out>`, true},
	{`<out><xsl:value-of select="count(.//text() | .//sub)"/></out>`, true},
	{`<out><xsl:copy-of select="//rec | //sub"/></out>`, true},
	// Child steps only, but branches of different lengths: matches nest.
	{`<out><xsl:copy-of select="/doc/rec | /doc/rec/sub"/></out>`, true},
	// ... so a loop whose body reads its match is refused.
	{`<out><xsl:for-each select="/doc/rec | /doc/rec/sub"><e><xsl:value-of select="."/></e></xsl:for-each></out>`, false},
	{`<out><xsl:copy-of select="/doc/rec except /doc/rec[@k = 'b']"/></out>`, true},
	{`<out><xsl:value-of select="(//rec | //sub)/@k" separator=","/></out>`, true},
	{`<out><xsl:for-each select="/doc/rec | /doc/other"><e n="{name()}"/></xsl:for-each></out>`, true},
	// Simple maps (mapOf) and a filter by position under data() (liftData).
	{`<out><xsl:value-of select="(//rec | //sub) ! name()" separator=","/></out>`, true},
	{`<out><xsl:value-of select="//rec ! string(@k)" separator=","/></out>`, true},
	{`<out><xsl:value-of select="/doc/rec/@k ! string(.) ! upper-case(.)" separator=","/></out>`, true},
	{`<out><xsl:copy-of select="/doc/rec ! (sub | other)"/></out>`, true},
	{`<out><xsl:copy-of select="/doc/rec ! (sub except sub[@x = '2'])"/></out>`, true},
	{`<out><xsl:copy-of select="/doc/rec ! sub"/></out>`, true},
	{`<out><xsl:value-of select="(/doc/rec | //sub)[position() mod 2 = 0]" separator=","/></out>`, true},
	{`<out><xsl:for-each select="/doc/rec"><xsl:value-of select="(. | sub) ! local-name()"/></xsl:for-each></out>`, true},
	// Refused: operands with a tail of their own.
	{`<out><xsl:value-of select="(/doc/rec/@k | //sub/@x)[position() lt 3]" separator=","/></out>`, false},
	// One tail of axis steps on both: (A union B)/T (J1, sharedTail).
	{`<out><xsl:value-of select="(/doc/rec[1]/sub/ancestor::* union /doc/rec[2]/sub/ancestor::*) ! name()" separator=","/></out>`, true},
	{`<out><xsl:value-of select="(//sub/ancestor-or-self::*/@* union //rec/ancestor-or-self::*/@*)" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="(/doc/rec/@k | //sub/@k)" separator=","/></out>`, true},
	// ... a union only: except and intersect do not distribute; each operand
	// keeps its own climbing tail (L5, streamsplit_climbset.go).
	{`<out><xsl:value-of select="(//sub/ancestor::* except //rec/ancestor::*) ! name()" separator=","/></out>`, true},
	{`<out><xsl:value-of select="(/doc/rec[1]/sub/ancestor::* intersect /doc/rec[2]/sub/ancestor::*) ! name()" separator=","/></out>`, true},
	{`<out><xsl:value-of select="(//sub/ancestor-or-self::*/@* intersect //rec/ancestor-or-self::*/@*)" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="(//rec/ancestor-or-self::*/@k except //sub/ancestor::*/@k)" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="count(//sub/ancestor::* intersect //rec/ancestor-or-self::*)"/></out>`, true},
	// A map reading position() is refused by the split, but held at the
	// document node (streamhold.go) it streams.
	{`<out><xsl:value-of select="(//rec | //sub) ! position()" separator=","/></out>`, true},
	{`<out><xsl:value-of select="(/doc/rec | //sub)[. = 't3']" separator=","/></out>`, false},
	// The analysis does not trust (A | B)/C: no plan is built.
	{`<out><xsl:value-of select="(/doc/rec | /doc/w)/rec/@v" separator=","/></out>`, false},
	// A branch that can match a text, comment or PI node keeps one (fuzz
	// seeds 23489, 28756, 32026: branchKinds); refused before G4, kept since
	// (streamleaf.go), output equal to the tree's.
	{`<out><xsl:copy-of select="//comment() except /doc/rec"/></out>`, true},
	{`<out><xsl:copy-of select="/doc/rec ! (node() union sub)"/></out>`, true},
	{`<out><xsl:value-of select="count(//comment() | //sub)"/></out>`, true},
	// Refused: a predicate that may fail (the operands' errors come in
	// another order); the context itself as a match not read on its view.
	{`<out><xsl:value-of select="count(//rec[xs:integer(@v) gt 1] | //sub)"/></out>`, false},
	{`<out><xsl:for-each select="/doc/rec"><xsl:value-of select="count(. | sub)"/></xsl:for-each></out>`, false},

	// J1: a union with a variable's nodes under any holder: the outer sorts
	// the matches against them as the tree does (streamsplit_foreign.go).
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="//rec union $ins"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="$ins | /doc/rec/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="sum(//rec/@v union $ins/@v)"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="(//sub union $ins)[position() mod 2 = 0]" separator=","/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="($ins union //rec) ! name()" separator=","/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="copy-of(//sub) union $ins"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="(//rec[1]//text() union $ins)[position() lt 4]"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="(//sub/ancestor::* union $ins) ! name()" separator=","/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="(//sub/ancestor-or-self::*/@k union $ins)" separator="|"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="($ins | /doc/rec) ! sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="copy-of(//sub) except $ins"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="count($ins intersect copy-of(//rec))"/></out>`, true},
	// (A, B)/T and insert-before(A, n, B)/T as A/T union B/T (distributedPath);
	// an atomic item: XPTY0019 as the tree raises it.
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="($ins, /doc/rec)/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="(/doc/rec, $ins)/@v" separator=","/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="insert-before(/doc/rec, 2, $ins)/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="insert-before($ins, 1, /doc/rec)/*"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="(1, /doc/rec)/sub"/></out>`, true},
	// [A, B]?*/T is (A, B)/T.
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:copy-of select="[$ins, /doc/rec]?*/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:value-of select="[/doc/rec, $ins]?*/@v" separator=","/></out>`, true},
	// A loop over it is a population (streampop.go): still the tree.
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b v="9">7</b></xsl:variable><out><xsl:for-each select="/doc/rec union $ins"><e n="{name()}"/></xsl:for-each></out>`, false},
}

func TestStreamUnionMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range unionBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
