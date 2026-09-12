package xslt

import "testing"

// Tests for the streamability lattice of XSLT 3.0 §19, exercised on its own.
//
// These tests deliberately construct operands by hand rather than compiling a
// stylesheet. The general rules of §19.8.1 are shared by nearly every
// construct in the language, so a mistake in them is a mistake everywhere at
// once — and a silent one, because a wrong posture is still a posture. Driving
// the rules directly is the only way to see them fail on their own terms.
//
// Wherever the spec gives a worked example, the example is used verbatim and
// cited, so that a future reader can check the test against the spec rather
// than against the implementation it is testing.

// --- sweep ordering ---------------------------------------------------------

func TestSweepOrderingIsMotionlessConsumingFreeRanging(t *testing.T) {
	// §19.6: "a free-ranging expression has wider sweep than a consuming
	// expression, which has wider sweep than a motionless expression."
	// (§19.7 in the Last Call draft; the Recommendation numbers it 19.6.)
	if !(sweepMotionless < sweepConsuming && sweepConsuming < sweepFreeRanging) {
		t.Fatal("sweep constants are not in width order")
	}
	cases := []struct{ a, b, want sweep }{
		{sweepMotionless, sweepMotionless, sweepMotionless},
		{sweepMotionless, sweepConsuming, sweepConsuming},
		{sweepConsuming, sweepMotionless, sweepConsuming},
		{sweepConsuming, sweepFreeRanging, sweepFreeRanging},
		{sweepFreeRanging, sweepMotionless, sweepFreeRanging},
	}
	for _, c := range cases {
		if got := wider(c.a, c.b); got != c.want {
			t.Errorf("wider(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// --- adjusted usage ---------------------------------------------------------

func TestAbsorptionDowngradesToInspectionWithoutChildren(t *testing.T) {
	// §19.8.1: absorption becomes inspection when the operand's type
	// permits no node with children, "because the entire subtree of nodes
	// such as text nodes is available without reading further data from the
	// input stream."
	withChildren := operand{usage: usageAbsorption, allowsChildren: true}
	if got := withChildren.adjustedUsage(); got != usageAbsorption {
		t.Errorf("absorption of an element operand = %v, want absorption", got)
	}
	withoutChildren := operand{usage: usageAbsorption, allowsChildren: false}
	if got := withoutChildren.adjustedUsage(); got != usageInspection {
		t.Errorf("absorption of an attribute operand = %v, want inspection", got)
	}
	// The downgrade applies to absorption only; no other usage is touched.
	for _, u := range []usage{usageInspection, usageTransmission, usageNavigation} {
		o := operand{usage: u, allowsChildren: false}
		if got := o.adjustedUsage(); got != u {
			t.Errorf("adjustedUsage(%v) = %v, want it unchanged", u, got)
		}
	}
}

// --- adjusted sweep ---------------------------------------------------------

func TestAdjustedSweepTable(t *testing.T) {
	// The table in §19.8.1, read row by row. Rows are keyed by posture and
	// adjusted usage; S is the operand's own sweep.
	//
	//   Posture   Absorption    Inspection  Transmission  Navigation
	//   Climbing  free-ranging  S           S             free-ranging
	//   Striding  consuming     S           S             free-ranging
	//   Crawling  consuming     S           S             free-ranging
	for _, own := range []sweep{sweepMotionless, sweepConsuming} {
		cases := []struct {
			p    posture
			u    usage
			want sweep
		}{
			{postureClimbing, usageAbsorption, sweepFreeRanging},
			{postureClimbing, usageInspection, own},
			{postureClimbing, usageTransmission, own},
			{postureClimbing, usageNavigation, sweepFreeRanging},

			{postureStriding, usageAbsorption, sweepConsuming},
			{postureStriding, usageInspection, own},
			{postureStriding, usageTransmission, own},
			{postureStriding, usageNavigation, sweepFreeRanging},

			{postureCrawling, usageAbsorption, sweepConsuming},
			{postureCrawling, usageInspection, own},
			{postureCrawling, usageTransmission, own},
			{postureCrawling, usageNavigation, sweepFreeRanging},
		}
		for _, c := range cases {
			o := operand{
				props:          props{c.p, own},
				usage:          c.u,
				allowsChildren: true,
			}
			if got := o.adjustedSweep(); got != c.want {
				t.Errorf("adjustedSweep(posture=%v, usage=%v, S=%v) = %v, want %v",
					c.p, c.u, own, got, c.want)
			}
		}
	}
}

func TestAdjustedSweepShortCircuits(t *testing.T) {
	// §19.8.1 takes three cases in order, and the order is what these
	// assertions pin down.

	// A roaming operand is free-ranging whatever its usage — even
	// inspection, which would otherwise preserve a motionless sweep.
	o := operand{
		props:          props{postureRoaming, sweepMotionless},
		usage:          usageInspection,
		allowsChildren: true,
	}
	if got := o.adjustedSweep(); got != sweepFreeRanging {
		t.Errorf("roaming operand adjusted sweep = %v, want free-ranging", got)
	}

	// A grounded operand keeps its own sweep whatever its usage — even
	// navigation, which is free-ranging from every streamed posture. This
	// is what lets a stylesheet navigate freely around a document it built
	// itself while a streamed one is open.
	for _, u := range []usage{usageAbsorption, usageInspection, usageTransmission, usageNavigation} {
		g := operand{
			props:          props{postureGrounded, sweepMotionless},
			usage:          u,
			allowsChildren: true,
		}
		if got := g.adjustedSweep(); got != sweepMotionless {
			t.Errorf("grounded operand with usage %v = %v, want motionless", u, got)
		}
	}
}

// --- potentially consuming --------------------------------------------------

func TestPotentiallyConsuming(t *testing.T) {
	// §19.8.1 defines potentially consuming as either an adjusted sweep of
	// consuming, or a transmission usage on an operand that is not
	// grounded. The second half is what stops two operands each handing a
	// streamed node upward, as in "(.., *)", even though neither of them
	// moves the stream.
	striding := props{postureStriding, sweepMotionless}

	transmitting := operand{props: striding, usage: usageTransmission, allowsChildren: true}
	if !transmitting.potentiallyConsuming() {
		t.Error("a motionless operand that transmits a streamed node should be potentially consuming")
	}
	if transmitting.adjustedSweep() != sweepMotionless {
		t.Error("that operand should still be motionless")
	}

	inspecting := operand{props: striding, usage: usageInspection, allowsChildren: true}
	if inspecting.potentiallyConsuming() {
		t.Error("a motionless operand that only inspects should not be potentially consuming")
	}

	grounded := operand{
		props:          props{postureGrounded, sweepMotionless},
		usage:          usageTransmission,
		allowsChildren: true,
	}
	if grounded.potentiallyConsuming() {
		t.Error("transmitting a grounded node should not be potentially consuming")
	}
}

// --- combined posture of a choice group -------------------------------------

func TestCombinedPosture(t *testing.T) {
	// §19.3, taken clause by clause.
	cases := []struct {
		name string
		in   []posture
		want posture
	}{
		{"empty group is grounded", nil, postureGrounded},
		{"all grounded", []posture{postureGrounded, postureGrounded}, postureGrounded},
		{"any roaming wins", []posture{postureGrounded, postureRoaming, postureStriding}, postureRoaming},
		{"climbing among grounded", []posture{postureGrounded, postureClimbing}, postureClimbing},
		{"striding among grounded", []posture{postureStriding, postureGrounded}, postureStriding},
		{"crawling among grounded", []posture{postureCrawling, postureGrounded}, postureCrawling},
		{"crawling with striding is crawling", []posture{postureCrawling, postureStriding}, postureCrawling},
		{"crawling with striding and grounded", []posture{postureCrawling, postureStriding, postureGrounded}, postureCrawling},
		// The spec names this one explicitly as the example of a group
		// that does not combine.
		{"climbing with crawling is roaming", []posture{postureClimbing, postureCrawling}, postureRoaming},
		{"climbing with striding is roaming", []posture{postureClimbing, postureStriding}, postureRoaming},
	}
	for _, c := range cases {
		if got := combinedPosture(c.in); got != c.want {
			t.Errorf("%s: combinedPosture(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// --- the general rules ------------------------------------------------------

func TestCombineNoOperands(t *testing.T) {
	// §19.8.1: "If C has no operands, then grounded and motionless."
	if got := combine(nil, false); got != groundedMotionless {
		t.Errorf("combine(no operands) = %v/%v, want grounded/motionless", got.posture, got.sweep)
	}
}

// TestCombineSpecExamples runs the worked examples of §19.8.2. Each case names
// the expression the spec uses and the operands it has, given the stated
// assumption that the context item is an element with a striding posture.
func TestCombineSpecExamples(t *testing.T) {
	// Building blocks, as the spec derives them.
	//
	// A literal such as "2" is grounded and motionless.
	literal := func(u usage) operand {
		return operand{props: groundedMotionless, usage: u, allowsChildren: false}
	}
	// A single-step path such as "price" is striding and consuming: the
	// child axis from a striding context (§19.8.8.8).
	childStep := func(u usage) operand {
		return operand{
			props:          props{postureStriding, sweepConsuming},
			usage:          u,
			allowsChildren: true,
		}
	}
	// An attribute step such as "@discount" is striding and motionless, and
	// an attribute has no children.
	attrStep := func(u usage) operand {
		return operand{
			props:          props{postureStriding, sweepMotionless},
			usage:          u,
			allowsChildren: false,
		}
	}
	// "a/b/c" is striding and consuming; "descendant::c" is crawling and
	// consuming.
	striding := func(u usage) operand {
		return operand{
			props:          props{postureStriding, sweepConsuming},
			usage:          u,
			allowsChildren: true,
		}
	}
	crawling := func(u usage) operand {
		return operand{
			props:          props{postureCrawling, sweepConsuming},
			usage:          u,
			allowsChildren: true,
		}
	}

	cases := []struct {
		expr      string
		operands  []operand
		singleton bool
		want      props
	}{
		{
			// "2 + 2 is grounded and motionless, because both the
			// operands are grounded and motionless."
			expr:     "2 + 2",
			operands: []operand{literal(usageAbsorption), literal(usageAbsorption)},
			want:     groundedMotionless,
		},
		{
			// "price * 2 is grounded and consuming, because one of the
			// operands is consuming and the relevant operand usage is
			// absorption."
			expr:     "price * 2",
			operands: []operand{childStep(usageAbsorption), literal(usageAbsorption)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "price - discount is roaming and free-ranging, because
			// both the operands are consuming (and they are not members
			// of a parallel operand group)."
			expr:     "price - discount",
			operands: []operand{childStep(usageAbsorption), childStep(usageAbsorption)},
			want:     roamingFreeRanging,
		},
		{
			// "price * @discount is grounded and consuming. The
			// left-hand operand is consuming ... while the right-hand
			// operand is motionless ... and its item type is
			// attribute() which changes the effective usage to
			// inspection."
			expr:     "price * @discount",
			operands: []operand{childStep(usageAbsorption), attrStep(usageAbsorption)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "count(a/b/c) is grounded and consuming, because the
			// operand ... is striding and consuming and the operand
			// usage is inspection."
			expr:     "count(a/b/c)",
			operands: []operand{striding(usageInspection)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "sum(a/b/c) is grounded and consuming ... the operand
			// usage is absorption."
			expr:     "sum(a/b/c)",
			operands: []operand{striding(usageAbsorption)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "count(descendant::c) is grounded and consuming, because
			// the operand ... is crawling and consuming and the operand
			// usage is inspection."
			expr:     "count(descendant::c)",
			operands: []operand{crawling(usageInspection)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "tail(descendant::c) is crawling and consuming. The
			// operand is crawling, the operand usage is transmission,
			// so the posture and sweep of the result are the same as
			// the posture and sweep of the consuming operand."
			expr:     "tail(descendant::c)",
			operands: []operand{crawling(usageTransmission)},
			want:     props{postureCrawling, sweepConsuming},
		},
		{
			// "zero-or-one(descendant::c) is striding and consuming.
			// Although the operand is crawling, the operand usage is
			// transmission and the cardinality of the expression is
			// zero or one, so the posture of the result is striding."
			expr:      "zero-or-one(descendant::c)",
			operands:  []operand{crawling(usageTransmission)},
			singleton: true,
			want:      props{postureStriding, sweepConsuming},
		},
		{
			// "sum(descendant::c) is grounded and consuming, because
			// the operand ... is crawling and consuming and the operand
			// usage is absorption."
			expr:     "sum(descendant::c)",
			operands: []operand{crawling(usageAbsorption)},
			want:     props{postureGrounded, sweepConsuming},
		},
		{
			// "For example, the expression (@a, @b) is motionless and
			// striding." Two operands are potentially consuming — each
			// transmits a streamed node — but neither moves the stream
			// and both are striding.
			expr: "(@a, @b)",
			operands: []operand{
				attrStep(usageTransmission),
				attrStep(usageTransmission),
			},
			want: props{postureStriding, sweepMotionless},
		},
		{
			// "the expression count((.., *)) is not guaranteed
			// streamable" — the sequence delivers streamed nodes from
			// two operands with different postures, climbing and
			// striding, so the sequence itself is roaming.
			expr: "(.., *)",
			operands: []operand{
				{props: props{postureClimbing, sweepMotionless}, usage: usageTransmission, allowsChildren: true},
				{props: props{postureStriding, sweepConsuming}, usage: usageTransmission, allowsChildren: true},
			},
			want: roamingFreeRanging,
		},
	}

	for _, c := range cases {
		got := combine(c.operands, c.singleton)
		if got != c.want {
			t.Errorf("%s: got %v and %v, want %v and %v",
				c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

func TestCombineChoiceGroupAllowsTwoConsumingOperands(t *testing.T) {
	// §19.8.1 and the note that follows it: "if (X) then @name else name
	// is guaranteed streamable." The two arms form a choice operand group,
	// so both may consume; the result takes the combined posture and the
	// widest sweep.
	//
	// "@name" is striding and motionless; "name" is striding and consuming.
	arm := func(s sweep) operand {
		return operand{
			props:          props{postureStriding, s},
			usage:          usageTransmission,
			allowsChildren: true,
			choiceGroup:    true,
		}
	}
	got := combine([]operand{
		{props: groundedMotionless, usage: usageInspection}, // the condition X
		arm(sweepMotionless),
		arm(sweepConsuming),
	}, false)
	want := props{postureStriding, sweepConsuming}
	if got != want {
		t.Errorf("if (X) then @name else name = %v/%v, want %v/%v",
			got.posture, got.sweep, want.posture, want.sweep)
	}
	if !got.streamable() {
		t.Error("that expression is guaranteed streamable and should not be rejected")
	}

	// The same two arms outside a choice group are two independent
	// consuming operands, and so not streamable. This is the arm of the
	// test that fails if choiceGroup is ignored.
	loose := func(s sweep) operand {
		o := arm(s)
		o.choiceGroup = false
		return o
	}
	if got := combine([]operand{loose(sweepMotionless), loose(sweepConsuming)}, false); got.streamable() {
		t.Errorf("two consuming operands outside a choice group should be roaming, got %v/%v",
			got.posture, got.sweep)
	}
}

func TestCombineChoiceGroupWithIncompatiblePosturesIsRoaming(t *testing.T) {
	// A choice group only rescues arms whose postures combine. Climbing
	// with crawling does not (§19.3), so "if (X) then ancestor::a else
	// descendant::d" is roaming even though only one arm is evaluated.
	group := func(p posture) operand {
		return operand{
			props:          props{p, sweepConsuming},
			usage:          usageTransmission,
			allowsChildren: true,
			choiceGroup:    true,
		}
	}
	got := combine([]operand{group(postureClimbing), group(postureCrawling)}, false)
	if got.streamable() {
		t.Errorf("a choice group of climbing and crawling arms should be roaming, got %v/%v",
			got.posture, got.sweep)
	}
}

func TestCombineHigherOrderConsumingOperandIsRoaming(t *testing.T) {
	// §19.8.1: a single consuming operand that is a higher-order operand of
	// its parent makes the parent roaming — the body of a for expression is
	// evaluated once per item, and the stream cannot be re-read.
	body := operand{
		props:          props{postureStriding, sweepConsuming},
		usage:          usageTransmission,
		allowsChildren: true,
		higherOrder:    true,
	}
	if got := combine([]operand{body}, false); got != roamingFreeRanging {
		t.Errorf("a consuming higher-order operand should be roaming, got %v/%v",
			got.posture, got.sweep)
	}

	// The same operand, evaluated once, is fine. Without this arm the test
	// above would pass even if higherOrder were treated as always roaming.
	body.higherOrder = false
	if got := combine([]operand{body}, false); !got.streamable() {
		t.Errorf("the same operand evaluated once should be streamable, got %v/%v",
			got.posture, got.sweep)
	}

	// A higher-order operand that is motionless is not consuming, so it
	// does not trigger the rule: "for $i in 1 to 5 return $i" streams.
	motionless := operand{
		props:          groundedMotionless,
		usage:          usageTransmission,
		allowsChildren: false,
		higherOrder:    true,
	}
	if got := combine([]operand{motionless}, false); !got.streamable() {
		t.Errorf("a motionless higher-order operand should be streamable, got %v/%v",
			got.posture, got.sweep)
	}
}

func TestCombineFreeRangingOperandDominates(t *testing.T) {
	// §19.8.1: a free-ranging operand makes the construct roaming and
	// free-ranging "regardless of any other operands" — including when
	// every other operand is grounded and motionless, and including when
	// the free-ranging operand is one arm of a choice group.
	bad := operand{
		props:          props{postureRoaming, sweepFreeRanging},
		usage:          usageInspection,
		allowsChildren: true,
	}
	good := operand{props: groundedMotionless, usage: usageAbsorption}
	if got := combine([]operand{good, bad, good}, false); got != roamingFreeRanging {
		t.Errorf("a free-ranging operand should dominate, got %v/%v", got.posture, got.sweep)
	}

	bad.choiceGroup = true
	arm := operand{
		props:       props{postureStriding, sweepConsuming},
		usage:       usageTransmission,
		choiceGroup: true, allowsChildren: true,
	}
	if got := combine([]operand{bad, arm}, false); got != roamingFreeRanging {
		t.Errorf("a free-ranging choice arm should still dominate, got %v/%v", got.posture, got.sweep)
	}
}

func TestCombineSingletonBuiltinOnlyNarrowsCrawling(t *testing.T) {
	// The singleton rule of §19.8.1 applies only where the operand is
	// crawling. head(a/b/c), whose operand is striding, stays striding
	// rather than being changed to anything else, and head() of a grounded
	// operand stays grounded.
	striding := operand{
		props:          props{postureStriding, sweepConsuming},
		usage:          usageTransmission,
		allowsChildren: true,
	}
	if got := combine([]operand{striding}, true); got != (props{postureStriding, sweepConsuming}) {
		t.Errorf("head(a/b/c) = %v/%v, want striding/consuming", got.posture, got.sweep)
	}

	// And it must not fire for a construct that is not one of the three
	// singleton built-ins: tail(descendant::c) stays crawling.
	crawling := operand{
		props:          props{postureCrawling, sweepConsuming},
		usage:          usageTransmission,
		allowsChildren: true,
	}
	if got := combine([]operand{crawling}, false); got.posture != postureCrawling {
		t.Errorf("tail(descendant::c) posture = %v, want crawling", got.posture)
	}
}

func TestCombineAllMotionlessRequiresSamePosture(t *testing.T) {
	// The "(@a, @b)" rule requires that all the potentially consuming
	// operands share one posture. Two motionless transmitting operands with
	// different postures — "(@a, ..)" — are roaming.
	a := operand{props: props{postureStriding, sweepMotionless}, usage: usageTransmission, allowsChildren: false}
	b := operand{props: props{postureClimbing, sweepMotionless}, usage: usageTransmission, allowsChildren: true}
	if got := combine([]operand{a, b}, false); got.streamable() {
		t.Errorf("(@a, ..) should be roaming, got %v/%v", got.posture, got.sweep)
	}
}

// --- the axis step table ----------------------------------------------------

func TestPostureOfAxisStepTable(t *testing.T) {
	// The final table of §19.8.8.8, row by row. The "selects elements"
	// column is only consulted on the descendant axes and on self, so the
	// other rows are asserted for both values of it.
	type row struct {
		ctx      posture
		axis     axisKind
		elements bool
		want     props
	}
	rows := []row{
		// Grounded context: any axis, grounded and motionless.
		{postureGrounded, axisChild, true, groundedMotionless},
		{postureGrounded, axisDescendant, true, groundedMotionless},
		{postureGrounded, axisFollowing, true, groundedMotionless},
		{postureGrounded, axisParent, false, groundedMotionless},

		// Climbing context.
		{postureClimbing, axisSelf, true, props{postureClimbing, sweepMotionless}},
		{postureClimbing, axisParent, true, props{postureClimbing, sweepMotionless}},
		{postureClimbing, axisAncestorOrSelf, true, props{postureClimbing, sweepMotionless}},
		{postureClimbing, axisAncestor, true, props{postureClimbing, sweepMotionless}},
		{postureClimbing, axisAttribute, false, props{postureStriding, sweepMotionless}},
		{postureClimbing, axisNamespace, false, props{postureStriding, sweepMotionless}},
		// A climbing context may not descend.
		{postureClimbing, axisChild, true, roamingFreeRanging},
		{postureClimbing, axisDescendant, true, roamingFreeRanging},

		// Striding context.
		{postureStriding, axisParent, true, props{postureClimbing, sweepMotionless}},
		{postureStriding, axisAncestorOrSelf, true, props{postureClimbing, sweepMotionless}},
		{postureStriding, axisAncestor, true, props{postureClimbing, sweepMotionless}},
		{postureStriding, axisSelf, true, props{postureStriding, sweepMotionless}},
		{postureStriding, axisAttribute, false, props{postureStriding, sweepMotionless}},
		{postureStriding, axisNamespace, false, props{postureStriding, sweepMotionless}},
		{postureStriding, axisChild, true, props{postureStriding, sweepConsuming}},
		{postureStriding, axisDescendant, true, props{postureCrawling, sweepConsuming}},
		{postureStriding, axisDescendantOrSelf, true, props{postureCrawling, sweepConsuming}},
		// A descendant step that cannot select an element cannot nest.
		{postureStriding, axisDescendant, false, props{postureStriding, sweepConsuming}},
		{postureStriding, axisDescendantOrSelf, false, props{postureStriding, sweepConsuming}},

		// Crawling context.
		{postureCrawling, axisParent, true, props{postureClimbing, sweepMotionless}},
		{postureCrawling, axisAncestorOrSelf, true, props{postureClimbing, sweepMotionless}},
		{postureCrawling, axisAncestor, true, props{postureClimbing, sweepMotionless}},
		{postureCrawling, axisAttribute, false, props{postureStriding, sweepMotionless}},
		{postureCrawling, axisNamespace, false, props{postureStriding, sweepMotionless}},
		{postureCrawling, axisSelf, true, props{postureCrawling, sweepMotionless}},
		{postureCrawling, axisSelf, false, props{postureStriding, sweepMotionless}},
		// A crawling context may not descend further.
		{postureCrawling, axisChild, true, roamingFreeRanging},
		{postureCrawling, axisDescendant, true, roamingFreeRanging},

		// The reordering axes are roaming from every streamed posture.
		{postureStriding, axisFollowing, true, roamingFreeRanging},
		{postureStriding, axisPreceding, true, roamingFreeRanging},
		{postureStriding, axisFollowingSibling, true, roamingFreeRanging},
		{postureStriding, axisPrecedingSibling, true, roamingFreeRanging},

		// A roaming context stays roaming.
		{postureRoaming, axisSelf, true, roamingFreeRanging},
		{postureRoaming, axisAttribute, false, roamingFreeRanging},
	}
	for _, r := range rows {
		got := postureOfAxisStep(r.ctx, r.axis, r.elements)
		if got != r.want {
			t.Errorf("axis step (ctx=%v, axis=%d, elements=%v) = %v/%v, want %v/%v",
				r.ctx, r.axis, r.elements, got.posture, got.sweep, r.want.posture, r.want.sweep)
		}
	}
}

func TestStreamableRejectsOnlyFreeRanging(t *testing.T) {
	// The predicate the caller uses to decide whether to raise XTSE3430.
	// Every pair that §19 can produce for a streamable construct must pass
	// it, and only the roaming/free-ranging pair must fail.
	ok := []props{
		groundedMotionless,
		{postureGrounded, sweepConsuming},
		{postureStriding, sweepMotionless},
		{postureStriding, sweepConsuming},
		{postureClimbing, sweepMotionless},
		{postureCrawling, sweepConsuming},
	}
	for _, p := range ok {
		if !p.streamable() {
			t.Errorf("%v/%v should be streamable", p.posture, p.sweep)
		}
	}
	if roamingFreeRanging.streamable() {
		t.Error("roaming/free-ranging should not be streamable")
	}
}
