package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Tests for §19.8.9.4's third condition: "The focus-setting container of C is
// F", where C is a call on fn:current-group() and F is its nearest containing
// xsl:for-each-group.
//
// §19.6 defines the focus-setting container as the innermost focus-changing
// construct containing the construct in a controlled operand, so a
// focus-changing instruction between the call and the group takes the title
// and the condition fails. The refusing arm below is si-group-031, whose
// catalog entry records the resolution of bug 29482 as "this is not
// streamable"; the accepting arms are the shapes that must keep compiling,
// and they are the half that matters -- a spurious XTSE3430 refuses a valid
// stylesheet at compile time, where the author has no way around it.

// analyzeRuleBody assesses the body of a template rule of a streamable mode,
// which is what §19.6's third clause makes a focus-setting container in its
// own right, with a striding context posture.
func analyzeRuleBody(t *testing.T, body string) (props, bool) {
	t.Helper()
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xsl:mode streamable="yes"/>
	<xsl:template match="/*">` + body + `</xsl:template>
	</xsl:transform>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	var rule *xdm.Node
	walkElements(doc.Root, func(el *xdm.Node) bool {
		if rule == nil && isXSL(el, "template") && el.Attr("", "match") != nil {
			rule = el
		}
		return rule == nil
	})
	if rule == nil {
		t.Fatal("no template rule in the test stylesheet")
	}
	return analyzeSequenceConstructor(rule, postureStriding,
		attributeSetDeclarations(doc.Root), collectStreamFuncs(doc.Root))
}

// refusesRuleBody reports whether checkStreamableModeBodies raises XTSE3430
// for a template rule of a streamable mode with the given body.
func refusesRuleBody(t *testing.T, body string) error {
	t.Helper()
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xsl:mode streamable="yes"/>
	<xsl:template match="/*">` + body + `</xsl:template>
	</xsl:transform>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	return checkStreamableModeBodies(doc.Root)
}

func TestCurrentGroupDisplacedByAFocusSettingCopyIsRoaming(t *testing.T) {
	// si-group-031, reduced. The call sits inside <xsl:copy select="$root">,
	// whose select makes the xsl:copy a focus-changing construct, so the
	// focus-setting container of the call is that xsl:copy and not the
	// xsl:for-each-group. §19.8.9.4's third condition fails, and its
	// "otherwise" clause gives the call roaming and free-ranging.
	//
	// The stylesheet's own inline comment argues the other way, from $root
	// being grounded. §19.8.9.4 does not ask what the intervening container
	// binds its focus to; it asks only which construct sets it, and the
	// catalog records the Working Group's answer.
	p, known := analyzeRuleBody(t,
		`<xsl:variable name="root" as="element()"><xsl:copy/></xsl:variable>`+
			`<xsl:for-each-group select="product" group-adjacent="position()">`+
			`<xsl:copy select="$root"><xsl:copy-of select="current-group()"/></xsl:copy>`+
			`</xsl:for-each-group>`)
	if !known {
		t.Fatal("a current-group() call displaced by <xsl:copy select=...> was " +
			"reported unmodelled; §19.8.9.4 gives it a definite answer")
	}
	if p.streamable() {
		t.Errorf("got %v and %v; §19.8.9.4's third condition fails when a "+
			"focus-changing xsl:copy separates the call from its group, "+
			"so the call is roaming and free-ranging", p.posture, p.sweep)
	}
	if err := refusesRuleBody(t, `<xsl:variable name="root" as="element()"><xsl:copy/></xsl:variable>`+
		`<xsl:for-each-group select="product" group-adjacent="position()">`+
		`<xsl:copy select="$root"><xsl:copy-of select="current-group()"/></xsl:copy>`+
		`</xsl:for-each-group>`); err == nil {
		t.Error("no XTSE3430 was raised; the refusal rests on §19.8.9.4 alone, " +
			"not on the §19.8.8.4 widening bodyCallsCurrentGroup withholds for")
	} else if !strings.Contains(err.Error(), "XTSE3430") {
		t.Errorf("got %v, want an XTSE3430", err)
	}
}

func TestCurrentGroupUnderABareCopyStillBelongsToItsGroup(t *testing.T) {
	// The acceptance arm, and the distinction the rule turns on. An xsl:copy
	// with NO select is not focus-changing: §19.8.4.12 assesses its body
	// "with ... the outer focus", so it sets no focus and displaces nothing.
	// si-group-018, -019 and -030 all write current-group() inside a bare
	// <xsl:copy> and are expected to compile.
	body := `<xsl:for-each-group select="product" group-adjacent="@cat">` +
		`<xsl:copy><xsl:value-of select="count(current-group())"/></xsl:copy>` +
		`</xsl:for-each-group>`
	if err := refusesRuleBody(t, body); err != nil {
		t.Errorf("a bare <xsl:copy> around current-group() was refused: %v; "+
			"an xsl:copy without a select sets no focus, so §19.8.9.4's "+
			"third condition still holds", err)
	}
}

func TestCurrentGroupDisplacedFromAGroundedGroupIsAccepted(t *testing.T) {
	// si-group-048, reduced: the group's select is "copy-of(tr)", which is
	// grounded, and the call sits inside an xsl:for-each. The catalog
	// asserts OUTPUT.
	//
	// §19.8.9.4's "otherwise" clause is written without qualification, but
	// its own note says why the conditions are there -- "for streamed
	// evaluation to be possible, a call to current-group must not appear in
	// a construct that is evaluated repeatedly" -- and a group already
	// materialised in memory can be read as often as one likes. Every other
	// clause of this analysis that refuses an xsl:for-each-group exempts a
	// grounded select the same way.
	body := `<xsl:for-each-group select="copy-of(tr)" group-adjacent="@cat">` +
		`<xsl:for-each select="current-group()">` +
		`<xsl:value-of select="current-group()[1]/@cat"/>` +
		`</xsl:for-each></xsl:for-each-group>`
	if err := refusesRuleBody(t, body); err != nil {
		t.Errorf("a grounded group was refused for a displaced current-group(): "+
			"%v; a group built by copy-of sits in memory and reads nothing "+
			"from the stream", err)
	}
}

func TestForEachGroupWithNoDisplacementIsUnaffected(t *testing.T) {
	// The plain shape: the call is a direct descendant of the group's body
	// with nothing focus-changing between them, so the third condition holds
	// and §19.8.9.4 gives the call the select's own properties. This is the
	// arm that would go quiet if subFocus were applied to every sub().
	body := `<xsl:for-each-group select="product" group-adjacent="@cat">` +
		`<out><xsl:value-of select="count(current-group())"/></out>` +
		`</xsl:for-each-group>`
	if err := refusesRuleBody(t, body); err != nil {
		t.Errorf("an undisplaced current-group() was refused: %v", err)
	}
}

func TestNestedForEachGroupReclaimsItsOwnCalls(t *testing.T) {
	// An inner xsl:for-each-group re-establishes the focus-setting container
	// for calls below it, even where a focus-changing instruction separates
	// the inner group from the outer one: the call's nearest containing
	// group is the inner one, and that group IS its focus-setting container.
	body := `<xsl:for-each-group select="product" group-adjacent="@cat">` +
		`<xsl:for-each select="1 to 3">` +
		`<xsl:for-each-group select="copy-of(*)" group-adjacent="@k">` +
		`<xsl:value-of select="count(current-group())"/>` +
		`</xsl:for-each-group></xsl:for-each></xsl:for-each-group>`
	if err := refusesRuleBody(t, body); err != nil {
		t.Errorf("a call reclaimed by a nested xsl:for-each-group was refused: %v", err)
	}
}
