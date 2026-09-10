package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// Per-construct tests for the §19.8.8 expression rules in streamexprs.go.
//
// Every case asserts what the specification requires of the construct, taken
// from the rule text or from an example the specification itself gives. As in
// streamability_test.go, the streamable arms matter at least as much as the
// unstreamable ones: a construct wrongly found roaming becomes a spurious
// XTSE3430, which rejects a valid stylesheet at compile time.

// analyze31 parses an XPath 3.1 expression -- needed for the map and array
// constructors, which XPath 3.0 does not have -- and assesses it with a
// striding context posture, as inside xsl:stream (§19.6).
func analyze31(t *testing.T, src string) (props, bool) {
	t.Helper()
	expr, err := xpath.ParseVersion(src, nil, xpath.XPath31)
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	return analyzeExpr(expr, postureStriding)
}

// TestAnalyzeUnionExpr checks the five-step cascade of §19.8.8.4. Four of the
// five cases carry an example in the rule text itself, and those examples are
// used verbatim.
func TestAnalyzeUnionExpr(t *testing.T) {
	cases := []struct {
		expr    string
		want    props
		comment string
	}{
		// Rule 1: "if either of the two operands is free-ranging, then
		// roaming and free-ranging". The spec's own example.
		{". | following-sibling::*", roamingFreeRanging,
			"a free-ranging operand poisons the union"},

		// Rule 2: "if either of the two operands is grounded and motionless,
		// then the posture and sweep of the other operand". The spec writes
		// this as ". | doc('abc.com')//x"; "2" is a grounded motionless
		// operand that needs no document.
		{"2 | child::a", props{postureStriding, sweepConsuming},
			"a grounded motionless operand yields the other operand"},
		{"child::a | 2", props{postureStriding, sweepConsuming},
			"and does so from either side"},

		// Rule 3: "if both operands are climbing, then climbing and the
		// wider of the sweeps". The spec's example.
		{"parent::A | */ancestor::B", props{postureClimbing, sweepConsuming},
			"two climbing operands stay climbing"},
		{"parent::A | ancestor::B", props{postureClimbing, sweepMotionless},
			"two motionless climbing operands stay motionless"},

		// Rule 4: "if the left-hand operand is striding or crawling and the
		// right-hand operand is also striding or crawling, then crawling and
		// the wider of the sweeps". The spec's example is "* | */*", and its
		// note explains why the result is crawling even when both operands
		// stride: "author | author/name" may yield nested subtrees.
		{"* | */*", props{postureCrawling, sweepConsuming},
			"two striding operands compose to crawling, not striding"},
		{"author | editor", props{postureCrawling, sweepConsuming},
			"the note's own example: still crawling, not striding"},
		{"descendant::a | child::b", props{postureCrawling, sweepConsuming},
			"crawling with striding is crawling"},

		// Rule 5: "otherwise, roaming and free-ranging". The spec's example
		// is the heterogeneous "child::div | parent::div".
		{"child::div | parent::div", roamingFreeRanging,
			"a striding operand and a climbing one do not compose"},

		// "intersect" and "except" take the same rules as "union"; §19.8.8.4
		// names all three in its title and gives them one cascade.
		{"child::a intersect child::b", props{postureCrawling, sweepConsuming},
			"intersect follows the union cascade"},
		{"child::a except child::b", props{postureCrawling, sweepConsuming},
			"except follows the union cascade"},
		{"child::div except parent::div", roamingFreeRanging,
			"and its roaming case too"},
	}
	for _, c := range cases {
		got, known := analyze(t, c.expr)
		if !known {
			t.Errorf("%s: %q was not modelled, but §19.8.8.4 gives it a rule",
				c.comment, c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %q is %v and %v, want %v and %v",
				c.comment, c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

// TestAnalyzeMapConstructor checks §19.8.8.16, which defers to §19.8.4.23 and
// §19.8.4.24: the key expression absorbs and the value expression navigates.
func TestAnalyzeMapConstructor(t *testing.T) {
	cases := []struct {
		expr    string
		want    props
		comment string
	}{
		// A map of constants has no operand that reads the stream.
		{"map{'red': false(), 'green': true()}", groundedMotionless,
			"the spec's own example map is grounded and motionless"},

		// §19.8.4.24 gives the value expression usage navigation, and
		// §19.8.1 makes navigation from any streamed posture free-ranging.
		// This is the rule that stops a streamed node being stored in a map,
		// and it is what sx-MapExpr-901 asserts.
		{"map{'authors': //AUTHOR}", roamingFreeRanging,
			"a streamed node may not be stored as a map value"},
		{"map{'authors': child::AUTHOR}", roamingFreeRanging,
			"navigation is free-ranging even from a striding operand"},

		// The supported way to write it: ground the value first. §19.8.4.23's
		// note says the call on copy-of "is necessary to ensure that the
		// content of the map entry is grounded".
		{"map{'authors': copy-of(child::AUTHOR)}", props{postureGrounded, sweepConsuming},
			"a grounded value is allowed, and costs one pass"},

		// §19.8.4.23: with several entries, "grounded and the widest sweep of
		// the xsl:map-entry children" -- each entry may read the stream in
		// the same pass, as an implicit fork.
		{"map{'a': copy-of(child::A), 'b': copy-of(child::B)}",
			props{postureGrounded, sweepConsuming},
			"two grounded entries still make one pass"},

		// The key absorbs (§19.8.4.24), so a consuming key costs a pass but
		// does not roam.
		{"map{string(child::A): 'v'}", props{postureGrounded, sweepConsuming},
			"an absorbing key is consuming, not roaming"},

		// "If any of these xsl:map-entry children is roaming or free-ranging,
		// then roaming and free-ranging."
		{"map{'a': 'v', 'b': //B}", roamingFreeRanging,
			"one roaming entry makes the whole map roaming"},
	}
	for _, c := range cases {
		got, known := analyze31(t, c.expr)
		if !known {
			t.Errorf("%s: %q was not modelled, but §19.8.8.16 gives it a rule",
				c.comment, c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %q is %v and %v, want %v and %v",
				c.comment, c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

// TestAnalyzeArrayConstructor checks that an array constructor is assessed
// under the general streamability rules with navigation members.
//
// XPath 3.1's array constructors postdate the §19.8.8 proforma table, which
// enumerates XPath 3.0 productions, so the general rules are what remain. They
// give an array the same answer they give a map, and for the same reason: an
// array is a function item that may outlive the stream position at which it
// was built.
func TestAnalyzeArrayConstructor(t *testing.T) {
	cases := []struct {
		expr    string
		want    props
		comment string
	}{
		{"[1, 2]", groundedMotionless,
			"an array of constants reads nothing"},
		{"array{1, 2}", groundedMotionless,
			"and the curly spelling likewise"},

		// A member that would deliver a streamed node navigates, and
		// navigation from a streamed posture is free-ranging (§19.8.1). This
		// is what sx-square-array-201 turns on.
		{"[//ITEM]", roamingFreeRanging,
			"a streamed node may not be stored as an array member"},
		{"[child::ITEM]", roamingFreeRanging,
			"navigation is free-ranging even from a striding member"},
		{"array{child::ITEM}", roamingFreeRanging,
			"the curly spelling stores nodes no more safely"},

		// Grounding the member first is allowed, and the array is grounded.
		{"[copy-of(child::ITEM)]", props{postureGrounded, sweepConsuming},
			"a grounded member is allowed, and costs one pass"},
		// Two members that each read the stream are still one pass, exactly
		// as two xsl:map-entry children are under §19.8.4.23. The array
		// constructor is an implicit fork over its members, so §19.8.1's
		// limit of one potentially-consuming operand does not apply to them.
		{"[string(child::A), string(child::B)]", props{postureGrounded, sweepConsuming},
			"two grounded members are one pass, not two"},

		// The case from sx-square-array-B.xsl that this turns on: both
		// members are attribute steps, which read nothing beyond the start
		// tag, and the array is streamable.
		//
		// Motionless, not consuming: §19.8.1 downgrades an absorption to
		// inspection when the operand's type cannot hold a node with
		// children, and the context item of string() here is the attribute
		// the step before it delivered. So neither member moves the stream
		// at all -- a stronger statement than "one pass", and the reason
		// this array would be streamable even outside the fork rule above.
		{"[@DESC/string(), @CODE/string()]", props{postureGrounded, sweepMotionless},
			"two attribute members do not make an array unstreamable"},
	}
	for _, c := range cases {
		got, known := analyze31(t, c.expr)
		if !known {
			t.Errorf("%s: %q was not modelled", c.comment, c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %q is %v and %v, want %v and %v",
				c.comment, c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

// TestAnalyzeLastAndPosition checks §19.8.9.14 and §19.8.9.16.
func TestAnalyzeLastAndPosition(t *testing.T) {
	// §19.8.9.16: "the position function follows the general streamability
	// rules. Since it has no operands, this means it is grounded and
	// motionless."
	if got, known := analyze(t, "position()"); !known || got != groundedMotionless {
		t.Errorf("position() is %v and %v (known=%v), want grounded and motionless",
			got.posture, got.sweep, known)
	}

	// §19.8.9.14: "if the context posture for a call on the last function is
	// striding, crawling, or roaming, then the posture of the function is
	// roaming, and the sweep is free-ranging. In all other cases the function
	// is grounded and motionless."
	postures := []struct {
		ctx  posture
		want props
		why  string
	}{
		{postureStriding, roamingFreeRanging, "striding"},
		{postureCrawling, roamingFreeRanging, "crawling"},
		{postureRoaming, roamingFreeRanging, "roaming"},
		{postureGrounded, groundedMotionless, "grounded"},
		{postureClimbing, groundedMotionless, "climbing"},
	}
	expr, err := xpath.Parse("last()", nil)
	if err != nil {
		t.Fatalf("parsing last(): %v", err)
	}
	for _, c := range postures {
		got, known := analyzeExpr(expr, c.ctx)
		if !known {
			t.Errorf("last() was not modelled in a %s context posture", c.why)
			continue
		}
		if got != c.want {
			t.Errorf("last() in a %s context posture is %v and %v, want %v and %v",
				c.why, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}

	// The note to §19.8.9.14 gives the reason the climbing case is exempt:
	// "the latter condition makes expressions like
	// ancestor::*[@xml:space][last()] streamable."
	if got, known := analyze(t, "ancestor::*[@xml:space][last()]"); !known || !got.streamable() {
		t.Errorf("ancestor::*[@xml:space][last()] is %v and %v (known=%v), "+
			"but the note to §19.8.9.14 says it is streamable",
			got.posture, got.sweep, known)
	}

	// A call on last() in a striding predicate is what makes the six
	// sx-GeneralComp-901 stylesheets non-streamable.
	if got, known := analyze(t, "(child::ITEM[position() ne last()]/PRICE) = 432"); !known || got.streamable() {
		t.Errorf("a predicate using last() from a striding posture is %v and %v (known=%v), "+
			"want roaming and free-ranging", got.posture, got.sweep, known)
	}
}

// TestAnalyzeTreatExpr checks the §19.8.8 proforma "T treat as TYPE" and the
// §19.8.8.5 exception for document-node(element(X)).
func TestAnalyzeTreatExpr(t *testing.T) {
	// The proforma gives a single transmission operand, which is what makes
	// the worked example in §19.8.8.7 motionless: there, "root(.) treat as
	// document-node()" keeps the striding posture and motionless sweep of
	// its operand.
	if got, known := analyze(t, ". treat as document-node()"); !known ||
		got != (props{postureStriding, sweepMotionless}) {
		t.Errorf(". treat as document-node() is %v and %v (known=%v), "+
			"but §19.8.8.7's worked example makes it striding and motionless",
			got.posture, got.sweep, known)
	}
	if got, known := analyze(t, "child::a treat as element(a)"); !known ||
		got != (props{postureStriding, sweepConsuming}) {
		t.Errorf("child::a treat as element(a) is %v and %v (known=%v), "+
			"want the posture and sweep of the transmitted operand",
			got.posture, got.sweep, known)
	}

	// §19.8.8.5's note: an item type of the form document-node(element(X))
	// "matches a document node only if it has exactly one element node child,
	// and this cannot be determined without consuming the document". The test
	// costs a read whichever operator asks for it, so the same type makes
	// "instance of" absorb and "treat as" both transmit and absorb.
	if got, known := analyze(t, ". treat as document-node(element(account))"); !known ||
		got.streamable() {
		t.Errorf(". treat as document-node(element(account)) is %v and %v (known=%v), "+
			"but the type cannot be tested without consuming the document",
			got.posture, got.sweep, known)
	}
	if got, known := analyze(t, ". instance of document-node(element(account))"); !known ||
		got.sweep == sweepMotionless {
		t.Errorf(". instance of document-node(element(account)) is %v and %v (known=%v), "+
			"but §19.8.8.5 makes that item type absorption",
			got.posture, got.sweep, known)
	}

	// The exception is confined to the constrained form. A bare
	// document-node() matches on sight, so it inspects rather than absorbs,
	// and an ordinary element test is unaffected.
	if got, known := analyze(t, ". instance of document-node()"); !known ||
		got.sweep != sweepMotionless {
		t.Errorf(". instance of document-node() is %v and %v (known=%v), "+
			"want motionless: an unconstrained document test reads nothing",
			got.posture, got.sweep, known)
	}
	if got, known := analyze(t, ". instance of element(account)"); !known ||
		got.sweep != sweepMotionless {
		t.Errorf(". instance of element(account) is %v and %v (known=%v), "+
			"want motionless: an element test is decided at the start tag",
			got.posture, got.sweep, known)
	}
}

// TestAnalyzeCurrentFunction checks §19.8.9.3: "The sweep of the function is
// motionless; the posture is the context posture for evaluation of the
// outermost containing XPath expression (that is, the context posture that
// would obtain if the entire XPath expression were replaced with '.')."
//
// The point of the rule is the word OUTERMOST. Descending into a predicate
// changes the context posture that "." reports, and current() must not follow
// it. sf-current-902's "AUTHOR[..[@CAT = current()/../@CAT]]" is the shape
// that distinguishes the two: inside the inner predicate "." is the parent,
// while current() is still the AUTHOR the pattern matched.
func TestAnalyzeCurrentFunction(t *testing.T) {
	cases := []struct {
		expr    string
		ctx     posture
		want    props
		comment string
	}{
		// The bare call takes the outermost context posture and is
		// motionless, exactly as "." is.
		{"current()", postureStriding,
			props{postureStriding, sweepMotionless},
			"striding context"},
		{"current()", postureGrounded,
			props{postureGrounded, sweepMotionless},
			"grounded context"},
		// Absorbing a striding current() is consuming, by the §19.8.1
		// table -- the same charge "string(.)" carries. This is what makes
		// sf-current-901's "*[string(.) = string(current())]" unstreamable.
		{"string(current())", postureStriding,
			props{postureGrounded, sweepConsuming},
			"absorption of a striding current() is consuming"},
		// Inspecting it costs nothing: sf-current-100's
		// "ITEM[namespace-uri(current()) = '']" is a pattern the catalog
		// expects to run.
		{"namespace-uri(current())", postureStriding,
			groundedMotionless,
			"inspection of current() is motionless"},
		// Navigating from it. "current()/@UNIT" stays striding and
		// motionless -- the attribute axis reads nothing further --
		// which is sf-current-100's DIMENSIONS rule.
		{"current()/@UNIT", postureStriding,
			props{postureStriding, sweepMotionless},
			"attribute step off current()"},
		// A grounded outermost context makes even absorption free: the
		// §19.8.1 rule "If P is grounded, then S' is S".
		{"string(current())", postureGrounded,
			groundedMotionless,
			"absorbing a grounded current() costs nothing"},
	}
	for _, c := range cases {
		expr, err := xpath.ParseVersion(c.expr, nil, xpath.XPath31)
		if err != nil {
			t.Fatalf("parsing %q: %v", c.expr, err)
		}
		got, known := analyzeExpr(expr, c.ctx)
		if !known {
			t.Errorf("%s (%s): the analysis abandoned it, but §19.8.9.3 "+
				"gives fn:current a rule", c.expr, c.comment)
			continue
		}
		if got != c.want {
			t.Errorf("%s in a %v context (%s) = %v and %v, want %v and %v",
				c.expr, c.ctx, c.comment,
				got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

// TestCurrentPostureIsOutermost checks the one thing a bare-context test
// cannot: that descending into a predicate leaves current() alone. At the
// outermost level ctxPosture and currentPosture are equal, so every case
// above passes just as well if current() reads the wrong one; only a nested
// context tells them apart.
//
// "@x[string(current()) = 'y']" is that shape. Inside the predicate the
// context item is the attribute, which has no children, so §19.8.1 downgrades
// the absorption in "string(.)" to inspection and the whole expression is
// striding and motionless. current() is not the attribute -- it is the
// outermost context item, an element that may have children -- so absorbing
// it stands, and a consuming predicate makes the step roaming and
// free-ranging. The two spellings must therefore disagree, and a current()
// that followed the inner context would make them agree.
func TestCurrentPostureIsOutermost(t *testing.T) {
	for _, c := range []struct {
		expr    string
		want    props
		comment string
	}{
		{"@x[string(.) = 'y']", props{postureStriding, sweepMotionless},
			"absorbing the attribute context item is downgraded to inspection"},
		{"@x[string(current()) = 'y']", roamingFreeRanging,
			"current() is the outermost item, not the attribute, so absorbing it consumes"},
		// The posture half of the same asymmetry. A filter on a variable
		// gives the predicate a GROUNDED context posture, while the
		// outermost posture is still striding. §19.8.1 charges nothing for
		// absorbing a grounded operand, so "string(.)" here is motionless;
		// current() keeps the striding outermost posture, so absorbing it
		// consumes. This pair is what pins currentPosture apart from
		// ctxPosture -- at the outermost level the two are equal, and every
		// case that does not nest passes either way.
		{"$v[string(.) = 'y']", groundedMotionless,
			"the predicate's own context posture is grounded"},
		{"$v[string(current()) = 'y']", roamingFreeRanging,
			"current() keeps the striding OUTERMOST posture, so absorbing it consumes"},
	} {
		expr, err := xpath.ParseVersion(c.expr, nil, xpath.XPath31)
		if err != nil {
			t.Fatalf("parsing %q: %v", c.expr, err)
		}
		got, known := analyzeExpr(expr, postureStriding)
		if !known {
			t.Errorf("%s: the analysis abandoned a modelled expression", c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("%s (%s) = %v and %v, want %v and %v",
				c.expr, c.comment, got.posture, got.sweep,
				c.want.posture, c.want.sweep)
		}
	}

	// The pattern half of §19.8.9.3, and the reason currentAllowsChildren
	// exists. "part-name/text()[$selected-parts = current()]" is the
	// accumulator rule of stream-200..203, which the catalog expects to RUN:
	// current() there is the text node the pattern matched, which has no
	// children, so the absorption is downgraded and the predicate is
	// motionless. stream-204 is the same rule on an element step, which the
	// catalog expects to be REFUSED with XTSE3430. Both directions are
	// pinned here, because loosening either one costs the other.
	for _, c := range []struct {
		pattern string
		want    bool // free-ranging?
	}{
		{"part-name/text()[$selected-parts = current()]", false},
		{"part-name[$selected-parts = current()]", true},
	} {
		pat, err := xpath.ParseVersion(c.pattern, nil, xpath.XPath31)
		if err != nil {
			t.Fatalf("parsing %q: %v", c.pattern, err)
		}
		free, known := patternExprFreeRanging(pat, nil)
		if !known {
			t.Errorf("%s: the pattern classification abandoned it", c.pattern)
			continue
		}
		if free != c.want {
			t.Errorf("%s: free-ranging = %v, want %v", c.pattern, free, c.want)
		}
	}
}
