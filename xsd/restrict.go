package xsd

import (
	"fmt"
	"sort"

	"github.com/knroy/go-xml/xdm"
)

// Particle Valid (Restriction) (§3.9.6).
//
// When a complex type restricts another, the derived content model must accept
// only sequences the base already accepted. The spec does not say that
// directly — deciding language inclusion over two regular expressions is
// possible but expensive — so it defines a *structural* approximation instead:
// a table of cases keyed on the pair (kind of R, kind of B), each with its own
// rule. The working group says as much in a note: "The structural
// correspondence approach to guaranteeing the subset relation set out here is
// necessarily verbose, but has the advantage of being checkable in a
// straightforward way."
//
// The consequence worth remembering is that the check is deliberately
// incomplete in the *conservative* direction: there are restrictions that
// accept a subset of the base's language and are still rejected here, because
// they are not structurally parallel to it. That is what the spec requires, so
// a model that "obviously" narrows its base but reorders it is an error.
//
// Two preliminaries in clause 2 must be applied before the table is consulted:
// substitution group heads expand into choices (clause 2.1) and "pointless"
// group nesting is stripped (clause 2.2). Both change which table cell applies,
// so skipping them rejects schemas the spec accepts.

// checkParticleRestriction reports whether every complex type derived by
// restriction has a content model that is a valid restriction of its base's.
//
// It runs at load time rather than behind CheckConstraints because it is a
// property of the schema alone: unlike Unique Particle Attribution, which a
// caller may reasonably decline to pay for, a content model that does not
// restrict its base makes the *derivation* meaningless, and the suite's
// schemaTest cases expect it rejected.
func (p *parser) checkParticleRestriction() error {
	var errs []error
	for name, t := range p.schema.Types {
		ct, ok := t.(*ComplexType)
		if !ok || ct.DerivationMethod != DerivationRestriction {
			continue
		}
		base, ok := ct.Base.(*ComplexType)
		if !ok {
			continue
		}
		// The ur-type restricts nothing: it is its own base, and its
		// content model is the open wildcard every type is free to
		// narrow. Recursing into it would compare a type against
		// itself.
		if base == ct || isUrType(base) {
			continue
		}
		// Only element-only and mixed content have a particle to
		// check. A restriction to empty content is governed by
		// Particle Emptiable instead: the base's particle must be able
		// to match nothing at all.
		if ct.Particle == nil {
			if base.Particle != nil && !particleEmptiable(base.Particle) {
				errs = append(errs, fmt.Errorf(
					"rcase-Recurse.2: %s restricts %s to empty content, "+
						"but the base's content model cannot be empty",
					typeLabel(name, ct), typeLabel(base.Name, base)))
			}
			continue
		}
		if base.Particle == nil {
			// An empty base admits only the empty sequence, so a
			// derived particle that can also match nothing — the
			// bare <xs:sequence/> a restriction writes to say "and
			// no content" — is a valid restriction of it. Anything
			// that must match an element is not.
			if !particleEmptiable(ct.Particle) {
				errs = append(errs, fmt.Errorf(
					"derivation-ok-restriction: %s requires content but its "+
						"base %s has none", typeLabel(name, ct), typeLabel(base.Name, base)))
			}
			continue
		}
		if err := particleValidRestriction(ct.Particle, base.Particle); err != nil {
			errs = append(errs, fmt.Errorf(
				"%s is not a valid restriction of %s: %w",
				typeLabel(name, ct), typeLabel(base.Name, base), err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// The map walk above has no stable order, and a schema author
	// comparing two runs should see the same list.
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	sort.Strings(msgs)
	return &SchemaErrors{Errors: sortedErrors(msgs)}
}

// typeLabel names a type for a diagnostic, distinguishing the anonymous ones
// a schema may have many of.
func typeLabel(name xdm.QName, ct *ComplexType) string {
	if name.Local == "" {
		return "an anonymous complex type"
	}
	if name.URI == "" {
		return "complex type " + name.Local
	}
	return fmt.Sprintf("complex type {%s}%s", name.URI, name.Local)
}

// isUrType reports whether a type is xs:anyType, whose content model is the
// wildcard every other type's is free to narrow.
func isUrType(ct *ComplexType) bool {
	return ct.Name.URI == NSSchema && ct.Name.Local == "anyType"
}

// particleValidRestriction is the constraint itself: R must be a valid
// restriction of B.
//
// The dispatch below is the spec's table, read with R on the rows and B on the
// columns. Every cell not named is Forbidden, which is why the fallthrough is
// an error rather than a pass.
func particleValidRestriction(r, b *Particle) error {
	return particleRestricts(r, b, false)
}

// particleRestricts carries the clause 2.1 state that particleValidRestriction
// hides.
//
// expanded records that a substitution group head on one side has already been
// rewritten into a choice on this path. The flag is necessary because a head is
// a member of its own substitution group: expanding again on the way down
// through RecurseAsIfGroup and RecurseLax reproduces the same head particle
// forever.
func particleRestricts(r, b *Particle, expanded bool) error {
	// Clause 1: the same particle restricts itself. This is not merely an
	// optimisation — a group reference shared between base and derived
	// resolves to one *Particle, and the structural rules below would have
	// to rediscover the identity the hard way.
	if r == b {
		return nil
	}

	// Clause 2.2: strip the group nesting the spec calls pointless before
	// choosing a cell, because an unstripped <sequence> wrapping one
	// element lands in a Recurse cell where the element itself would land
	// in NameAndTypeOK.
	r = stripPointless(r)
	b = stripPointless(b)

	// Clause 2.1: a top-level element declaration heading a non-trivial
	// substitution group stands for a choice over that group. Without this
	// a restriction that names a *member* where the base names the *head*
	// is rejected for having the wrong name — and naming a member is the
	// whole reason a substitution group exists.
	//
	// Only the base side is expanded. The clause is written for both, but
	// expanding R buys nothing: whatever R names must correspond to
	// something B admits, and B's expansion already offers every member.
	// The expansion is also guarded by `expanded`, because the choice it
	// produces holds the head itself and comparing head against head would
	// otherwise expand forever.
	if !expanded && !sameElementDecl(r, b) {
		if sub := asSubstitutionChoice(b); sub != nil {
			return particleRestricts(r, sub, true)
		}
	}

	switch rt := r.Term.(type) {
	case *ElementDecl:
		switch bt := b.Term.(type) {
		case *ElementDecl:
			return nameAndTypeOK(r, rt, b, bt)
		case *Wildcard:
			return nsCompat(r, rt, b, bt)
		case *ModelGroup:
			return recurseAsIfGroup(r, b, bt, expanded)
		}
	case *Wildcard:
		switch bt := b.Term.(type) {
		case *Wildcard:
			return nsSubset(r, rt, b, bt)
		}
	case *ModelGroup:
		switch bt := b.Term.(type) {
		case *Wildcard:
			return nsRecurseCheckCardinality(r, rt, b, bt, expanded)
		case *ModelGroup:
			switch {
			case rt.Compositor == CompositorAll && bt.Compositor == CompositorAll,
				rt.Compositor == CompositorSequence && bt.Compositor == CompositorSequence:
				return recurse(r, rt, b, bt, expanded)
			case rt.Compositor == CompositorChoice && bt.Compositor == CompositorChoice:
				return recurseLax(r, rt, b, bt, expanded)
			case rt.Compositor == CompositorSequence && bt.Compositor == CompositorAll:
				return recurseUnordered(r, rt, b, bt, expanded)
			case rt.Compositor == CompositorSequence && bt.Compositor == CompositorChoice:
				return mapAndSum(r, rt, b, bt, expanded)
			}
		}
	}
	return fmt.Errorf("a %s may not restrict a %s", particleKind(r), particleKind(b))
}

// particleKind names a particle's term for a diagnostic.
func particleKind(p *Particle) string {
	switch t := p.Term.(type) {
	case *ElementDecl:
		return "an element declaration"
	case *Wildcard:
		return "a wildcard"
	case *ModelGroup:
		return t.Compositor.String() + " group"
	}
	return "a particle"
}

// stripPointless removes the group wrappers clause 2.2 calls pointless.
//
// The clause is written from the perspective of the *containing* particle,
// which makes it awkward to read: a <sequence> is pointless when it has one
// member and unit occurrence, or when its particles are empty. Applied
// repeatedly — a single-member sequence inside a single-member sequence
// collapses twice — until nothing more can be removed.
//
// The rule for an <all> is looser than for the other two: clause 2.2 lets a
// one-member all group be stripped regardless of the containing particle's
// occurrence range, because an all group of one particle imposes no ordering
// to lose.
func stripPointless(p *Particle) *Particle {
	for {
		g, ok := p.Term.(*ModelGroup)
		if !ok || len(g.Particles) != 1 {
			return p
		}
		inner := g.Particles[0]
		// Collapsing is only sound when the outer particle occurs
		// exactly once: otherwise (a)* and a differ, and folding one
		// into the other would compare the wrong ranges.
		if p.MinOccurs != 1 || p.MaxOccurs != 1 {
			return p
		}
		p = inner
	}
}

// occurrenceRangeOK is Occurrence Range OK (§3.9.6): R's range must fit inside
// B's.
func occurrenceRangeOK(r, b *Particle) error {
	return rangeOK(r.MinOccurs, r.MaxOccurs, b.MinOccurs, b.MaxOccurs)
}

func rangeOK(rMin, rMax, bMin, bMax int) error {
	if rMin < bMin {
		return fmt.Errorf("minOccurs %d is below the base's %d", rMin, bMin)
	}
	if bMax == Unbounded {
		return nil
	}
	if rMax == Unbounded {
		return fmt.Errorf("maxOccurs is unbounded but the base's is %d", bMax)
	}
	if rMax > bMax {
		return fmt.Errorf("maxOccurs %d exceeds the base's %d", rMax, bMax)
	}
	return nil
}

// nameAndTypeOK is Particle Restriction OK (Elt:Elt) (§3.9.6).
//
// Clause 3.2.5 — that R's type be validly derived from B's — is the part that
// makes this more than a name comparison, and it is what catches a restriction
// that keeps an element's name while widening what may go inside it.
func nameAndTypeOK(r *Particle, rd *ElementDecl, b *Particle, bd *ElementDecl) error {
	if rd.Name != bd.Name {
		return fmt.Errorf("element %s does not correspond to the base's %s",
			rd.Name.Local, bd.Name.Local)
	}
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("element %s: %w", rd.Name.Local, err)
	}
	// Clause 3.2.1: a restriction may not make an element nillable that its
	// base did not, because a nilled element satisfies the derived
	// declaration while failing the base's.
	if rd.Nillable && !bd.Nillable {
		return fmt.Errorf("element %s is nillable but the base's is not", rd.Name.Local)
	}
	// Clause 3.2.2: a fixed value in the base must be preserved. A
	// restriction that dropped it, or changed it, would admit values the
	// base rejects.
	if bd.Constraint != nil && bd.Constraint.Fixed {
		if rd.Constraint == nil || !rd.Constraint.Fixed ||
			rd.Constraint.Lexical != bd.Constraint.Lexical {
			return fmt.Errorf(
				"element %s must keep the base's fixed value %q",
				rd.Name.Local, bd.Constraint.Lexical)
		}
	}
	// Clause 3.2.5: the type must be a restriction of the base's. An
	// unresolved type reference is reported where it is used, not here, so
	// a declaration missing its type is left alone rather than blamed for
	// a derivation fault it did not cause.
	if rd.Type != nil && bd.Type != nil && !typeRestricts(rd.Type, bd.Type) {
		return fmt.Errorf("element %s's type is not derived from the base's",
			rd.Name.Local)
	}
	return nil
}

// typeRestricts reports whether a type is validly derived from another,
// walking the base chain.
//
// Clause 3.2.5 permits extension as well as restriction — the set it names is
// {extension, list, union}, which are the derivations *excluded*, leaving
// restriction — but in practice the check that matters is reachability along
// the base chain, plus the union-member case that lets a restriction name one
// member of the base's union.
func typeRestricts(t, want Type) bool {
	if want == nil || t == want {
		return true
	}
	// xs:anyType is the base of everything, so nothing fails against it.
	if ct, ok := want.(*ComplexType); ok && isUrType(ct) {
		return true
	}
	if st, ok := want.(*SimpleType); ok && st.Variety == VarietyUnion {
		for _, m := range st.MemberTypes {
			if m != nil && typeRestricts(t, m) {
				return true
			}
		}
	}
	// The walk stops on self as well as nil: the ur-type is its own base
	// and a chain testing only for nil would not terminate.
	for cur := t; cur != nil; {
		if cur == want {
			return true
		}
		next := cur.BaseType()
		if next == cur {
			break
		}
		cur = next
	}
	return false
}

// nsCompat is Particle Derivation OK (Elt:Any) (§3.9.6): an element may
// restrict a wildcard that admits its namespace.
func nsCompat(r *Particle, rd *ElementDecl, b *Particle, bw *Wildcard) error {
	if !bw.Allows(rd.Name.URI) {
		return fmt.Errorf("element %s is not in a namespace the base wildcard admits",
			rd.Name.Local)
	}
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("element %s: %w", rd.Name.Local, err)
	}
	return nil
}

// recurseAsIfGroup is Particle Derivation OK (Elt:All/Choice/Sequence)
// (§3.9.6): an element restricting a group is checked as a one-member group of
// the base's variety.
func recurseAsIfGroup(r *Particle, b *Particle, bg *ModelGroup, expanded bool) error {
	wrapped := &Particle{
		MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: bg.Compositor, Particles: []*Particle{r}},
	}
	switch bg.Compositor {
	case CompositorChoice:
		return recurseLax(wrapped, wrapped.Term.(*ModelGroup), b, bg, expanded)
	default:
		return recurse(wrapped, wrapped.Term.(*ModelGroup), b, bg, expanded)
	}
}

// nsSubset is Particle Derivation OK (Any:Any) (§3.9.6).
func nsSubset(r *Particle, rw *Wildcard, b *Particle, bw *Wildcard) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("wildcard: %w", err)
	}
	if !wildcardSubset(rw, bw) {
		return fmt.Errorf("the wildcard's namespace constraint is not a subset of the base's")
	}
	// Clause 3: processContents may only be tightened. The exception for
	// the ur-type's own wildcard is handled by the caller declining to
	// descend into xs:anyType at all — its wildcard is lax, and without the
	// exception no skip wildcard could ever be written.
	if processStrength(rw.ProcessContents) < processStrength(bw.ProcessContents) {
		return fmt.Errorf("processContents %s is weaker than the base's %s",
			rw.ProcessContents, bw.ProcessContents)
	}
	return nil
}

// processStrength orders the processContents modes: strict is stronger than
// lax is stronger than skip.
func processStrength(p ProcessContents) int {
	switch p {
	case ProcessStrict:
		return 2
	case ProcessLax:
		return 1
	}
	return 0
}

// wildcardSubset is Wildcard Subset (§3.10.6): is sub an intensional subset of
// super?
func wildcardSubset(sub, super *Wildcard) bool {
	// Clause 1: everything is a subset of ##any.
	if super.Kind == NSAny {
		return true
	}
	switch sub.Kind {
	case NSAny:
		// ##any is a subset only of ##any, already handled.
		return false
	case NSNot:
		// Clause 2: a negation is a subset of a negation of the same
		// value. Two negations of *different* values are incomparable:
		// each admits a namespace the other excludes.
		if super.Kind != NSNot {
			return false
		}
		return sameNamespaceSet(sub.Namespace, super.Namespace) &&
			sub.ExcludesAbsent == super.ExcludesAbsent
	default:
		// Clause 3: an enumerated set is a subset of a superset of
		// itself, or of a negation excluding nothing the set contains.
		if super.Kind == NSEnumerated {
			for _, ns := range sub.Namespace {
				if !containsNamespace(super.Namespace, ns) {
					return false
				}
			}
			return true
		}
		// super is a negation: clause 3.2.2 requires that neither the
		// negated value nor the absent namespace appear in sub's set.
		for _, ns := range sub.Namespace {
			if !super.Allows(ns) {
				return false
			}
		}
		return true
	}
}

func containsNamespace(set []string, ns string) bool {
	for _, s := range set {
		if s == ns {
			return true
		}
	}
	return false
}

func sameNamespaceSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !containsNamespace(b, x) {
			return false
		}
	}
	return true
}

// nsRecurseCheckCardinality is Particle Derivation OK (All/Choice/Sequence:Any)
// (§3.9.6): a group restricting a wildcard.
func nsRecurseCheckCardinality(r *Particle, rg *ModelGroup, b *Particle, bw *Wildcard, expanded bool) error {
	// Clause 1: every member of the group must itself restrict the
	// wildcard. The wildcard is passed with unit occurrence because the
	// group's own range is checked separately, in clause 2.
	// The wildcard is compared at the base particle's own occurrence range:
	// clause 1 says each member must be a valid restriction of "the
	// wildcard", meaning B itself, not a unit-occurrence copy of it.
	// Narrowing it to 1..1 would reject a member repeating within a range
	// the base wildcard already permits.
	for _, member := range rg.Particles {
		if err := particleRestricts(member, b, expanded); err != nil {
			return err
		}
	}
	// Clause 2: the group's *effective total range* — how many elements it
	// can contribute in total, not how many times it repeats — must fit
	// inside the wildcard's occurrence range. This is the clause that
	// distinguishes a group from a particle: repeating a two-member
	// sequence twice contributes four elements, not two.
	min, max := effectiveTotalRange(r)
	if err := rangeOK(min, max, b.MinOccurs, b.MaxOccurs); err != nil {
		return fmt.Errorf("the group's total range does not fit the base wildcard: %w", err)
	}
	return nil
}

// recurse is Particle Derivation OK (All:All, Sequence:Sequence) (§3.9.6).
//
// The mapping clause 2 asks for is order-preserving, which makes the search a
// single left-to-right walk rather than a search over permutations: each
// particle of R must match the next particle of B that it can, and every
// particle of B skipped along the way must be emptiable.
func recurse(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("%s group: %w", rg.Compositor, err)
	}
	bi := 0
	for _, rp := range rg.Particles {
		matched := false
		for bi < len(bg.Particles) {
			bp := bg.Particles[bi]
			bi++
			if particleRestricts(rp, bp, expanded) == nil {
				matched = true
				break
			}
			// A base particle skipped over must be optional:
			// otherwise the derived model omits something the base
			// requires, which is not a restriction but a different
			// language.
			if !particleEmptiable(bp) {
				return fmt.Errorf(
					"%s group: the base requires %s, which the restriction omits",
					rg.Compositor, particleKind(bp))
			}
		}
		if !matched {
			return fmt.Errorf("%s group: %s has no corresponding particle in the base",
				rg.Compositor, particleKind(rp))
		}
	}
	// Clause 2.2: whatever remains unmapped at the tail must be emptiable
	// too.
	for ; bi < len(bg.Particles); bi++ {
		if !particleEmptiable(bg.Particles[bi]) {
			return fmt.Errorf(
				"%s group: the base requires %s, which the restriction omits",
				rg.Compositor, particleKind(bg.Particles[bi]))
		}
	}
	return nil
}

// recurseLax is Particle Derivation OK (Choice:Choice) (§3.9.6).
//
// Like Recurse, the mapping is order-preserving — but unlike Recurse, the
// unmapped particles of B need not be emptiable: dropping an alternative from
// a choice is exactly what restricting a choice means.
func recurseLax(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("choice group: %w", err)
	}
	bi := 0
	for _, rp := range rg.Particles {
		matched := false
		for bi < len(bg.Particles) {
			bp := bg.Particles[bi]
			bi++
			if particleRestricts(rp, bp, expanded) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("choice group: %s is not one of the base's alternatives",
				particleKind(rp))
		}
	}
	return nil
}

// recurseUnordered is Particle Derivation OK (Sequence:All) (§3.9.6).
//
// The mapping here is *not* order-preserving — a sequence may take an all
// group's particles in any order, which is the whole point of restricting an
// all group to a sequence — but it must still be injective: clause 2.1 forbids
// two particles of R mapping to one of B.
func recurseUnordered(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("sequence restricting an all group: %w", err)
	}
	used := make([]bool, len(bg.Particles))
	for _, rp := range rg.Particles {
		matched := false
		for i, bp := range bg.Particles {
			if used[i] {
				continue
			}
			if particleRestricts(rp, bp, expanded) == nil {
				used[i] = true
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf(
				"sequence restricting an all group: %s has no unused corresponding particle",
				particleKind(rp))
		}
	}
	// Clause 2.3: the particles of B left unmapped must be emptiable.
	for i, bp := range bg.Particles {
		if !used[i] && !particleEmptiable(bp) {
			return fmt.Errorf(
				"sequence restricting an all group: the base requires %s, "+
					"which the restriction omits", particleKind(bp))
		}
	}
	return nil
}

// mapAndSum is Particle Derivation OK (Sequence:Choice) (§3.9.6).
//
// This is the "unfolding" case: a sequence of n alternatives restricts a choice
// that may repeat n times. The mapping in clause 1 is neither order-preserving
// nor injective — the same alternative may be chosen repeatedly — and clause 2
// compares the *total* length of the unfolded sequence against the choice's
// range.
func mapAndSum(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool) error {
	for _, rp := range rg.Particles {
		matched := false
		for _, bp := range bg.Particles {
			if particleRestricts(rp, bp, expanded) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("sequence restricting a choice: %s is not one of the "+
				"base's alternatives", particleKind(rp))
		}
	}
	// Clause 2: the pair is (min occurs × length, max occurs × length),
	// with unbounded propagating. An empty sequence has length zero, and
	// the product is zero rather than the particle's own range.
	n := len(rg.Particles)
	min := r.MinOccurs * n
	max := r.MaxOccurs * n
	if r.MaxOccurs == Unbounded {
		max = Unbounded
	}
	if err := rangeOK(min, max, b.MinOccurs, b.MaxOccurs); err != nil {
		return fmt.Errorf("sequence restricting a choice: %w", err)
	}
	return nil
}

// particleEmptiable is Particle Emptiable (§3.9.6): can this particle match
// nothing at all?
func particleEmptiable(p *Particle) bool {
	if p.MinOccurs == 0 {
		return true
	}
	if _, ok := p.Term.(*ModelGroup); !ok {
		return false
	}
	min, _ := effectiveTotalRange(p)
	return min == 0
}

// effectiveTotalRange is Effective Total Range (§3.8.6): the number of elements
// a particle can contribute, counting its repetitions.
//
// The distinction from the particle's own occurrence range is the point: a
// sequence of three elements repeated twice contributes six, and it is that
// total — not the two — that a wildcard restriction must accommodate.
func effectiveTotalRange(p *Particle) (int, int) {
	g, ok := p.Term.(*ModelGroup)
	if !ok {
		return p.MinOccurs, p.MaxOccurs
	}

	var min, max int
	first := true
	maxUnbounded := false
	anyNonZeroMax := false

	for _, child := range g.Particles {
		cMin, cMax := effectiveTotalRange(child)
		if cMax == Unbounded {
			maxUnbounded = true
			anyNonZeroMax = true
		} else if cMax > 0 {
			anyNonZeroMax = true
		}

		if g.Compositor == CompositorChoice {
			// A choice contributes the *minimum* of its
			// alternatives' minima and the maximum of their maxima:
			// the shortest path through it is the cheapest
			// alternative.
			if first || cMin < min {
				min = cMin
			}
			if cMax > max {
				max = cMax
			}
		} else {
			min += cMin
			if !maxUnbounded {
				max += cMax
			}
		}
		first = false
	}
	// An empty group contributes nothing, whatever its occurrence range.
	if len(g.Particles) == 0 {
		return 0, 0
	}

	min *= p.MinOccurs
	switch {
	case maxUnbounded:
		max = Unbounded
	case p.MaxOccurs == Unbounded:
		if anyNonZeroMax {
			max = Unbounded
		} else {
			max = 0
		}
	default:
		max *= p.MaxOccurs
	}
	return min, max
}

// asSubstitutionChoice expands an element declaration particle into the choice
// group clause 2.1 says it stands for, or returns nil when the declaration
// heads no substitution group.
//
// The synthesised choice keeps the particle's own occurrence range and gives
// each member unit occurrence, exactly as the clause specifies. Only a group
// with a member *other than the head itself* is expanded: a declaration that
// substitutes for nothing is left alone so that it still lands in
// NameAndTypeOK.
func asSubstitutionChoice(p *Particle) *Particle {
	d, ok := p.Term.(*ElementDecl)
	if !ok || d.Scope != ScopeGlobal {
		return nil
	}
	members := d.substitutable
	if len(members) == 0 {
		return nil
	}
	nonTrivial := false
	for _, m := range members {
		if m != d {
			nonTrivial = true
			break
		}
	}
	if !nonTrivial {
		return nil
	}

	parts := make([]*Particle, 0, len(members)+1)
	seen := map[*ElementDecl]bool{}
	add := func(m *ElementDecl) {
		if seen[m] {
			return
		}
		seen[m] = true
		parts = append(parts, &Particle{MinOccurs: 1, MaxOccurs: 1, Term: m})
	}
	// The head is a member of its own substitution group, but an abstract
	// head cannot itself appear, so it is offered only when it is not.
	if !d.Abstract {
		add(d)
	}
	for _, m := range members {
		if m != d {
			add(m)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return &Particle{
		MinOccurs: p.MinOccurs, MaxOccurs: p.MaxOccurs,
		Term: &ModelGroup{Compositor: CompositorChoice, Particles: parts},
	}
}

// sameElementDecl reports whether two particles name the same element
// declaration, which is the case clause 2.1's expansion must not disturb.
func sameElementDecl(a, b *Particle) bool {
	da, okA := a.Term.(*ElementDecl)
	db, okB := b.Term.(*ElementDecl)
	return okA && okB && da == db
}
