package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Tests for the XSLT instruction rules of §19.8.4, the attribute-set rule of
// §19.8.6, and the value-template rule of §19.8.7.
//
// Each case states what the spec requires of the construct, and cites the
// clause. None of them was written by running the analysis and recording what
// it said: where the spec gives a worked example it is used verbatim, and
// where it does not, the expected posture and sweep are derived from the
// operand-usage table in the cited section.

// analyzeInstrSource parses a fragment as the body of a streamable
// xsl:source-document and returns the properties of the named instruction --
// the first element child of the container -- assessed with a striding context
// posture, which is what §19.6 gives inside a streamable source document.
func analyzeInstrSource(t *testing.T, body string) (props, bool) {
	t.Helper()
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema"
	 xmlns:map="http://www.w3.org/2005/xpath-functions/map">
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml">` + body + `</xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	root := doc.Root
	if root == nil {
		t.Fatal("no document element")
	}
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
	sets := attributeSetDeclarations(root)
	kids := container.ChildElements()
	if len(kids) != 1 {
		t.Fatalf("test fragment has %d top-level elements, want exactly 1", len(kids))
	}
	return analyzeInstruction(kids[0], postureStriding, sets)
}

// analyzeInstrWithDecls is analyzeInstrSource with extra top-level
// declarations (an xsl:attribute-set, say) placed before the template.
func analyzeInstrWithDecls(t *testing.T, decls, body string) (props, bool) {
	t.Helper()
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema">` + decls + `
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml">` + body + `</xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	root := doc.Root
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
	sets := attributeSetDeclarations(root)
	kids := container.ChildElements()
	if len(kids) != 1 {
		t.Fatalf("test fragment has %d top-level elements, want exactly 1", len(kids))
	}
	return analyzeInstruction(kids[0], postureStriding, sets)
}

func wantProps(t *testing.T, got props, known bool, wantP posture, wantS sweep, what string) {
	t.Helper()
	if !known {
		t.Fatalf("%s: the analysis reported the construct unmodelled; "+
			"want it modelled as %v and %v", what, wantP, wantS)
	}
	if got.posture != wantP || got.sweep != wantS {
		t.Errorf("%s: got %v and %v, want %v and %v",
			what, got.posture, got.sweep, wantP, wantS)
	}
}

// --- §19.8.4.36 xsl:text: no operands ---------------------------------------

func TestTextInstructionIsGroundedMotionless(t *testing.T) {
	// §19.8.4.36: "The posture and sweep of xsl:text follow the general
	// streamability rules. There are no operands." §19.8.1 makes a
	// construct with no operands grounded and motionless.
	p, known := analyzeInstrSource(t, `<xsl:text>hello</xsl:text>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless, "xsl:text")
}

// --- §19.8.4.38 xsl:value-of: select absorbs --------------------------------

func TestValueOfAbsorbsItsSelect(t *testing.T) {
	// §19.8.4.38 gives the select expression usage absorption. Absorbing a
	// striding element operand costs a read of the stream, so by the
	// single-consuming-operand rule of §19.8.1 the result is grounded and
	// consuming.
	p, known := analyzeInstrSource(t, `<xsl:value-of select="a"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming, "xsl:value-of select='a'")
}

func TestValueOfAbsorbingAnAttributeIsMotionless(t *testing.T) {
	// The §19.8.1 downgrade: an attribute has no children, so absorbing it
	// reads nothing further from the stream.
	p, known := analyzeInstrSource(t, `<xsl:value-of select="@a"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless, "xsl:value-of select='@a'")
}

func TestValueOfTwoConsumingOperandsIsRoaming(t *testing.T) {
	// §19.8.1: more than one potentially-consuming operand, not in a choice
	// group and not all motionless, is roaming and free-ranging. Here the
	// separator AVT and the select both absorb a striding element.
	p, known := analyzeInstrSource(t,
		`<xsl:value-of select="a" separator="{b}"/>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:value-of with a consuming select and a consuming separator")
}

// --- §19.8.4.34 xsl:sequence: select transmits ------------------------------

func TestSequenceTransmitsItsSelect(t *testing.T) {
	// §19.8.4.34 gives select usage transmission, so the instruction
	// inherits the operand's posture rather than grounding it. By the
	// §19.8.8.8 table a child step from a striding context posture is
	// striding and consuming, and transmission leaves both alone -- which
	// is exactly why xsl:fork rejects it: the branch returns streamed nodes.
	p, known := analyzeInstrSource(t, `<xsl:sequence select="a"/>`)
	wantProps(t, p, known, postureStriding, sweepConsuming, "xsl:sequence select='a'")
}

// --- §19.8.4.20 xsl:fork ----------------------------------------------------

func TestForkWithTwoConsumingBranchesIsRoaming(t *testing.T) {
	// §19.8.4.20 gives this exact example as not streamable, "because it
	// returns streamed nodes in an order that might not be document order".
	// Both branches are striding and consuming, and two branches cannot each
	// move the input. This is si-fork-901.
	p, known := analyzeInstrSource(t, `<xsl:fork>
	 <xsl:sequence select="author"/>
	 <xsl:sequence select="editor"/>
	</xsl:fork>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:fork whose branches both consume")
}

func TestForkMayReturnStreamedNodesFromOneConsumingBranch(t *testing.T) {
	// The companion constraint, and the one that keeps the rule from
	// rejecting valid stylesheets: a fork MAY return streamed nodes so long
	// as only one branch consumes. si-fork-A's f-006 is captioned exactly
	// that and is expected to run -- here the xsl:attribute branch absorbs an
	// attribute, which has no children and so reads nothing (§19.8.1), while
	// only the second branch moves the input.
	p, known := analyzeInstrSource(t, `<xsl:fork>
	 <xsl:sequence><xsl:attribute name="category" select="@CAT"/></xsl:sequence>
	 <xsl:sequence select="TITLE"/>
	</xsl:fork>`)
	if !known {
		t.Fatal("the fork was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("xsl:fork with one consuming branch: got %v and %v, "+
			"want it guaranteed-streamable", p.posture, p.sweep)
	}
}

func TestForkWithGroundedSequencesIsStreamable(t *testing.T) {
	// §19.8.4.20 gives this example as streamable: each branch grounds its
	// result with copy-of, so the fork is grounded, and its sweep is the
	// widest of the branches -- consuming.
	p, known := analyzeInstrSource(t, `<xsl:fork>
	 <xsl:sequence select="copy-of(author)"/>
	 <xsl:sequence select="copy-of(editor)"/>
	</xsl:fork>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:fork over two grounded branches")
}

func TestForkWithNoSequenceChildrenIsGroundedMotionless(t *testing.T) {
	// §19.8.4.20, first clause: "If there are no child xsl:sequence
	// instructions (other than xsl:fallback), then grounded and motionless."
	p, known := analyzeInstrSource(t, `<xsl:fork/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless, "an empty xsl:fork")
}

// --- §19.8.4.18 xsl:for-each ------------------------------------------------

func TestForEachOverStridingSelectIsStreamable(t *testing.T) {
	// §19.8.4.18, final clause: the posture is that of the body assessed in
	// the select's posture, and the sweep is the wider of the two. The body
	// absorbs a child of the striding context, so it is grounded and
	// consuming; the select "a" is striding and motionless.
	p, known := analyzeInstrSource(t, `<xsl:for-each select="a">
	 <xsl:value-of select="b"/>
	</xsl:for-each>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:for-each over a striding select")
}

func TestForEachWithCrawlingSelectAndConsumingBodyIsRoaming(t *testing.T) {
	// §19.8.4.18: "If the posture of the select expression is crawling and
	// the sweep of the contained sequence constructor is consuming, then
	// roaming and free-ranging." "//ITEM" is crawling; the body consumes.
	// This is si-for-each-806.
	p, known := analyzeInstrSource(t, `<xsl:for-each select="//ITEM">
	 <xsl:value-of select="."/>
	</xsl:for-each>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:for-each over a crawling select with a consuming body")
}

func TestForEachWithSortIsRoaming(t *testing.T) {
	// §19.8.4.18, second clause: "If there is an xsl:sort child element,
	// then roaming and free-ranging." Sorting cannot be done in one forward
	// pass over the stream.
	p, known := analyzeInstrSource(t, `<xsl:for-each select="a">
	 <xsl:sort select="b"/>
	 <xsl:value-of select="c"/>
	</xsl:for-each>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:for-each with an xsl:sort child")
}

func TestForEachBodyReturningStreamedNodesKeepsThatPosture(t *testing.T) {
	// §19.8.4.18, final clause: "The posture of the instruction is the
	// posture of the contained sequence constructor", and "the sweep of the
	// instruction is the wider of the sweep of the select expression and the
	// sweep of the contained sequence constructor". An xsl:sequence
	// selecting "." transmits the streamed context node, so the for-each is
	// striding rather than grounded -- which is what makes si-for-each-907
	// fail: the result of a streamable source document must not be a
	// streamed node. The sweep is the select's, the wider of the two.
	p, known := analyzeInstrSource(t, `<xsl:for-each select="a">
	 <xsl:sequence select="."/>
	</xsl:for-each>`)
	wantProps(t, p, known, postureStriding, sweepConsuming,
		"xsl:for-each whose body transmits the context node")
}

// --- §19.8.4.10 xsl:choose: the branches are a choice group -----------------

func TestChooseBranchesMayEachConsume(t *testing.T) {
	// §19.8.4.10: the sequence constructors of xsl:when and xsl:otherwise
	// "form a choice operand group", so each may read the stream -- only one
	// of them is ever evaluated. The note spells this out: "Every sequence
	// constructor in an xsl:when or xsl:otherwise branch may read the input
	// stream. In this situation the test expressions must be motionless."
	p, known := analyzeInstrSource(t, `<xsl:choose>
	 <xsl:when test="@x"><xsl:value-of select="a"/></xsl:when>
	 <xsl:otherwise><xsl:value-of select="b"/></xsl:otherwise>
	</xsl:choose>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:choose whose branches both consume")
}

// --- §19.8.4.37 xsl:try: catch branches are a choice group ------------------

func TestTryAndCatchMayNotBothConsume(t *testing.T) {
	// §19.8.4.37, note: "either the xsl:try branch or the xsl:catch branch
	// may consume the streamed input, but not both." The try body is not a
	// member of the catch choice group, so two consuming operands remain.
	p, known := analyzeInstrSource(t, `<xsl:try>
	 <xsl:value-of select="a"/>
	 <xsl:catch><xsl:value-of select="b"/></xsl:catch>
	</xsl:try>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:try where both the try and the catch consume")
}

// --- §19.8.4.12 xsl:copy ----------------------------------------------------

func TestNestedCopyChainIsStreamable(t *testing.T) {
	// §19.8.4.12 assesses the body of an xsl:copy "with context posture and
	// context item type based on the select expression if present". The
	// select is focus-setting, so it does not compete with the body for the
	// stream; one downward pass copies the whole chain. si-copy-028 nests
	// four deep and is expected to run.
	p, known := analyzeInstrSource(t, `<xsl:copy>
	 <xsl:copy select="*">
	  <xsl:copy select="*[1]">
	   <xsl:copy select="*[1]"/>
	  </xsl:copy>
	 </xsl:copy>
	</xsl:copy>`)
	if !known {
		t.Fatal("the nested xsl:copy chain was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("a nested xsl:copy chain: got %v and %v, want it guaranteed-streamable",
			p.posture, p.sweep)
	}
}

func TestCopyIsGrounded(t *testing.T) {
	// xsl:copy constructs a new node, so whatever it copied, what it returns
	// is not a node of the streamed document. The §19.8.6 note makes the
	// same point about attribute sets: constructed nodes are grounded.
	p, known := analyzeInstrSource(t, `<xsl:copy select="*"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming, "xsl:copy of a striding child")
}

// --- §19.8.4.23 xsl:map and §19.8.4.24 xsl:map-entry ------------------------

func TestMapEntriesFormAnImplicitFork(t *testing.T) {
	// §19.8.4.23: when the body is exclusively xsl:map-entry instructions,
	// the map is "grounded and the widest sweep of the xsl:map-entry
	// children" -- an implicit fork, so several entries may each read the
	// stream. The spec gives this example as streamable.
	p, known := analyzeInstrSource(t, `<xsl:map>
	 <xsl:map-entry key="'authors'" select="copy-of(author)"/>
	 <xsl:map-entry key="'editors'" select="copy-of(editor)"/>
	</xsl:map>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:map over two grounded entries")
}

func TestMapEntrySelectNavigatesSoStreamedNodesAreRejected(t *testing.T) {
	// §19.8.4.24 gives the select expression usage navigation, and the note
	// says this "effectively means that the select expression must not
	// return nodes from a streamed input document". Navigation from a
	// non-grounded posture is free-ranging (§19.8.1). This is si-map-901.
	p, known := analyzeInstrSource(t, `<xsl:map>
	 <xsl:map-entry key="'authors'" select="//AUTHOR"/>
	</xsl:map>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"an xsl:map-entry whose select returns streamed nodes")
}

// --- §19.8.4.25 xsl:merge ---------------------------------------------------

func TestMergeWithGroundedSourcesIsMotionless(t *testing.T) {
	// §19.8.4.25: when every xsl:merge-source is anchored by a grounded,
	// motionless expression, "the xsl:merge instruction is grounded and
	// motionless" -- it does not touch the enclosing stream at all.
	p, known := analyzeInstrSource(t, `<xsl:merge>
	 <xsl:merge-source select="doc('a.xml')/a" for-each-item="1">
	  <xsl:merge-key select="@k"/>
	 </xsl:merge-source>
	 <xsl:merge-action><xsl:text>x</xsl:text></xsl:merge-action>
	</xsl:merge>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"xsl:merge over grounded sources")
}

func TestMergeWithStreamedSourceIsRoaming(t *testing.T) {
	// §19.8.4.25: a merge source with neither for-each-item nor
	// for-each-stream, whose select is not grounded and motionless, makes
	// the instruction roaming and free-ranging.
	p, known := analyzeInstrSource(t, `<xsl:merge>
	 <xsl:merge-source select="a/b">
	  <xsl:merge-key select="@k"/>
	 </xsl:merge-source>
	 <xsl:merge-action><xsl:text>x</xsl:text></xsl:merge-action>
	</xsl:merge>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:merge over a streamed source")
}

// --- §19.8.4.22 xsl:iterate -------------------------------------------------

func TestIterateWithConsumingParamIsRoaming(t *testing.T) {
	// §19.8.4.22: "If there is an xsl:param child whose initializing select
	// expression or sequence constructor is not grounded and motionless,
	// then roaming and free-ranging." The parameter is carried across
	// iterations, so it can neither hold a streamed node nor read the stream.
	p, known := analyzeInstrSource(t, `<xsl:iterate select="a">
	 <xsl:param name="p" select="string(b)"/>
	 <xsl:value-of select="c"/>
	</xsl:iterate>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:iterate with a consuming xsl:param")
}

func TestIterateOverStridingSelectIsStreamable(t *testing.T) {
	// §19.8.4.22, final clause: the posture is the body's, assessed in the
	// select's posture, and the sweep is the wider of the two.
	p, known := analyzeInstrSource(t, `<xsl:iterate select="a">
	 <xsl:value-of select="b"/>
	</xsl:iterate>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:iterate over a striding select")
}

// --- §19.8.4.19 xsl:for-each-group ------------------------------------------

func TestForEachGroupWithGroupByOutsideForkIsRoaming(t *testing.T) {
	// §19.8.4.19: "If there is a group-by attribute and the instruction is
	// not a child of xsl:fork, then roaming and free-ranging." Grouping by
	// an arbitrary key needs every group held at once, which only the
	// implicit fork of a surrounding xsl:fork makes possible.
	p, known := analyzeInstrSource(t, `<xsl:for-each-group select="a" group-by="@k">
	 <xsl:value-of select="b"/>
	</xsl:for-each-group>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:for-each-group with group-by outside an xsl:fork")
}

func TestForEachGroupGroupingKeyIsAssessedInTheSelectsPosture(t *testing.T) {
	// §19.8.4.19 assesses the group-adjacent expression "with a context
	// posture of grounded" -- but only in its FIRST clause, the one for a
	// grounded select. There the group has already been materialised, so the
	// key reads from memory.
	//
	// In the cascade the select is still a stream, and clause 3 asks whether
	// the key "is not motionless" as read from there. §19.9's worked example
	// settles the context posture in as many words: of the group-adjacent
	// operand "@timestamp" it writes "the context posture is the posture of
	// the controlling operand of the focus-setting container, that is, the
	// select expression of the containing xsl:for-each-group instruction,
	// which as established above is striding."
	//
	// So a key of "b" over a striding select is consuming, and clause 3
	// fires. si-group-901 groups on "PRICE/text()" and the catalog asserts
	// XTSE3430 for it; assessing every key as grounded made every key
	// motionless and clause 3 unreachable.
	p, known := analyzeInstrSource(t, `<xsl:for-each-group select="a" group-adjacent="b">
	 <xsl:value-of select="c"/>
	</xsl:for-each-group>`)
	wantProps(t, p, known, postureRoaming, sweepFreeRanging,
		"xsl:for-each-group whose group-adjacent reads the stream")
}

func TestForEachGroupMotionlessGroupingKeyIsAccepted(t *testing.T) {
	// The other half of the same rule, and §19.9's own key: an attribute
	// step read from a striding context posture is motionless (§19.8.8.8),
	// so clause 3 does not fire and the instruction stays streamable. This
	// is what keeps the corrected posture from rejecting the common case.
	p, known := analyzeInstrSource(t, `<xsl:for-each-group select="a" group-adjacent="@b">
	 <xsl:value-of select="c"/>
	</xsl:for-each-group>`)
	if !known {
		t.Fatal("the grouping-key expression was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("xsl:for-each-group with a motionless group-adjacent: got %v and %v, "+
			"want it guaranteed-streamable", p.posture, p.sweep)
	}
}

func TestForEachGroupWithSortIsRoaming(t *testing.T) {
	// §19.8.4.19: "If there is an xsl:sort child element, then roaming and
	// free-ranging." This is si-fork-953, where the sort sits inside an
	// xsl:fork so that the group-by clause does not fire first.
	p, known := analyzeInstrSource(t, `<xsl:fork>
	 <xsl:for-each-group select="a" group-by="@k">
	  <xsl:sort select="position()"/>
	  <xsl:value-of select="b"/>
	 </xsl:for-each-group>
	</xsl:fork>`)
	if !known {
		t.Fatal("the instruction was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("xsl:for-each-group with an xsl:sort child: got %v and %v, "+
			"want it not guaranteed-streamable", p.posture, p.sweep)
	}
}

// --- §19.8.6 attribute sets -------------------------------------------------

func TestConsumingAttributeSetMakesTheReferenceUnstreamable(t *testing.T) {
	// §19.8.6: "the streamability of a construct such as an xsl:element
	// instruction containing a use-attribute-sets attribute is based on the
	// declared streamability of the named attribute sets". A set that does
	// not declare streamable="yes" may be overridden in another package by
	// one that consumes, so a reference to it from a streamable construct is
	// not guaranteed-streamable -- whatever the set happens to contain here.
	// This is si-element-902, whose set has no streamable attribute at all.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:attribute-set name="as"><xsl:attribute name="x" select=".//x"/></xsl:attribute-set>`,
		`<xsl:element name="e" use-attribute-sets="as"/>`)
	if !known {
		t.Fatal("the attribute-set reference was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("xsl:element using an attribute set not declared streamable: got %v and %v, "+
			"want it not guaranteed-streamable", p.posture, p.sweep)
	}
}

func TestAttributeSetDeclaredNotStreamableIsRejected(t *testing.T) {
	// si-element-901: the same, spelled streamable="no". The content is
	// identical to the streamable case below, so only the declaration can
	// account for the difference in verdict.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:attribute-set name="as" streamable="no"><xsl:attribute name="y" select="2"/></xsl:attribute-set>`,
		`<xsl:element name="e" use-attribute-sets="as"/>`)
	if !known {
		t.Fatal("the attribute-set reference was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf(`xsl:element using an attribute set declared streamable="no": got %v and %v, `+
			"want it not guaranteed-streamable", p.posture, p.sweep)
	}
}

func TestDeclaredStreamableAttributeSetMustBeStreamable(t *testing.T) {
	// §19.8.6: "a streaming processor is required to check that an attribute
	// set containing such a declaration does in fact satisfy the
	// streamability rules". This is si-element-903 verbatim: the set
	// declares streamable="yes", but last() is free-ranging in a streamed
	// context -- the number of siblings is not known until the stream has
	// been read past them -- so the declaration is dishonest.
	//
	// A consuming attribute set is NOT dishonest: the §19.8.6 note says a
	// set that accesses the context item "may be consuming or
	// free-ranging", and only the free-ranging half is an error.
	decl := `<xsl:attribute-set name="as" streamable="yes">` +
		`<xsl:attribute name="x" select="last()"/></xsl:attribute-set>`
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		decl + `<xsl:template name="main"><out/></xsl:template></xsl:transform>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	sets := attributeSetDeclarations(doc.Root)
	var as *xdm.Node
	walkElements(doc.Root, func(el *xdm.Node) bool {
		if as == nil && isXSL(el, "attribute-set") {
			as = el
		}
		return as == nil
	})
	if as == nil {
		t.Fatal("no xsl:attribute-set in the test stylesheet")
	}
	p, known := analyzeAttributeSet(as, sets)
	if known {
		// Once fn:last() is classified under §19.8.9, the honest verdict is
		// available and must be a rejection.
		if p.streamable() {
			t.Errorf("a declared-streamable attribute set using last(): got %v and %v, "+
				"want it not guaranteed-streamable", p.posture, p.sweep)
		}
		return
	}
	// fn:last() has no entry in the §19.8.9 operand-usage table yet, so the
	// analysis has no opinion. That must stay a silence rather than becoming
	// a rejection: reporting XTSE3430 from an unmodelled construct is how a
	// valid stylesheet gets refused at compile time. si-element-903 is
	// therefore still expected to fail, and the gap is recorded in
	// docs/known-gaps.md.
	if p.streamable() {
		t.Fatal("an unmodelled construct returned a streamable verdict; " +
			"unknown must always come with roaming and free-ranging")
	}
}

func TestMotionlessAttributeSetIsStreamable(t *testing.T) {
	// The converse, and the guard against the rule rejecting every
	// use-attribute-sets: a set declared streamable whose attributes are
	// constants costs the reference nothing.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:attribute-set name="as" streamable="yes"><xsl:attribute name="y" select="2"/></xsl:attribute-set>`,
		`<xsl:element name="e" use-attribute-sets="as"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"xsl:element using a motionless attribute set declared streamable")
}

func TestDeclaredStreamableAttributeSetIsTakenAtItsWord(t *testing.T) {
	// §19.8.6: "the streamability of a construct such as an xsl:element
	// instruction containing a use-attribute-sets attribute is based on the
	// declared streamability of the named attribute sets". A set declared
	// streamable="yes" therefore contributes nothing to the reference, even
	// though last() would otherwise not be motionless.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:attribute-set name="as" streamable="yes"><xsl:attribute name="x" select="last()"/></xsl:attribute-set>`,
		`<xsl:element name="e" use-attribute-sets="as"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"xsl:element using an attribute set declared streamable")
}

// --- §19.8.7 value templates ------------------------------------------------

func TestLiteralValueTemplateIsMotionless(t *testing.T) {
	// §19.8.7: "If there are no expressions contained within curly braces,
	// the value template is motionless."
	p, known := analyzeInstrSource(t, `<xsl:element name="e"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"xsl:element with a literal name")
}

func TestValueTemplateExpressionsAbsorb(t *testing.T) {
	// §19.8.7: the operands of a value template are the expressions in
	// braces, with usage absorption. Absorbing a striding element consumes.
	p, known := analyzeInstrSource(t, `<xsl:element name="{a}"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:element whose name is an AVT reading the stream")
}

func TestAVTExpressionExtraction(t *testing.T) {
	// The brace scanner underlying §19.8.7, including the doubled-brace
	// escapes, which decide whether a CSS or JSON attribute value is read as
	// an expression.
	cases := []struct {
		src  string
		want []string
	}{
		{"plain", nil},
		{"{a}", []string{"a"}},
		{"x{a}y{b}z", []string{"a", "b"}},
		{"{{literal}}", nil},
		{"{{{a}}}", []string{"a"}},
	}
	for _, c := range cases {
		got, ok := avtExpressions(c.src)
		if !ok {
			t.Errorf("avtExpressions(%q) reported malformed braces", c.src)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("avtExpressions(%q) = %v, want %v", c.src, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("avtExpressions(%q) = %v, want %v", c.src, got, c.want)
				break
			}
		}
	}
	if _, ok := avtExpressions("{unclosed"); ok {
		t.Error("avtExpressions accepted an unclosed brace")
	}
}

// --- the known guard --------------------------------------------------------

func TestUnmodelledInstructionIsReportedUnknown(t *testing.T) {
	// The guard the whole check rests on: an instruction whose §19.8.4 rule
	// is not written must be reported unmodelled, so that streamcheck.go
	// stays silent rather than rejecting a valid stylesheet.
	// xsl:where-populated is one such instruction.
	_, known := analyzeInstrSource(t, `<xsl:where-populated><a/></xsl:where-populated>`)
	if known {
		t.Error("xsl:where-populated was reported as modelled; its §19.8.4 rule is not " +
			"implemented, so treating it as modelled risks a spurious XTSE3430")
	}
}

func TestUnmodelledPredicateInAScanningPathIsReportedUnknown(t *testing.T) {
	// The subtle arm of the same guard, and the one that cost si-group-051.
	//
	// "//x[...]" is assessed by §19.8.8.7's second phase, which asks whether
	// the path is a scanning expression. A predicate the analysis cannot
	// model makes the answer "no" -- but "no" there is a fact about this
	// implementation, not about the path, and the roaming verdict that
	// follows must not be reported as an XTSE3430.
	//
	// fn:unparsed-text() has no entry in the §19.8.9 table, so a path
	// filtered on it must come back unmodelled.
	_, known := analyzeInstrSource(t,
		`<xsl:value-of select="//transaction[@date = unparsed-text('d.txt')]"/>`)
	if known {
		t.Error("a path whose predicate calls the unmodelled unparsed-text() was reported " +
			"as modelled; the roaming verdict would then be raised as a spurious XTSE3430")
	}

	// The guard must not be so wide that it swallows real rejections: the
	// same path shape with a predicate the analysis does model stays modelled.
	_, known = analyzeInstrSource(t, `<xsl:value-of select="//transaction[@date]"/>`)
	if !known {
		t.Error("a path with an ordinary predicate was reported unmodelled")
	}
}

func TestUnmodelledInstructionInABodyPropagates(t *testing.T) {
	// known must propagate out of a nested sequence constructor, not just
	// out of the instruction itself.
	_, known := analyzeInstrSource(t, `<xsl:for-each select="a">
	 <xsl:where-populated><b/></xsl:where-populated>
	</xsl:for-each>`)
	if known {
		t.Error("an unmodelled instruction inside xsl:for-each did not clear known")
	}
}

// --- §19.4 type-determined usage --------------------------------------------

func TestTypeDeterminedUsageFollowsTheRequiredType(t *testing.T) {
	// §19.4: "if the required type (ignoring occurrence indicator) is
	// function(*) or a subtype thereof, then inspection; if the required
	// type (ignoring occurrence indicator) is xs:anyAtomicType or a subtype
	// thereof, then absorption; otherwise navigation."
	cases := []struct {
		as   string
		want usage
	}{
		// Atomic types, and the same types under every occurrence indicator,
		// since the rule ignores the indicator.
		{"xs:string", usageAbsorption},
		{"xs:integer", usageAbsorption},
		{"xs:anyAtomicType", usageAbsorption},
		{"xs:double*", usageAbsorption},
		{"xs:date?", usageAbsorption},
		{"xs:untypedAtomic+", usageAbsorption},
		// function(*) and its subtypes.
		{"function(*)", usageInspection},
		{"map(*)", usageInspection},
		{"array(*)", usageInspection},
		{"map(xs:string, item())", usageInspection},
		// Everything else, including the item()* that §19.8.4 substitutes
		// wherever an as attribute is absent.
		{"", usageNavigation},
		{"item()*", usageNavigation},
		{"node()", usageNavigation},
		{"element(employee)*", usageNavigation},
		{"document-node()", usageNavigation},
	}
	for _, c := range cases {
		if got := instrTypeDeterminedUsage(c.as); got != c.want {
			t.Errorf("instrTypeDeterminedUsage(%q) = %v, want %v", c.as, got, c.want)
		}
	}
}

// --- §19.8.4.39 xsl:variable with a declared type ---------------------------

func TestVariableWithNodeTypeNavigatesAndIsFreeRanging(t *testing.T) {
	// §19.8.4.39: with an as attribute, the select expression takes the
	// type-determined usage based on that type. element(a)* permits nodes,
	// so §19.4 gives navigation, and §19.8.1 makes navigation from a
	// striding operand free-ranging. This is the rule that stops a streamed
	// node being bound to a variable.
	p, known := analyzeInstrSource(t,
		`<xsl:variable name="v" as="element(a)*" select="a"/>`)
	if !known {
		t.Fatal("xsl:variable with as=element(a)* was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("binding a streamed selection to as=element(a)* was reported streamable "+
			"(%v, %v); §19.8.4.39 gives the select navigation usage, which is free-ranging",
			p.posture, p.sweep)
	}
}

func TestVariableWithAtomicTypeAbsorbs(t *testing.T) {
	// The other half of the same rule: an atomic required type gives
	// absorption, which is consuming rather than free-ranging, so the
	// construct stays streamable. Without this the rule above would be
	// satisfied by a check that rejected every typed xsl:variable.
	p, known := analyzeInstrSource(t,
		`<xsl:variable name="v" as="xs:string" select="a"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:variable with as=xs:string over a striding select")
}

// --- §19.8.4.8 xsl:break ----------------------------------------------------

func TestBreakTransmitsItsSelect(t *testing.T) {
	// §19.8.4.8: "The select expression (usage transmission)". Transmission
	// leaves the operand's posture and sweep alone, and by the §19.8.8.8
	// table a child step from a striding context posture is striding and
	// consuming -- so xsl:break returns streamed nodes, exactly as
	// xsl:sequence does under §19.8.4.34.
	p, known := analyzeInstrSource(t, `<xsl:break select="a"/>`)
	wantProps(t, p, known, postureStriding, sweepConsuming, "xsl:break select='a'")
}

// --- §19.8.4.4 xsl:apply-imports and xsl:next-match -------------------------

func TestApplyImportsAbsorbsTheContextItem(t *testing.T) {
	// §19.8.4.4: "An implicit operand: a context item expression (.), with
	// usage absorption". Absorbing a striding context item reads its whole
	// subtree, which is grounded and consuming -- the note's "will normally
	// be grounded and consuming".
	p, known := analyzeInstrSource(t, `<xsl:apply-imports/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming, "xsl:apply-imports")
}

func TestNextMatchFollowsTheApplyImportsRule(t *testing.T) {
	// §19.8.4.29: "The rules are the same as for xsl:apply-imports: see
	// 19.8.4.4". The two must therefore agree.
	p, known := analyzeInstrSource(t, `<xsl:next-match/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming, "xsl:next-match")
}

func TestNextMatchWithNodeValuedParameterIsNotStreamable(t *testing.T) {
	// §19.8.4.4's second operand: each xsl:with-param takes the
	// type-determined usage of its as attribute, defaulting to item()*.
	// item()* is not atomic, so §19.4 gives navigation, and navigating from
	// a streamed node is free-ranging -- the note's "provided that nodes in
	// a streamed document are not passed as parameters".
	p, known := analyzeInstrSource(t,
		`<xsl:next-match><xsl:with-param name="p" select="a"/></xsl:next-match>`)
	if !known {
		t.Fatal("xsl:next-match with an xsl:with-param was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("passing a streamed node as a parameter was reported streamable (%v, %v); "+
			"§19.8.4.4 gives the parameter navigation usage", p.posture, p.sweep)
	}
}

// --- §19.8.4.28 xsl:next-iteration ------------------------------------------

func TestNextIterationWithNodeValuedParameterIsNotStreamable(t *testing.T) {
	// §19.8.4.28: the operands are the xsl:with-param children, with
	// type-determined usage. An undeclared type is item()*, hence navigation.
	p, known := analyzeInstrSource(t,
		`<xsl:next-iteration><xsl:with-param name="p" select="a"/></xsl:next-iteration>`)
	if !known {
		t.Fatal("xsl:next-iteration was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("carrying a streamed node to the next iteration was reported streamable "+
			"(%v, %v); §19.8.4.28 gives the parameter navigation usage", p.posture, p.sweep)
	}
}

func TestNextIterationWithAtomicParameterIsStreamable(t *testing.T) {
	// The complement: an atomic declared type gives absorption, which is
	// consuming rather than free-ranging, so the instruction stays
	// streamable. Without this the rule above would be satisfied by a check
	// that rejected every xsl:next-iteration.
	p, known := analyzeInstrSource(t,
		`<xsl:next-iteration><xsl:with-param name="p" as="xs:integer" select="count(a)"/></xsl:next-iteration>`)
	if !known {
		t.Fatal("xsl:next-iteration with an atomic parameter was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("an atomic-typed parameter was reported not streamable (%v, %v); "+
			"§19.4 gives an atomic required type absorption usage", p.posture, p.sweep)
	}
}

// --- §19.8.4.3 xsl:analyze-string -------------------------------------------

func TestAnalyzeStringAbsorbsSelectAndGroundsItsSubstrings(t *testing.T) {
	// §19.8.4.3: select and the regex value template absorb; the two
	// substring constructors have usage navigation and "the context posture
	// for the two sequence constructors is grounded, reflecting the fact
	// that their context item type is xs:string". Absorbing a striding
	// select is grounded and consuming, and the grounded substring bodies
	// add nothing, so the note's "sweep will usually be the same as the
	// sweep of the select expression, and its posture will be grounded"
	// holds.
	p, known := analyzeInstrSource(t, `<xsl:analyze-string select="a" regex="x">
	 <xsl:matching-substring><m/></xsl:matching-substring>
	 <xsl:non-matching-substring><n/></xsl:non-matching-substring>
	</xsl:analyze-string>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming, "xsl:analyze-string")

	// The substring bodies must be assessed with a grounded context posture,
	// not the instruction's own. A body that reads "." is reading the matched
	// string, which is already in hand, so it stays motionless and the
	// instruction stays streamable. Were the striding posture carried in
	// instead, "." would be a streamed node, absorbing it would consume, and
	// the second consuming operand would make the whole instruction roaming.
	p, known = analyzeInstrSource(t, `<xsl:analyze-string select="a" regex="x">
	 <xsl:matching-substring><xsl:value-of select="."/></xsl:matching-substring>
	</xsl:analyze-string>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		`xsl:analyze-string whose matching-substring reads "."`)
}

// --- §19.8.4.30 xsl:number --------------------------------------------------

func TestNumberWithValueAbsorbs(t *testing.T) {
	// §19.8.4.30: "The value attribute if present: usage absorption". A
	// literal value reads nothing, so the instruction is motionless.
	p, known := analyzeInstrSource(t, `<xsl:number value="1"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless, "xsl:number value='1'")
}

func TestNumberWithoutValueNavigatesFromTheContextItem(t *testing.T) {
	// §19.8.4.30: with no value attribute the select expression has usage
	// navigation, "defaulting to the context item expression (.) if the
	// select attribute is also absent". Navigating from a striding context
	// item is free-ranging: xsl:number counts preceding siblings, which a
	// streamed reader cannot revisit.
	p, known := analyzeInstrSource(t, `<xsl:number/>`)
	if !known {
		t.Fatal("xsl:number with no attributes was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("bare xsl:number over a streamed context item was reported streamable "+
			"(%v, %v); §19.8.4.30 gives the implicit select navigation usage",
			p.posture, p.sweep)
	}
}

// --- §19.8.4.5 xsl:apply-templates ------------------------------------------

func TestApplyTemplatesWithGroundedSelectIsGroundedAndConsuming(t *testing.T) {
	// §19.8.4.5 clause 1, with the spec's own worked example: "For example,
	// <xsl:apply-templates select="copy-of(.)"/> is grounded and consuming."
	// The clause applies whatever the mode, because nothing streamed reaches
	// the templates.
	p, known := analyzeInstrSource(t, `<xsl:apply-templates select="copy-of(.)"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		`xsl:apply-templates select="copy-of(.)"`)
}

func TestApplyTemplatesWithSortIsNotStreamable(t *testing.T) {
	// §19.8.4.5 clause 2: "If there is an xsl:sort child element, then
	// roaming and free-ranging." The select is striding so clause 1 does not
	// fire first, and mode="#current" is treated as streamable so that
	// clause 3 does not fire either -- without that, clause 3 would reject
	// this stylesheet for an unrelated reason and the test would pass however
	// clause 2 behaved.
	p, known := analyzeInstrSource(t,
		`<xsl:apply-templates select="a" mode="#current"><xsl:sort select="@n"/></xsl:apply-templates>`)
	if !known {
		t.Fatal("xsl:apply-templates with xsl:sort was reported unmodelled")
	}
	if p.posture != postureRoaming || p.sweep != sweepFreeRanging {
		t.Errorf("got %v and %v, want roaming and free-ranging; §19.8.4.5 clause 2 "+
			"rejects any xsl:apply-templates with an xsl:sort child", p.posture, p.sweep)
	}

	// The complement: the same instruction without the xsl:sort reaches
	// clause 5 and is streamable, so clause 2 is what makes the difference.
	p, known = analyzeInstrSource(t, `<xsl:apply-templates select="a" mode="#current"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"the same xsl:apply-templates without an xsl:sort")
}

func TestApplyTemplatesToANonStreamableModeIsNotStreamable(t *testing.T) {
	// §19.8.4.5 clause 3: "If the implicit or explicit mode attribute
	// identifies a mode that is not declared with streamable='yes', then
	// roaming and free-ranging."
	p, known := analyzeInstrWithDecls(t, `<xsl:mode name="m"/>`,
		`<xsl:apply-templates select="a" mode="m"/>`)
	if !known {
		t.Fatal("xsl:apply-templates to a declared mode was reported unmodelled")
	}
	if p.posture != postureRoaming || p.sweep != sweepFreeRanging {
		t.Errorf("got %v and %v, want roaming and free-ranging; mode m is not "+
			"declared streamable", p.posture, p.sweep)
	}
}

func TestApplyTemplatesToAStreamableModeIsStreamable(t *testing.T) {
	// The complement of clause 3, and the guard against a rule that rejects
	// every xsl:apply-templates: a mode declared streamable="yes" reaches
	// clause 5, where a striding select absorbs to grounded and consuming.
	p, known := analyzeInstrWithDecls(t, `<xsl:mode name="m" streamable="yes"/>`,
		`<xsl:apply-templates select="a" mode="m"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		`xsl:apply-templates to a streamable mode`)
}

func TestApplyTemplatesWithCurrentModeIsTreatedAsStreamable(t *testing.T) {
	// §19.8.4.5's note on clause 3: "When mode='#current' is specified, this
	// is treated as equivalent to specifying a streamable mode".
	p, known := analyzeInstrSource(t, `<xsl:apply-templates select="a" mode="#current"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		`xsl:apply-templates mode="#current"`)
}

func TestApplyTemplatesWithCrawlingSelectIsNotStreamable(t *testing.T) {
	// §19.8.4.5 clause 4: "If the select expression is climbing or crawling,
	// then roaming and free-ranging." A descendant selection is crawling.
	p, known := analyzeInstrSource(t, `<xsl:apply-templates select="//a" mode="#current"/>`)
	if !known {
		t.Fatal("xsl:apply-templates with a crawling select was reported unmodelled")
	}
	if p.posture != postureRoaming || p.sweep != sweepFreeRanging {
		t.Errorf("got %v and %v, want roaming and free-ranging; §19.8.4.5 clause 4 "+
			"rejects a crawling select", p.posture, p.sweep)
	}
}

// --- §19.8.4.9 xsl:call-template --------------------------------------------

func TestCallTemplateNavigatesTheContextItemByDefault(t *testing.T) {
	// §19.8.4.9: the implicit context item operand takes the type-determined
	// usage of the target's xsl:context-item/@as, "defaulting to item()* if
	// absent". item()* gives navigation, and navigating a streamed context
	// item is free-ranging.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:template name="t"><out/></xsl:template>`,
		`<xsl:call-template name="t"/>`)
	if !known {
		t.Fatal("xsl:call-template on a template in the same stylesheet was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("calling a template with a streamed context item was reported streamable "+
			"(%v, %v); §19.8.4.9 defaults the context item operand to navigation",
			p.posture, p.sweep)
	}
}

func TestCallTemplateWithProhibitedContextItemHasNoContextOperand(t *testing.T) {
	// §19.8.4.9: "Unless the referenced template has a child
	// xsl:context-item element with the attribute use='prohibited', there is
	// an implicit operand". With use="prohibited" there is none, so nothing
	// is navigated and the call is grounded and motionless.
	p, known := analyzeInstrWithDecls(t,
		`<xsl:template name="t"><xsl:context-item use="prohibited"/><out/></xsl:template>`,
		`<xsl:call-template name="t"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		`xsl:call-template on a template with use="prohibited"`)
}

func TestCallTemplateOnAnUnknownNameIsUnmodelled(t *testing.T) {
	// The target's xsl:context-item decides the usage, so a name this scan
	// cannot resolve leaves the rule without an input. It must report
	// unmodelled rather than assume a default, or a template declared in
	// another package would draw a spurious XTSE3430.
	_, known := analyzeInstrSource(t, `<xsl:call-template name="elsewhere"/>`)
	if known {
		t.Error("xsl:call-template naming a template outside the stylesheet was reported " +
			"as modelled; §19.8.4.9 needs the target's xsl:context-item to decide the usage")
	}
}

// --- §19.8.4.16 xsl:evaluate ------------------------------------------------

func TestEvaluateNavigatesItsContextItem(t *testing.T) {
	// §19.8.4.16: "The xpath expression (usage absorption)" and "The
	// context-item expression (usage navigation)". Navigating a streamed
	// node is free-ranging -- the note's "provided that streamed nodes are
	// not passed to the dynamic expression either as the context item or as
	// the value of a parameter".
	p, known := analyzeInstrSource(t, `<xsl:evaluate xpath="'1'" context-item="a"/>`)
	if !known {
		t.Fatal("xsl:evaluate was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("xsl:evaluate given a streamed context item was reported streamable "+
			"(%v, %v); §19.8.4.16 gives context-item navigation usage", p.posture, p.sweep)
	}
}

func TestEvaluateWithLiteralXPathIsStreamable(t *testing.T) {
	// The complement: with nothing streamed passed in, the instruction is
	// grounded and motionless, so the rule above is not satisfied by
	// rejecting every xsl:evaluate.
	p, known := analyzeInstrSource(t, `<xsl:evaluate xpath="'1+1'"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		`xsl:evaluate with a literal xpath`)
}

// --- §19.8.4.35 xsl:stream --------------------------------------------------

func TestStreamIsGroundedWithTheSweepOfItsHref(t *testing.T) {
	// §19.8.4.35's final clause: "Otherwise the posture is grounded and the
	// sweep is the sweep of the href attribute value template." A literal
	// href is motionless. The posture is grounded whatever the containing
	// construct: the document xsl:stream opens is assessed separately, by
	// §18.1, and not by this rule.
	p, known := analyzeInstrSource(t, `<xsl:stream href="in.xml"><out/></xsl:stream>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless, "xsl:stream")
}

func TestStreamMentioningCurrentGroupIsUnmodelled(t *testing.T) {
	// §19.8.4.35's first two clauses reject an xsl:stream whose body calls
	// current-group() or current-merge-group() belonging to an instruction
	// that is an ancestor of the xsl:stream. Which instruction a call binds
	// to is not tracked here, so such a body is reported unmodelled rather
	// than grounded -- the direction that cannot invent an XTSE3430.
	_, known := analyzeInstrSource(t,
		`<xsl:stream href="in.xml"><xsl:value-of select="current-group()"/></xsl:stream>`)
	if known {
		t.Error("an xsl:stream whose body calls current-group() was reported as modelled; " +
			"§19.8.4.35 clause 1 turns on which instruction the call binds to, which is not tracked")
	}
}

func TestNestedSourceDocumentIsGroundedWithTheSweepOfItsHref(t *testing.T) {
	// §19.8.4.35 applies to xsl:source-document, which is the
	// Recommendation's name for the instruction this Last Call draft calls
	// xsl:stream. The clause is the same one, and so is the reason for it:
	// the document the inner instruction opens is assessed separately by
	// §18.1, so whatever its body does to that stream it reads nothing from
	// the stream of the construct that encloses it. Grounded, with the sweep
	// of the href value template -- motionless for a literal href.
	p, known := analyzeInstrSource(t,
		`<xsl:source-document streamable="yes" href="in.xml"><out/></xsl:source-document>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"a nested streamable xsl:source-document")
}

func TestNestedSourceDocumentGroundsAConsumingBody(t *testing.T) {
	// The point of the rule that the si-fork and si-iterate cases turn on:
	// the inner instruction's body may consume the stream it opens without
	// that consumption counting against the enclosing construct. A body
	// that would be consuming on its own is still grounded and motionless
	// as seen from outside. Were this not so, the two-branch xsl:fork the
	// spec's own §19.8.4.20 example shows would be rejected whenever it sat
	// inside a source document.
	p, known := analyzeInstrSource(t,
		`<xsl:source-document streamable="yes" href="in.xml">`+
			`<xsl:value-of select="BOOKLIST/BOOKS/ITEM/PRICE"/>`+
			`</xsl:source-document>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"a streamable xsl:source-document whose body consumes the stream it opens")
}

func TestSourceDocumentSweepFollowsItsHrefValueTemplate(t *testing.T) {
	// "the sweep is the sweep of the href attribute value template" -- so an
	// href that reads the enclosing stream makes the instruction consuming,
	// even though its posture stays grounded. This is the half of the clause
	// that a literal href cannot exercise, and the half that keeps the rule
	// from being a blanket grounded/motionless.
	p, known := analyzeInstrSource(t,
		`<xsl:source-document streamable="yes" href="{BOOKLIST/BOOKS/ITEM/@HREF}"><out/></xsl:source-document>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"an xsl:source-document whose href value template reads the enclosing stream")
}

func TestSourceDocumentMentioningCurrentGroupIsUnmodelled(t *testing.T) {
	// §19.8.4.35's first two clauses reject the instruction when its body
	// calls current-group() or current-merge-group() belonging to an
	// instruction that is an ancestor of it. Which instruction such a call
	// binds to is not tracked, so the body is reported unmodelled rather
	// than grounded -- the direction that cannot invent an XTSE3430. This
	// guard is what keeps si-fork-951's inner current-group() from being
	// waved through by the new case.
	_, known := analyzeInstrSource(t,
		`<xsl:source-document streamable="yes" href="in.xml">`+
			`<xsl:value-of select="current-group()"/></xsl:source-document>`)
	if known {
		t.Error("an xsl:source-document whose body calls current-group() was reported as modelled; " +
			"§19.8.4.35 clause 1 turns on which instruction the call binds to, which is not tracked")
	}
}

// --- §15.4 streamable merging ------------------------------------------------

// mergeSourceSheet wraps one xsl:merge-source attribute list in a stylesheet
// whose xsl:merge is otherwise well formed and streamable, so that the only
// thing a compile error can be attributed to is the §15.4 conditions.
func mergeSourceSheet(atts string) string {
	return `<xsl:stylesheet version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xsl:template match="/">
	 <events>
	  <xsl:merge>
	   <xsl:merge-source ` + atts + `>
	    <xsl:merge-key select="@timestamp"/>
	   </xsl:merge-source>
	   <xsl:merge-action><g><xsl:copy-of select="current-merge-group()"/></g></xsl:merge-action>
	  </xsl:merge>
	 </events>
	</xsl:template>
	</xsl:stylesheet>`
}

func compileMergeSource(t *testing.T, atts string) error {
	t.Helper()
	stree, err := xdm.ParseString(mergeSourceSheet(atts), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	_, err = Compile(stree.Root, CompileOptions{})
	return err
}

func TestStreamedMergeSourceRequiresAStridingSelect(t *testing.T) {
	// §15.4's third condition: "The expression in the select attribute of
	// that xsl:merge-source element has striding posture." A path with a
	// descendant step is crawling, not striding, so the source is not
	// guaranteed-streamable. This is merge-094, whose own description reads
	// "not streamable because select expression is crawling".
	//
	// §19.8.4.25 cannot catch this: it asks only that the for-each-source
	// expression be grounded and motionless, which 'log-file-2.xml' is, and
	// then declares the whole xsl:merge grounded and motionless. The select
	// is measured against the document the source opens, which is a
	// different stream, and §15.4 is the rule that measures it.
	err := compileMergeSource(t,
		`for-each-source="'log-file-2.xml'" select="log//record" streamable="yes"`)
	if err == nil {
		t.Fatal("a streamed xsl:merge-source with a crawling select compiled; " +
			"§15.4 condition 3 requires striding posture")
	}
	if !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("want XTSE3430, got: %v", err)
	}
}

func TestStreamedMergeSourceRejectsSortBeforeMerge(t *testing.T) {
	// §15.4's fourth condition: "The sort-before-merge attribute of that
	// xsl:merge-source element is either absent or takes its default value
	// of no." Re-ordering the selected nodes means holding them, which a
	// streamed read cannot do. This is merge-095, which is merge-094's
	// select made striding and sort-before-merge added instead, so the two
	// cases isolate the two conditions from one another.
	err := compileMergeSource(t,
		`for-each-source="'log-file-2.xml'" select="log/day/record" `+
			`streamable="yes" sort-before-merge="yes"`)
	if err == nil {
		t.Fatal(`a streamed xsl:merge-source with sort-before-merge="yes" compiled; ` +
			"§15.4 condition 4 forbids it")
	}
	if !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("want XTSE3430, got: %v", err)
	}
}

func TestStridingMergeSourceIsStreamable(t *testing.T) {
	// The positive half, and the one that keeps the rule from being a
	// blanket refusal: §15.4's own worked example streams
	// select="log/day/record", a striding path, with no sort-before-merge.
	// It must compile.
	if err := compileMergeSource(t,
		`for-each-source="'log-file-2.xml'" select="log/day/record" streamable="yes"`); err != nil {
		t.Fatalf("§15.4's own example of a streamed merge source was rejected: %v", err)
	}
}

func TestUnstreamedMergeSourceIsNotCheckedBy154(t *testing.T) {
	// §15.4's first two conditions select what is in scope, and a source
	// that does not ask to be streamed is not. A for-each-item source has no
	// stream to be striding against at all, so it must pass a select that
	// condition 3 would otherwise reject.
	//
	// The other spelling of "not streamed" -- for-each-source with an
	// explicit streamable="no" -- used to be tested here too, and is no
	// longer writable: XTSE3195's last clause says that with for-each-source
	// present "the only permitted value (and the default value) of the
	// streamable attribute is yes", so the combination is refused before any
	// posture is measured. That refusal is asserted by
	// TestMergeSourceStreamableNoIsRefusedWithForEachSource.
	for _, atts := range []string{
		`for-each-item="'log-file-2.xml'" select="log//record"`,
	} {
		if err := compileMergeSource(t, atts); err != nil {
			t.Errorf("an xsl:merge-source that is not streamed was rejected by §15.4: %v\n  %s",
				err, atts)
		}
	}
}

// analyzeFirstInSourceDoc parses a whole stylesheet and returns the properties
// of the single instruction inside its first streamable xsl:source-document.
//
// analyzeInstrSource and analyzeInstrWithDecls build the stylesheet around the
// fragment; this takes one already written, which the rules that read
// declarations elsewhere in the module need -- default-mode on the
// xsl:transform element, an xsl:import, or a named template's own xsl:param
// declarations.
func analyzeFirstInSourceDoc(t *testing.T, src string) (props, bool) {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	root := doc.Root
	if root == nil {
		t.Fatal("no document element")
	}
	var container *xdm.Node
	walkElements(root, func(el *xdm.Node) bool {
		if container == nil && isXSL(el, "source-document") && isYes(el.AttrValue("streamable")) {
			container = el
		}
		return container == nil
	})
	if container == nil {
		t.Fatal("no streamable xsl:source-document in the test stylesheet")
	}
	kids := container.ChildElements()
	if len(kids) != 1 {
		t.Fatalf("the source-document holds %d elements, want exactly 1", len(kids))
	}
	return analyzeInstruction(kids[0], postureStriding, attributeSetDeclarations(root))
}

func TestApplyTemplatesUsesTheDefaultModeInScope(t *testing.T) {
	// §3.8.2: when the mode attribute is omitted, "the mode is taken from the
	// [xsl:]default-mode attribute of the innermost ancestor element that has
	// such an attribute". A bare xsl:apply-templates under
	// default-mode="m" therefore reaches mode m, and §19.8.4.5 clause 3 must
	// read m's declaration rather than the unnamed mode's. Treating it as the
	// unnamed mode rejects sf-current-100, which the spec requires to run.
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 default-mode="m">
	<xsl:mode name="m" streamable="yes"/>
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml"><xsl:apply-templates select="a"/></xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	p, known := analyzeFirstInSourceDoc(t, src)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:apply-templates under default-mode=m")
}

func TestApplyTemplatesToAModeDeclaredInAnImportedModuleIsUnmodelled(t *testing.T) {
	// §19.8.4.5 clause 3 turns on whether the target mode is declared
	// streamable, and this check runs before xsl:import is inlined. A mode
	// with no declaration in this module is therefore not "declared
	// non-streamable" -- the declaration may be in the module imported. The
	// analysis must say unmodelled rather than reject: si-apply-imports-068
	// declares its streamable mode in the module it imports.
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:import href="other.xsl"/>
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml"><xsl:apply-templates select="a"/></xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	_, known := analyzeFirstInSourceDoc(t, src)
	if known {
		t.Error("xsl:apply-templates to a mode with no declaration in a module that imports " +
			"another was reported as modelled; the declaration may be in the imported module")
	}

	// The complement, and the guard against a rule that never decides: with
	// nothing imported, an undeclared mode really is not streamable, and
	// clause 3 rejects.
	src = `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml"><xsl:apply-templates select="a"/></xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	p, known := analyzeFirstInSourceDoc(t, src)
	if !known {
		t.Fatal("a self-contained stylesheet left the mode unresolved")
	}
	if p.posture != postureRoaming || p.sweep != sweepFreeRanging {
		t.Errorf("got %v and %v, want roaming and free-ranging; the unnamed mode is not "+
			"declared streamable and nothing is imported", p.posture, p.sweep)
	}
}

func TestCallTemplateTakesTheTargetsParameterType(t *testing.T) {
	// §19.8.4.9: a with-param's usage comes from "the xsl:with-param/@as
	// attribute, or the xsl:param/@as attribute of the corresponding
	// parameter on the target named template, whichever is more
	// restrictive". Here the with-param declares nothing and the target
	// declares xs:decimal, which atomizes the streamed PRICE. Reading only
	// the with-param would give item()* and so navigation, rejecting
	// si-call-template-002, which the spec requires to run.
	src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	 xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xsl:template name="t">
	 <xsl:context-item use="absent"/>
	 <xsl:param name="price" as="xs:decimal"/>
	</xsl:template>
	<xsl:template name="main">
	 <xsl:source-document streamable="yes" href="in.xml"><xsl:call-template name="t"><xsl:with-param name="price" select="PRICE"/></xsl:call-template></xsl:source-document>
	</xsl:template>
	</xsl:transform>`
	p, known := analyzeFirstInSourceDoc(t, src)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:call-template whose target declares the parameter as xs:decimal")
}

func TestContextItemAbsentIsSpeltAbsent(t *testing.T) {
	// §19.8.4.9 words the no-context-item case as use="prohibited", but no
	// such value exists: §9.6's grammar is use? = "required" | "optional" |
	// "absent". Both spellings must give no context item operand, or
	// si-call-template-002 is rejected.
	for _, use := range []string{"absent", "prohibited"} {
		src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template name="t"><xsl:context-item use="` + use + `"/><out/></xsl:template>
		<xsl:template name="main">
		 <xsl:source-document streamable="yes" href="in.xml"><xsl:call-template name="t"/></xsl:source-document>
		</xsl:template>
		</xsl:transform>`
		p, known := analyzeFirstInSourceDoc(t, src)
		wantProps(t, p, known, postureGrounded, sweepMotionless,
			`xsl:call-template on a template with use="`+use+`"`)
	}
}
