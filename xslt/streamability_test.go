package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Per-construct tests for the §19.8 streamability analysis.
//
// Each construct gets both arms. The positive arm shows that a construct which
// is not guaranteed-streamable is found to be roaming; the negative arm shows
// that a guaranteed-streamable construct is not. The negative arms are the
// important half: a spurious XTSE3430 rejects a valid stylesheet at compile
// time, which is a worse failure than a missing check.

// analyze parses an XPath expression and assesses it with a striding context
// posture, as inside xsl:stream (§19.6).
func analyze(t *testing.T, src string) (props, bool) {
	t.Helper()
	expr, err := xpath.Parse(src, nil)
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	return analyzeExpr(expr, postureStriding)
}

func TestAnalyzeExprPathsAndAxes(t *testing.T) {
	cases := []struct {
		expr    string
		want    props
		known   bool
		comment string
	}{
		// §19.8.2's own examples.
		{"2 + 2", groundedMotionless, true, "two grounded operands"},
		{"a/b/c", props{postureStriding, sweepConsuming}, true, "child steps stay striding"},
		{"descendant::c", props{postureCrawling, sweepConsuming}, true, "the descendant axis crawls"},
		{".", props{postureStriding, sweepMotionless}, true, "the context item takes the context posture"},
		{"@code", props{postureStriding, sweepMotionless}, true, "an attribute step reads nothing"},
		{"a/@code", props{postureStriding, sweepConsuming}, true, "the wider of consuming and motionless"},
		{"./@code", props{postureStriding, sweepMotionless}, true, "both operands motionless"},
		{"parent::a", props{postureClimbing, sweepMotionless}, true, "the parent axis climbs"},
		{"ancestor::a", props{postureClimbing, sweepMotionless}, true, "the ancestor axis climbs"},

		// The reordering axes are not streamable, and this is derived
		// rather than assumed: the analysis models them.
		{"following::b", roamingFreeRanging, true, "the following axis roams"},
		{"preceding-sibling::b", roamingFreeRanging, true, "preceding-sibling roams"},
		{"a/following::b", roamingFreeRanging, true, "a roaming step poisons the path"},

		// A climbing posture may not descend again (§19.8.8.8).
		{"parent::a/child::b", roamingFreeRanging, true, "descending from a climbing posture"},
		// But it may keep climbing, or take attributes.
		{"parent::a/@code", props{postureStriding, sweepMotionless}, true, "attributes of a parent"},
		{"ancestor::a/parent::b", props{postureClimbing, sweepMotionless}, true, "climbing twice"},

		// A crawling posture may not descend again by the axis table alone,
		// but §19.8.8.7's second phase rescues the case: every step is a
		// scanning step, so the path as a whole crawls.
		{"descendant::c/child::d", props{postureCrawling, sweepConsuming}, true, "a scanning path crawls"},
		// The rescue is only for scanning steps. A following:: step is not
		// one, so this stays roaming.
		{"descendant::c/following::d", roamingFreeRanging, true, "following:: never scans"},
		// The posture is that of the right-hand step; the sweep is the
		// wider of the two, so the consuming descendant step carries.
		{"descendant::c/@code", props{postureStriding, sweepConsuming}, true, "attributes of a descendant"},

		// A descendant step that cannot select an element cannot nest, so
		// it is striding rather than crawling (§19.8.8.8).
		{"descendant::text()", props{postureStriding, sweepConsuming}, true, "text nodes never nest"},
		{"descendant::text()/parent::a", props{postureClimbing, sweepConsuming}, true, "and may then be climbed from"},
	}
	for _, c := range cases {
		got, known := analyze(t, c.expr)
		if known != c.known {
			t.Errorf("%s: known = %v, want %v", c.expr, known, c.known)
		}
		if got != c.want {
			t.Errorf("%s (%s): got %v/%v, want %v/%v",
				c.expr, c.comment, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

func TestAnalyzeExprFunctionCalls(t *testing.T) {
	cases := []struct {
		expr  string
		want  props
		known bool
	}{
		// §19.8.2's worked examples for function calls.
		{"count(a/b/c)", props{postureGrounded, sweepConsuming}, true},
		{"sum(a/b/c)", props{postureGrounded, sweepConsuming}, true},
		{"count(descendant::c)", props{postureGrounded, sweepConsuming}, true},
		{"sum(descendant::c)", props{postureGrounded, sweepConsuming}, true},
		{"tail(descendant::c)", props{postureCrawling, sweepConsuming}, true},
		// The singleton rule narrows crawling to striding.
		{"head(descendant::c)", props{postureStriding, sweepConsuming}, true},
		{"zero-or-one(descendant::c)", props{postureStriding, sweepConsuming}, true},
		{"exactly-one(descendant::c)", props{postureStriding, sweepConsuming}, true},
		// but tail does not.
		{"count(.)", props{postureGrounded, sweepMotionless}, true},
		{"string(.)", props{postureGrounded, sweepConsuming}, true},
		{"count(@a)", props{postureGrounded, sweepMotionless}, true},
		{"string(@a)", props{postureGrounded, sweepMotionless}, true},
		// A function absent from the table leaves the analysis with no
		// opinion, and says so.
		{"fn:innermost(descendant::c)", roamingFreeRanging, false},
		{"fn:reverse(a/b)", roamingFreeRanging, false},
	}
	for _, c := range cases {
		got, known := analyze(t, c.expr)
		if known != c.known {
			t.Errorf("%s: known = %v, want %v", c.expr, known, c.known)
		}
		if got != c.want {
			t.Errorf("%s: got %v/%v, want %v/%v",
				c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

func TestAnalyzeExprAbsorptionOfAttributes(t *testing.T) {
	// §19.8.2: "price * @discount is grounded and consuming", because the
	// attribute operand's absorption is downgraded to inspection. The same
	// expression with two child steps is roaming, and the pair is what shows
	// the downgrade is doing the work.
	got, known := analyze(t, "price * @discount")
	if !known {
		t.Fatal("price * @discount should be fully modelled")
	}
	if want := (props{postureGrounded, sweepConsuming}); got != want {
		t.Errorf("price * @discount = %v/%v, want grounded/consuming", got.posture, got.sweep)
	}
	got, _ = analyze(t, "price - discount")
	if got.streamable() {
		t.Errorf("price - discount should be roaming, got %v/%v", got.posture, got.sweep)
	}
}

func TestAnalyzeExprConditional(t *testing.T) {
	// §19.8.1's note: "if (X) then @name else name is guaranteed streamable"
	// because the arms form a choice operand group.
	got, known := analyze(t, "if (@x) then @name else name")
	if !known {
		t.Fatal("that conditional should be fully modelled")
	}
	if !got.streamable() {
		t.Errorf("if (@x) then @name else name should be streamable, got %v/%v",
			got.posture, got.sweep)
	}
	// Two consuming operands that are not alternatives are not streamable.
	got, _ = analyze(t, "(a, b)")
	if got.streamable() {
		t.Errorf("(a, b) should be roaming, got %v/%v", got.posture, got.sweep)
	}
	// But two motionless transmitting operands of the same posture are:
	// §19.8.1's "(@a, @b) is motionless and striding".
	got, known = analyze(t, "(@a, @b)")
	if !known {
		t.Fatal("(@a, @b) should be fully modelled")
	}
	if want := (props{postureStriding, sweepMotionless}); got != want {
		t.Errorf("(@a, @b) = %v/%v, want striding/motionless", got.posture, got.sweep)
	}
}

func TestAnalyzeExprPredicates(t *testing.T) {
	// §19.8.8.8: a step whose predicate is not motionless is roaming. This is
	// the rule the suite's sf-count-901 and sx-gc-eq-902 turn on.
	got, known := analyze(t, "BOOKLIST/BOOKS/ITEM[AUTHOR='Jasper Fforde']")
	if !known {
		t.Fatal("that path should be fully modelled")
	}
	if got.streamable() {
		t.Errorf("a consuming predicate should make the step roaming, got %v/%v",
			got.posture, got.sweep)
	}

	// The negative arm: a motionless predicate leaves the step alone. An
	// attribute test reads nothing from the stream, so the step keeps the
	// posture and sweep it had.
	got, known = analyze(t, "BOOKLIST/BOOKS/ITEM[@code]")
	if !known {
		t.Fatal("that path should be fully modelled")
	}
	if !got.streamable() {
		t.Errorf("a motionless predicate should be streamable, got %v/%v",
			got.posture, got.sweep)
	}
	if want := (props{postureStriding, sweepConsuming}); got != want {
		t.Errorf("ITEM[@code] = %v/%v, want striding/consuming", got.posture, got.sweep)
	}
}

// --- the XTSE3430 check ------------------------------------------------------

// compileSrc compiles a stylesheet and returns the error, if any.
func compileSrc(t *testing.T, src string) error {
	t.Helper()
	stree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	_, err = Compile(stree.Root, CompileOptions{})
	return err
}

const streamableShell = `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="main">
    <xsl:source-document streamable="yes" href="in.xml">
      <out>%s</out>
    </xsl:source-document>
  </xsl:template>
</xsl:stylesheet>`

func stylesheetWith(body string) string {
	return strings.Replace(streamableShell, "%s", body, 1)
}

func TestStreamabilityRaisesXTSE3430(t *testing.T) {
	// A predicate that consumes the stream inside a streamable
	// source-document. This is the shape of the suite's sf-count-901.
	err := compileSrc(t, stylesheetWith(
		`<xsl:copy-of select="count(./BOOKLIST/BOOKS/ITEM[AUTHOR='Jasper Fforde'])"/>`))
	if err == nil {
		t.Fatal("a consuming predicate in a streamable source-document should be XTSE3430")
	}
	if !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("want XTSE3430, got: %v", err)
	}
}

func TestStreamabilityRaisesOnReorderingAxis(t *testing.T) {
	err := compileSrc(t, stylesheetWith(
		`<xsl:value-of select="following::x"/>`))
	if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("the following axis should be XTSE3430, got: %v", err)
	}
}

func TestStreamabilityRaisesOnTwoConsumingOperands(t *testing.T) {
	err := compileSrc(t, stylesheetWith(
		`<xsl:value-of select="price - discount"/>`))
	if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("two consuming operands should be XTSE3430, got: %v", err)
	}
}

// TestStreamabilityAcceptsStreamableConstructs is the negative arm, and the
// one that guards against a spurious error. Every stylesheet here is
// guaranteed-streamable, and none of them may be rejected.
func TestStreamabilityAcceptsStreamableConstructs(t *testing.T) {
	bodies := []string{
		// The §19.8.2 examples that are streamable.
		`<xsl:value-of select="count(a/b/c)"/>`,
		`<xsl:value-of select="sum(descendant::c)"/>`,
		`<xsl:value-of select="price * @discount"/>`,
		`<xsl:value-of select="2 + 2"/>`,
		`<xsl:sequence select="head(descendant::c)"/>`,
		`<xsl:sequence select="if (@x) then @name else name"/>`,
		`<xsl:sequence select="(@a, @b)"/>`,
		`<xsl:copy-of select="a/b/c"/>`,
		`<xsl:value-of select="BOOKLIST/BOOKS/ITEM[@code]"/>`,
		`<xsl:value-of select="."/>`,
		`<xsl:value-of select="@code"/>`,
		`<xsl:value-of select="parent::a/@code"/>`,
		// Constructs the analysis does not model must not be rejected
		// either: an unmodelled construct is "no opinion", not an error.
		`<xsl:sequence select="a|b"/>`,
		`<xsl:sequence select="for $i in 1 to 3 return $i"/>`,
		`<xsl:sequence select="reverse(a/b)"/>`,
		`<xsl:sequence select="map{'a': 1}"/>`,
		`<xsl:sequence select="innermost(descendant::c)"/>`,
	}
	for _, b := range bodies {
		if err := compileSrc(t, stylesheetWith(b)); err != nil &&
			strings.Contains(err.Error(), "XTSE3430") {
			t.Errorf("%s should not raise XTSE3430, got: %v", b, err)
		}
	}
}

func TestStreamabilityIgnoresNonStreamableContexts(t *testing.T) {
	// The same expression that is XTSE3430 inside a streamable
	// source-document must be accepted where nothing is streamable. Without
	// this, the check would reject ordinary stylesheets everywhere.
	src := `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/">
	    <out>
	      <xsl:value-of select="following::x"/>
	      <xsl:value-of select="price - discount"/>
	      <xsl:copy-of select="count(./BOOKLIST/BOOKS/ITEM[AUTHOR='x'])"/>
	    </out>
	  </xsl:template>
	</xsl:stylesheet>`
	if err := compileSrc(t, src); err != nil && strings.Contains(err.Error(), "XTSE3430") {
		t.Errorf("a non-streamable template must not raise XTSE3430: %v", err)
	}

	// And an xsl:source-document without streamable="yes" is not streamable.
	src2 := `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:source-document href="in.xml">
	      <out><xsl:value-of select="following::x"/></out>
	    </xsl:source-document>
	  </xsl:template>
	</xsl:stylesheet>`
	if err := compileSrc(t, src2); err != nil && strings.Contains(err.Error(), "XTSE3430") {
		t.Errorf("a non-streamable source-document must not raise XTSE3430: %v", err)
	}
}

func TestStreamabilityAssessesAForEachBodyInItsOwnPosture(t *testing.T) {
	// §19.8.4.18 assesses the body of an xsl:for-each with the context
	// posture of its select expression, so the analysis descends into it --
	// and must use the right posture when it does.
	//
	// The select "a/b" is striding, so inside the loop "." is striding too.
	// A step on the following axis from a striding posture is roaming and
	// free-ranging by the §19.8.8.8 table (it is one of the "any other
	// combination" rows), so this stylesheet is correctly rejected.
	err := compileSrc(t, stylesheetWith(
		`<xsl:for-each select="a/b"><xsl:value-of select="following::x"/></xsl:for-each>`))
	if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
		t.Errorf("a for-each body selecting on the following axis must raise XTSE3430, got %v", err)
	}

	// The companion case, which guards against the rule rejecting every
	// for-each: a child step from the same striding posture is streamable.
	if err := compileSrc(t, stylesheetWith(
		`<xsl:for-each select="a/b"><xsl:value-of select="c"/></xsl:for-each>`)); err != nil {
		t.Errorf("a streamable for-each body was refused: %v", err)
	}
}

func TestAnalyzeExprScanningExpressions(t *testing.T) {
	// §19.8.8.7's second phase. A path whose provisional posture is roaming
	// is reassessed: if every step only descends, with motionless
	// non-positional predicates, the path scans and is crawling (or striding
	// where it cannot select an element).
	//
	// This is the rule that makes "//ITEM" streamable, and without it the
	// analysis rejects most of the streaming corpus.
	cases := []struct {
		expr    string
		want    props
		comment string
	}{
		{"//ITEM", props{postureCrawling, sweepConsuming}, "the canonical scanning path"},
		{".//ITEM", props{postureCrawling, sweepConsuming}, "the same relative to ."},
		{"//text()", props{postureStriding, sweepConsuming}, "text nodes never nest, so striding"},
		{"a//b/c", props{postureCrawling, sweepConsuming}, "child steps after a descendant step still scan"},

		// A scanning prefix leaves a posture the rest of the path can be
		// assessed against: "//PRICE" crawls, and climbing from there is
		// what the axis table allows.
		{"//PRICE/..", props{postureClimbing, sweepConsuming}, "a parent step after a scanning prefix"},
		{"//PRICE/../@code", props{postureStriding, sweepConsuming}, "and then an attribute"},

		// §19.8.8.7's note: a positional predicate is allowed on the child
		// axis but not on the descendant axis.
		{"a/b[1]//text()", props{postureStriding, sweepConsuming}, "[1] on the child axis still scans"},
		// "a//b[1]" puts the predicate on the expanded child::b step, so it
		// still scans; the descendant axis has to be written out to place a
		// positional predicate on it.
		{"a//b[1]/c", props{postureCrawling, sweepConsuming}, "[1] lands on the child step"},
		{"descendant::b[1]/c", roamingFreeRanging, "[1] on the descendant axis does not scan"},

		// A step that is not a scanning step stops the rescue.
		{"//PRICE/following::x", roamingFreeRanging, "following:: never scans"},
		// A consuming predicate is not motionless, so the step does not scan.
		{"//ITEM[AUTHOR='x']/PRICE", roamingFreeRanging, "a consuming predicate stops the scan"},
	}
	for _, c := range cases {
		got, known := analyze(t, c.expr)
		if !known {
			t.Errorf("%s should be fully modelled", c.expr)
		}
		if got != c.want {
			t.Errorf("%s (%s): got %v/%v, want %v/%v",
				c.expr, c.comment, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

func TestAnalyzeExprPredicateOnAValuelessNode(t *testing.T) {
	// §19.8.1's absorption downgrade applies to the context item inside a
	// predicate too. In "@code[. = 'x']" and "text()[. < 1000]" the context
	// item is an attribute or a text node, whose whole value is already in
	// hand, so the predicate is motionless and the step survives.
	//
	// The element form is the contrast: in "PAGES[. < 1000]" the context
	// item is an element, absorbing it reads the subtree, and the step is
	// roaming.
	streamable := []string{
		"@code[. = 'x']",
		"text()[. < 1000]",
		"a/b/text()[. < 1000][. > 0]",
	}
	for _, s := range streamable {
		got, known := analyze(t, s)
		if !known {
			t.Errorf("%s should be fully modelled", s)
		}
		if !got.streamable() {
			t.Errorf("%s should be streamable, got %v/%v", s, got.posture, got.sweep)
		}
	}
	got, _ := analyze(t, "PAGES[. < 1000]")
	if got.streamable() {
		t.Errorf("PAGES[. < 1000] should be roaming, got %v/%v", got.posture, got.sweep)
	}
}
