package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The tests here assert what XSLT 3.0 §19.8.5 requires of a streamable
// stylesheet function, not what this implementation happens to produce. Each
// case names the rule it comes from, and several are transcribed from the
// worked examples the spec gives in §19.8.5.2 through §19.8.5.7.

// parseSheet parses a stylesheet source into its document root.
func parseSheet(t *testing.T, src string) *xdm.Node {
	t.Helper()
	stree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	return stree.Root
}

// parseExprForTest parses an XPath expression whose only prefix is "f", bound
// to urn:f, and returns it as a function call.
func parseExprForTest(t *testing.T, src string) *xpath.FuncCall {
	t.Helper()
	e, err := xpath.ParseVersion(src, testNS{}, xpath.XPath31)
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	call, ok := e.(*xpath.FuncCall)
	if !ok {
		t.Fatalf("%q parsed as %T, want a function call", src, e)
	}
	return call
}

// testNS resolves the single prefix these tests use.
type testNS struct{}

func (testNS) ResolvePrefix(p string) (string, bool) {
	if p == "f" {
		return "urn:f", true
	}
	return "", false
}

func (testNS) DefaultElementNamespace() string { return "" }

func (testNS) DefaultFunctionNamespace() string {
	return "http://www.w3.org/2005/xpath-functions"
}

// TestStreamCategoryDefaultsToUnclassified checks §19.8.5: "The category to
// which a function belongs is declared in the streamability attribute of the
// xsl:function declaration, and defaults to unclassified."
func TestStreamCategoryDefaultsToUnclassified(t *testing.T) {
	if got := parseStreamCategory(""); got != catUnclassified {
		t.Errorf("absent streamability = %v, want unclassified", got)
	}
	if catUnclassified.declaredStreamable() {
		t.Error("unclassified must not be declared-streamable")
	}
	for _, c := range []streamCategory{
		catAbsorbing, catInspection, catFilter,
		catShallowDescent, catDeepDescent, catAscent,
	} {
		if !c.declaredStreamable() {
			t.Errorf("%v must be declared-streamable", c)
		}
	}
}

// TestUnrecognizedCategoryIsUnclassified checks §19.8.5: a category given as a
// QName in an implementation-defined namespace must be analysed "as if
// streamablity='unclassified' were specified" by a processor that does not
// recognize it.
func TestUnrecognizedCategoryIsUnclassified(t *testing.T) {
	for _, v := range []string{"vendor:magic", "nonsense"} {
		if got := parseStreamCategory(v); got != catUnclassified {
			t.Errorf("streamability=%q = %v, want unclassified", v, got)
		}
	}
}

// TestBodyRequirementsMatchSpec transcribes the "Rules for the function body"
// paragraph of each of §19.8.5.2 through §19.8.5.7. A body whose result falls
// outside the listed posture/sweep pairs is not guaranteed-streamable.
func TestBodyRequirementsMatchSpec(t *testing.T) {
	cases := []struct {
		cat  streamCategory
		p    props
		want bool
		why  string
	}{
		// §19.8.5.2 absorbing: grounded; motionless or consuming.
		{catAbsorbing, props{postureGrounded, sweepMotionless}, true, "grounded+motionless"},
		{catAbsorbing, props{postureGrounded, sweepConsuming}, true, "grounded+consuming"},
		{catAbsorbing, props{postureStriding, sweepMotionless}, false, "striding is not grounded"},
		{catAbsorbing, props{postureGrounded, sweepFreeRanging}, false, "free-ranging sweep"},

		// §19.8.5.3 inspection: grounded; motionless only.
		{catInspection, props{postureGrounded, sweepMotionless}, true, "grounded+motionless"},
		{catInspection, props{postureGrounded, sweepConsuming}, false, "inspection may not consume"},
		{catInspection, props{postureStriding, sweepMotionless}, false, "striding is not grounded"},

		// §19.8.5.4 filter: striding or grounded; motionless only.
		{catFilter, props{postureStriding, sweepMotionless}, true, "striding+motionless"},
		{catFilter, props{postureGrounded, sweepMotionless}, true, "grounded+motionless"},
		{catFilter, props{postureStriding, sweepConsuming}, false, "filter may not consume"},
		{catFilter, props{postureCrawling, sweepMotionless}, false, "crawling not permitted"},

		// §19.8.5.5 shallow-descent: striding or grounded; motionless or consuming.
		{catShallowDescent, props{postureStriding, sweepConsuming}, true, "striding+consuming"},
		{catShallowDescent, props{postureGrounded, sweepMotionless}, true, "grounded+motionless"},
		{catShallowDescent, props{postureCrawling, sweepConsuming}, false, "crawling not permitted"},

		// §19.8.5.6 deep-descent: crawling, striding or grounded; motionless
		// or consuming. Crawling is what separates it from shallow-descent.
		{catDeepDescent, props{postureCrawling, sweepConsuming}, true, "crawling+consuming"},
		{catDeepDescent, props{postureStriding, sweepConsuming}, true, "striding+consuming"},
		{catDeepDescent, props{postureClimbing, sweepMotionless}, false, "climbing not permitted"},

		// §19.8.5.7 ascent: climbing or grounded; motionless only.
		{catAscent, props{postureClimbing, sweepMotionless}, true, "climbing+motionless"},
		{catAscent, props{postureGrounded, sweepMotionless}, true, "grounded+motionless"},
		{catAscent, props{postureClimbing, sweepConsuming}, false, "ascent may not consume"},
		{catAscent, props{postureStriding, sweepMotionless}, false, "striding not permitted"},
	}
	for _, c := range cases {
		req, ok := bodyRequirements[c.cat]
		if !ok {
			t.Fatalf("no body requirement for %v", c.cat)
		}
		if got := req.satisfiedBy(c.p); got != c.want {
			t.Errorf("%v body %v/%v = %v, want %v (%s)",
				c.cat, c.p.posture, c.p.sweep, got, c.want, c.why)
		}
	}
}

// TestVarPostureTable transcribes the "Rules for references to the streaming
// parameter" sentence from each of §19.8.5.2 through §19.8.5.7.
//
// The earlier version of this test cited §19.8.8.12 and asserted a table keyed
// on singularity, with grounded rows for absorbing and inspection. Both parts
// were wrong. §19.8.8.11 is *dynamic function calls*; the rule for a variable
// reference is §19.8.8.12, which says only "see the rules for the streamability
// category of the containing function, under 19.8.5" -- and every one of those
// six sections gives a single unconditional answer:
//
//	absorbing        striding   (§19.8.5.2, "striding and consuming" / "striding and motionless")
//	inspection       striding   (§19.8.5.3)
//	filter           striding   (§19.8.5.4)
//	shallow-descent  striding   (§19.8.5.5)
//	deep-descent     striding   (§19.8.5.6)
//	ascent           climbing   (§19.8.5.7)
//
// No section names grounded, and none is keyed on singularity. Reading
// grounded for absorbing and inspection is what let a function whose body
// returns the streaming parameter -- a NODE, which in a streamed tree is never
// grounded -- clear the "must be grounded" requirement its own category
// imposes. su-absorbing-901/-905 and su-inspection-901/-903 are the cases.
//
// Singularity is a real rule, but a different one: §19.8.4.18's note says a
// reference inside a higher-order operand "will not be singular, which in many
// cases will make the entire function non-streamable". It overrides the table
// rather than indexing it, so it is asserted separately below.
func TestVarPostureTable(t *testing.T) {
	cases := []struct {
		cat  streamCategory
		want posture
	}{
		{catAbsorbing, postureStriding},
		{catInspection, postureStriding},
		{catFilter, postureStriding},
		{catShallowDescent, postureStriding},
		{catDeepDescent, postureStriding},
		{catAscent, postureClimbing},
	}
	for _, c := range cases {
		if got := varPosture(c.cat, true); got != c.want {
			t.Errorf("varPosture(%v, singular) = %v, want %v", c.cat, got, c.want)
		}
	}
}

// A reference that is not singular is roaming whatever its category, which is
// the override §19.8.4.18's note describes. su-absorbing-906
// ("for $i in 1 to 3 return name($element[$i])"), su-shallow-descent-903
// ("(1 to 5) ! $n") and su-ascent-903 each want XTSE3430 and get it only
// through this rule: deleting it gained four cases and lost three, measured.
func TestVarPostureNonSingularIsRoaming(t *testing.T) {
	for _, c := range []streamCategory{
		catAbsorbing, catInspection, catFilter,
		catShallowDescent, catDeepDescent, catAscent,
	} {
		if got := varPosture(c, false); got != postureRoaming {
			t.Errorf("varPosture(%v, non-singular) = %v, want roaming; a "+
				"reference evaluated once per item of a higher-order operand "+
				"cannot be streamed", c, got)
		}
	}
}

// TestTypePermitsNodes checks whether a parameter's declared type permits
// nodes, which is the premise §19.8.5.2-.7 put on the streaming parameter.
//
// The sentence once quoted here as spec text -- "The declared type permits
// nodes if the as attribute on the xsl:param element is absent, or if it is a
// SequenceType that maps to a U-type that has a non-empty intersection with
// U{N}" -- is NOT in the Recommendation in that form. It is this
// implementation's own reading, assembled from §19.2's U-types and the
// default of item()* for an absent as attribute, and it is stated here as
// such rather than in quotation marks.
func TestTypePermitsNodes(t *testing.T) {
	yes := []string{
		"", "item()*", "node()", "node()*", "node()?", "element()*",
		"element(region)*", "document-node()", "attribute()", "text()",
		"schema-element(x)",
	}
	for _, v := range yes {
		if !typePermitsNodes(v) {
			t.Errorf("typePermitsNodes(%q) = false, want true", v)
		}
	}
	no := []string{"xs:integer", "xs:string", "xs:string?", "xs:anyAtomicType*"}
	for _, v := range no {
		if typePermitsNodes(v) {
			t.Errorf("typePermitsNodes(%q) = true, want false", v)
		}
	}
}

// TestTypePermitsChildren checks the U{document-node(), element()}
// intersection that §19.8.5.5 and §19.8.5.6 apply to T0. Only element and
// document nodes have children, so an attribute or text type must answer no.
func TestTypePermitsChildren(t *testing.T) {
	yes := []string{"", "item()*", "node()*", "element()*", "document-node()"}
	for _, v := range yes {
		if !typePermitsChildren(v) {
			t.Errorf("typePermitsChildren(%q) = false, want true", v)
		}
	}
	no := []string{"attribute()*", "text()", "comment()", "xs:integer",
		"processing-instruction()", "attribute(code)"}
	for _, v := range no {
		if typePermitsChildren(v) {
			t.Errorf("typePermitsChildren(%q) = true, want false", v)
		}
	}
}

// TestZeroArityFunctionIsUnclassified checks §19.8.5: "The only category
// permitted for a zero-arity function (one with no arguments) is
// unclassified."
func TestZeroArityFunctionIsUnclassified(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:f="urn:f">
  <xsl:function name="f:g" streamability="absorbing" as="xs:integer">
    <xsl:sequence select="1"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	for k, f := range funcs {
		if k.arity != 0 {
			continue
		}
		if f.category != catUnclassified {
			t.Errorf("zero-arity f:g category = %v, want unclassified "+
				"(§19.8.5 permits no other)", f.category)
		}
	}
}

// TestFirstArgUsagePerCategory checks the "Rules for function calls" of the
// categories that fix an operand usage for the first argument:
// absorbing gives it absorption (§19.8.5.2), inspection gives it inspection
// (§19.8.5.3), filter gives it transmission (§19.8.5.4), and ascent gives it
// inspection (§19.8.5.7).
func TestFirstArgUsagePerCategory(t *testing.T) {
	cases := []struct {
		cat  streamCategory
		want usage
	}{
		{catAbsorbing, usageAbsorption},
		{catInspection, usageInspection},
		{catFilter, usageTransmission},
		{catAscent, usageInspection},
	}
	for _, c := range cases {
		got, ok := firstArgUsage(c.cat)
		if !ok {
			t.Errorf("%v: no fixed first-argument usage, want %v", c.cat, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("%v first-argument usage = %v, want %v", c.cat, got, c.want)
		}
	}
	// shallow-descent and deep-descent compute their calls directly rather
	// than through a fixed first-argument usage.
	for _, c := range []streamCategory{catShallowDescent, catDeepDescent} {
		if _, ok := firstArgUsage(c); ok {
			t.Errorf("%v must not fix a first-argument usage: §19.8.5.5 and "+
				"§19.8.5.6 give explicit call rules instead", c)
		}
	}
}

// TestAscentCallRejectsConsuming checks the third rule of §19.8.5.7: "If S0 is
// not motionless, then the function call is roaming and free-ranging."
//
// No case in the W3C suite reaches this branch -- every ascent call there is
// already rejected by an earlier rule or by the body check -- so it is
// asserted directly. A call whose first argument consumes must be rejected
// even though the general rules found it otherwise streamable.
func TestAscentCallRejectsConsuming(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:up" as="element()*" streamability="ascent">
    <xsl:param name="in" as="element()*"/>
    <xsl:sequence select="$in/ancestor::*"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	f, ok := funcs[funcKey{uri: "urn:f", local: "up", arity: 1}]
	if !ok {
		t.Fatal("f:up not collected")
	}
	a := &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: true,
		known: true, funcs: funcs}
	// A synthetic P0 that is climbing and consuming: §19.8.5.7 requires the
	// call to be roaming and free-ranging, because S0 is not motionless.
	got := a.ascentFromP0(props{postureClimbing, sweepConsuming})
	if got.streamable() {
		t.Errorf("ascent call with a consuming first argument = %v/%v, "+
			"want roaming and free-ranging (§19.8.5.7)", got.posture, got.sweep)
	}
	// A climbing, motionless P0 must survive as climbing and motionless.
	got = a.ascentFromP0(props{postureClimbing, sweepMotionless})
	if got.posture != postureClimbing || got.sweep != sweepMotionless {
		t.Errorf("ascent call with a climbing motionless first argument = %v/%v, "+
			"want climbing and motionless (§19.8.5.7)", got.posture, got.sweep)
	}
	// A grounded P0 must give a grounded, motionless call.
	got = a.ascentFromP0(props{postureGrounded, sweepMotionless})
	if got.posture != postureGrounded || got.sweep != sweepMotionless {
		t.Errorf("ascent call with a grounded first argument = %v/%v, "+
			"want grounded and motionless (§19.8.5.7)", got.posture, got.sweep)
	}
	_ = f
}

// TestDescentCallRejectsNonGroundedRest checks the rule of §19.8.5.5 and
// §19.8.5.6: "If P1 is not grounded, the function call is roaming and
// free-ranging", where P1 is the posture of the construct formed from the
// arguments after the first.
//
// No case in the W3C suite reaches this rule, so it is asserted directly
// against callDescent.
//
// A note on what this can and cannot isolate. With the operand usages this
// analysis assigns, a later argument that returns streamed nodes always comes
// out *roaming*, never merely striding: a parameter whose declared type
// permits nodes gives its argument navigation usage (§19.4), and navigation
// from any streamed posture is free-ranging (§19.8.1). So the "not grounded"
// test and a narrower "is roaming" test cannot be told apart by any input
// reachable here. The test therefore asserts the rule's observable effect --
// a non-grounded P1 is rejected, a grounded one is not -- rather than claiming
// to pin the exact comparison. The wider form is kept in the code because it
// is what §19.8.5.5 says, and a future operand-usage refinement that made a
// later argument striding would need it.
func TestDescentCallRejectsNonGroundedRest(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:f="urn:f">
  <xsl:function name="f:kids" as="node()*" streamability="shallow-descent">
    <xsl:param name="in" as="element()*"/>
    <xsl:param name="other" as="element()*"/>
    <xsl:sequence select="$in/node()"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	f, ok := funcs[funcKey{uri: "urn:f", local: "kids", arity: 2}]
	if !ok {
		t.Fatal("f:kids not collected")
	}

	// A second argument that is striding and motionless. "self::a" from a
	// striding context is exactly that, and its declared parameter type
	// element()* gives it transmission rather than navigation, so it stays
	// streamable and P1 comes out striding -- not grounded.
	call := parseExprForTest(t, "f:kids(self::a, self::b)")
	a := &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: true,
		known: true, funcs: funcs}
	got, known := a.callDescent(call, f)
	if !known {
		t.Fatal("callDescent reported the call as unmodelled")
	}
	if got.streamable() {
		t.Errorf("shallow-descent call with a striding second argument = %v/%v, "+
			"want roaming and free-ranging: §19.8.5.5 requires P1 to be grounded",
			got.posture, got.sweep)
	}

	// The same call with a grounded second argument must survive, which is
	// what shows the rejection above came from P1 and not from something else.
	call = parseExprForTest(t, "f:kids(self::a, ())")
	a = &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: true,
		known: true, funcs: funcs}
	got, known = a.callDescent(call, f)
	if !known || !got.streamable() {
		t.Errorf("shallow-descent call with a grounded second argument = %v/%v, "+
			"want a streamable result", got.posture, got.sweep)
	}
}

// TestTypeDeterminedUsageAtomizes checks §19.8.5.1: the operand usage of an
// argument is the type-determined usage of the corresponding parameter. A
// parameter declared with an atomic type triggers atomization of any node
// supplied, which is absorption (§19.4).
func TestTypeDeterminedUsageAtomizes(t *testing.T) {
	if got := typeDeterminedUsage("xs:string"); got != usageAbsorption {
		t.Errorf("type-determined usage of xs:string = %v, want absorption", got)
	}
	if got := typeDeterminedUsage("node()*"); got != usageNavigation {
		t.Errorf("type-determined usage of node()* = %v, want navigation", got)
	}
}

// TestNavigationFromStreamingParamIsFreeRanging checks the rule that makes
// fn:path unstreamable inside a declared-streamable stylesheet function.
//
// §19.8.8.12 gives a reference to the streaming parameter of an absorbing
// function a *grounded* posture. §19.8.1 would then stop at "If P is grounded,
// then S' is S" and charge nothing for any usage at all. But the note under
// §19.8.5 says the nodes such a reference denotes "can only derive from
// streamed nodes passed in an argument to the function", and navigation
// reaches outside the subtree of such a node -- to an ancestor or a preceding
// sibling that a streaming processor has already discarded.
//
// So an operand that is grounded-but-streamed is free-ranging under a
// navigation usage, and unaffected under every other usage. §19.8.9's table
// gives fn:path the operand usage navigation, which is what test
// su-absorbing-912 ("The path() function is not streamable") relies on.
func TestNavigationFromStreamingParamIsFreeRanging(t *testing.T) {
	for _, tc := range []struct {
		usage usage
		want  sweep
		why   string
	}{
		{usageNavigation, sweepFreeRanging,
			"navigation leaves the subtree the stream still holds"},
		{usageAbsorption, sweepMotionless,
			"absorption reads the subtree forward, which a grounded posture already accounts for"},
		{usageInspection, sweepMotionless,
			"inspection reads no further than the node itself"},
		{usageTransmission, sweepMotionless,
			"transmission hands on a value the grounded posture says is not streamed"},
	} {
		o := operand{
			props:            groundedMotionless,
			usage:            tc.usage,
			allowsChildren:   true,
			streamedGrounded: true,
		}
		if got := o.adjustedSweep(); got != tc.want {
			t.Errorf("adjusted sweep of a grounded streamed operand under %v = %v, want %v (%s)",
				tc.usage, got, tc.want, tc.why)
		}
	}

	// Without the streamedGrounded mark, a grounded operand keeps its own
	// sweep under every usage, navigation included (§19.8.1).
	o := operand{props: groundedMotionless, usage: usageNavigation, allowsChildren: true}
	if got := o.adjustedSweep(); got != sweepMotionless {
		t.Errorf("adjusted sweep of a plain grounded operand under navigation = %v, "+
			"want motionless (§19.8.1: if P is grounded, S' is S)", got)
	}
}

// TestPathOnStreamingParamIsNotStreamable checks the same rule end to end, on
// the shape su-absorbing-912 uses: an absorbing function whose body is
// "$input ! path()". §19.8.5.2 requires the body of an absorbing function to
// be grounded, so a free-ranging body is not guaranteed-streamable.
//
// The call reaches fn:path through the simple map operator, so this also
// covers the context-item half of the rule: "path()" is "path(.)", and "."
// here denotes the very node $input denotes.
func TestPathOnStreamingParamIsNotStreamable(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:z" as="xs:string*" streamability="absorbing">
    <xsl:param name="input" as="node()*"/>
    <xsl:sequence select="$input ! path()"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	f, ok := funcs[funcKey{uri: "urn:f", local: "z", arity: 1}]
	if !ok {
		t.Fatal("f:z not collected")
	}
	p, known := analyzeFunctionBody(f, funcs)
	if !known {
		t.Fatal("the body of f:z was not modelled, so no verdict is reached")
	}
	if p.streamable() {
		t.Errorf("body of an absorbing f:z = %v/%v, want not streamable: "+
			"fn:path navigates away from the streaming parameter (§19.8.9, §19.8.1)",
			p.posture, p.sweep)
	}
	if satisfiesCategory(f, p) {
		t.Error("a free-ranging body must not satisfy the absorbing category (§19.8.5.2)")
	}

	// The same body without fn:path stays streamable, so the rejection above
	// is the navigation usage and not the shape of the expression.
	doc2 := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:z" as="xs:string*" streamability="absorbing">
    <xsl:param name="input" as="node()*"/>
    <xsl:sequence select="$input ! name()"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs2 := collectStreamFuncs(doc2)
	f2 := funcs2[funcKey{uri: "urn:f", local: "z", arity: 1}]
	p2, known2 := analyzeFunctionBody(f2, funcs2)
	if !known2 || !p2.streamable() {
		t.Errorf("body of \"$input ! name()\" = %v/%v known=%v, want streamable: "+
			"fn:name has usage inspection, which costs nothing here",
			p2.posture, p2.sweep, known2)
	}
}

// TestStreamingParameterSignatureRule pins §19.8.5's signature rule, which is
// a constraint on the DECLARATION rather than on the body.
//
// Five of the six categories carry the identical sentence: "If the declared
// type of the streaming parameter permits more than one node, the function is
// not guaranteed-streamable" (§19.8.5.3 inspection, .4 filter, .5
// shallow-descent, .6 deep-descent, .7 ascent). §19.8.5.2 absorbing is the
// exception -- "there are no constraints" -- so the category is consulted
// rather than the rule applied uniformly.
//
// This rule was missing, and its absence is what left su-inspection-902,
// su-shallow-descent-906 and su-ascent-902 running when the suite wants
// XTSE3430; each of their descriptions reads "first arg accepts a sequence".
//
// A KNOWN TENSION, recorded rather than hidden: §19.8.5.7 states the rule and
// then gives a worked example that violates it --
//
//	<xsl:function name="f:containing-section" as="element(section)"
//	              streamability="ascent">
//	  <xsl:param name="input" as="element(para)*"/>
//	  <xsl:sequence select="$input/ancestor::section[last()]"/>
//	</xsl:function>
//
// -- of which the spec says "the function body meets the rules for this
// category". The body does; the signature does not, and the example is silent
// about the signature rule stated three paragraphs above it. The W3C test
// suite sides with the rule, not the example: su-ascent-902 is that example's
// shape and wants XTSE3430. The rule is implemented and the example is
// therefore refused, which the last subtest states explicitly so that the
// choice is visible to whoever reads this next.
func TestStreamingParameterSignatureRule(t *testing.T) {
	mk := func(t *testing.T, body string) error {
		t.Helper()
		doc, err := xdm.ParseString(
			`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"`+
				` xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema"`+
				` version="3.0">`+body+
				`<xsl:template match="/"><out/></xsl:template>`+
				`</xsl:stylesheet>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		_, err = Compile(doc.Root, CompileOptions{})
		return err
	}
	// The body has to satisfy each category's OWN body rule, or the negative
	// arms below would pass for the wrong reason and the positive arms would
	// fail for it. inspection and filter permit only a motionless sweep, so
	// their body may not read the stream: name() is motionless where string()
	// is consuming. ascent must be climbing or grounded, which an ancestor
	// step gives.
	bodies := map[string]string{
		"inspection":      `name($p)`,
		"filter":          `$p`,
		"shallow-descent": `$p`,
		"deep-descent":    `$p`,
		"ascent":          `$p/ancestor::x[last()]`,
		"absorbing":       `string($p)`,
	}
	fn := func(cat, as string) string {
		return `<xsl:function name="f:x" streamability="` + cat + `">` +
			`<xsl:param name="p" as="` + as + `"/>` +
			`<xsl:sequence select="` + bodies[cat] + `"/></xsl:function>`
	}

	// The five categories that carry the rule refuse "*" and "+".
	for _, cat := range []string{
		"inspection", "filter", "shallow-descent", "deep-descent", "ascent",
	} {
		for _, as := range []string{"node()*", "node()+", "element(para)*"} {
			if err := mk(t, fn(cat, as)); err == nil {
				t.Errorf("%s with a streaming parameter declared %q was "+
					"accepted; §19.8.5 makes a type permitting more than one "+
					"node not guaranteed-streamable", cat, as)
			}
		}
		// ... and accept the singular forms, which is what keeps this a
		// cardinality rule rather than a blanket refusal.
		for _, as := range []string{"node()", "node()?", "element(para)"} {
			if err := mk(t, fn(cat, as)); err != nil {
				t.Errorf("%s with a streaming parameter declared %q was "+
					"refused: %v; at most one node is exactly what the rule "+
					"permits", cat, as, err)
			}
		}
	}

	// absorbing is the exception: §19.8.5.2 says "there are no constraints".
	for _, as := range []string{"node()*", "node()+", "node()?"} {
		if err := mk(t, fn("absorbing", as)); err != nil {
			t.Errorf("absorbing with a streaming parameter declared %q was "+
				"refused: %v; §19.8.5.2 places no constraint on the signature",
				as, err)
		}
	}

	// A parameter that permits no nodes at all is not a streaming parameter,
	// so the rule has nothing to say about its cardinality.
	if err := mk(t, fn("inspection", "xs:string*")); err != nil {
		t.Errorf("a non-node streaming parameter was refused: %v; the rule "+
			"is about nodes", err)
	}

	// The tension above, stated as an assertion so it cannot be forgotten.
	t.Run("the spec's own §19.8.5.7 example is refused by its own rule", func(t *testing.T) {
		err := mk(t, `<xsl:function name="f:cs" as="element(section)" `+
			`streamability="ascent"><xsl:param name="input" as="element(para)*"/>`+
			`<xsl:sequence select="$input/ancestor::section[last()]"/></xsl:function>`)
		if err == nil {
			t.Fatal("the worked example was accepted; if that is now " +
				"deliberate, su-ascent-902 has changed verdict and this test " +
				"and the conformance-gaps entry must both be revisited")
		}
	})
}
