package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Tests for the five §19.8 rules this file's neighbours gained: the
// group-starting-with / group-ending-with pattern operand and the context
// posture of the grouping key (§19.8.4.19), the constructor-function operand
// (§19.8.8.14), the fn:outermost exception (§19.8.9.15), and §18.1's demand
// that the body of a streamed document be grounded.
//
// Each case states what the spec requires and cites the clause. The expected
// posture and sweep are derived from the cited text, not read off the
// implementation.

// compileStreamCheck parses a whole stylesheet and runs the three static
// streamability checks over it, returning the first error or nil.
func compileStreamCheck(t *testing.T, src string) error {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test stylesheet: %v", err)
	}
	for _, f := range []func(*xdm.Node) error{
		checkStreamability, checkStreamableModePatterns, checkStreamableModeBodies,
	} {
		if e := f(doc.Root); e != nil {
			return e
		}
	}
	return nil
}

func wantXTSE3430(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: the analysis raised no error; want XTSE3430", what)
	}
	if !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("%s: got %v, want XTSE3430", what, err)
	}
}

func wantNoError(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: the analysis refused a valid stylesheet: %v", what, err)
	}
}

// --- §19.8.9.15 fn:outermost ------------------------------------------------

func TestOutermostNarrowsCrawlingToStriding(t *testing.T) {
	// §19.8.9.15: "The single argument to this function has operand usage
	// transmission. The streamability of the function call follows the
	// general streamability rules with one exception: if the posture of the
	// argument is crawling, then the posture of the result is striding."
	//
	// "//AUTHOR" is a descendant-or-self path, so §19.8.8.8 makes it
	// crawling and consuming. Transmission hands those nodes on, and the
	// exception narrows crawling to striding.
	p, known := analyzeInstrSource(t,
		`<xsl:sequence select="outermost(//AUTHOR)"/>`)
	wantProps(t, p, known, postureStriding, sweepConsuming,
		"xsl:sequence select=outermost(//AUTHOR)")
}

func TestOutermostArgumentIsTransmission(t *testing.T) {
	// The same clause, with a motionless argument: transmission of an
	// attribute step leaves the posture striding and the sweep motionless,
	// so the exception does not fire and nothing is widened.
	p, known := analyzeInstrSource(t,
		`<xsl:sequence select="outermost(@code)"/>`)
	wantProps(t, p, known, postureStriding, sweepMotionless,
		"xsl:sequence select=outermost(@code)")
}

// --- §19.8.8.14 constructor functions ---------------------------------------

func TestConstructorFunctionOperandIsAbsorption(t *testing.T) {
	// §19.8.8.14: "For a call to a constructor function, the general rules
	// for streamability apply. There is a single operand role (the argument
	// to the function), with operand usage absorption." §19.9's worked
	// example applies this to xs:date(@timestamp) and finds it motionless,
	// because absorbing an attribute reads nothing further from the stream.
	p, known := analyzeInstrSource(t,
		`<xsl:value-of select="xs:date(@timestamp)"/>`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"xsl:value-of select=xs:date(@timestamp)")
}

func TestConstructorFunctionAbsorbsAConsumingArgument(t *testing.T) {
	// The same rule with an argument that does read the stream: absorbing a
	// striding element step is consuming, and absorption grounds the result.
	p, known := analyzeInstrSource(t,
		`<xsl:value-of select="xs:string(PRICE)"/>`)
	wantProps(t, p, known, postureGrounded, sweepConsuming,
		"xsl:value-of select=xs:string(PRICE)")
}

// --- §19.9 the body of a streamed document must be grounded ----------------

func TestStreamedDocumentBodyMustBeGrounded(t *testing.T) {
	// §19.9: "The rules for xsl:source-document say that the instruction
	// is guaranteed-streamable if the contained sequence constructor is
	// grounded." §18.1's note makes the
	// consequence explicit: "it cannot contain the instruction
	// <xsl:sequence select='//chapter'/>. If nodes from this document are to
	// be returned, they must first be copied."
	//
	// A body that is striding and consuming passes the general streamability
	// rules and still hands streamed nodes out, so streamable() alone is not
	// the test.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <xsl:iterate select="account/transaction">
	    <xsl:sequence select="."/>
	   </xsl:iterate>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantXTSE3430(t, err, "a streamed source-document whose body is striding")
}

func TestStreamedDocumentBodyThatCopiesIsAccepted(t *testing.T) {
	// The remedy §18.1's note names: copying grounds the result, so the same
	// stylesheet with xsl:copy-of must compile.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <xsl:iterate select="account/transaction">
	    <xsl:copy-of select="."/>
	   </xsl:iterate>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "a streamed source-document whose body copies")
}

// --- §19.8.4.19 the grouping pattern ----------------------------------------

func TestFreeRangingGroupPatternIsRefused(t *testing.T) {
	// §19.8.4.19 lists "the group-starting-with or group-ending-with
	// patterns if present" among the instruction's operands, and §19.8.10
	// classifies "record[foo = 'a']" as free-ranging: the predicate is not
	// motionless, which is the spec's own "p[b]" example. §19.8.1 makes any
	// free-ranging operand decisive, so the instruction is roaming.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <xsl:for-each-group select="record" group-starting-with="record[foo = 'a']">
	    <xsl:copy-of select="current-group()"/>
	   </xsl:for-each-group>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantXTSE3430(t, err, "group-starting-with a free-ranging pattern")
}

func TestMotionlessGroupPatternIsAccepted(t *testing.T) {
	// The same instruction with a pattern §19.8.10 lists as motionless --
	// "p[@status='red']" is one of its worked examples -- must compile.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <xsl:for-each-group select="record" group-starting-with="record[@status='red']">
	    <xsl:copy-of select="current-group()"/>
	   </xsl:for-each-group>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "group-starting-with a motionless pattern")
}

func TestFreeRangingGroupPatternOverAGroundedSelectIsAccepted(t *testing.T) {
	// The pattern is matched against the items the select delivers. A
	// grounded select has already materialised them, so reading their
	// children advances nothing and §19.8.10's stream-reading objection does
	// not arise. si-group-203 is written this way -- "//Item/copy-of()"
	// started on "Item[processing-instruction('start')]" -- and the catalog
	// asserts output for it.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <Root>
	    <xsl:for-each-group select="//Item/copy-of()"
	                        group-starting-with="Item[processing-instruction('start')]">
	     <xsl:copy-of select="current-group()"/>
	    </xsl:for-each-group>
	   </Root>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "a free-ranging pattern over a grounded select")
}

// --- §19.8.4.19 clause 3, the context posture of the grouping key -----------

func TestNonMotionlessGroupingKeyIsRefused(t *testing.T) {
	// §19.8.4.19 clause 3: "If there is a group-by or group-adjacent
	// attribute that is not motionless, then roaming and free-ranging."
	//
	// The key is assessed in the SELECT's context posture, not in grounded.
	// §19.9's worked example says so of the group-adjacent operand
	// "@timestamp": "the context posture is the posture of the controlling
	// operand of the focus-setting container, that is, the select expression
	// of the containing xsl:for-each-group instruction, which as established
	// above is striding." Read from a striding context, "PRICE/text()" is
	// consuming, so clause 3 fires.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <out>
	    <xsl:for-each-group select="ITEM" group-adjacent="PRICE/text()">
	     <group/>
	    </xsl:for-each-group>
	   </out>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantXTSE3430(t, err, "group-adjacent on a consuming key")
}

func TestMotionlessGroupingKeyIsAccepted(t *testing.T) {
	// The key of §19.9's own worked example. An attribute step read from a
	// striding context is motionless, so clause 3 does not fire.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:template name="main">
	  <xsl:source-document streamable="yes" href="in.xml">
	   <out>
	    <xsl:for-each-group select="ITEM" group-adjacent="@CAT">
	     <group/>
	    </xsl:for-each-group>
	   </out>
	  </xsl:source-document>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "group-adjacent on an attribute key")
}

// --- the withholding for fn:current-group, and what lifts it ----------------

func TestCurrentGroupWithholdingIsLiftedByAnIndependentRefusal(t *testing.T) {
	// checkStreamableModeBodies withholds a roaming verdict where the rule
	// calls fn:current-group inside a grouping of its own, because §19.8.8.4
	// widens two striding operands to crawling and the analysis cannot tell
	// a real nesting from that widening.
	//
	// A grouping key that is not motionless is a second, independent reason
	// for the same verdict, and §19.8.4.19 clause 3 derives it without
	// consulting the call at all. The withholding must not silence it.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:mode name="s" streamable="yes"/>
	 <xsl:template match="BOOKS" mode="s">
	  <out>
	   <xsl:for-each-group select="ITEM" group-adjacent="PRICE/text()">
	    <group count="{count(current-group())}"/>
	   </xsl:for-each-group>
	  </out>
	 </xsl:template>
	</xsl:transform>`)
	wantXTSE3430(t, err, "a streamable rule with a consuming grouping key")
}

func TestCurrentGroupWithholdingStandsWithoutAnIndependentRefusal(t *testing.T) {
	// The case the withholding exists for: a motionless key, a striding
	// select, and a call on fn:current-group inside an xsl:fork. The roaming
	// verdict here rests on §19.8.8.4's widening, which the spec admits is a
	// choice rather than a necessity, so the refusal is withheld and the
	// stylesheet compiles. si-group-055 and si-group-203 depend on this.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:mode name="s" streamable="yes"/>
	 <xsl:template match="BOOKS" mode="s">
	  <out>
	   <xsl:for-each-group select="ITEM" group-starting-with="ITEM">
	    <xsl:fork>
	     <xsl:sequence select="count(current-group() except .)"/>
	    </xsl:fork>
	   </xsl:for-each-group>
	  </out>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "a streamable rule whose only refusal is the widening")
}

// --- §19.8.9.4 for a container nested inside an xsl:for-each-group ----------

func TestNestedStreamedDocumentReadingAStreamedGroupIsRefused(t *testing.T) {
	// §19.8.9.4 gives a call on fn:current-group the posture and sweep of
	// the group's select expression, and "otherwise, roaming and
	// free-ranging". A nested streamable container is that otherwise: the
	// group's members are still streamed nodes that the inner document would
	// have to read a second time.
	//
	// That answer is a fact about the stylesheet only where the outer
	// select was assessed all the way to a posture. si-group-052 is this
	// stylesheet, with a select of "Order".
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:mode streamable="yes"/>
	 <xsl:template match="Orders">
	  <xsl:copy>
	   <xsl:for-each-group select="Order" group-adjacent="@number">
	    <group>
	     <xsl:source-document href="t.xml" streamable="yes">
	      <xsl:for-each select="//transaction[@date = current-group()[1]/Date]">
	       <value><xsl:value-of select="@value"/></value>
	      </xsl:for-each>
	     </xsl:source-document>
	    </group>
	   </xsl:for-each-group>
	  </xsl:copy>
	 </xsl:template>
	</xsl:transform>`)
	wantXTSE3430(t, err, "a nested streamed document reading a streamed group")
}

func TestNestedStreamedDocumentReadingAGroundedGroupIsAccepted(t *testing.T) {
	// The same stylesheet with a grounded select. The group is already
	// materialised, so reading it inside the nested container costs nothing
	// further and §19.8.9.4's roaming answer does not apply. si-group-051 is
	// this stylesheet, and the catalog asserts output for it.
	err := compileStreamCheck(t, `<xsl:transform version="3.0"
	 xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	 <xsl:mode streamable="yes"/>
	 <xsl:template match="Orders">
	  <xsl:copy>
	   <xsl:for-each-group select="Order/copy-of()" group-adjacent="@number">
	    <group>
	     <xsl:source-document href="t.xml" streamable="yes">
	      <xsl:for-each select="//transaction[@date = current-group()[1]/Date]">
	       <value><xsl:value-of select="@value"/></value>
	      </xsl:for-each>
	     </xsl:source-document>
	    </group>
	   </xsl:for-each-group>
	  </xsl:copy>
	 </xsl:template>
	</xsl:transform>`)
	wantNoError(t, err, "a nested streamed document reading a grounded group")
}
