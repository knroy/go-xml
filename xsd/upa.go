package xsd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
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

	// Version selects the UPA rule. XSD 1.1 relaxed the constraint so that
	// an element particle competing with a wildcard is no longer an error;
	// only element-against-element and wildcard-against-wildcard remain.
	// The suite states it outright — the feature category is
	// xsd1_1-Wildcards-RelaxationOfUPA, "wildcard/element competition no
	// longer violates UPA" — and s3_10_1v04s through s3_10_1ii09s are
	// version="1.1" schemaTests expected valid for exactly that shape.
	//
	// The zero value is 1.0, which keeps the stricter rule.
	Version Version
}

// CheckConstraints applies the schema component constraints that are checked
// against a compiled content model: Unique Particle Attribution and Element
// Declarations Consistent.
//
// Loading already applies both — see checkContentModelConstraints — so this
// re-runs work the schema has passed. It remains exported because it is the
// only way to ask for the *permissive* UPA reading after the fact: a caller
// that loaded with Options.LaxUPA set cannot otherwise re-check under the
// strict rule, and a caller holding a Schema from elsewhere may want the
// constraints stated as a list of errors rather than as a load failure.
func (s *Schema) CheckConstraints(opts CheckOptions) error {
	return checkContentModelConstraints(s, opts)
}

// checkContentModelConstraints applies Unique Particle Attribution (§3.8.6)
// and Element Declarations Consistent (§3.8.6) to every complex type.
//
// These run at load time for the same reason Particle Valid (Restriction)
// does — see checkParticleRestriction: both are properties of the schema
// alone, and a document violating either "is not a schema" in the spec's
// terms. The suite is unambiguous that they gate loading, not validation:
// mgR001..mgR022 and mgQ001/mgQ021 are schemaTest cases expected invalid for
// declaring one element name with two types, and mgS002..mgS005 are
// schemaTest cases expected invalid for an ambiguous content model. All 26
// loaded clean while these constraints were opt-in, because nothing in the
// load path ever called them.
//
// The earlier design deferred them on the Xerces schema-full-checking
// precedent. That precedent governs whether a *validator* pays the cost, and
// it is the wrong analogy for a loader asked "is this a schema?": answering
// yes for a schema the spec says does not exist is a false accept, and the
// cost is paid once per load rather than once per document.
func checkContentModelConstraints(s *Schema, opts CheckOptions) error {
	var errs []error

	// The content models to check are those of every named type *and* of
	// every anonymous type declared inline in an element declaration.
	//
	// Walking only s.Types missed the inline case entirely: a schema whose
	// only complex type is anonymous — <xs:element name="foo"> with the
	// complexType nested inside it, which is the ordinary spelling for a
	// one-off model — had UPA and Element Declarations Consistent checked
	// against nothing at all. The suite's wildI009, wildI010, wildI013 and
	// wildI014 are exactly that shape: two competing wildcards inside an
	// inline complexType, expected invalid and silently accepted.
	//
	// Anonymous types are keyed by the element that owns them so the error
	// can name a place the author recognises, and the element names are
	// sorted for the same reason the error list below is: a map walk that
	// decides which of two faults is reported makes the loader's answer
	// depend on hash order.
	type modelSite struct {
		particle *Particle
		where    string
	}
	sites := make([]modelSite, 0, len(s.Types)+len(s.Elements))

	typeNames := make([]xdm.QName, 0, len(s.Types))
	for name := range s.Types {
		typeNames = append(typeNames, name)
	}
	sortQNames(typeNames)
	for _, name := range typeNames {
		ct, ok := s.Types[name].(*ComplexType)
		if !ok || ct.Particle == nil {
			continue
		}
		where := name.Local
		if where == "" {
			where = "an anonymous type"
		}
		sites = append(sites, modelSite{ct.Particle, where})
	}

	elemNames := make([]xdm.QName, 0, len(s.Elements))
	for name := range s.Elements {
		elemNames = append(elemNames, name)
	}
	sortQNames(elemNames)
	for _, name := range elemNames {
		ct, ok := s.Elements[name].Type.(*ComplexType)
		if !ok || ct.Particle == nil {
			continue
		}
		// A named type reached through an element is already in the
		// list above; only the inline anonymous ones are new.
		if ct.Name.Local != "" || ct.Name.URI != "" {
			continue
		}
		sites = append(sites, modelSite{
			ct.Particle,
			"the type of element " + name.Local,
		})
	}

	// The two walks above reach a named type, and an anonymous type
	// declared inline on a *global* element. An anonymous type on a
	// **local** element is reachable through neither, so its content model
	// went unchecked however ambiguous it was. particlesZ033_e nests one
	// four levels down and puts two branches of a choice on the same
	// substitution group inside it. allComplexTypes holds every complex
	// type read, in document order, so appending it last both fills the
	// gap and leaves the labels the first two walks produce in place for
	// the types they already covered.
	seenParticle := make(map[*Particle]bool, len(sites))
	for _, site := range sites {
		seenParticle[site.particle] = true
	}
	for _, ct := range s.allComplexTypes {
		if ct.Particle == nil || seenParticle[ct.Particle] {
			continue
		}
		seenParticle[ct.Particle] = true
		where := ct.Name.Local
		if where == "" {
			where = "an anonymous type"
		}
		sites = append(sites, modelSite{ct.Particle, where})
	}

	for _, site := range sites {
		m, err := compileContentModel(site.particle)
		if err != nil {
			continue
		}
		where := site.where
		if err := checkUPA(m, where, opts); err != nil {
			errs = append(errs, err)
		}
		if err := checkElementDeclarationsConsistent(m, where, s.Version); err != nil {
			errs = append(errs, err)
		}
		if err := checkWildcardEDC(s, m, where); err != nil {
			errs = append(errs, err)
		}
		if err := checkSubstitutionEDC(s, m, where); err != nil {
			errs = append(errs, err)
		}
	}
	// Conditional type assignment is 1.1 only, and its two schema-level
	// constraints are checked here rather than at parse time because an
	// alternative's type= may name a type the parser had not yet resolved.
	if s.Version >= Version11 {
		errs = append(errs, checkTypeTables(s)...)
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

// sortQNames orders names so that a walk driven by them is reproducible.
// Which of two faulty content models is reported must not depend on map
// iteration order.
func sortQNames(names []xdm.QName) {
	sort.Slice(names, func(i, j int) bool {
		if names[i].URI != names[j].URI {
			return names[i].URI < names[j].URI
		}
		return names[i].Local < names[j].Local
	})
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
	type upaState struct {
		positions []int
		// initial marks the automaton's start set, where no counter
		// has been entered yet.
		initial bool
	}
	states := make([]upaState, 0, len(m.follow)+1)
	states = append(states, upaState{m.first, true})
	for _, f := range m.follow {
		states = append(states, upaState{f, false})
	}

	for _, st := range states {
		state := st.positions
		for i := 0; i < len(state); i++ {
			for j := i + 1; j < len(state); j++ {
				a, b := m.positions[state[i]], m.positions[state[j]]
				if !positionsCompete(a, b, opts.Version) {
					continue
				}
				if opts.LaxUPA && sameDeclaration(a, b) {
					// The permissive reading: the element
					// declaration is identifiable even
					// though the particle is not.
					continue
				}
				if !st.initial && counterForces(m, a, b) {
					// Not a free choice: a counter that
					// has not reached its minimum has to
					// keep going, so the automaton is
					// still deterministic. See
					// counterForces.
					//
					// Only away from the start set. The
					// argument is that the automaton is
					// already *inside* one position's
					// counter scope and cannot leave it
					// yet. In the initial state no counter
					// has been entered, so nothing is
					// holding it: two branches of a choice
					// that happen to be counted compete
					// exactly as uncounted ones would.
					// particlesZ033_e writes that pair —
					// a substitution group head and one of
					// its members, both minOccurs="3" — and
					// it was silently accepted.
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
//
// The third case is 1.0-only. XSD 1.1 resolves element-against-wildcard in
// favour of the element rather than calling the model ambiguous, so the pair
// no longer competes; see CheckOptions.Version.
func positionsCompete(a, b *position, version Version) bool {
	switch ta := a.term.(type) {
	case *ElementDecl:
		switch tb := b.term.(type) {
		case *ElementDecl:
			return elementNamesOverlap(ta, tb)
		case *Wildcard:
			return version < Version11 && wildcardAdmitsElement(tb, ta)
		}
	case *Wildcard:
		switch tb := b.term.(type) {
		case *ElementDecl:
			return version < Version11 && wildcardAdmitsElement(ta, tb)
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
func checkElementDeclarationsConsistent(m *contentModel, where string, v Version) error {
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
		// Conditional type assignment is 1.1-only, so the type table half
		// of the constraint must not fire under 1.0, where <xs:alternative>
		// is not a component at all. See sameTypeTable.
		if v >= Version11 && !sameTypeTable(prev, d) {
			return fmt.Errorf(
				"cos-element-consistent: %s declares %s with two different "+
					"type tables", where, describeTerm(&position{term: d}))
		}
	}
	return nil
}

// checkSubstitutionEDC extends Element Declarations Consistent (§3.8.6) to the
// substitution groups of the declarations a content model names.
//
// cos-element-consistent is stated over "the element declarations in the
// {particles} ... together with the members of their substitution groups": a
// particle referring to a head stands for every element that may substitute
// for it, so each member is in the content model as surely as the head is. Two
// declarations of one name still have to agree on their type.
//
// XSD 1.1 is where this bites, and deliberately so. Under 1.0 a head's *actual*
// substitution group excludes its abstract members, so a schema whose only
// conflict comes through an abstract element is legal; 1.1 settled bug 4337 the
// other way, making the abstract element a member and the schema
// non-conforming. wgData's sg-abstract-edc carries both answers explicitly --
// valid for 1.0, invalid for 1.1 -- and saxon's subsgroup901, whose testSet is
// 1.1 throughout, is the non-abstract shape of the same fault: a local "n" of
// type xs:date beside a ref to a head one of whose members is a global "n" of
// type xs:string.
//
// A member is skipped when the head's {disallowed substitutions} keep it out,
// since a blocked member can never appear where the head does and so brings no
// second meaning with it.
func checkSubstitutionEDC(s *Schema, m *contentModel, where string) error {
	if s.Version < Version11 {
		return nil
	}
	// byName holds one declaration per name, whether it reached the model
	// as a particle of its own or through a head's substitution group. The
	// particles are seeded first so that a conflict is reported against the
	// declaration the author actually wrote.
	byName := map[xdm.QName]*ElementDecl{}
	var heads []*ElementDecl
	for _, p := range m.positions {
		d, ok := p.term.(*ElementDecl)
		if !ok {
			continue
		}
		if _, seen := byName[d.Name]; !seen {
			byName[d.Name] = d
		}
		if len(d.Substitutable()) > 0 {
			heads = append(heads, d)
		}
	}
	if len(heads) == 0 {
		return nil
	}
	// Sorted so a schema with two faults reports the same one every run:
	// substitutable order is stable, but which head is visited first is
	// not enough on its own to fix the choice of message.
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].Name.URI != heads[j].Name.URI {
			return heads[i].Name.URI < heads[j].Name.URI
		}
		return heads[i].Name.Local < heads[j].Name.Local
	})
	for _, head := range heads {
		for _, mem := range head.Substitutable() {
			if mem == nil || mem == head {
				continue
			}
			if substitutionBlockedBy(head, mem) {
				continue
			}
			prev, seen := byName[mem.Name]
			if !seen {
				byName[mem.Name] = mem
				continue
			}
			if prev == mem {
				continue
			}
			if prev.Type != mem.Type {
				return fmt.Errorf(
					"cos-element-consistent: %s declares %q with two "+
						"different types: once directly and once as a "+
						"member of the substitution group of %q",
					where, mem.Name.Local, head.Name.Local)
			}
			if !sameTypeTable(prev, mem) {
				return fmt.Errorf(
					"cos-element-consistent: %s declares %q with two "+
						"different type tables: once directly and once "+
						"as a member of the substitution group of %q",
					where, mem.Name.Local, head.Name.Local)
			}
		}
	}
	return nil
}

// checkWildcardEDC extends Element Declarations Consistent (§3.8.6) to the
// wildcards in a content model.
//
// The 1.0 constraint compares element *particles* with each other. XSD 1.1
// adds the case the suite calls out directly: a wildcard with
// processContents="strict" or "lax" can match an element that a global
// declaration governs, and if a like-named element particle sits in the same
// model, the same name in one content model again means two things — once
// through the particle, once through the wildcard resolving to the global
// declaration.
//
// wild078 and wild079 are the pair that differ only in strict versus lax, and
// wild081 is the mirror where the local particle carries the type table and the
// global declaration does not. All three are expected invalid.
//
// processContents="skip" is exempt: a skipped element is not validated against
// any declaration, so no second meaning arises. That exemption is what keeps
// this from firing on the ordinary "a wildcard beside an element" model, which
// is legal and common.
func checkWildcardEDC(s *Schema, m *contentModel, where string) error {
	if s.Version < Version11 {
		return nil
	}
	var wildcards []*Wildcard
	locals := map[xdm.QName]*ElementDecl{}
	for _, p := range m.positions {
		switch t := p.term.(type) {
		case *ElementDecl:
			if _, seen := locals[t.Name]; !seen {
				locals[t.Name] = t
			}
		case *Wildcard:
			if t.ProcessContents != ProcessSkip {
				wildcards = append(wildcards, t)
			}
		}
	}
	if len(wildcards) == 0 || len(locals) == 0 {
		return nil
	}
	defined := func(n xdm.QName) bool { _, ok := s.Elements[n]; return ok }

	// The names are sorted so that a schema with two faults reports the
	// same one on every run; s.Elements is a map and locals is keyed by one.
	names := make([]xdm.QName, 0, len(locals))
	for n := range locals {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].URI != names[j].URI {
			return names[i].URI < names[j].URI
		}
		return names[i].Local < names[j].Local
	})

	for _, n := range names {
		global, ok := s.Elements[n]
		if !ok || global == locals[n] {
			continue
		}
		for _, w := range wildcards {
			if !w.AllowsName(n, defined) {
				continue
			}
			local := locals[n]
			// Only the TYPE TABLE half of the constraint is a
			// schema-validity rule. A differing {type definition}
			// is checked when a document is validated, not when
			// the schema is read: wild061 says so outright —
			// "schema is valid, though in 1.1 no document can
			// satisfy it" — and wild061..wild076 are ten valid
			// schemas that a type comparison here rejects. The
			// three the suite expects invalid, wild078/079/081,
			// all differ in their <xs:alternative>s.
			if !sameTypeTable(local, global) {
				return fmt.Errorf(
					"cos-element-consistent: %s: a %s wildcard can match the "+
						"global declaration of %s, whose type table differs "+
						"from that of the like-named element particle beside it",
					where, processWord(w.ProcessContents), n.Local)
			}
		}
	}
	return nil
}

// processWord names a processContents mode for a diagnostic.
func processWord(pc ProcessContents) string {
	switch pc {
	case ProcessStrict:
		return "strict"
	case ProcessLax:
		return "lax"
	}
	return "skip"
}

// checkAllGroupLimited enforces clause 1 of All Group Limited (§3.8.6): a
// model group whose {compositor} is all "appears only as" the {model group} of
// a model group definition (1.1), or the {term} of a particle with
// {max occurs} = 1 that constitutes the {content type} of a complex type (1.2).
//
// Read together those two clauses say an all group is only ever a whole
// content model. It may be *written* inside a named <xs:group>, because 1.1
// permits the definition, but the reference to that definition still has to
// land where 1.2 allows — as the content type itself, once.
//
// Everything else is out of bounds, and the suite tests each way of getting it
// wrong: mgA020 references a group holding an all from inside a <sequence>,
// particlesEa025 references one with maxOccurs="2", particlesEc009 nests a
// repeating choice where the all's own members must be 0 or 1, and mgA016 and
// particlesFb002 make an all the content of an <xs:extension>, where the
// effective content type is the base's model followed by this one — a
// sequence, so the all is no longer the content type. particlesFb003 is the
// mirror image: the base's all survives into an extension whose own content is
// a choice.
//
// The parser already applies 1.2's occurrence half to an *inline* <xs:all>
// (see checkAllOccurs). It cannot see the group-reference cases, because the
// referenced definition is bound by a fixup long after the reference is read,
// which is why this runs over the settled component graph instead.
func checkAllGroupLimited(s *Schema) error {
	var errs []error
	for _, ct := range s.allComplexTypes {
		if ct.Particle == nil {
			continue
		}
		where := ct.Name.Local
		if where == "" {
			where = "an anonymous type"
		}
		// The content type's own particle is the one place clause 1.2
		// allows, and only at maxOccurs=1. Its descendants are not.
		if g, ok := ct.Particle.Term.(*ModelGroup); ok && g.Compositor == CompositorAll {
			if ct.Particle.MaxOccurs != 1 {
				errs = append(errs, fmt.Errorf(
					"cos-all-limited.1.2: the xs:all group of %s must have maxOccurs=1", where))
			}
			// An all group directly inside the content type's own all
			// group is 1.1's doing, not the schema author's: a
			// <xs:group ref> naming an all group is how 1.1 shares
			// one, and §3.4.2.3.3 clause 2.2 builds an all-of-alls
			// when an all group extends an all group. Both are
			// legal there, so only the members below them are
			// examined.
			for _, sub := range g.Particles {
				if inner, ok := sub.Term.(*ModelGroup); ok &&
					inner.Compositor == CompositorAll && s.Version >= Version11 {
					// §3.8.3: a <xs:group ref> inside an all
					// group is how 1.1 shares one, but the
					// reference stands in for a member of the
					// all group, and a member is chosen at
					// most once — so it may not repeat.
					// all010 gives it maxOccurs="3" and is
					// expected invalid on that ground alone.
					//
					// minOccurs is deliberately NOT checked
					// here, even though all009 makes the
					// reference optional and is also expected
					// invalid. A nested all group in the
					// settled component graph has two
					// possible origins — a group reference,
					// and the merge §3.4.2.3.3 clause 2.2
					// performs when an all group extends an
					// all group — and the graph does not
					// record which. all314 is the guard: it
					// extends an all group with minOccurs="0"
					// by another with minOccurs="0", is
					// expected VALID, and a minOccurs rule
					// here rejects it. Catching all009 needs
					// the distinction the parser can see and
					// this pass cannot.
					if sub.MaxOccurs != 1 {
						errs = append(errs, fmt.Errorf(
							"cos-all-limited.1.2: %s: a group reference "+
								"to an xs:all group inside an xs:all "+
								"group may not repeat",
							where))
					}
					for _, deep := range inner.Particles {
						errs = append(errs, badNestedAll(deep, where, s.Version, map[*ModelGroup]bool{})...)
					}
					continue
				}
				// A member of an all group is an element, a
				// wildcard, or — in 1.1 only, handled above —
				// another all group. A sequence or a choice
				// here can only have arrived through a
				// <xs:group ref> naming one, which §3.8.3 does
				// not permit: all008 references a sequence and
				// all011 a choice, both expected invalid.
				if inner, ok := sub.Term.(*ModelGroup); ok &&
					inner.Compositor != CompositorAll {
					errs = append(errs, fmt.Errorf(
						"cos-all-limited.1: %s: a model group inside an "+
							"xs:all group must itself be an xs:all group",
						where))
					continue
				}
				errs = append(errs, badNestedAll(sub, where, s.Version, map[*ModelGroup]bool{})...)
			}
			continue
		}
		errs = append(errs, badNestedAll(ct.Particle, where, s.Version, map[*ModelGroup]bool{})...)
	}
	if len(errs) == 0 {
		return nil
	}
	// No sort here: allComplexTypes is in document order already, which is
	// a more useful order to read than an alphabetical one.
	return &SchemaErrors{Errors: errs}
}

// badNestedAll reports every all group at or below a particle that is not the
// content type, all of which clause 1 forbids.
//
// The recursion stops descending into an offending all group: one report per
// misplaced group is what a schema author needs, and the members of a group
// that should not be there at all say nothing further about the fault.
//
// seen records the groups already walked. A <group ref> resolves to the
// definition's own ModelGroup pointer, so a group reachable by several routes
// is the same pointer each time and re-walking it can find nothing new. Two
// references to one group used to be walked twice, which made this exponential
// in the number of distinct paths rather than linear in the graph: a 3.0 KB
// schema of 29 doubly-referencing groups spent 8% of a 35-second load here,
// behind cycleFrom's 86%. Fixing only the larger one would have left this to
// reach the same wall a few groups later.
//
// It also makes the duplicate reports go away, which is the visible change: a
// misplaced all group reachable twice was reported twice, and is now reported
// once. One report per fault is what the doc comment above already promised.
func badNestedAll(p *Particle, where string, version Version, seen map[*ModelGroup]bool) []error {
	if p == nil {
		return nil
	}
	g, ok := p.Term.(*ModelGroup)
	if !ok {
		return nil
	}
	if seen[g] {
		return nil
	}
	seen[g] = true
	if g.Compositor == CompositorAll {
		return []error{fmt.Errorf(
			"cos-all-limited.1: an xs:all group may only be the whole content "+
				"type of a complex type, but %s nests one inside another group", where)}
	}
	var errs []error
	for _, sub := range g.Particles {
		errs = append(errs, badNestedAll(sub, where, version, seen)...)
	}
	return errs
}

// counterForces reports whether a repetition counter decides between two
// competing positions, leaving no ambiguity for UPA to object to.
//
// This automaton counts rather than duplicating positions: <element name="b"
// minOccurs="2" maxOccurs="2"/> is one position carrying a counter, not two
// positions in sequence. Two positions on the same element name are therefore
// far more common here than in the textbook Glushkov construction, and most of
// them are not ambiguous at all.
//
// The case that matters is one position sitting inside a counter scope the
// other has left. While that counter is below its minimum the automaton has no
// choice — it must take the transition that stays inside, because leaving would
// strand the counter short — so the element is still attributed to exactly one
// particle. mgZ005 is the shape: <b minOccurs="2" maxOccurs="2"/> followed by
// <b/>, where the first two b's belong to the counted particle and the third to
// the plain one, and which is which is never in doubt. The W3C expects it
// valid.
//
// A counter whose minimum is 0 or 1 forces nothing, because leaving it
// immediately is allowed, and then the choice is real.
func counterForces(m *contentModel, a, b *position) bool {
	return exitBlocked(m, a, b) || exitBlocked(m, b, a)
}

// exitBlocked reports whether inner sits in a counter scope that outer is not
// in, and whose minimum has still to be met.
func exitBlocked(m *contentModel, inner, outer *position) bool {
	for _, c := range inner.counters {
		if containsScope(outer.counters, c) {
			continue
		}
		if m.counters[c].min > 1 {
			return true
		}
	}
	return false
}

// containsScope reports whether a counter scope is in a position's chain.
func containsScope(scopes []int, want int) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// typeAlternativesOK enforces the two schema-level constraints on a
// declaration's {type table} that hold regardless of any instance.
//
// Clause 1 is the Schema Representation Constraint on <xs:alternative>
// (§3.3.3): an alternative with no test is the *default*, chosen when no
// earlier test held, so it can only be the last one. An earlier bare
// alternative makes every alternative after it dead code, and the spec
// spells the arrangement out — {type table} has {alternatives}, which all
// carry a test, and a separate {default type definition}. cta9001err is the
// suite's case: a bare <xs:alternative type="messageTypeDate"/> sitting
// fourth of seven.
//
// Clause 2 is Element Declaration Properties Correct (§3.3.6) clause 5:
// every type an alternative can select must be validly derived from the
// declaration's own {type definition}. Conditional type assignment narrows
// the declared type; it does not replace it, and a document validated
// against the alternative must still satisfy anything written against the
// declared type. cta9008err declares chap as docType and then offers two
// anonymous types descending from xs:anyType, which docType does not reach.
//
// xs:error is exempt: it is the type that accepts nothing, and §3.16.7.3
// gives it as the way to write "this combination is an error", so it is
// deliberately not a restriction of whatever was declared. cta0007 uses it
// exactly so.
func typeAlternativesOK(d *ElementDecl, where string) []error {
	if len(d.Alternatives) == 0 {
		return nil
	}
	var errs []error
	for i, alt := range d.Alternatives {
		if alt.Test == nil && i != len(d.Alternatives)-1 {
			errs = append(errs, fmt.Errorf(
				"src-type-alternative: element %s: an alternative "+
					"with no test is the default and must be "+
					"the last one, but one appears at position %d of %d",
				where, i+1, len(d.Alternatives)))
		}
		if alt.Type == nil || d.Type == nil || isErrorType(alt.Type) {
			continue
		}
		if !typeDerivedFrom(alt.Type, d.Type) {
			errs = append(errs, fmt.Errorf(
				"e-props-correct.5: element %s: the type selected by "+
					"alternative %d is not validly derived from the "+
					"declared type %s",
				where, i+1, d.Type.TypeName().Local))
		}
	}
	return errs
}

// isErrorType reports whether t is xs:error, the type with no valid instances.
func isErrorType(t Type) bool {
	n := t.TypeName()
	return n.URI == NSSchema && n.Local == "error"
}

// typeDerivedFrom reports whether t is, or descends from, want.
//
// This is the schema-time twin of (*validator).derivedFrom: the same walk up
// the base chain, with the same two terminations — a self-referential base
// (xs:anyType is its own) and a depth bound, because a malformed schema can
// build a cycle that is not a self-loop. It is a free function rather than a
// validator method because the constraint it serves is a property of the
// schema, checked before any document exists.
func typeDerivedFrom(t, want Type) bool {
	if want == nil || t == nil {
		return true
	}
	// A member of a union is validly derived from it (§3.14.6 clause 2.2.3).
	if u, ok := want.(*SimpleType); ok && u.Variety == VarietyUnion {
		for _, m := range u.MemberTypes {
			if m != nil && typeDerivedFrom(t, m) {
				return true
			}
		}
	}
	for cur, seen := t, 0; cur != nil; seen++ {
		if cur == want {
			return true
		}
		if n := cur.TypeName(); n.Local != "" && n == want.TypeName() {
			return true
		}
		base := cur.BaseType()
		if base == cur || base == nil || seen > 256 {
			return false
		}
		cur = base
	}
	return false
}

// checkTypeTables applies typeAlternativesOK to every element declaration in
// the schema.
//
// Local declarations are not indexed on the Schema — they are reachable only
// through the type that contains them — so the walk descends every complex
// type's content model as well as the global map. allComplexTypes is in
// document order, which keeps the error list stable; the global map is walked
// too, and checkContentModelConstraints sorts the messages before reporting,
// so the map's unordered walk cannot change the outcome.
func checkTypeTables(s *Schema) []error {
	var errs []error
	seen := map[*ElementDecl]bool{}

	visit := func(d *ElementDecl, where string) {
		if d == nil || seen[d] {
			return
		}
		seen[d] = true
		errs = append(errs, typeAlternativesOK(d, where)...)
	}

	for name, d := range s.Elements {
		visit(d, name.Local)
	}
	for _, ct := range s.allComplexTypes {
		if ct.Particle == nil {
			continue
		}
		walkParticleElements(ct.Particle, map[*Particle]bool{}, func(d *ElementDecl) {
			visit(d, d.Name.Local)
		})
	}
	return errs
}

// walkParticleElements calls fn for every element declaration a particle
// reaches, descending through model groups.
//
// A group may reach itself, and following terms rather than nodes would
// otherwise not terminate. The visited set is what stops that: a particle
// already on the path has had its declarations delivered already.
//
// This used to be a `depth > 64` bound, which conflated "cyclic" with "deep"
// the same way the `depth > 32` bounds elsewhere in this package did. Not
// reaching a declaration means checkTypeTables never visits it, and an
// unvisited declaration is an unchecked one: a schema whose xs:alternative
// violates src-type-alternative loaded clean once its declaration sat 64
// groups deep. Silence from a walk is indistinguishable from a clean result,
// which is what makes the truncating form dangerous.
func walkParticleElements(p *Particle, seen map[*Particle]bool, fn func(*ElementDecl)) {
	if p == nil || seen[p] {
		return
	}
	seen[p] = true
	switch t := p.Term.(type) {
	case *ElementDecl:
		fn(t)
	case *ModelGroup:
		for _, c := range t.Particles {
			walkParticleElements(c, seen, fn)
		}
	}
}
