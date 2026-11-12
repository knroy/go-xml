package xsd

import (
	"fmt"
	"math"

	"github.com/knroy/go-xml/v2/xdm"
)

// SequenceMatcher decides whether a sequence of element names satisfies a
// content model.
//
// Schema validation does far more than this — it annotates types, collects
// identity-constraint tables, and reports where a failure happened — so it
// does not go through here. This exists for a caller that has a particle and a
// list of names and wants only the yes-or-no: the DTD validator is the case it
// was added for, since a DTD content model is a strict subset of what a
// particle expresses and rebuilding the automaton for it would be duplication.
//
// A matcher is immutable once compiled and safe to share across goroutines.
type SequenceMatcher struct {
	model *contentModel
	// maxStates is SequenceMatcherOptions.MaxMatchStates as given, resolved
	// by matcherBound when Match runs.
	maxStates int
	// classes is positionClasses(model), which lets Match walk the live
	// states that step alike once per child.
	classes []int
}

// NewSequenceMatcher compiles p for repeated matching, under the default
// bounds; it is NewSequenceMatcherWith with zero SequenceMatcherOptions.
//
// There is no UPA gate in front of a DTD, so the follow width is not bounded
// here. The model is built shared — a repeated group's follow sets are one
// reference to its first set — and the stored representation is bounded by
// maxSequenceMatcherEdges.
func NewSequenceMatcher(p *Particle) (*SequenceMatcher, error) {
	return NewSequenceMatcherWith(p, SequenceMatcherOptions{})
}

// SequenceMatcherOptions bounds what NewSequenceMatcherWith may build and what
// Match may spend. Each field's zero value is the default NewSequenceMatcher
// applies, and a negative value means no limit, for a content model the
// caller produced itself. dtd.Options carries the same three fields for
// dtd.Validate, which compiles every model through here.
type SequenceMatcherOptions struct {
	// MaxPositions bounds the positions the compiled model may hold (its
	// particles after occurrence ranges are unrolled). Zero means
	// DefaultMaxContentModelPositions (8192), as Options.MaxContentModelPositions
	// does for a schema. Unlimited lets a model such as a{0,100000} allocate
	// a position per repetition.
	MaxPositions int

	// MaxEdges bounds the stored follow representation: one per piece plus
	// each member a piece holds, a first set the pieces share counted once.
	// Zero means 1<<20 (1,048,576), which refuses a dense (a?,a?,...) model
	// at about 1450 positions while a repeated choice of n costs about 2n.
	// Unlimited lets such a dense model cost n²/2 edges, gigabytes for a
	// 24 KB declaration.
	MaxEdges int

	// MaxMatchStates bounds the simultaneous automaton states Match may
	// track for one sequence. Zero means DefaultMaxMatchStates (4096). A
	// sequence that needs more is reported as not matching, at the name
	// where the walk gave up, so a lower value can turn a valid sequence
	// into a rejection; unlimited lets a model of nested counted
	// repetitions grow the state set with the input.
	MaxMatchStates int
}

// NewSequenceMatcherWith compiles p for repeated matching under opts.
//
// It takes a bare particle rather than a Schema — the DTD validator is its
// caller, and a DTD has no schema Options — so opts is the only budget that
// applies.
func NewSequenceMatcherWith(p *Particle, opts SequenceMatcherOptions) (*SequenceMatcher, error) {
	m, err := compileForChecks(p, matcherBound(opts.MaxPositions, 0),
		0, matcherBound(opts.MaxEdges, maxSequenceMatcherEdges))
	if err != nil {
		return nil, err
	}
	return &SequenceMatcher{model: m, maxStates: opts.MaxMatchStates,
		classes: positionClasses(m)}, nil
}

// matcherBound resolves a SequenceMatcherOptions field: zero is def, negative
// is no limit. compileForChecks reads a zero position limit as its default and
// needs a positive edge limit to build the model shared, so no limit is
// math.MaxInt rather than zero.
func matcherBound(v, def int) int {
	switch {
	case v == 0:
		return def
	case v < 0:
		return math.MaxInt
	}
	return v
}

// Match reports whether names is admitted by the model, and when it is not,
// the index of the first name that could not be placed.
//
// A rejection at index len(names) means the sequence ended early — every name
// was placed but the model required more.
func (s *SequenceMatcher) Match(names []xdm.QName) (bool, int) {
	ok, at, err := matchNames(s.model, func(i, pos int) bool {
		return s.model.positions[pos].matches(names[i], nil)
	}, len(names), matcherBound(s.maxStates, DefaultMaxMatchStates), s.classes)
	if err != nil {
		// The caller wants a yes-or-no and has nowhere to put an error.
		// A model too tangled to decide is not one this can call a
		// match, so it is a rejection at the point the walk gave up.
		return false, at
	}
	return ok, at
}

// CheckBuiltinValue reports whether lexical is a legal value of the built-in
// simple type named local, and returns its canonical form.
//
// It exists for callers outside schema validation that hold a value and a type
// name — the RELAX NG engine is the case it was added for, since that language
// names XSD types through its datatype library and would otherwise have to
// reimplement lexical checks that already exist here.
//
// An unknown type name is an error rather than an acceptance: a validator that
// silently passes what it cannot check is the failure this exists to avoid.
func CheckBuiltinValue(local, lexical string) (string, error) {
	t := BuiltinType(local)
	if t == nil {
		return "", fmt.Errorf("no built-in type named %q", local)
	}
	return validateSimpleValue(lexical, t)
}

// CompilePattern compiles an XML Schema pattern facet.
//
// The compiled form is anchored, matching the facet's semantics rather than
// fn:matches's containment test, and is safe to reuse across goroutines.
//
// Exported for callers that need the XML Schema regular-expression flavour
// outside a schema — RELAX NG's <param name="pattern"> means exactly this, and
// reimplementing the translation would guarantee the two drift apart.
func CompilePattern(src string) (*Pattern, error) {
	return compilePattern(src)
}
