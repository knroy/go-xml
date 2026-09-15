package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Tests for §19.8.9.4 through §19.8.9.7, the streamability of the
// current-group family of functions.
//
// Each case states what the spec requires and cites the clause. The rules are
// short enough to quote: current-grouping-key, current-merge-key and
// current-merge-group are grounded and motionless unconditionally, while
// current-group takes "the sweep and posture of the select expression" of the
// containing xsl:for-each-group, and is otherwise roaming and free-ranging.

func TestCurrentGroupingKeyIsGroundedMotionless(t *testing.T) {
	// §19.8.9.5: "A call to the current-grouping-key function is grounded
	// and motionless." The key is an atomic value already computed, so
	// naming it reads nothing further from the stream.
	p, known := analyze(t, "current-grouping-key()")
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"a call to current-grouping-key()")
}

func TestCurrentMergeKeyAndGroupAreGroundedMotionless(t *testing.T) {
	// §19.8.9.7 for current-merge-key and §19.8.9.6 for current-merge-group.
	// The spec justifies the latter: "the nodes to be merged are always
	// snapshots, and therefore grounded".
	for _, fn := range []string{"current-merge-key()", "current-merge-group()"} {
		p, known := analyze(t, fn)
		wantProps(t, p, known, postureGrounded, sweepMotionless, "a call to "+fn)
	}
}

func TestCurrentGroupOutsideForEachGroupIsRoaming(t *testing.T) {
	// §19.8.9.4's final clause: with no containing xsl:for-each-group the
	// call is "roaming and free-ranging". That is a fact about the
	// stylesheet rather than a gap in this analysis, so the verdict must
	// come back modelled -- if it did not, the caller would withhold the
	// XTSE3430 that the rule exists to produce.
	p, known := analyze(t, "current-group()")
	if !known {
		t.Fatal("current-group() with no containing xsl:for-each-group was " +
			"reported unmodelled; §19.8.9.4 gives it a definite answer")
	}
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"current-group() with no containing xsl:for-each-group")
}

func TestCurrentGroupTakesThePropertiesOfTheSelect(t *testing.T) {
	// §19.8.9.4: where the call has a containing xsl:for-each-group F, no
	// higher-order operand lies on the path between them, and F is the
	// focus-setting container of the call, "the sweep and posture of C are
	// the sweep and posture of the select expression of F".
	//
	// The select here is "ITEM": striding and motionless. Inspecting the
	// group once never widens a sweep, so the construct stays streamable.
	p, known := analyzeInstrSource(t,
		`<xsl:fork><xsl:for-each-group select="ITEM" group-by="@CAT">`+
			`<xsl:value-of select="count(current-group())"/>`+
			`</xsl:for-each-group></xsl:fork>`)
	if !known {
		t.Fatal("count(current-group()) inside xsl:for-each-group was reported " +
			"unmodelled; §19.8.9.4 gives it the properties of the select expression")
	}
	if !p.streamable() {
		t.Errorf("got %v and %v; by §19.8.9.4 a single inspection of "+
			"current-group() over a striding motionless select is "+
			"guaranteed-streamable", p.posture, p.sweep)
	}
}

func TestCurrentGroupConsumedTwiceIsNotStreamable(t *testing.T) {
	// §19.8.9.4 gives each call the select expression's properties, so two
	// absorbing calls on current-group() are two consuming operands of one
	// construct. The general rules of §19.8.1 make that combination roaming,
	// which is what si-fork-951 asserts: "current-group() used repeatedly".
	p, known := analyzeInstrSource(t,
		`<xsl:fork><xsl:for-each-group select="ITEM" group-by="@CAT">`+
			`<in a="{count(current-group())}" b="{avg(current-group()/PRICE)}"/>`+
			`</xsl:for-each-group></xsl:fork>`)
	if !known {
		t.Fatal("two calls on current-group() were reported unmodelled; " +
			"§19.8.9.4 gives each of them a definite answer")
	}
	if p.streamable() {
		t.Errorf("got %v and %v, want a non-streamable verdict: two operands "+
			"that each read the group cannot both be served by a single pass",
			p.posture, p.sweep)
	}
}

func TestCurrentGroupInANestedContainerOverAStreamedGroup(t *testing.T) {
	// The walk in streamcheck.go starts at each streamable container, so a
	// container nested inside an xsl:for-each-group is assessed without the
	// outer instruction's select expression -- the very thing §19.8.9.4 asks
	// for. enclosingGroupSelect goes and gets it.
	//
	// When it comes back with a posture, "not grounded" is a fact about the
	// stylesheet: the group's members are still streamed nodes that the
	// nested container would have to read a second time, which is
	// §19.8.9.4's "otherwise, roaming and free-ranging". The verdict is
	// modelled, and si-group-052 -- this stylesheet -- is refused, as the
	// catalog requires.
	//
	// The entry point matters: this must go through
	// analyzeSequenceConstructor at the nested container, which is what
	// checkStreamability does, not through the enclosing for-each-group.
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template match="Orders">
	 <xsl:for-each-group select="Order" group-adjacent="@number">
	  <xsl:source-document streamable="yes" href="t.xml">
	   <xsl:for-each select="//transaction[@date = current-group()[1]/Date]">
	    <value><xsl:value-of select="@value"/></value>
	   </xsl:for-each>
	  </xsl:source-document>
	 </xsl:for-each-group>
	</xsl:template>
	</xsl:transform>`
	p, known := analyzeNestedContainer(t, src)
	if !known {
		t.Fatal("a current-group() call over a streamed group was withheld; the " +
			"outer select was assessed and is striding, so §19.8.9.4's roaming " +
			"answer is a fact about the stylesheet")
	}
	if p.streamable() {
		t.Errorf("got %v and %v, want roaming and free-ranging", p.posture, p.sweep)
	}
}

func TestCurrentGroupInANestedContainerIsWithheldWhenTheGroupIsUnmodelled(t *testing.T) {
	// The withholding that remains, and the only one that is warranted: the
	// outer select is itself unmodelled -- here a call on a function this
	// analysis has no entry for -- so nothing is known about the group and
	// answering "roaming" would raise XTSE3430 on the strength of what was
	// never looked at.
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:my="http://example.com/">
	<xsl:template match="Orders">
	 <xsl:for-each-group select="my:pick(Order)" group-adjacent="@number">
	  <xsl:source-document streamable="yes" href="t.xml">
	   <xsl:for-each select="//transaction[@date = current-group()[1]/Date]">
	    <value><xsl:value-of select="@value"/></value>
	   </xsl:for-each>
	  </xsl:source-document>
	 </xsl:for-each-group>
	</xsl:template>
	</xsl:transform>`
	if _, known := analyzeNestedContainer(t, src); known {
		t.Error("a current-group() call whose group is unmodelled was reported as " +
			"modelled; the roaming verdict would be raised as a spurious XTSE3430")
	}
}

// analyzeNestedContainer assesses the body of the first streamable
// xsl:source-document in src, which is the entry point checkStreamability uses.
func analyzeNestedContainer(t *testing.T, src string) (props, bool) {
	t.Helper()
	root := parseSheet(t, src)
	var container *xdm.Node
	walkElements(root, func(el *xdm.Node) bool {
		if container == nil && isXSL(el, "source-document") {
			container = el
		}
		return container == nil
	})
	if container == nil {
		t.Fatal("no xsl:source-document in the test stylesheet")
	}
	return analyzeSequenceConstructor(container, postureStriding,
		attributeSetDeclarations(root), collectStreamFuncs(root))
}
