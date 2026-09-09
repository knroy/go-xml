package xslt

import (
	"strings"
	"testing"
)

// §19.6's third clause -- "if the focus-setting container of C is a template
// rule whose mode is declared with streamable='yes', then the context posture
// is striding" -- makes a template rule of a streamable mode a focus-setting
// container in its own right. Its body is therefore a sequence constructor
// assessed against a striding context posture under the §19.8.4 instruction
// rules, exactly as the body of xsl:stream or a streamable
// xsl:source-document is.
//
// The consequence the tests below rest on is that the assessment is LOCAL: it
// never depends on which xsl:apply-templates dispatched to the rule, nor on
// the bodies of the rules the body's own apply-templates might reach. That is
// what makes the whole of a streamable mode decidable without a fixed point.
func TestStreamableModeBodyXTSE3430(t *testing.T) {
	// §19.8.8.8's axis table: from a striding context posture the descendant
	// and descendant-or-self axes selecting elements are crawling and
	// consuming. §19.8.4.5 then makes xsl:apply-templates over a crawling
	// selection roaming, because the nodes it delivers have nested subtrees
	// and processing them would need the stream rewound. This is
	// streamable-060, -061, -062 and -106.
	t.Run("apply-templates over a descendant selection", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="chap" mode="s">`+
				`<xsl:apply-templates select=".//section" mode="s"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.8.8 makes \".//section\" crawling from a striding "+
				"context, and §19.8.4.5 makes xsl:apply-templates over a "+
				"crawling selection roaming; want XTSE3430, got: %v", err)
		}
	})

	// The same axis table with xsl:for-each (§19.8.4.16), which is
	// streamable-071, -072 and -079. The instruction differs; the reason the
	// selection cannot be streamed does not.
	//
	// The body must itself read something: §19.8.4.16 assesses the body
	// against the posture of the select, and a crawling context with a
	// motionless body is still streamable -- the nested subtrees are never
	// looked into. It is reading a child of a crawling node, as
	// streamable-071 does with LoanStatus/LoanStatusType, that cannot be
	// streamed, because two nodes of the selection may overlap.
	t.Run("for-each over a descendant selection", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="myroot" mode="s">`+
				`<xsl:for-each select=".//Loan">`+
				`<loan><xsl:value-of select="LoanStatus/LoanStatusType"/></loan>`+
				`</xsl:for-each>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.8.8 makes \".//Loan\" crawling, and reading a "+
				"child of a crawling node cannot be streamed; want "+
				"XTSE3430, got: %v", err)
		}
	})

	// §19.8.1: "if more than one operand is potentially consuming, then
	// roaming and free-ranging" -- the input cannot be rewound to read the
	// same streamed node a second time. Each xsl:attribute absorbs its
	// select, and string(.) from a striding context is consuming, so two of
	// them compete. This is streamable-107.
	t.Run("two operands consuming the context node", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="Amount" mode="s">`+
				`<xsl:attribute name="a" select="string(.)"/>`+
				`<xsl:attribute name="b" select="string(.)"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.1 makes a construct with more than one "+
				"potentially-consuming operand roaming; want XTSE3430, got: %v", err)
		}
	})

	// The same rule reached through an expression rather than two
	// instructions: "and" has two operands, and each child step from a
	// striding context is consuming. This is streamable-109.
	t.Run("two children read in one condition", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="LoanStatus" mode="s">`+
				`<xsl:if test="Type='O' and Balance > 1"><e/></xsl:if>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.1 makes two potentially-consuming operands "+
				"roaming; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.8.7 rewrites a leading "/" as a call on fn:root, so "//chtitle"
	// is a RootedPath reaching the whole document. From inside a streamed
	// template that is free-ranging. This is streamable-117.
	t.Run("rooted path", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="book" mode="s">`+
				`<xsl:apply-templates select="//chtitle" mode="s"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("a RootedPath reaches nodes outside the streamed "+
				"window; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.4.5 clause 2: "if there is an xsl:sort child element, then
	// roaming and free-ranging". Sorting cannot begin until the whole
	// selection is known, so it cannot be streamed. This is streamable-120.
	t.Run("apply-templates with a sort", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="book" mode="s">`+
				`<xsl:apply-templates select="chtitle" mode="s">`+
				`<xsl:sort select="."/></xsl:apply-templates>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.4.5 makes an xsl:apply-templates with an "+
				"xsl:sort child roaming; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.4.39: with no "as" attribute an xsl:variable's select has usage
	// navigation, and §19.8.1 makes navigation from a streamed posture
	// free-ranging. That is the rule that stops a streamed node being held in
	// a variable and read again later. This is streamable-110.
	t.Run("variable bound to a streamed node", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="Loan" mode="s">`+
				`<xsl:variable name="this" select="."/>`+
				`<xsl:value-of select="$this"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.4.39 gives an untyped xsl:variable's select "+
				"usage navigation, which §19.8.1 makes free-ranging from a "+
				"streamed posture; want XTSE3430, got: %v", err)
		}
	})

	// A rule whose body IS streamable must not be rejected. §19.8.8.8 gives
	// the child axis from striding a striding-and-consuming result, and one
	// consuming operand is what streaming allows.
	t.Run("a streamable body is accepted", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="chap" mode="s">`+
				`<xsl:value-of select="title"/>`+
				`</xsl:template>`)); err != nil {
			t.Fatalf("one consuming operand is streamable, so §19.8 requires "+
				"this rule to compile; got: %v", err)
		}
	})

	// The locality claim, stated as a test: the rule reached by the
	// apply-templates is not consulted. Here "section" is selected on the
	// child axis, which is striding and consuming, so the chap rule is
	// streamable -- even though the section rule it dispatches to is itself
	// not. Were the check following apply-templates into other rules, this
	// would be rejected at chap.
	t.Run("a rule is judged without following apply-templates", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="chap" mode="s">`+
				`<xsl:apply-templates select="section" mode="s"/>`+
				`</xsl:template>`+
				`<xsl:template match="section" mode="s">`+
				`<xsl:apply-templates select=".//page" mode="s"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("the section rule is roaming on its own terms; "+
				"want XTSE3430, got: %v", err)
		}
		// The error must name the rule that is actually unstreamable, not
		// the one that dispatched to it.
		if !strings.Contains(err.Error(), "section") {
			t.Fatalf("the error should name the section rule, whose body is "+
				"the roaming one; got: %v", err)
		}
	})

	// A rule in a mode that is not declared streamable has a roaming context
	// posture by §19.6's "any other declaration" clause, and §19.1 asks for
	// no streamability of it at all. Nothing here may be reported.
	t.Run("a rule outside a streamable mode is not assessed", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="chap" mode="other">`+
				`<xsl:apply-templates select=".//section" mode="other"/>`+
				`</xsl:template>`+
				`<xsl:mode name="other"/>`)); err != nil {
			t.Fatalf("a mode not declared streamable is not subject to "+
				"§19.8; got: %v", err)
		}
	})
}

// §19.8.1 computes an operand's adjusted usage by downgrading absorption to
// inspection when "the intersection of T with U{element(), document-node()} is
// U{}" -- a type that cannot hold nodes with children is entirely available
// without reading further from the stream.
//
// The type in question is the operand's, so for a union, intersect or except
// expression it is bounded by the two operand types: every node the result can
// return comes from one side or the other.
func TestUnionOfAttributesAllowsNoChildren(t *testing.T) {
	// "@* except @length" can return attributes only. §19.8.4.13 gives
	// xsl:copy-of's select usage absorption, which the downgrade turns to
	// inspection, leaving the operand motionless -- so it does not compete
	// with the xsl:apply-templates beside it. streamable-046 and -063 are
	// both written this way and the catalog expects them to run.
	t.Run("attribute union does not consume", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="*" mode="s">`+
				`<xsl:copy><xsl:copy-of select="@* except @length"/>`+
				`<xsl:apply-templates mode="s"/></xsl:copy>`+
				`</xsl:template>`)); err != nil {
			t.Fatalf("attributes have no children, so §19.8.1 downgrades the "+
				"absorption to inspection and the copy-of is motionless; "+
				"the rule must compile, got: %v", err)
		}
	})

	// The downgrade must not extend to a union that can return an element:
	// "@a | b" can, so its absorption stands and it competes with the
	// apply-templates, which §19.8.1 makes roaming.
	t.Run("a union that can return an element still consumes", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="*" mode="s">`+
				`<xsl:copy><xsl:copy-of select="@a | b"/>`+
				`<xsl:apply-templates mode="s"/></xsl:copy>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("\"@a | b\" can return an element, so the absorption "+
				"stands and two operands consume; want XTSE3430, got: %v", err)
		}
	})
}

// §19.8.4.25 assesses an xsl:merge by its xsl:merge-source children, and a
// source that names its own input reads nothing from the streamed context: its
// select is evaluated against the document the source itself opens. The
// attribute naming that input is spelled for-each-stream in the working draft
// and for-each-source in the Recommendation, which is what merge.go implements
// and the suite writes.
func TestMergeSourceReadsItsOwnInput(t *testing.T) {
	// merge-098: two sources, each with for-each-source, whose selects walk
	// down from the root of their OWN document. Read as selections from the
	// streamed context they would be striding and consuming, and two of them
	// would collide; read correctly they cost the streamed input nothing.
	t.Run("for-each-source is not a selection from the stream", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="/" mode="s">`+
				`<xsl:variable name="u" as="xs:anyURI" select="base-uri()"/>`+
				`<xsl:merge>`+
				`<xsl:merge-source for-each-source="$u" streamable="yes" select="Root/A/Employee">`+
				`<xsl:merge-key select="ID"/></xsl:merge-source>`+
				`<xsl:merge-source for-each-source="$u" streamable="yes" select="Root/B/Empl">`+
				`<xsl:merge-key select="Emplid"/></xsl:merge-source>`+
				`<xsl:merge-action><out/></xsl:merge-action>`+
				`</xsl:merge></xsl:template>`)); err != nil {
			t.Fatalf("each xsl:merge-source reads the document its "+
				"for-each-source names, not the streamed context, so the "+
				"two selects do not compete; the rule must compile, got: %v", err)
		}
	})
}

// A step's context item is the node the steps before it deliver, so §19.8.1's
// downgrade applies inside a path too: in "@nr/string()" the context of
// string() is an attribute, which has no children, and the atomization is
// therefore motionless rather than consuming.
func TestPathStepCarriesTheContextItemType(t *testing.T) {
	// streamable-031 binds a variable to
	// "chapter/(@nr/string(), @length/string(), chtitle/string())". Each
	// attribute atomization is motionless, so they do not compete, and the
	// catalog expects the stylesheet to run.
	t.Run("atomizing attributes in a path does not consume twice", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="book" mode="s">`+
				`<xsl:variable name="t" as="text()*">`+
				`<xsl:value-of select="chapter/(@nr/string(), @length/string())"/>`+
				`</xsl:variable><out/>`+
				`</xsl:template>`)); err != nil {
			t.Fatalf("an attribute has no children, so atomizing two of them "+
				"is two motionless operands, not two consuming ones; "+
				"the rule must compile, got: %v", err)
		}
	})
}

// §14.4 takes the current group and the current grouping key away inside a
// declared-streamable construct, but the two functions differ in streamability
// and so in whether a rule that calls them can be refused statically.
//
// The suite fixes the line with two stylesheets that are otherwise identical,
// both wrapping a grouping in xsl:fork inside a streamable xsl:source-document
// and applying templates in a streamable mode:
//
//	si-fork-115  the rule calls current-grouping-key()  -> asserts OUTPUT
//	si-fork-116  the rule calls current-group()         -> asserts XTSE3430
func TestGroupingFunctionsInAStreamableRule(t *testing.T) {
	// §19.8.9.5 is one line: "A call to the current-grouping-key function is
	// grounded and motionless." The key is an atomic value, so reading it
	// costs no access to the streamed input wherever it is written, and a
	// rule that asks for it must compile with no special handling anywhere --
	// which is why nothing in streamcontext.go mentions it. Its absence at
	// run time is XTDE1071 for an xsl:catch to handle: si-fork-115's note
	// says the processor "can't know where the template with the offending
	// call of current-grouping-key() is going to be called from".
	t.Run("current-grouping-key does not make a rule unstreamable", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="*" mode="s">`+
				`<h key="{current-grouping-key()}"/>`+
				`</xsl:template>`)); err != nil {
			t.Fatalf("§19.8.9.5 makes a call on current-grouping-key "+
				"grounded and motionless, so the rule must compile, got: %v", err)
		}
	})

	// §19.8.9.4's "otherwise" clause: a call on current-group() with no
	// xsl:for-each-group of its own has the group out of reach, and the
	// catalog asserts XTSE3430 for exactly this stylesheet, noting "it's a
	// static error".
	t.Run("current-group outside any grouping is refused", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="*" mode="s">`+
				`<h size="{count(current-group())}"/>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.9.4 makes a call on current-group roaming when "+
				"the group is out of reach; want XTSE3430, got: %v", err)
		}
	})

	// Inside the rule's own xsl:for-each-group the group is in reach, and
	// §19.8.9.4 gives the call the posture of the group's select. si-group-055
	// applies templates to "current-group() except .", which §19.8.8.4 widens
	// to crawling by its own admission rather than by necessity -- the group's
	// members are siblings and except only removes items -- so the refusal is
	// withheld. The catalog asserts output for that case, naming the Saxon bug
	// (3256) it was written against.
	t.Run("current-group inside its own grouping is not refused", func(t *testing.T) {
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match="contents" mode="s">`+
				`<xsl:for-each-group select="unnumbered" `+
				`group-starting-with="unnumbered[@type = 'PT']">`+
				`<xsl:apply-templates select="current-group() except ." mode="s"/>`+
				`</xsl:for-each-group>`+
				`</xsl:template>`)); err != nil {
			t.Fatalf("the group is in reach inside its own "+
				"xsl:for-each-group, and §19.8.8.4's widening to crawling is "+
				"an over-approximation the spec's own note concedes; "+
				"the rule must compile, got: %v", err)
		}
	})

	// Two references to the group in one expression read it twice, which
	// §19.8.1's limit of one potentially-consuming operand refuses on its own
	// terms -- independently of §19.8.8.4's widening. si-group-017 writes
	// exactly this and the catalog asserts XTSE3430, so the withholding above
	// must not extend to it.
	t.Run("two references to the group are still refused", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="account" mode="s">`+
				`<xsl:for-each-group select="transaction" group-adjacent="@date">`+
				`<first><xsl:sequence select="count(current-group()), current-group()"/></first>`+
				`</xsl:for-each-group>`+
				`</xsl:template>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.1 refuses a construct with more than one "+
				"potentially-consuming operand, and two current-group() "+
				"references are two; want XTSE3430, got: %v", err)
		}
	})
}
