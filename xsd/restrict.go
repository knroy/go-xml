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
	// allComplexTypes rather than Types: the constraint is a property of
	// every complex type component, and an inline <xs:complexType> inside
	// an element declaration is a component like any other. Types holds
	// only the named ones, so walking it skipped every restriction written
	// anonymously — which is how the suite writes most of them. The slice
	// is in document order, so the errors below stay stable between runs
	// where a map walk would not.
	for _, ct := range p.schema.allComplexTypes {
		if ct.DerivationMethod != DerivationRestriction {
			continue
		}
		errs = append(errs, attributeTypeRestrictions(ct)...)
		name := ct.Name
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
		// derivation-ok-restriction clauses 4 and 5 split on the pair
		// of *content kinds* before any particle is compared. A base
		// with simple content admits character data, and neither an
		// empty content type (clause 5.2) nor an element-only one
		// (clause 5.4) is a subset of that: only simple content, or a
		// mixed content type that is emptiable, may restrict it.
		// particlesZ039 restricts a simpleContent base with
		// <complexContent><restriction><sequence/></restriction>, an
		// empty content type, which the base's character data is not a
		// superset of.
		// Clause 5.2.2.1: where both content types are simple, the
		// derived one must be validly derived from the base's. Naming
		// an inline <simpleType> inside a simpleContent restriction is
		// otherwise unchecked, so particlesZ018 restricted a decimal
		// content type with a list of xs:int — a type that shares no
		// base with it at all.
		if base.Content == ContentSimple && ct.Content == ContentSimple &&
			ct.SimpleContent != nil && base.SimpleContent != nil &&
			!typeRestricts(ct.SimpleContent, base.SimpleContent) {
			errs = append(errs, fmt.Errorf(
				"derivation-ok-restriction.5.2.2.1: %s gives its simple content "+
					"a type that is not a restriction of base %s's",
				typeLabel(name, ct), typeLabel(base.Name, base)))
			continue
		}
		if base.Content == ContentSimple && ct.Content != ContentSimple {
			errs = append(errs, fmt.Errorf(
				"derivation-ok-restriction.5: %s has %s content but its base %s "+
					"has simple content", typeLabel(name, ct), ct.Content,
				typeLabel(base.Name, base)))
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
	// The walk above is in document order, but a schema reached through
	// several <xs:include>s contributes its types in file order rather than
	// any order the author would recognise, so the list is sorted anyway.
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	sort.Strings(msgs)
	return &SchemaErrors{Errors: sortedErrors(msgs)}
}

// attributeTypeRestrictions is derivation-ok-restriction clause 2.1.2 (§3.4.6):
// where a restriction redeclares an attribute its base already declares, the
// type it gives that attribute must be a valid restriction of the type the
// base gave it.
//
// checkAttributeRestriction in parse_decl.go enforces the rest of clause 2 —
// use, {inheritable} and the value constraint — but deliberately leaves this
// sub-clause alone, on the grounds that the type-derivation machinery lives
// here and a second, weaker copy of it would risk disagreeing with the first.
// So it is implemented here, against the same typeRestricts the particle rules
// use, rather than duplicated there.
//
// The consequence of the gap was that a restriction could give an attribute a
// *wider* type than its base and still load. particlesZ013 is the shape:
// CT2 restricts CT1 and redeclares att1, narrowing it from xs:integer to a
// union of float, integer, boolean and an enumerated string — which admits
// every integer the base did and a great deal else besides, so the "restricted"
// type accepts documents the base rejects.
//
// typeRestricts already answers the question correctly for the cases that
// matter: it walks the base chain, treats xs:anyType as the universal base,
// and accepts a derivation from any member of a union. An attribute whose type
// is missing on either side is skipped rather than guessed at — an unresolved
// type reference is reported by the normal resolution path, and inventing a
// second error for it here would only obscure that one.
func attributeTypeRestrictions(ct *ComplexType) []error {
	base, ok := ct.Base.(*ComplexType)
	if !ok || base == ct || isUrType(base) {
		return nil
	}
	baseType := make(map[xdm.QName]Type, len(base.AttributeUses))
	for _, u := range base.AttributeUses {
		if u.Decl != nil && u.Decl.Type != nil {
			baseType[u.Decl.Name] = u.Decl.Type
		}
	}
	var errs []error
	// Clause 4: where both types have an attribute wildcard, the derived
	// one must be an intensional subset of the base's, and — by errata
	// E1-21 — may not weaken its processContents. The same two tests
	// nsSubset applies to element wildcards, minus the occurrence range an
	// attribute wildcard does not have. errC009 restricts a strict
	// anyAttribute with a skip one, which lets through attributes the base
	// insists on validating.
	if rw, bw := ct.AttributeWildcard, base.AttributeWildcard; rw != nil && bw != nil {
		switch {
		case !wildcardSubset(rw, bw):
			errs = append(errs, fmt.Errorf(
				"derivation-ok-restriction.4: %s gives its attribute wildcard a "+
					"namespace constraint that is not a subset of %s's",
				typeLabel(ct.Name, ct), typeLabel(base.Name, base)))
		case processStrength(rw.ProcessContents) < processStrength(bw.ProcessContents):
			errs = append(errs, fmt.Errorf(
				"derivation-ok-restriction.4: %s gives its attribute wildcard "+
					"processContents %s, weaker than %s's %s",
				typeLabel(ct.Name, ct), rw.ProcessContents,
				typeLabel(base.Name, base), bw.ProcessContents))
		}
	}
	for _, u := range ct.AttributeUses {
		if u.Decl == nil || u.Decl.Type == nil || u.Prohibited {
			continue
		}
		want, inBase := baseType[u.Decl.Name]
		if !inBase {
			// Clause 2.2 governs an attribute the base never declared;
			// it is checked in parse_decl.go and is not this clause's
			// business.
			continue
		}
		if typeRestricts(u.Decl.Type, want) {
			continue
		}
		errs = append(errs, fmt.Errorf(
			"derivation-ok-restriction.2.1.2: %s gives attribute %s a type "+
				"that is not a restriction of the type %s gives it",
			typeLabel(ct.Name, ct), u.Decl.Name.Local,
			typeLabel(base.Name, base)))
	}
	return errs
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
	// Under 1.1 a one-member choice keeps its identity when the base is also
	// a choice, because the table dispatches on the compositor and the pair
	// decides the cell. Against any other base the choice has nothing to
	// preserve and stripping it is what lets the derivation reach a cell at
	// all: particlesR001 restricts a sequence-with-wildcard by a one-member
	// choice, which is valid only once the choice is gone.
	keepChoice := v >= Version11 && bothChoices(r, b)
	r = stripPointless(r, keepChoice)
	b = stripPointless(b, keepChoice)

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
		// The general form of the same 1.1 constraint, decided by
		// language inclusion where the models can be unrolled exactly.
		// It declines for an all group, a recursive model or a range
		// too wide to unroll, and the table below then answers.
		if err, ok := particleSubsumes(r, b); ok {
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
func stripPointless(p *Particle, keepChoice bool) *Particle {
	for {
		g, ok := p.Term.(*ModelGroup)
		if !ok || len(g.Particles) != 1 {
			return p
		}
		// Under 1.1 a one-member *choice* keeps its identity. The table
		// dispatches on the compositor, so stripping one turns a
		// choice-restricting-choice derivation into a sequence restricting a
		// choice — a different cell with a different rule. particlesZ023 and
		// Z024 are exactly that: a derived <choice> holding one three-element
		// sequence, restricting a base <choice> of two such sequences. Read
		// as Recurse the derivation drops one alternative and is valid; read
		// as MapAndSum it is rejected for "maxOccurs 3 exceeds the base's 1",
		// the three elements having been summed against a choice that admits
		// one branch.
		//
		// Under 1.0 the stripping is *correct* and the derivation really is
		// invalid: the suite marks Z023 and Z024 invalid under 1.0 and valid
		// under 1.1, which is the 1.1 relaxation from a structural table to
		// language inclusion. Removing the strip unconditionally therefore
		// fixed two 1.1 cases and broke the same two under 1.0.
		if keepChoice && g.Compositor == CompositorChoice {
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

// inlineSameCompositor is the other half of clause 2.2: a <sequence> directly
// inside a <sequence>, or a <choice> inside a <choice>, with unit occurrence,
// is pointless and its members belong to the parent's list.
//
// stripPointless only unwraps a group that *is* the particle being compared;
// it cannot see a pointless wrapper sitting among a group's members. Without
// this the two sides of a Recurse land at different depths and the walk
// compares R's elements against B's groups. groupB003 is the case: R is
// <sequence><group ref="g1"/></sequence> and B is <sequence><group
// ref="g1"/><group ref="g2" minOccurs="0"/></sequence>, where g1 is itself a
// sequence. R unwraps to g1's bare <sequence>, so its members are elements,
// while B keeps g1 as a nested sequence — and r1 is compared against the whole
// group. Inlining g1 into B puts both sides on the same footing.
//
// Only a wrapper with the *same* compositor is inlined. A choice inside a
// sequence orders nothing the sequence orders and flattening it would change
// the language; the clause names only the matching pair.
func inlineSameCompositor(g *ModelGroup) []*Particle {
	if g.Compositor != CompositorSequence && g.Compositor != CompositorChoice {
		return g.Particles
	}
	changed := false
	for _, p := range g.Particles {
		if inlinable(p, g.Compositor) {
			changed = true
			break
		}
	}
	if !changed {
		return g.Particles
	}
	out := make([]*Particle, 0, len(g.Particles))
	for _, p := range g.Particles {
		if inlinable(p, g.Compositor) {
			out = append(out, inlineSameCompositor(p.Term.(*ModelGroup))...)
			continue
		}
		out = append(out, p)
	}
	return out
}

// inlinable reports whether a member particle is a pointless wrapper of the
// containing compositor.
func inlinable(p *Particle, outer Compositor) bool {
	if p.MinOccurs != 1 || p.MaxOccurs != 1 {
		return false
	}
	g, ok := p.Term.(*ModelGroup)
	return ok && g.Compositor == outer
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
		if !fixedConstraintsAgree(bd.Constraint, rd.Constraint, rd.Type) {
			return fmt.Errorf(
				"element %s must keep the base's fixed value %q",
				rd.Name.Local, bd.Constraint.Lexical)
		}
	}
	// Clause 3.2.4: R's {disallowed substitutions} must be a *superset* of
	// B's. block= says which derivations may not stand in for this element
	// at validation time, so a restriction that blocks less than its base
	// admits substitutes the base refuses — widening rather than narrowing.
	//
	// The comparison is over the whole set including #all, which is why the
	// bitmask is tested directly rather than member by member: particlesIg006
	// blocks "#all" in the base and only "substitution extension" in the
	// restriction, which is exactly the missing member the clause forbids.
	if blockSet(bd.DisallowedSubstitutions)&^blockSet(rd.DisallowedSubstitutions) != 0 {
		return fmt.Errorf(
			"element %s does not block everything the base blocks (base %q, restriction %q)",
			rd.Name.Local, bd.DisallowedSubstitutions, rd.DisallowedSubstitutions)
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

// blockSet narrows a {disallowed substitutions} value to the three derivations
// block= can actually name, so that #all and the explicit list compare equal.
//
// All is stored as 0xff, a mask wide enough to hold list and union too. Those
// are simple-type derivations that never substitute for an element, so block=
// cannot name them and #all does not really mean them either. Comparing the
// raw masks would make block="#all" a strict superset of
// block="substitution extension restriction" when the two name the same set —
// and that is precisely W3C bug 4144: particlesIg004 writes exactly that pair
// and the working group ruled the schema valid. Ig006 keeps the rule honest by
// omitting "restriction" from the list, which really is a missing member.
func blockSet(s DerivationSet) DerivationSet {
	const blockable = DerivationSet(uint8(DerivationSubstitution) |
		uint8(DerivationExtension) | uint8(DerivationRestriction))
	return s & blockable
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
	// A member of a union stands in for the union itself, but only while
	// the union is *pure* — its {facets} empty. A union derived by
	// restriction constrains its members further, and the member type on
	// its own carries none of that: substituting it would admit values the
	// restricted union rejects. saxonData simple014 restricts a union with
	// a pattern and then names a bare member in the derived element;
	// simple015 does the same one level down, where the member named is
	// itself a member of a restricted union.
	//
	// Purity is checked at every step of the walk, not only at the top,
	// because the substitutability has to survive the whole chain: a pure
	// union whose member is a restricted union is no more substitutable
	// than the restricted union was.
	if st, ok := want.(*SimpleType); ok && st.Variety == VarietyUnion &&
		unionIsPure(st) {
		for _, m := range st.MemberTypes {
			if m != nil && typeRestricts(t, m) {
				return true
			}
		}
	}
	// The walk stops on self as well as nil: the ur-type is its own base
	// and a chain testing only for nil would not terminate.
	//
	// Clause 3.2.5 names {extension, list, union} as the *disallowed*
	// derivations, so an extension step anywhere on the way up breaks the
	// chain: particlesIj008 restricts an element whose type in the base is
	// "foo" with one whose type is "bar", and bar extends foo. Reaching foo
	// from bar proves derivation, not derivation by restriction, and an
	// extension may add content the base's element would reject.
	for cur := t; cur != nil; {
		if cur == want {
			return true
		}
		if ct, ok := cur.(*ComplexType); ok &&
			ct.DerivationMethod == DerivationExtension {
			return false
		}
		next := cur.BaseType()
		if next == cur {
			break
		}
		cur = next
	}
	return false
}

// unionIsPure reports whether a union type constrains nothing of its own, so
// that any one of its members may stand in for it.
//
// The union's own facet set has to be empty, and so does every set inherited
// from a union it was itself restricted from: the facets of a derivation chain
// accumulate, and a member is unconstrained by all of them alike. The walk
// stops when it leaves the union variety, which is where xs:anySimpleType sits.
func unionIsPure(st *SimpleType) bool {
	for cur := st; cur != nil && cur.Variety == VarietyUnion; {
		if !cur.Facets.IsEmpty() {
			return false
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	return true
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
	inner, outerMin, outerMax := r, 1, 1
	// An *optional* element against a non-repeating choice puts its own range
	// on the wrapper. particlesHa161 is <element name="a" minOccurs="0"/>
	// restricting <choice minOccurs="0"> whose branches are 1..1: comparing
	// the element's 0..1 against a branch's 1..1 rejects a valid derivation,
	// because the optionality belongs to the choice, not to the alternative
	// inside it.
	//
	// Version-gated: Ha161 is marked invalid under 1.0 and valid under 1.1,
	// the same split as the reordered choice above, and relaxing it for both
	// cost two 1.0 cases. Confined further to a base that occurs at most
	// once, and to a derived particle that occurs at most once. The derived
	// minimum must also already satisfy the base's: without that, moving a
	// minOccurs of 0 onto the wrapper made it violate a base requiring 1,
	// which turned ctF007 into a false reject for exactly one case gained. Where the base *repeats*, moving the range is
	// what broke particlesV020: the wrapper's range also feeds
	// effectiveTotalRange, where a group of one repeating N times contributes
	// N elements, so one range cannot serve both uses. A non-repeating base
	// has no such sum to get wrong.
	if v >= Version11 && bg.Compositor == CompositorChoice &&
		b.MaxOccurs != Unbounded && b.MaxOccurs <= 1 &&
		r.MaxOccurs != Unbounded && r.MaxOccurs <= 1 &&
		r.MinOccurs >= b.MinOccurs {
		outerMin, outerMax = r.MinOccurs, r.MaxOccurs
		inner = &Particle{MinOccurs: 1, MaxOccurs: 1, Term: r.Term}
	}
	wrapped := &Particle{
		MinOccurs: outerMin, MaxOccurs: outerMax,
		Term: &ModelGroup{Compositor: bg.Compositor, Particles: []*Particle{inner}},
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
	if !disallowedNamesSubset(sub, super) {
		return false
	}
	// Clause 1: everything is a subset of ##any.
	if super.Kind == NSAny {
		return true
	}
	switch sub.Kind {
	case NSAny:
		// ##any is a subset only of ##any, already handled.
		return false
	case NSNot:
		// Clause 2: a negation is a subset of another negation exactly
		// when it excludes at least as much.
		//
		// In XSD 1.0 a negation names a single namespace, so "excludes
		// at least as much" collapses to "excludes the same one", and
		// the rule was written as set equality. XSD 1.1's
		// notNamespace names a *set*, and there the relation is
		// contravariant: not-S1 admits everything outside S1, so
		// not-S1 is a subset of not-S2 exactly when S2 is a subset of
		// S1 — the *larger* exclusion set is the smaller wildcard.
		//
		// Equality is the special case where each contains the other,
		// so this subsumes the 1.0 reading rather than replacing it,
		// and needs no version gate. Reading it as equality refuses
		// every genuine narrowing of a 1.1 notNamespace, such as
		// restricting not-{cain, abel, adam} to not-{adam}.
		if super.Kind != NSNot {
			return false
		}
		for _, ns := range super.Namespace {
			if !containsNamespace(sub.Namespace, ns) {
				return false
			}
		}
		// Excluding the absent namespace narrows in the same
		// direction: sub may exclude it where super does not, but not
		// the reverse.
		return !(super.ExcludesAbsent && !sub.ExcludesAbsent)
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

// disallowedNamesSubset is the {disallowed names} half of Wildcard Subset
// (§3.10.6): every name the superset refuses must also be refused by the
// subset.
//
// The namespace clauses above compare only {namespace constraint}, and until
// this was added they decided the whole question — so a subset could quietly
// re-admit a name the superset had excluded by notQName. That is the wrong
// direction for a restriction, and it slipped through in the one case the
// namespace clauses answer without looking: everything is a subset of ##any,
// and a ##any base carrying notQName="##defined" is exactly wild057's base.
//
// A name in super's list need not appear in sub's list, though: it is enough
// that sub cannot admit it at all. wild055 is the case — its base disallows
// xml:space, its subset does not name it, and the subset's namespace
// constraint is ##local, which excludes the xml namespace outright. So the
// test is "disallowed by sub, or unreachable through sub's namespace", not
// list containment. wild050 makes the same point twice over with a two-branch
// restriction, and wild051 is wild050 plus one base-disallowed name that one
// of those branches does admit, which is the only difference between the two
// and the reason one is valid and the other is not.
//
// ##defined is not a set of names but a standing rule -- "whatever the schema
// declares" -- and no finite notQName list implies it, since the schema may
// always declare one more. So it transfers only to a subset that also writes
// ##defined. wild057's subset replaces the base's ##defined with the two names
// the schema happens to declare today, which does not restrict the base's
// wildcard so much as freeze it, and is expected invalid; wild055 and wild056
// keep ##defined and are expected valid. ##definedSibling is the same rule
// scoped to one content model, and carries across the same way.
func disallowedNamesSubset(sub, super *Wildcard) bool {
	if super.DisallowDefined && !sub.DisallowDefined {
		return false
	}
	if super.DisallowDefinedSibling && !sub.DisallowDefinedSibling {
		return false
	}
	for _, name := range super.DisallowedNames {
		if !sub.Allows(name.URI) {
			continue
		}
		if !containsQName(sub.DisallowedNames, name) {
			return false
		}
	}
	return true
}

func containsQName(set []xdm.QName, name xdm.QName) bool {
	for _, n := range set {
		if n == name {
			return true
		}
	}
	return false
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
	// Clause 2.2 again, this time on the member lists: a same-compositor
	// wrapper among the members is pointless, and leaving it in place makes
	// the walk compare one side's elements against the other's groups.
	rps := inlineSameCompositor(rg)
	bps := inlineSameCompositor(bg)
	bi := 0
	for _, rp := range rps {
		matched := false
		for bi < len(bps) {
			bp := bps[bi]
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
	for ; bi < len(bps); bi++ {
		if !particleEmptiable(bps[bi]) {
			return fmt.Errorf(
				"%s group: the base requires %s, which the restriction omits",
				rg.Compositor, particleKind(bps[bi]))
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
	// Under 1.1 the alternatives may be reordered. A choice imposes no order
	// on what it admits, so a derived choice offering the base's alternatives
	// in a different sequence accepts exactly the same language — which is
	// what 1.1's Content type restricts (Complex Content) asks about.
	//
	// 1.0's RecurseLax is written as an order-preserving walk and really does
	// forbid it: particlesT002 and T009 are marked invalid under 1.0 and valid
	// under 1.1, and they are the base's two alternatives swapped. So the
	// relaxation is version-gated, exactly like the one-member-choice strip.
	if v >= Version11 {
		return recurseLaxUnordered(rg, b, bg, expanded, v)
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

// recurseLaxUnordered is recurseLax without the ordering requirement.
//
// Each derived alternative must be a restriction of *some* base alternative,
// and each base alternative may be used once — a derived choice may drop
// alternatives but not merge two of its own onto one of the base's, since that
// would let it admit a sequence twice where the base admits it once.
//
// The assignment is a bipartite matching, done greedily with a retry: taking
// the first free base alternative that fits is enough when each derived
// alternative fits only one, and the fallback scan handles the case where an
// earlier choice consumed the only match a later one had.
func recurseLaxUnordered(rg *ModelGroup, b *Particle, bg *ModelGroup, expanded bool, v Version) error {
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
	// The budget is the range over a whole match of B, not over one
	// occurrence of B's group. When the group itself may be skipped — B's
	// {min occurs} is 0 — the empty sequence is locally valid against B, so
	// every member's floor is 0 however the member spells its own
	// minOccurs. Reading the member's minOccurs as the budget's floor made
	// an <all minOccurs="0"> require its content, and rejected mgO029,
	// whose derived and base groups are textually identical. The derived
	// side is already scaled this way by allBranchCounts, so this only
	// restores the symmetry the two sides must share.
	baseSkippable := b.MinOccurs == 0
	budget := map[xdm.QName]*occBudget{}
	for _, bp := range flattenAllGroups(bg.Particles) {
		bd, ok := bp.Term.(*ElementDecl)
		if !ok {
			return nil, false
		}
		if _, dup := budget[bd.Name]; dup {
			return nil, false
		}
		bMin := bp.MinOccurs
		if baseSkippable {
			bMin = 0
		}
		b := &occBudget{decl: bd, min: bMin, max: bp.MaxOccurs}
		budget[bd.Name] = b
		// A base particle naming a substitution group head stands for a
		// choice over the whole group (clause 2.1), so every member
		// draws on the head's budget — and they draw on *one* budget,
		// which is what makes their occurrences sum against it.
		//
		// all221 is the case: the base allows <a> 10 to 20 times, and
		// the restriction offers A1 and A2, each 6 to 8. Neither fits
		// alone; together 12..16 sits inside 10..20. Keying the budget
		// by exact name only, the members matched nothing at all and
		// four valid schemas were rejected (all221, all222, all225,
		// all226).
		//
		// An abstract head is excluded because it cannot itself appear,
		// matching asSubstitutionChoice.
		if bd.Scope == ScopeGlobal {
			for _, m := range bd.substitutable {
				if m == bd || m.Abstract {
					continue
				}
				if _, dup := budget[m.Name]; dup {
					return nil, false
				}
				budget[m.Name] = b
			}
		}
	}

	// Counting says nothing about *what* each occurrence may contain. The
	// 1.1 inclusion is over sequences of typed elements, so a derived
	// declaration keeping a base name while widening its type produces
	// sequences the base rejects however well the counts fit.
	// particlesS010 retypes an all-group member from "address" to
	// "address1", which adds an element; particlesU009 retypes one from
	// xs:NMTOKENS to xs:boolean. Both counted clean.
	for _, rd := range allDerivedDecls(r) {
		bud, ok := budget[rd.Name]
		if !ok || bud.decl == nil {
			continue
		}
		// A member of the head's substitution group is checked against
		// the head, which is the declaration the base actually names.
		if rd == bud.decl {
			continue
		}
		if rd.Type != nil && bud.decl.Type != nil &&
			!typeRestricts(rd.Type, bud.decl.Type) {
			return fmt.Errorf(
				"element %s's type is not derived from the base's",
				rd.Name.Local), true
		}
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

// allDerivedDecls collects every element declaration reachable in a derived
// particle, at any depth and through any compositor.
//
// Order and occurrence are irrelevant here — the caller only wants each
// declaration once, to compare its type against the base's budget for the same
// name.
func allDerivedDecls(p *Particle) []*ElementDecl {
	var out []*ElementDecl
	seen := map[*ElementDecl]bool{}
	var walk func(*Particle, int)
	walk = func(p *Particle, depth int) {
		if p == nil || depth > 64 {
			return
		}
		switch t := p.Term.(type) {
		case *ElementDecl:
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		case *ModelGroup:
			for _, c := range t.Particles {
				walk(c, depth+1)
			}
		}
	}
	walk(p, 0)
	return out
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
	// Several names may share one budget, because a base particle naming a
	// substitution group head budgets every member of the group. Their
	// occurrences are drawn from that single allowance and so are summed
	// before it is consulted — checking each name on its own would reject
	// A1 6..8 and A2 6..8 against a base allowing 10..20, when together
	// they fit exactly (all221).
	spend := map[*occBudget]*occRange{}
	name0 := map[*occBudget]xdm.QName{}
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
		cur, ok := spend[bud]
		if !ok {
			spend[bud] = &occRange{min: rng.min, max: rng.max}
			name0[bud] = name
			continue
		}
		cur.min += rng.min
		if cur.max == Unbounded || rng.max == Unbounded {
			cur.max = Unbounded
		} else {
			cur.max += rng.max
		}
		// br is a map, so which name arrives first is not stable across
		// runs. The name only labels the diagnostic, but a diagnostic
		// that changes between identical runs is a bug of its own, so
		// the lowest-sorting name is chosen rather than the first seen.
		if less := qnameLess(name, name0[bud]); less {
			name0[bud] = name
		}
	}
	// Reported in name order for the same reason: a schema violating two
	// budgets must name the same one every time.
	order := make([]*occBudget, 0, len(spend))
	for bud := range spend {
		order = append(order, bud)
	}
	sort.Slice(order, func(i, j int) bool {
		return qnameLess(name0[order[i]], name0[order[j]])
	})
	for _, bud := range order {
		rng := spend[bud]
		if err := rangeOK(rng.min, rng.max, bud.min, bud.max); err != nil {
			return fmt.Errorf("element %s: %w", name0[bud].Local, err)
		}
	}
	// A budget with a non-zero minimum must be met by this branch, even
	// when the branch never mentions the name at all. all204 and all214
	// drop a required element that way.
	//
	// The budget map is walked by value, not by name, since one budget may
	// be reachable under several names and requiring each of them to meet
	// the minimum on its own would be a different, stricter rule.
	names := make([]xdm.QName, 0, len(budget))
	for name := range budget {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return qnameLess(names[i], names[j]) })
	seen := map[*occBudget]bool{}
	for _, name := range names {
		bud := budget[name]
		if bud.min == 0 || seen[bud] {
			continue
		}
		seen[bud] = true
		if _, ok := spend[bud]; !ok {
			return fmt.Errorf(
				"the base requires element %s at least %d times, which the restriction omits",
				name.Local, bud.min)
		}
	}
	return nil
}

// qnameLess orders expanded names so that a diagnostic naming one of several
// candidates names the same one on every run. Go map iteration is randomised,
// and an error message that changes between identical runs is a bug in its own
// right, however cosmetic.
func qnameLess(a, b xdm.QName) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	return a.Local < b.Local
}

// bothChoices reports whether r and b are both choice groups, ignoring any
// pointless wrapping that stripPointless would remove from a non-choice.
//
// The question is asked before stripping, so a <sequence> holding one <choice>
// counts: that sequence is pointless and the choice underneath it is what the
// table will see.
func bothChoices(r, b *Particle) bool {
	return choiceUnder(r) && choiceUnder(b)
}

func choiceUnder(p *Particle) bool {
	for {
		g, ok := p.Term.(*ModelGroup)
		if !ok {
			return false
		}
		if g.Compositor == CompositorChoice {
			return true
		}
		// Only a pointless wrapper is looked through, matching what
		// stripPointless would itself remove.
		if len(g.Particles) != 1 || p.MinOccurs != 1 || p.MaxOccurs != 1 {
			return false
		}
		p = g.Particles[0]
	}
}

// flattenAllGroups inlines a nested all group into its parent's particle list.
//
// XSD 1.1 §3.8.6 requires a group reference inside an all group to refer to a
// group whose model is itself an all group, and an all group of all groups
// admits exactly the interleaving of their members. So the nesting carries no
// information the flat list does not, and flattening it lets allSubsumes see
// element declarations where it would otherwise find a group particle and give
// up — falling back to the 1.0 table, which calls the derivation Forbidden.
//
// all206 is the case: a base <all> holding <group ref="abc"/> and an element,
// restricted by an <all> holding that element and a narrower group.
//
// Only a nested group occurring exactly once is inlined. A repeating one would
// multiply its members' occurrence ranges, and folding that into the parent
// would compare the wrong budgets — the ambiguity allSubsumes exists to avoid.
func flattenAllGroups(ps []*Particle) []*Particle {
	var out []*Particle
	for _, p := range ps {
		g, ok := p.Term.(*ModelGroup)
		if !ok || g.Compositor != CompositorAll ||
			p.MinOccurs != 1 || p.MaxOccurs != 1 {
			out = append(out, p)
			continue
		}
		out = append(out, flattenAllGroups(g.Particles)...)
	}
	return out
}
