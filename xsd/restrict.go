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
		if err := particleValidRestrictionVersion(ct.Particle, base.Particle, p.schema.Version); err != nil {
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
	return particleRestricts(r, b, false, Version10)
}

// particleValidRestrictionVersion is particleValidRestriction with the schema
// version, which decides whether the 1.1 all-group subsumption rule applies.
func particleValidRestrictionVersion(r, b *Particle, v Version) error {
	return particleRestricts(r, b, false, v)
}

// particleRestricts carries the clause 2.1 state that particleValidRestriction
// hides.
//
// expanded records that a substitution group head on one side has already been
// rewritten into a choice on this path. The flag is necessary because a head is
// a member of its own substitution group: expanding again on the way down
// through RecurseAsIfGroup and RecurseLax reproduces the same head particle
// forever.
func particleRestricts(r, b *Particle, expanded bool, v Version) error {
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

	// Clause 2.2.1 makes an empty <sequence> or <all> — and an empty
	// <choice> under a particle with {min occurs} of 0 — pointless, so it
	// is *ignored* rather than matched against a cell of the table. A
	// restriction whose whole content model is ignored away contributes no
	// element at all, and admitting nothing is a valid restriction of any
	// base that is itself allowed to match nothing.
	//
	// The table has no row for "no particle", which is why omitting this
	// lands such a derivation in the Forbidden fallthrough instead. mgE014
	// pins it: <sequence><element name="title" minOccurs="0"
	// maxOccurs="0"/></sequence> restricting <sequence><element
	// name="title" minOccurs="0"/></sequence>. The 0..0 element maps to no
	// component at all (§3.9.2, "it maps to no component at all"), leaving
	// R an empty sequence, while B's single-member sequence collapses to
	// the bare optional element — a group against an element declaration,
	// which the table forbids. Both W3C versions expect it valid.
	if particleIgnorable(r) {
		if !particleEmptiable(b) {
			return fmt.Errorf(
				"the restriction has no content but the base requires %s",
				particleKind(b))
		}
		return nil
	}

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
			return particleRestricts(r, sub, true, v)
		}
	}

	// XSD 1.1 replaced the structural table below with a single semantic
	// constraint, Content type restricts (Complex Content) (§3.4.6.4):
	// every sequence locally valid against R must be locally valid against
	// B. That is language inclusion, and it accepts derivations the 1.0
	// table calls Forbidden — a choice or a wildcard restricting an all
	// group, an all group reordered, one base particle covered by several
	// derived ones.
	//
	// allSubsumes decides that inclusion exactly for the shape that
	// accounts for the whole 1.1 all-group cluster: a base all group whose
	// particles are element declarations. It returns notApplicable for
	// anything outside that shape, so wildcards and non-all bases keep the
	// 1.0 table, which stays sound under 1.1 — the table is a conservative
	// approximation of the same inclusion, never wrong in the accepting
	// direction.
	if v >= Version11 {
		if err, ok := allSubsumes(r, b); ok {
			return err
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
			return recurseAsIfGroup(r, b, bt, expanded, v)
		}
	case *Wildcard:
		switch bt := b.Term.(type) {
		case *Wildcard:
			return nsSubset(r, rt, b, bt)
		}
	case *ModelGroup:
		switch bt := b.Term.(type) {
		case *Wildcard:
			return nsRecurseCheckCardinality(r, rt, b, bt, expanded, v)
		case *ModelGroup:
			switch {
			case rt.Compositor == CompositorAll && bt.Compositor == CompositorAll,
				rt.Compositor == CompositorSequence && bt.Compositor == CompositorSequence:
				return recurse(r, rt, b, bt, expanded, v)
			case rt.Compositor == CompositorChoice && bt.Compositor == CompositorChoice:
				return recurseLax(r, rt, b, bt, expanded, v)
			case rt.Compositor == CompositorSequence && bt.Compositor == CompositorAll:
				return recurseUnordered(r, rt, b, bt, expanded, v)
			case rt.Compositor == CompositorSequence && bt.Compositor == CompositorChoice:
				return mapAndSum(r, rt, b, bt, expanded, v)
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

// particleIgnorable reports whether clause 2.2.1 erases this particle
// entirely: an empty <sequence> or <all>, or an empty <choice> whose
// containing particle may occur zero times.
//
// The emptiness that matters is emptiness after the same erasure, because a
// sequence holding nothing but an empty sequence is itself empty. Erasing is
// not the same as being ·emptiable·: an optional element is emptiable but is
// still a particle the table has a row for, and collapsing it here would
// discard the name check that makes a restriction meaningful.
func particleIgnorable(p *Particle) bool {
	g, ok := p.Term.(*ModelGroup)
	if !ok {
		return false
	}
	for _, child := range g.Particles {
		if !particleIgnorable(child) {
			return false
		}
	}
	// A <choice> with no alternatives matches nothing at all rather than
	// the empty sequence, so the spec erases it only when the containing
	// particle is free to skip it. A <sequence> or <all> with no members
	// already matches the empty sequence, whatever its range.
	if g.Compositor == CompositorChoice && len(g.Particles) == 0 {
		return p.MinOccurs == 0
	}
	return true
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
func recurseAsIfGroup(r *Particle, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
	wrapped := &Particle{
		MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: bg.Compositor, Particles: []*Particle{r}},
	}
	switch bg.Compositor {
	case CompositorChoice:
		return recurseLax(wrapped, wrapped.Term.(*ModelGroup), b, bg, expanded, v)
	default:
		return recurse(wrapped, wrapped.Term.(*ModelGroup), b, bg, expanded, v)
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
func nsRecurseCheckCardinality(r *Particle, rg *ModelGroup, b *Particle, bw *Wildcard, expanded bool, v Version) error {
	// Clause 1 says each member must be a valid restriction of "the
	// wildcard" — the {term}, not the particle B. A wildcard term carries a
	// namespace constraint and a processContents, but no occurrence range;
	// the range lives on the particle that contains it. So clause 1 has no
	// occurrence range to compare against, and the only thing it can
	// meaningfully test is namespace and processContents compatibility.
	//
	// Comparing against B's own range instead rejects valid schemas
	// (particlesHa070: three unit-occurrence elements under a 3..3 wildcard,
	// each of which individually "fails" 1 < 3). Substituting a
	// unit-occurrence 1..1 copy is equally wrong in the other direction, and
	// is what the spec's own test suite rules out: particlesHa080 has a base
	// wildcard of 2..3 restricted by a three-member sequence, where a 1..1
	// comparison passes each member yet the suite marks the schema valid
	// only because the *total* is 3.
	//
	// All the counting belongs to clause 2, and clause 2 is sufficient for
	// it. The discriminating pairs in the suite confirm this: Ha070 (total
	// 3, valid) against Ha071 (total 2, invalid) share a 3..3 base wildcard,
	// and Ha080 (total 3 under 2..3, valid) against Ha081 (total 3 under
	// 2..2, invalid) differ only in the base range. In every case the
	// effective total range decides correctly and a per-member range check
	// decides wrongly.
	//
	// An unbounded range makes Occurrence Range OK vacuous for any member,
	// which leaves exactly the namespace test clause 1 is for.
	anyOccurs := &Particle{MinOccurs: 0, MaxOccurs: Unbounded, Term: bw}
	for _, member := range rg.Particles {
		if err := particleRestricts(member, anyOccurs, expanded, v); err != nil {
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
func recurse(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("%s group: %w", rg.Compositor, err)
	}
	bi := 0
	for _, rp := range rg.Particles {
		matched := false
		for bi < len(bg.Particles) {
			bp := bg.Particles[bi]
			bi++
			if particleRestricts(rp, bp, expanded, v) == nil {
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
func recurseLax(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
	if err := occurrenceRangeOK(r, b); err != nil {
		return fmt.Errorf("choice group: %w", err)
	}
	bi := 0
	for _, rp := range rg.Particles {
		matched := false
		for bi < len(bg.Particles) {
			bp := bg.Particles[bi]
			bi++
			if particleRestricts(rp, bp, expanded, v) == nil {
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
func recurseUnordered(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
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
			if particleRestricts(rp, bp, expanded, v) == nil {
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
func mapAndSum(r *Particle, rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
	for _, rp := range rg.Particles {
		matched := false
		for _, bp := range bg.Particles {
			if particleRestricts(rp, bp, expanded, v) == nil {
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

// allSubsumes decides Content type restricts (Complex Content) (§3.4.6.4) for
// the case that dominates the 1.1 all-group tests: a base <all> group whose
// particles are all element declarations.
//
// The second result reports whether the rule applied at all. When it is false
// the caller falls back to the 1.0 structural table, which remains sound: the
// table is a conservative approximation of this same language inclusion, so
// everything it accepts is genuinely included.
//
// Why this shape is decidable cheaply. An <all> group's particles are
// unordered and each names a distinct element (Unique Particle Attribution
// forbids two particles of one all group matching the same name), so the base
// is exactly a per-name occurrence budget: name n may appear between min and
// max times, in any order, and no other name may appear. A sequence of
// elements is therefore locally valid against B if and only if, for every
// name, its total count lies in that name's budget. Order is irrelevant, which
// is what makes counting sufficient and why no automaton is needed.
//
// R is then included in B exactly when every branch through R produces counts
// inside every budget. allBranchCounts enumerates the branches.
func allSubsumes(r, b *Particle) (error, bool) {
	bg, ok := b.Term.(*ModelGroup)
	if !ok || bg.Compositor != CompositorAll {
		return nil, false
	}
	// A base particle that is not an element declaration — a wildcard, or
	// a nested group — has no single name to budget, and a wildcard can
	// absorb names belonging to other buckets, which makes the assignment
	// ambiguous rather than a count. all244 is the case that punishes
	// guessing: a derived wildcard spanning two base buckets is invalid
	// precisely because of how its occurrences may be split between them.
	budget := map[xdm.QName]*occBudget{}
	for _, bp := range bg.Particles {
		bd, ok := bp.Term.(*ElementDecl)
		if !ok {
			return nil, false
		}
		if _, dup := budget[bd.Name]; dup {
			return nil, false
		}
		budget[bd.Name] = &occBudget{decl: bd, min: bp.MinOccurs, max: bp.MaxOccurs}
	}

	branches, ok := allBranchCounts(r)
	if !ok {
		return nil, false
	}
	for _, br := range branches {
		if err := branchFitsBudget(br, budget); err != nil {
			return err, true
		}
	}
	// Clause 1 also requires that R admit nothing B forbids by way of
	// *absence*: a name whose budget has a non-zero minimum must be
	// produced by every branch, which branchFitsBudget checks, since a
	// name a branch never mentions has count zero.
	return nil, true
}

// occBudget is one base all-group particle seen as an occurrence budget for a
// single element name.
type occBudget struct {
	decl     *ElementDecl
	min, max int
}

// branchCount is the number of times each element name occurs along one branch
// through the derived content model, as a range because a branch still
// contains its own optional and repeated particles.
type branchCount map[xdm.QName]*occRange

type occRange struct{ min, max int }

// add accumulates n more occurrences of a name, propagating unbounded.
func (c branchCount) add(name xdm.QName, min, max int) {
	cur, ok := c[name]
	if !ok {
		cur = &occRange{}
		c[name] = cur
	}
	cur.min += min
	if cur.max == Unbounded || max == Unbounded {
		cur.max = Unbounded
		return
	}
	cur.max += max
}

// allBranchCounts enumerates the branches through a derived particle, each as
// a per-name occurrence range.
//
// A <choice> forks — each alternative is its own branch, which is what lets
// all231 and all232 restrict an all group with a choice: every branch is
// independently checked, and all233 is rejected because only its third branch
// exceeds a budget. A <sequence> or <all> concatenates, summing the counts of
// its members, which is what makes all216 (the same name taken twice) and
// all234 (one name split across two particles) come out right.
//
// The second result is false when the model contains something this counting
// cannot describe — a wildcard, or an element with no resolvable name — in
// which case the caller falls back to the structural table.
func allBranchCounts(p *Particle) ([]branchCount, bool) {
	if p.MaxOccurs == 0 {
		return []branchCount{{}}, true
	}
	switch t := p.Term.(type) {
	case *ElementDecl:
		c := branchCount{}
		c.add(t.Name, p.MinOccurs, p.MaxOccurs)
		return []branchCount{c}, true

	case *ModelGroup:
		inner, ok := groupBranchCounts(t)
		if !ok {
			return nil, false
		}
		// Repeating a branch multiplies every count in it. An
		// unbounded repetition makes every name it can produce
		// unbounded, which is what stops a repeated group from
		// sneaking past a finite budget.
		out := make([]branchCount, 0, len(inner))
		for _, br := range inner {
			scaled := branchCount{}
			for name, rng := range br {
				min := rng.min * p.MinOccurs
				max := rng.max
				switch {
				case max == Unbounded || p.MaxOccurs == Unbounded:
					if max != 0 {
						max = Unbounded
					}
				default:
					max *= p.MaxOccurs
				}
				scaled[name] = &occRange{min: min, max: max}
			}
			out = append(out, scaled)
		}
		return out, true
	}
	// A wildcard cannot be counted per name.
	return nil, false
}

// groupBranchCounts enumerates the branches of a model group's particles.
//
// The number of branches is the product of the choice arities on the path, so
// a deeply nested model could in principle blow up; branchLimit caps it and
// falls back to the structural table rather than spending unbounded time.
const branchLimit = 4096

func groupBranchCounts(g *ModelGroup) ([]branchCount, bool) {
	if g.Compositor == CompositorChoice {
		if len(g.Particles) == 0 {
			return []branchCount{{}}, true
		}
		var out []branchCount
		for _, child := range g.Particles {
			brs, ok := allBranchCounts(child)
			if !ok {
				return nil, false
			}
			out = append(out, brs...)
			if len(out) > branchLimit {
				return nil, false
			}
		}
		return out, true
	}

	// <sequence> and <all> both concatenate: the counts of the members
	// add. They differ only in the order elements may appear, and order is
	// exactly what a per-name count discards — which is sound here because
	// the base is an all group, whose language is likewise order-blind.
	out := []branchCount{{}}
	for _, child := range g.Particles {
		brs, ok := allBranchCounts(child)
		if !ok {
			return nil, false
		}
		if len(out)*len(brs) > branchLimit {
			return nil, false
		}
		next := make([]branchCount, 0, len(out)*len(brs))
		for _, base := range out {
			for _, add := range brs {
				merged := branchCount{}
				for n, r := range base {
					merged[n] = &occRange{min: r.min, max: r.max}
				}
				for n, r := range add {
					merged.add(n, r.min, r.max)
				}
				next = append(next, merged)
			}
		}
		out = next
	}
	return out, true
}

// branchFitsBudget checks one branch of the derived model against the base's
// per-name budgets.
func branchFitsBudget(br branchCount, budget map[xdm.QName]*occBudget) error {
	for name, rng := range br {
		bud := budget[name]
		if bud == nil {
			// A name the base's all group never mentions cannot
			// appear, however few times. all205 and all215 pin
			// this.
			return fmt.Errorf(
				"element %s may occur in the restriction but the base's all group does not allow it",
				name.Local)
		}
		if rng.max == 0 {
			continue
		}
		if err := rangeOK(rng.min, rng.max, bud.min, bud.max); err != nil {
			return fmt.Errorf("element %s: %w", name.Local, err)
		}
	}
	// A budget with a non-zero minimum must be met by this branch, even
	// when the branch never mentions the name at all. all204 and all214
	// drop a required element that way.
	for name, bud := range budget {
		if bud.min == 0 {
			continue
		}
		if rng, ok := br[name]; !ok || rng.max == 0 {
			return fmt.Errorf(
				"the base requires element %s at least %d times, which the restriction omits",
				name.Local, bud.min)
		}
	}
	return nil
}
