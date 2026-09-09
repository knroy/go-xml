package xslt

import (
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

func TestForEachGroupGroupingKeyIsAssessedGrounded(t *testing.T) {
	// §19.8.4.19 assesses the group-adjacent expression "with a context
	// posture of grounded". A path such as "b" is therefore grounded and
	// motionless there -- §19.8.8.8 makes any axis step from a grounded
	// context posture grounded and motionless -- and so the "not motionless"
	// clause does not fire. This pins the grounded assessment: were the key
	// assessed in the instruction's own striding posture, "b" would be
	// consuming and the instruction would be rejected.
	p, known := analyzeInstrSource(t, `<xsl:for-each-group select="a" group-adjacent="b">
	 <xsl:value-of select="c"/>
	</xsl:for-each-group>`)
	if !known {
		t.Fatal("the grouping-key expression was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("xsl:for-each-group with a grounded-assessed group-adjacent: got %v and %v, "+
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
	// stays silent rather than rejecting a valid stylesheet. xsl:number is
	// one such instruction.
	_, known := analyzeInstrSource(t, `<xsl:number value="1"/>`)
	if known {
		t.Error("xsl:number was reported as modelled; its §19.8.4.30 rule is not implemented, " +
			"so treating it as modelled risks a spurious XTSE3430")
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
	// fn:current-group() has no entry in the §19.8.9 table, so a path
	// filtered on it must come back unmodelled.
	_, known := analyzeInstrSource(t,
		`<xsl:value-of select="//transaction[@date = current-group()[1]/Date]"/>`)
	if known {
		t.Error("a path whose predicate calls the unmodelled current-group() was reported " +
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
	 <xsl:number value="1"/>
	</xsl:for-each>`)
	if known {
		t.Error("an unmodelled instruction inside xsl:for-each did not clear known")
	}
}
