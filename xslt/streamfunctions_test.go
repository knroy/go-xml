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

// TestVarPostureTable transcribes the table in §19.8.8.11 giving the posture
// of a reference to a streaming parameter. Both the singular and non-singular
// rows are asserted, since three categories differ between them.
func TestVarPostureTable(t *testing.T) {
	cases := []struct {
		cat      streamCategory
		singular bool
		want     posture
	}{
		{catAbsorbing, true, postureGrounded},
		{catAbsorbing, false, postureRoaming},
		{catInspection, true, postureGrounded},
		{catInspection, false, postureGrounded},
		{catFilter, true, postureStriding},
		{catFilter, false, postureStriding},
		{catShallowDescent, true, postureStriding},
		{catShallowDescent, false, postureRoaming},
		{catDeepDescent, true, postureStriding},
		{catDeepDescent, false, postureRoaming},
		{catAscent, true, postureStriding},
		{catAscent, false, postureStriding},
	}
	for _, c := range cases {
		if got := varPosture(c.cat, c.singular); got != c.want {
			t.Errorf("varPosture(%v, singular=%v) = %v, want %v",
				c.cat, c.singular, got, c.want)
		}
	}
}

// TestTypePermitsNodes checks the §19.8.8.11 test for whether a parameter's
// declared type permits nodes: "The declared type permits nodes if the as
// attribute on the xsl:param element is absent, or if it is a SequenceType
// that maps to a U-type that has a non-empty intersection with U{N}."
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
// §19.8.8.11 gives a reference to the streaming parameter of an absorbing
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
