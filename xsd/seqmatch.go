package xsd

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
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
}

// NewSequenceMatcher compiles p for repeated matching.
func NewSequenceMatcher(p *Particle) (*SequenceMatcher, error) {
	m, err := compileContentModel(p)
	if err != nil {
		return nil, err
	}
	return &SequenceMatcher{model: m}, nil
}

// Match reports whether names is admitted by the model, and when it is not,
// the index of the first name that could not be placed.
//
// A rejection at index len(names) means the sequence ended early — every name
// was placed but the model required more.
func (s *SequenceMatcher) Match(names []xdm.QName) (bool, int) {
	m := s.model
	if len(m.positions) == 0 {
		return len(names) == 0, 0
	}

	// The walk mirrors the schema validator's: one position is chosen per
	// name rather than a set, because the counters that stand in for bounded
	// repetition are per-scope state and only make sense along a single path.
	// The model is deterministic — Unique Particle Attribution guarantees at
	// most one position can match — so there is nothing to backtrack over.
	counts := make([]int, len(m.counters))
	current := m.first
	prevIdx := -1

	for i, name := range names {
		next := -1
		for _, idx := range current {
			if !m.positions[idx].matches(name, nil) {
				continue
			}
			if !counterAllows(m, counts, prevIdx, idx) {
				continue
			}
			next = idx
			break
		}
		if next < 0 {
			return false, i
		}
		advanceCounters(m, counts, prevIdx, next)
		prevIdx = next
		current = m.follow[next]
	}

	// The sequence must be able to end here: either nothing was required, or
	// the last position reached is a valid ending point and every counter has
	// met its minimum.
	if prevIdx < 0 {
		return m.nullable, 0
	}
	if !contains(m.last, prevIdx) || !countersSatisfied(m, counts, prevIdx) {
		return false, len(names)
	}
	return true, 0
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
