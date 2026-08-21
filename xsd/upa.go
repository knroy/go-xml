package xsd

import (
	"fmt"
	"sort"
	"strings"
)

// Unique Particle Attribution (§3.8.6).
//
// A content model must be formed so that the particle to validate each item
// against "can be uniquely determined without examining the content or
// attributes of that item, and without any information about the items in the
// remainder of the sequence".
//
// This is nearly free given how the automaton is built. A Glushkov automaton is
// deterministic precisely when the expression is one-unambiguous, which is what
// UPA requires, so the check is the observation that two transitions out of one
// state can match the same element. The reason it *is* answerable here — and
// not in an automaton labelled by element name — is that positions carry the
// particle they came from, so two positions matching the same name are two
// distinct particles rather than one seen twice.
//
// Erratum E1-29 settles the case the spec's own working group argued about:
// particles at different points are always distinct "even if they originated
// from the same named model group". Saxon and XSV take the other reading, where
// only the element *declaration* must be identifiable; Michael Kay calls that
// "a known minor departure from the spec". CheckUPA implements the erratum, and
// Options.LaxUPA selects the permissive rule for schemas written against those
// processors.

// CheckOptions configure the schema component constraint checks.
type CheckOptions struct {
	// LaxUPA accepts a content model in which two competing particles are
	// references to the same element declaration.
	//
	// The strict reading of §3.8.6 rejects those; Saxon and XSV accept
	// them. Schemas written against either of those processors may rely on
	// it, so this exists — but it is off by default, because the strict
	// reading is the conforming one.
	LaxUPA bool
}

// CheckConstraints applies the schema component constraints that are checked
// against a compiled content model: Unique Particle Attribution and Element
// Declarations Consistent.
//
// It is a separate call rather than part of loading, following the precedent
// every mature implementation sets: Xerces gates exactly these two behind
// schema-full-checking, default false, and libxml2 omits particle restriction
// entirely. They are the expensive half, they say nothing about whether an
// instance document is valid, and a caller validating documents against a
// schema they already trust has no reason to pay for them.
func (s *Schema) CheckConstraints(opts CheckOptions) error {
	var errs []error
	for name, t := range s.Types {
		ct, ok := t.(*ComplexType)
		if !ok || ct.Particle == nil {
			continue
		}
		m, err := compileContentModel(ct.Particle)
		if err != nil {
			continue
		}
		where := name.Local
		if where == "" {
			where = "an anonymous type"
		}
		if err := checkUPA(m, where, opts); err != nil {
			errs = append(errs, err)
		}
		if err := checkElementDeclarationsConsistent(m, where); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// The order of a map walk is not stable, and a schema author comparing
	// two runs should see the same list.
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	sort.Strings(msgs)
	return &SchemaErrors{Errors: sortedErrors(msgs)}
}

func sortedErrors(msgs []string) []error {
	out := make([]error, len(msgs))
	for i, m := range msgs {
		out[i] = fmt.Errorf("%s", m)
	}
	return out
}

// checkUPA reports a content model in which two competing particles could match
// the same element.
//
// The states to examine are the initial set and each position's follow set: at
// every point where the automaton must choose, no two choices may accept the
// same element.
func checkUPA(m *contentModel, where string, opts CheckOptions) error {
	states := make([][]int, 0, len(m.follow)+1)
	states = append(states, m.first)
	states = append(states, m.follow...)

	for _, state := range states {
		for i := 0; i < len(state); i++ {
			for j := i + 1; j < len(state); j++ {
				a, b := m.positions[state[i]], m.positions[state[j]]
				if !positionsCompete(a, b) {
					continue
				}
				if opts.LaxUPA && sameDeclaration(a, b) {
					// The permissive reading: the element
					// declaration is identifiable even
					// though the particle is not.
					continue
				}
				return fmt.Errorf(
					"cos-nonambig: %s violates Unique Particle Attribution: "+
						"%s and %s can both match the same element",
					where, describeTerm(a), describeTerm(b))
			}
		}
	}
	return nil
}

// positionsCompete reports whether two positions can match the same element.
//
// Three cases, per Appendix H: two element declarations with the same name, two
// wildcards whose namespace constraints overlap, and an element against a
// wildcard that admits its namespace. processContents is irrelevant — a skip
// wildcard competes exactly as a strict one does, because the ambiguity is
// about which particle matches, not about what happens afterwards.
func positionsCompete(a, b *position) bool {
	switch ta := a.term.(type) {
	case *ElementDecl:
		switch tb := b.term.(type) {
		case *ElementDecl:
			return elementNamesOverlap(ta, tb)
		case *Wildcard:
			return wildcardAdmitsElement(tb, ta)
		}
	case *Wildcard:
		switch tb := b.term.(type) {
		case *ElementDecl:
			return wildcardAdmitsElement(ta, tb)
		case *Wildcard:
			return wildcardsOverlap(ta, tb)
		}
	}
	return false
}

// elementNamesOverlap reports whether two element declarations can match the
// same name, taking substitution groups into account.
//
// The spec says a list of particles "implicitly contains" an element
// declaration if a member of the list contains it in its substitution group, so
// the comparison is between the *sets* of names each can match rather than
// between the two declared names.
func elementNamesOverlap(a, b *ElementDecl) bool {
	if a.Name == b.Name {
		return true
	}
	for _, sub := range a.substitutable {
		if sub.Name == b.Name {
			return true
		}
		for _, other := range b.substitutable {
			if sub.Name == other.Name {
				return true
			}
		}
	}
	for _, sub := range b.substitutable {
		if sub.Name == a.Name {
			return true
		}
	}
	return false
}

// wildcardAdmitsElement reports whether a wildcard can match an element
// declaration's name, or any name in its substitution group.
func wildcardAdmitsElement(w *Wildcard, d *ElementDecl) bool {
	if w.Allows(d.Name.URI) {
		return true
	}
	for _, sub := range d.substitutable {
		if w.Allows(sub.Name.URI) {
			return true
		}
	}
	return false
}

// wildcardsOverlap reports whether two namespace constraints admit a common
// namespace.
func wildcardsOverlap(a, b *Wildcard) bool {
	if a.Kind == NSAny || b.Kind == NSAny {
		return true
	}
	// Two negations always overlap: whatever each excludes, there is some
	// namespace neither does — the set of namespaces is unbounded.
	if a.Kind == NSNot && b.Kind == NSNot {
		return true
	}
	if a.Kind == NSNot {
		a, b = b, a
	}
	// a is enumerated here; b is either enumerated or a negation.
	for _, ns := range a.Namespace {
		if b.Allows(ns) {
			return true
		}
	}
	return false
}

// sameDeclaration reports whether two positions are references to one element
// declaration, which is what the permissive UPA reading turns on.
func sameDeclaration(a, b *position) bool {
	da, okA := a.term.(*ElementDecl)
	db, okB := b.term.(*ElementDecl)
	return okA && okB && da == db
}

// describeTerm names a position's term for a diagnostic.
func describeTerm(p *position) string {
	switch t := p.term.(type) {
	case *ElementDecl:
		if t.Name.URI != "" {
			return fmt.Sprintf("element {%s}%s", t.Name.URI, t.Name.Local)
		}
		return "element " + t.Name.Local
	case *Wildcard:
		return "wildcard " + describeWildcard(t)
	}
	return "a particle"
}

func describeWildcard(w *Wildcard) string {
	switch w.Kind {
	case NSAny:
		return "##any"
	case NSNot:
		return "##other(" + strings.Join(w.Namespace, " ") + ")"
	}
	return "(" + strings.Join(w.Namespace, " ") + ")"
}

// checkElementDeclarationsConsistent reports two element declarations with the
// same name but different types in one content model (§3.8.6).
//
// This is separate from UPA and catches a different mistake: not "which
// particle matches" but "the same element name means two things here". A
// document could be validated either way, so the schema is what is wrong.
func checkElementDeclarationsConsistent(m *contentModel, where string) error {
	byName := map[string]*ElementDecl{}
	for _, p := range m.positions {
		d, ok := p.term.(*ElementDecl)
		if !ok {
			continue
		}
		key := d.Name.URI + " " + d.Name.Local
		prev, seen := byName[key]
		if !seen {
			byName[key] = d
			continue
		}
		if prev.Type != d.Type {
			return fmt.Errorf(
				"cos-element-consistent: %s declares %s with two different "+
					"types", where, describeTerm(&position{term: d}))
		}
	}
	return nil
}
