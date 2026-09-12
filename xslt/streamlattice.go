package xslt

// The streamability lattice of XSLT 3.0 §19.
//
// This file holds the core of the §19.8 streamability analysis and nothing
// else: the two properties every construct carries (posture and sweep), the
// operand usage that says how a construct uses each operand, and the
// combination rules that derive a construct's posture and sweep from those of
// its operands. Nothing here knows what an xsl:for-each or a path expression
// is. The per-construct rules live in streamability.go and are written in
// terms of the primitives below.
//
// Keeping the lattice separate is deliberate. §19.8.1 defines one set of
// general rules that most of the 43 instruction kinds and 41 expression kinds
// defer to, so an error in the combination rules would be an error in every
// construct at once, and it would be silent: the analysis would still produce
// a posture and a sweep, just the wrong ones. The lattice is therefore tested
// on its own, against the worked examples the spec gives in §19.8.2, without
// any construct in the way.

// posture describes where the nodes returned by a construct sit relative to
// the current position in a streamed input document (§19.5).
type posture int

const (
	// postureGrounded: the value contains no nodes from the streamed
	// document. Atomic values, function items, and copied or parsed nodes
	// are grounded.
	postureGrounded posture = iota
	// postureStriding: a sequence of streamed nodes in document order, no
	// one of which is an ancestor or descendant of another.
	postureStriding
	// postureClimbing: nodes reached by the parent, ancestor[-or-self],
	// attribute, or namespace axes from the current position.
	postureClimbing
	// postureCrawling: nodes reached by the descendant[-or-self] axis, and
	// so potentially nested one inside another.
	postureCrawling
	// postureRoaming: the nodes could be anywhere, which means the
	// construct cannot be streamed.
	postureRoaming
)

func (p posture) String() string {
	switch p {
	case postureGrounded:
		return "grounded"
	case postureStriding:
		return "striding"
	case postureClimbing:
		return "climbing"
	case postureCrawling:
		return "crawling"
	default:
		return "roaming"
	}
}

// sweep measures how far the current position in the input stream moves while
// the construct is evaluated (§19.7). The three values are ordered: motionless
// is narrower than consuming, which is narrower than free-ranging.
type sweep int

const (
	// sweepMotionless: evaluation does not move the input position.
	sweepMotionless sweep = iota
	// sweepConsuming: evaluation reads the subtree of the current node,
	// moving the position from its start to its end.
	sweepConsuming
	// sweepFreeRanging: evaluation moves the position arbitrarily, which
	// means the construct cannot be streamed.
	sweepFreeRanging
)

func (s sweep) String() string {
	switch s {
	case sweepMotionless:
		return "motionless"
	case sweepConsuming:
		return "consuming"
	default:
		return "free-ranging"
	}
}

// wider returns the wider of two sweeps. §19.7 orders them motionless <
// consuming < free-ranging, so this is a plain maximum, and it is the whole
// of the sweep rule for a path expression (§19.8.8.8).
func wider(a, b sweep) sweep {
	if a > b {
		return a
	}
	return b
}

// usage says how a construct uses the nodes supplied in one of its operands
// (§19.4). It matters only when the operand value can contain nodes.
type usage int

const (
	// usageAbsorption: the construct reads the subtree rooted at the
	// supplied node — atomizing it, taking its string value, or copying it.
	usageAbsorption usage = iota
	// usageInspection: the construct reads properties available without
	// descending into the subtree — the node's name, or merely that it
	// exists.
	usageInspection
	// usageTransmission: the supplied node is (potentially) returned as
	// part of the construct's own result, in document order.
	usageTransmission
	// usageNavigation: the construct may navigate freely from the supplied
	// node, in a way the streamability rules do not constrain. This is also
	// the answer when the analysis cannot tell what is done with the node.
	usageNavigation
)

func (u usage) String() string {
	switch u {
	case usageAbsorption:
		return "absorption"
	case usageInspection:
		return "inspection"
	case usageTransmission:
		return "transmission"
	default:
		return "navigation"
	}
}

// props is the pair of properties the analysis computes for every construct.
type props struct {
	posture posture
	sweep   sweep
}

var (
	// groundedMotionless is the answer for a construct with no operands
	// (§19.8.1) — a literal, or a call on fn:true().
	groundedMotionless = props{postureGrounded, sweepMotionless}
	// roamingFreeRanging is the answer whenever the analysis concludes the
	// construct is not guaranteed-streamable. Every rule that gives up
	// returns this pair, and the two always travel together: §19 defines no
	// construct that is roaming without being free-ranging, nor
	// free-ranging without being roaming.
	roamingFreeRanging = props{postureRoaming, sweepFreeRanging}
)

// streamable reports whether the construct may appear in a streamable
// context. A construct is not guaranteed-streamable exactly when it is
// free-ranging; §19 keeps posture and sweep in step, so testing the sweep is
// enough, but both are checked here so that a rule that produced an
// inconsistent pair by mistake is caught rather than quietly accepted.
func (p props) streamable() bool {
	return p.sweep != sweepFreeRanging && p.posture != postureRoaming
}

// operand is one operand of a construct, as the general rules see it: the
// properties the operand itself was found to have, the usage its role in the
// parent construct gives it, and two facts about its static type and position
// that the rules consult.
type operand struct {
	props props
	usage usage

	// allowsChildren records whether the operand's static type has a
	// non-empty intersection with U{element(), document-node()} — whether,
	// that is, the operand can deliver a node that has children.
	//
	// §19.8.1 downgrades an absorption usage to inspection when it cannot:
	// the whole subtree of a text or attribute node is already in hand, so
	// absorbing it reads nothing further from the stream. This is why
	// "price * @discount" is grounded and consuming rather than roaming —
	// the attribute operand absorbs nothing.
	allowsChildren bool

	// higherOrder records that the parent construct may evaluate this
	// operand more than once during a single evaluation of itself (the
	// body of a for expression, or of an inline function). A single
	// consuming higher-order operand makes the construct roaming: the
	// stream cannot be re-read.
	higherOrder bool

	// choiceGroup marks operands that are mutually exclusive — the then and
	// else arms of a conditional. Several consuming operands are tolerated
	// when they all belong to the choice group, because only one of them is
	// ever evaluated.
	choiceGroup bool

	// streamedGrounded marks an operand whose posture is grounded but whose
	// value is nevertheless a node of the streamed document: a reference to
	// the streaming parameter of an absorbing or inspection stylesheet
	// function, or a context item standing for one. §19.8.8.12 gives such a
	// reference a grounded posture, but the note under §19.8.5 is explicit
	// that the nodes it denotes "can only derive from streamed nodes passed
	// in an argument to the function".
	//
	// The distinction matters for one usage only. §19.8.1 makes an operand's
	// adjusted sweep equal to its own sweep as soon as the posture is
	// grounded, which is right for absorption and inspection — the subtree
	// below such a node is read forward, and the grounded posture records
	// that nothing streamed comes back out. It is not right for navigation,
	// which reaches *outside* that subtree, to an ancestor or a preceding
	// sibling a streaming processor has already discarded. So navigation
	// applied to a streamed node is free-ranging whatever the posture says.
	// That is what makes fn:path on a streaming parameter unstreamable:
	// §19.8.9's table gives fn:path the operand usage navigation.
	streamedGrounded bool
}

// adjustedUsage applies the §19.8.1 downgrade of absorption to inspection for
// an operand whose static type admits no node with children.
func (o operand) adjustedUsage() usage {
	if o.usage == usageAbsorption && !o.allowsChildren {
		return usageInspection
	}
	return o.usage
}

// adjustedSweep computes the operand's adjusted sweep S′ (§19.8.1).
//
// The three cases are taken in the order the spec gives them, which matters:
// a roaming operand is free-ranging whatever its usage, and a grounded operand
// keeps its own sweep whatever its usage, so only an operand that is neither
// reaches the posture/usage table.
func (o operand) adjustedSweep() sweep {
	if o.props.sweep == sweepFreeRanging || o.props.posture == postureRoaming {
		return sweepFreeRanging
	}
	// Navigation away from a streamed node is free-ranging even when the
	// posture is grounded: the grounded posture of a streaming-parameter
	// reference (§19.8.8.12) says the reference yields no streamed node to
	// its parent, not that the node it denotes is off the stream. Reaching
	// its ancestors or siblings still needs part of the document a streaming
	// processor no longer holds. See operand.streamedGrounded.
	if o.streamedGrounded && o.adjustedUsage() == usageNavigation {
		return sweepFreeRanging
	}
	if o.props.posture == postureGrounded {
		return o.props.sweep
	}
	// The operand can return streamed nodes. The table in §19.8.1 gives the
	// adjusted sweep from the posture and the adjusted usage. Navigation is
	// free-ranging from any streamed posture; inspection and transmission
	// leave the sweep alone; absorption is what costs a read of the stream,
	// and it costs the whole stream from a climbing posture, because
	// absorbing an ancestor means reading forward past everything below it.
	switch o.adjustedUsage() {
	case usageNavigation:
		return sweepFreeRanging
	case usageAbsorption:
		if o.props.posture == postureClimbing {
			return sweepFreeRanging
		}
		return sweepConsuming
	default: // inspection, transmission
		return o.props.sweep
	}
}

// potentiallyConsuming reports whether the operand is potentially consuming
// (§19.8.1): either its adjusted sweep is consuming, or it transmits a node it
// did not ground, in which case the node it hands upward is a streamed one and
// the parent must be told that only one operand may do so.
func (o operand) potentiallyConsuming() bool {
	if o.adjustedSweep() == sweepConsuming {
		return true
	}
	return o.usage == usageTransmission && o.props.posture != postureGrounded
}

// combinedPosture is the posture of a choice operand group (§19.3), given the
// postures of its members.
//
// The rule reads as a lattice join in which grounded is the identity: a group
// of grounded operands is grounded, and one non-grounded posture among
// grounded ones wins outright. Two different non-grounded postures are
// reconcilable only when they are striding and crawling — a set of peers mixed
// with a set of possibly-nested nodes is still just possibly-nested — and every
// other disagreement is roaming.
func combinedPosture(ps []posture) posture {
	seen := map[posture]bool{}
	for _, p := range ps {
		if p == postureRoaming {
			return postureRoaming
		}
		seen[p] = true
	}
	delete(seen, postureGrounded)
	switch len(seen) {
	case 0:
		// Every operand was grounded (or there were none).
		return postureGrounded
	case 1:
		for p := range seen {
			return p
		}
	}
	// More than one non-grounded posture. Only striding with crawling
	// combines; the result is crawling, the weaker guarantee of the two.
	if len(seen) == 2 && seen[postureStriding] && seen[postureCrawling] {
		return postureCrawling
	}
	return postureRoaming
}

// combine applies the general streamability rules of §19.8.1 to a construct
// with the given operands, and returns the construct's posture and sweep.
//
// singletonBuiltin says the construct is a call on a built-in function whose
// return type has maximum cardinality one — fn:head, fn:exactly-one, and
// fn:zero-or-one are the only such functions with a transmission operand. It
// selects the rule that turns a crawling operand into a striding result: one
// node cannot be nested inside itself, so a guaranteed singleton drawn from a
// crawling sequence is striding.
func combine(operands []operand, singletonBuiltin bool) props {
	// A construct with no operands is grounded and motionless.
	if len(operands) == 0 {
		return groundedMotionless
	}

	// Any free-ranging operand makes the whole construct roaming. This is
	// checked across every operand before anything else, so a single
	// unstreamable operand is decisive regardless of what the others do.
	for _, o := range operands {
		if o.adjustedSweep() == sweepFreeRanging {
			return roamingFreeRanging
		}
	}

	var consuming []operand
	for _, o := range operands {
		if o.potentiallyConsuming() {
			consuming = append(consuming, o)
		}
	}

	switch len(consuming) {
	case 0:
		// Every operand is motionless, so nothing reads the stream.
		return groundedMotionless

	case 1:
		o := consuming[0]
		// A construct that may evaluate its one consuming operand more than
		// once cannot stream: the input cannot be rewound.
		if o.higherOrder {
			return roamingFreeRanging
		}
		switch o.adjustedUsage() {
		case usageAbsorption, usageInspection:
			// The nodes were read but not passed on, so what comes out
			// contains no streamed node.
			return props{postureGrounded, sweepConsuming}
		}
		// Transmission: the streamed nodes are handed upward, so the
		// construct inherits the operand's posture — unless the singleton
		// rule applies and narrows crawling to striding.
		if singletonBuiltin && o.props.posture == postureCrawling {
			return props{postureStriding, o.adjustedSweep()}
		}
		return props{o.props.posture, o.adjustedSweep()}

	default:
		// More than one operand wants to read the stream.
		//
		// Two escapes exist. Either the operands are alternatives, so only
		// one of them is ever evaluated; or none of them actually moves the
		// stream, which can happen when each merely transmits a node it
		// reached motionlessly, as in "(@a, @b)".
		allChoice := true
		allMotionless := true
		samePosture := true
		first := consuming[0].props.posture
		ps := make([]posture, 0, len(consuming))
		var widest sweep
		for _, o := range consuming {
			if !o.choiceGroup {
				allChoice = false
			}
			if o.adjustedSweep() != sweepMotionless {
				allMotionless = false
			}
			if o.props.posture != first {
				samePosture = false
			}
			ps = append(ps, o.props.posture)
			widest = wider(widest, o.adjustedSweep())
		}
		if allChoice {
			return props{combinedPosture(ps), widest}
		}
		if allMotionless && samePosture {
			return props{first, sweepMotionless}
		}
		return roamingFreeRanging
	}
}

// postureOfAxisStep gives the posture and sweep of an axis step from the
// context posture, the axis, and whether the step can select an element
// (§19.8.8.8, final table).
//
// selectsElements is the "Selects elements?" column: it is true when the
// U-type of the step has a non-empty intersection with U{element()}. It only
// matters on the descendant axes and on self, and for the same reason in both
// places — a step that cannot return an element cannot return nested nodes,
// because only elements have children, so the result is striding rather than
// crawling.
//
// The caller is responsible for the rules that precede this table: a grounded
// or roaming context, a statically empty step, and the predicate rules.
func postureOfAxisStep(ctx posture, axis axisKind, selectsElements bool) props {
	switch ctx {
	case postureGrounded:
		// Navigating from a node that is not in the streamed document
		// reads nothing from the stream.
		return groundedMotionless

	case postureClimbing:
		switch axis {
		case axisSelf, axisParent, axisAncestorOrSelf, axisAncestor:
			return props{postureClimbing, sweepMotionless}
		case axisAttribute, axisNamespace:
			// Attributes of an ancestor are peers of one another, and
			// reaching them reads nothing further.
			return props{postureStriding, sweepMotionless}
		}

	case postureStriding:
		switch axis {
		case axisParent, axisAncestorOrSelf, axisAncestor:
			return props{postureClimbing, sweepMotionless}
		case axisSelf, axisAttribute, axisNamespace:
			return props{postureStriding, sweepMotionless}
		case axisChild:
			// Children are peers, but reaching them reads the subtree.
			return props{postureStriding, sweepConsuming}
		case axisDescendant, axisDescendantOrSelf:
			if selectsElements {
				return props{postureCrawling, sweepConsuming}
			}
			return props{postureStriding, sweepConsuming}
		}

	case postureCrawling:
		switch axis {
		case axisParent, axisAncestorOrSelf, axisAncestor:
			return props{postureClimbing, sweepMotionless}
		case axisAttribute, axisNamespace:
			return props{postureStriding, sweepMotionless}
		case axisSelf:
			if selectsElements {
				return props{postureCrawling, sweepMotionless}
			}
			return props{postureStriding, sweepMotionless}
		}
	}
	// Any other combination — a following or preceding axis, a child step
	// from a climbing posture, a descendant step from a crawling one.
	return roamingFreeRanging
}

// axisKind names the thirteen XPath axes for the posture table. It repeats
// xpath.Axis rather than using it so that this file, which is the lattice and
// nothing else, does not depend on the expression package; streamability.go
// translates between the two.
type axisKind int

const (
	axisChild axisKind = iota
	axisDescendant
	axisAttribute
	axisSelf
	axisDescendantOrSelf
	axisFollowingSibling
	axisFollowing
	axisParent
	axisAncestor
	axisPrecedingSibling
	axisPreceding
	axisAncestorOrSelf
	axisNamespace
)
