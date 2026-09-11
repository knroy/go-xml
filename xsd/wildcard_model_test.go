package xsd

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An independent model of wildcard acceptance and of wildcard competition,
// written from the XSD 1.1 Part 1 rules rather than from this package's code.
//
// The point of the exercise is that the oracle must be able to disagree. This
// repository has already been bitten once by an oracle that called the very
// function it was meant to check — the grouping-key test — so a wrong answer
// agreed with itself and stayed green for months. Nothing below calls
// Wildcard.Allows, Wildcard.AllowsName, Wildcard.Disallows, wildcardsOverlap,
// wildcardAdmitsElement or positionsCompete. The model is built from the
// normative text, restated here so a reader can check it against the spec
// without reading the implementation:
//
// Wildcard allows Namespace Name (§3.10.4.2). For a namespace name N (the
// absent namespace written as the empty string) and a {namespace constraint}
// C, N is allowed if and only if:
//
//	1 C.{variety} = any; or
//	2 C.{variety} = not and N is not in C.{namespaces}; or
//	3 C.{variety} = enumeration and N is in C.{namespaces}.
//
// In 1.1 the absent namespace is an ordinary member of {namespaces}: ##other
// compiles to a not-set holding both the target namespace and absent, while
// notNamespace holds absent only when ##local was written. That is the one
// asymmetry between the two spellings, and it is the whole content of the
// ExcludesAbsent field.
//
// Wildcard allows Expanded Name (§3.10.4.3). For an expanded name E and a
// wildcard W, E is allowed if and only if all of:
//
//	1 W.{namespace constraint} allows E's namespace name;
//	2 E is not in W.{disallowed names};
//	3 if defined is in W.{disallowed names}, E is not the name of a global
//	  element declaration; and
//	4 if definedSibling is in W.{disallowed names}, E is not the name of an
//	  element declaration in the content model's own particles.
//
// Clauses 1 and 2-4 are independent tests, and clause 2 can subtract a name
// the namespace constraint admits — that is the only reason to write notQName
// at all.
//
// Unique Particle Attribution (§3.8.6, Appendix H). Two terms compete when
// some element could be attributed to either. For the generated pairs here
// that is: two element declarations whose name sets (including substitution
// group members) intersect; two wildcards whose namespace constraints admit a
// common namespace; and, under 1.0 only, an element declaration against a
// wildcard admitting one of its names. XSD 1.1 resolves the third case in
// favour of the element declaration instead of reporting an error.

// The seed the CI run uses. Override with -wildcard.seed to replay a fuzz
// failure; the failure messages print the seed to pass back.
var wildcardSeed = flag.Int64("wildcard.seed", 0x5A1D11, "seed for the generated wildcard suite")

// wildcardCases is how many random wildcard/name pairs step 2 checks, and how
// many random term pairs step 4 checks.
const wildcardCases = 20000

// ---------------------------------------------------------------------------
// The model
// ---------------------------------------------------------------------------

// nsVariety is {namespace constraint}.{variety} from §3.10.1.
type nsVariety int

const (
	varAny nsVariety = iota
	varNot
	varEnum
)

// modelWildcard is a wildcard as the spec describes it: a namespace
// constraint, which is a variety plus a set of namespace names, and a set of
// disallowed names that may contain expanded names and the two keywords.
//
// The absent namespace is the empty string and is an ordinary member of
// namespaces, which is how §3.10.1 models it in 1.1.
type modelWildcard struct {
	variety    nsVariety
	namespaces map[string]bool

	disallowed     map[xdm.QName]bool
	defined        bool
	definedSibling bool
}

// allowsNamespaceName is Wildcard allows Namespace Name (§3.10.4.2).
func (w modelWildcard) allowsNamespaceName(ns string) bool {
	switch w.variety {
	case varAny:
		return true
	case varNot:
		return !w.namespaces[ns]
	case varEnum:
		return w.namespaces[ns]
	}
	panic("unreachable variety")
}

// allowsExpandedName is Wildcard allows Expanded Name (§3.10.4.3). globals
// answers clause 3 and siblings clause 4; either may be nil, meaning "no such
// declaration exists".
func (w modelWildcard) allowsExpandedName(name xdm.QName, globals, siblings map[xdm.QName]bool) bool {
	if !w.allowsNamespaceName(name.URI) { // clause 1
		return false
	}
	if w.disallowed[name] { // clause 2
		return false
	}
	if w.defined && globals[name] { // clause 3
		return false
	}
	if w.definedSibling && siblings[name] { // clause 4
		return false
	}
	return true
}

// modelNamespacesOverlap decides whether two namespace constraints admit some
// common namespace name, by cases on the two varieties rather than by probing
// a finite sample — the set of namespace names is infinite, and the not/not
// case depends on that.
func modelNamespacesOverlap(a, b modelWildcard) bool {
	switch {
	case a.variety == varAny || b.variety == varAny:
		// ##any admits everything, and there is always something to
		// admit, so it overlaps every constraint — including an empty
		// enumeration? No: an empty enumeration admits nothing. That
		// case cannot arise from a schema (xs:any requires at least one
		// namespace) but the model states it rather than assuming it.
		if a.variety == varEnum && len(a.namespaces) == 0 {
			return false
		}
		if b.variety == varEnum && len(b.namespaces) == 0 {
			return false
		}
		return true
	case a.variety == varNot && b.variety == varNot:
		// Both exclude finite sets from an infinite universe, so some
		// namespace escapes both. Always overlap.
		return true
	case a.variety == varEnum && b.variety == varEnum:
		for ns := range a.namespaces {
			if b.namespaces[ns] {
				return true
			}
		}
		return false
	default:
		// One enumeration against one negation: they overlap iff some
		// enumerated namespace survives the negation.
		enum, not := a, b
		if enum.variety == varNot {
			enum, not = b, a
		}
		for ns := range enum.namespaces {
			if !not.namespaces[ns] {
				return true
			}
		}
		return false
	}
}

// modelCompetes is the UPA competition rule of §3.8.6 for a pair of terms, in
// the model's own vocabulary. v selects the reading: XSD 1.1 no longer has an
// element declaration and a wildcard compete.
func modelCompetes(a, b modelTerm, v Version) bool {
	switch {
	case a.elem != nil && b.elem != nil:
		for n := range a.elem.names {
			if b.elem.names[n] {
				return true
			}
		}
		return false
	case a.wild != nil && b.wild != nil:
		return modelNamespacesOverlap(*a.wild, *b.wild)
	default:
		if v >= Version11 {
			return false
		}
		el, wc := a, b
		if el.elem == nil {
			el, wc = b, a
		}
		// 1.0 has no {disallowed names}, so competition is decided by
		// the namespace constraint alone — which is also what the
		// implementation does, and what the spec's "Wildcard allows
		// Namespace Name" reading of Appendix H requires.
		for n := range el.elem.names {
			if wc.wild.allowsNamespaceName(n.URI) {
				return true
			}
		}
		return false
	}
}

// modelElement is an element declaration and the set of names it can match,
// which is its own name together with its substitution group members.
type modelElement struct {
	name  xdm.QName
	names map[xdm.QName]bool
}

// modelTerm is one of the two term kinds a particle may carry.
type modelTerm struct {
	elem *modelElement
	wild *modelWildcard
}

// ---------------------------------------------------------------------------
// Translating the model into the implementation's components
// ---------------------------------------------------------------------------

// buildWildcard produces the *Wildcard this package would compile the model
// wildcard to. It is a translation of shape, not of decision: it copies the
// variety and the sets across and nothing else.
func buildWildcard(m modelWildcard) *Wildcard {
	w := &Wildcard{ProcessContents: ProcessStrict}
	switch m.variety {
	case varAny:
		w.Kind = NSAny
	case varNot:
		w.Kind = NSNot
		for ns := range m.namespaces {
			if ns == "" {
				// The implementation splits the absent namespace
				// out of the set into its own flag; the model
				// keeps it in the set, per §3.10.1.
				w.ExcludesAbsent = true
				continue
			}
			w.Namespace = append(w.Namespace, ns)
		}
	case varEnum:
		w.Kind = NSEnumerated
		for ns := range m.namespaces {
			w.Namespace = append(w.Namespace, ns)
		}
	}
	sort.Strings(w.Namespace)
	for n := range m.disallowed {
		w.DisallowedNames = append(w.DisallowedNames, n)
	}
	sort.Slice(w.DisallowedNames, func(i, j int) bool {
		if w.DisallowedNames[i].URI != w.DisallowedNames[j].URI {
			return w.DisallowedNames[i].URI < w.DisallowedNames[j].URI
		}
		return w.DisallowedNames[i].Local < w.DisallowedNames[j].Local
	})
	w.DisallowDefined = m.defined
	w.DisallowDefinedSibling = m.definedSibling
	return w
}

// describeModelWildcard renders a model wildcard the way a schema author would
// have written it, so a failure names the input rather than a struct dump.
func describeModelWildcard(m modelWildcard) string {
	var b strings.Builder
	switch m.variety {
	case varAny:
		b.WriteString("namespace='##any'")
	case varNot:
		b.WriteString("notNamespace='" + strings.Join(sortedNS(m.namespaces), " ") + "'")
	case varEnum:
		b.WriteString("namespace='" + strings.Join(sortedNS(m.namespaces), " ") + "'")
	}
	var dis []string
	for _, n := range sortedNames(m.disallowed) {
		dis = append(dis, qnameString(n))
	}
	if m.defined {
		dis = append(dis, "##defined")
	}
	if m.definedSibling {
		dis = append(dis, "##definedSibling")
	}
	if len(dis) > 0 {
		b.WriteString(" notQName='" + strings.Join(dis, " ") + "'")
	}
	return b.String()
}

func sortedNS(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for ns := range set {
		if ns == "" {
			out = append(out, "##local")
			continue
		}
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

func sortedNames(set map[xdm.QName]bool) []xdm.QName {
	out := make([]xdm.QName, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].URI != out[j].URI {
			return out[i].URI < out[j].URI
		}
		return out[i].Local < out[j].Local
	})
	return out
}

func qnameString(n xdm.QName) string {
	if n.URI == "" {
		return n.Local
	}
	return "{" + n.URI + "}" + n.Local
}

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

// The generated vocabulary is deliberately tiny. Wildcard acceptance turns on
// set membership, so three namespaces and three locals give every interesting
// collision — a name in the wildcard's namespace, one outside it, and an
// unqualified one — while keeping a failing case small enough to read.
var (
	genNamespaces = []string{"", "http://a", "http://b", "http://c"}
	genLocals     = []string{"x", "y", "z"}
)

func genQName(rnd *rand.Rand) xdm.QName {
	return xdm.QName{
		URI:   genNamespaces[rnd.Intn(len(genNamespaces))],
		Local: genLocals[rnd.Intn(len(genLocals))],
	}
}

// genWildcard draws a wildcard uniformly over the three varieties and over
// small disallowed-name sets, biased so that every keyword is exercised often.
func genWildcard(rnd *rand.Rand) modelWildcard {
	m := modelWildcard{namespaces: map[string]bool{}, disallowed: map[xdm.QName]bool{}}
	switch rnd.Intn(3) {
	case 0:
		m.variety = varAny
	case 1:
		m.variety = varNot
		// A not-set is non-empty in any schema that can be written;
		// ##other yields exactly {target, absent}.
		n := 1 + rnd.Intn(2)
		for i := 0; i < n; i++ {
			m.namespaces[genNamespaces[rnd.Intn(len(genNamespaces))]] = true
		}
	default:
		m.variety = varEnum
		n := 1 + rnd.Intn(3)
		for i := 0; i < n; i++ {
			m.namespaces[genNamespaces[rnd.Intn(len(genNamespaces))]] = true
		}
	}
	for n := rnd.Intn(3); n > 0; n-- {
		m.disallowed[genQName(rnd)] = true
	}
	m.defined = rnd.Intn(3) == 0
	m.definedSibling = rnd.Intn(3) == 0
	return m
}

// genNameSet draws a small set of expanded names, used for the global and
// sibling declarations that clauses 3 and 4 consult.
func genNameSet(rnd *rand.Rand) map[xdm.QName]bool {
	set := map[xdm.QName]bool{}
	for n := rnd.Intn(4); n > 0; n-- {
		set[genQName(rnd)] = true
	}
	return set
}

// ---------------------------------------------------------------------------
// Step 1 + 2: model vs Wildcard.AllowsName
// ---------------------------------------------------------------------------

// TestWildcardAcceptanceAgainstModel is steps 1 and 2 of the generated suite:
// the model decides whether a wildcard allows an expanded name, and the
// implementation's matcher is asked the same question.
//
// The sibling half needs the real binding path rather than a hand-filled
// field, because ##definedSibling is resolved by contentModel.bindSiblings and
// the unexported siblingNames map is not otherwise reachable. So the wildcard
// is placed in a content model beside the sibling declarations and compiled,
// which is what a schema does.
func TestWildcardAcceptanceAgainstModel(t *testing.T) {
	rnd := rand.New(rand.NewSource(*wildcardSeed))
	for i := 0; i < wildcardCases; i++ {
		m := genWildcard(rnd)
		globals := genNameSet(rnd)
		siblings := genNameSet(rnd)
		name := genQName(rnd)

		w := buildWildcard(m)
		if m.definedSibling {
			bindSiblingsFor(t, w, siblings)
		}
		defined := func(n xdm.QName) bool { return globals[n] }

		want := m.allowsExpandedName(name, globals, siblings)
		got := w.AllowsName(name, defined)
		if want != got {
			t.Fatalf("seed %d case %d: wildcard %s, name %s, "+
				"global declarations %v, sibling declarations %v:\n"+
				"  model says allowed=%v (XSD 1.1 Part 1 §3.10.4.3, "+
				"Wildcard allows Expanded Name)\n"+
				"  Wildcard.AllowsName says allowed=%v\n"+
				"replay with -wildcard.seed=%d",
				*wildcardSeed, i, describeModelWildcard(m), qnameString(name),
				namesString(globals), namesString(siblings),
				want, got, *wildcardSeed)
		}
	}
}

func namesString(set map[xdm.QName]bool) string {
	var out []string
	for _, n := range sortedNames(set) {
		out = append(out, qnameString(n))
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, " ")
}

// bindSiblingsFor puts the wildcard into a content model alongside element
// declarations for the given names and compiles it, so that the package's own
// bindSiblings fills in the wildcard's sibling set. Compiling is the only way
// to reach that field, and using it means the test exercises the real
// resolution path rather than a field the test wrote itself.
func bindSiblingsFor(t *testing.T, w *Wildcard, siblings map[xdm.QName]bool) {
	t.Helper()
	group := &ModelGroup{Compositor: CompositorSequence}
	for _, n := range sortedNames(siblings) {
		group.Particles = append(group.Particles, &Particle{
			MinOccurs: 0, MaxOccurs: 1,
			Term: &ElementDecl{Name: n, Scope: ScopeLocal},
		})
	}
	group.Particles = append(group.Particles, &Particle{
		MinOccurs: 0, MaxOccurs: 1, Term: w,
	})
	if _, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: 1, Term: group}, 0); err != nil {
		t.Fatalf("compiling the sibling content model: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Hand-written examples for each named keyword
// ---------------------------------------------------------------------------

// TestWildcardKeywordExamples pins one readable case per named keyword.
//
// The generated suite above subsumes these, but a generated failure names a
// seed and a case index, and neither says which rule broke. These do.
func TestWildcardKeywordExamples(t *testing.T) {
	const (
		nsA = "http://a"
		nsB = "http://b"
	)
	qn := func(uri, local string) xdm.QName { return xdm.QName{URI: uri, Local: local} }

	cases := []struct {
		name     string
		wildcard modelWildcard
		globals  map[xdm.QName]bool
		siblings map[xdm.QName]bool
		probe    xdm.QName
		want     bool
		why      string
	}{{
		name:     "##any admits a qualified name",
		wildcard: modelWildcard{variety: varAny},
		probe:    qn(nsA, "x"),
		want:     true,
		why:      "§3.10.4.2 clause 1",
	}, {
		name:     "##any admits an unqualified name",
		wildcard: modelWildcard{variety: varAny},
		probe:    qn("", "x"),
		want:     true,
		why:      "§3.10.4.2 clause 1 — the absent namespace is a namespace name",
	}, {
		name:     "##other refuses the excluded namespace",
		wildcard: modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true, "": true}},
		probe:    qn(nsA, "x"),
		want:     false,
		why:      "§3.10.4.2 clause 2",
	}, {
		name:     "##other refuses an unqualified name",
		wildcard: modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true, "": true}},
		probe:    qn("", "x"),
		want:     false,
		why:      "##other compiles to a not-set holding the target namespace and absent",
	}, {
		name:     "##other admits a third namespace",
		wildcard: modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true, "": true}},
		probe:    qn(nsB, "x"),
		want:     true,
		why:      "§3.10.4.2 clause 2",
	}, {
		name:     "notNamespace without ##local admits an unqualified name",
		wildcard: modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true}},
		probe:    qn("", "x"),
		want:     true,
		why:      "notNamespace excludes only what it lists; ##local was not written",
	}, {
		name:     "notNamespace with ##local refuses an unqualified name",
		wildcard: modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true, "": true}},
		probe:    qn("", "x"),
		want:     false,
		why:      "##local puts the absent namespace into the not-set",
	}, {
		name:     "an explicit set admits a listed namespace",
		wildcard: modelWildcard{variety: varEnum, namespaces: map[string]bool{nsA: true, nsB: true}},
		probe:    qn(nsB, "x"),
		want:     true,
		why:      "§3.10.4.2 clause 3",
	}, {
		name:     "an explicit set refuses an unlisted namespace",
		wildcard: modelWildcard{variety: varEnum, namespaces: map[string]bool{nsA: true}},
		probe:    qn(nsB, "x"),
		want:     false,
		why:      "§3.10.4.2 clause 3",
	}, {
		name:     "##local in an explicit set admits an unqualified name",
		wildcard: modelWildcard{variety: varEnum, namespaces: map[string]bool{"": true}},
		probe:    qn("", "x"),
		want:     true,
		why:      "##local enumerates the absent namespace",
	}, {
		name: "notQName subtracts a name the namespace admits",
		wildcard: modelWildcard{
			variety:    varEnum,
			namespaces: map[string]bool{nsA: true},
			disallowed: map[xdm.QName]bool{qn(nsA, "x"): true},
		},
		probe: qn(nsA, "x"),
		want:  false,
		why:   "§3.10.4.3 clause 2 — the only reason to write notQName",
	}, {
		name: "notQName leaves its namespace's other names alone",
		wildcard: modelWildcard{
			variety:    varEnum,
			namespaces: map[string]bool{nsA: true},
			disallowed: map[xdm.QName]bool{qn(nsA, "x"): true},
		},
		probe: qn(nsA, "y"),
		want:  true,
		why:   "§3.10.4.3 clause 2 names one expanded name, not a namespace",
	}, {
		name: "notQName cannot add a name the namespace constraint refuses",
		wildcard: modelWildcard{
			variety:    varEnum,
			namespaces: map[string]bool{nsA: true},
			disallowed: map[xdm.QName]bool{qn(nsB, "x"): true},
		},
		probe: qn(nsB, "x"),
		want:  false,
		why:   "clauses 1 and 2 are both required",
	}, {
		name:     "##defined refuses a globally declared name",
		wildcard: modelWildcard{variety: varAny, defined: true},
		globals:  map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "x"),
		want:     false,
		why:      "§3.10.4.3 clause 3",
	}, {
		name:     "##defined admits an undeclared name",
		wildcard: modelWildcard{variety: varAny, defined: true},
		globals:  map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "y"),
		want:     true,
		why:      "§3.10.4.3 clause 3 applies only to declared names",
	}, {
		name:     "##definedSibling refuses a name a sibling particle declares",
		wildcard: modelWildcard{variety: varAny, definedSibling: true},
		siblings: map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "x"),
		want:     false,
		why:      "§3.10.4.3 clause 4",
	}, {
		name:     "##definedSibling admits a name no sibling declares",
		wildcard: modelWildcard{variety: varAny, definedSibling: true},
		siblings: map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "y"),
		want:     true,
		why:      "§3.10.4.3 clause 4 is local to the content model",
	}, {
		name:     "##definedSibling ignores a merely global declaration",
		wildcard: modelWildcard{variety: varAny, definedSibling: true},
		globals:  map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "x"),
		want:     true,
		why:      "clause 4 consults the content model, clause 3 the schema",
	}, {
		name:     "##defined ignores a merely sibling declaration",
		wildcard: modelWildcard{variety: varAny, defined: true},
		siblings: map[xdm.QName]bool{qn(nsA, "x"): true},
		probe:    qn(nsA, "x"),
		want:     true,
		why:      "clause 3 consults the schema's global declarations only",
	}, {
		name: "the two keywords compose",
		wildcard: modelWildcard{
			variety: varAny, defined: true, definedSibling: true,
		},
		globals:  map[xdm.QName]bool{qn(nsA, "x"): true},
		siblings: map[xdm.QName]bool{qn(nsA, "y"): true},
		probe:    qn(nsA, "z"),
		want:     true,
		why:      "neither clause names z",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wildcard.namespaces == nil {
				tc.wildcard.namespaces = map[string]bool{}
			}
			if tc.wildcard.disallowed == nil {
				tc.wildcard.disallowed = map[xdm.QName]bool{}
			}
			if want := tc.wildcard.allowsExpandedName(tc.probe, tc.globals, tc.siblings); want != tc.want {
				t.Fatalf("the model itself disagrees with the case: the case "+
					"expects allowed=%v, the model says %v (%s)", tc.want, want, tc.why)
			}
			w := buildWildcard(tc.wildcard)
			if tc.wildcard.definedSibling {
				bindSiblingsFor(t, w, tc.siblings)
			}
			defined := func(n xdm.QName) bool { return tc.globals[n] }
			if got := w.AllowsName(tc.probe, defined); got != tc.want {
				t.Fatalf("wildcard %s, name %s: want allowed=%v (%s), "+
					"Wildcard.AllowsName says %v",
					describeModelWildcard(tc.wildcard), qnameString(tc.probe),
					tc.want, tc.why, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Step 3 + 4: particle composition vs the UPA decision
// ---------------------------------------------------------------------------

// genElement draws an element declaration with a small substitution group.
func genElement(rnd *rand.Rand) modelElement {
	e := modelElement{name: genQName(rnd), names: map[xdm.QName]bool{}}
	e.names[e.name] = true
	for n := rnd.Intn(3); n > 0; n-- {
		e.names[genQName(rnd)] = true
	}
	return e
}

func genTerm(rnd *rand.Rand) modelTerm {
	if rnd.Intn(2) == 0 {
		e := genElement(rnd)
		return modelTerm{elem: &e}
	}
	w := genWildcard(rnd)
	// {disallowed names} plays no part in UPA competition — §3.8.6 asks
	// whether two particles can match the same element, and the check the
	// spec's Appendix H describes compares namespace constraints. Keeping
	// the keywords in the draw confirms the implementation ignores them
	// too rather than letting them leak into the decision.
	return modelTerm{wild: &w}
}

// buildTerm renders a model term as the package's Term, so the two can be fed
// to the real content-model compiler.
func buildTerm(m modelTerm) Term {
	if m.elem != nil {
		d := &ElementDecl{Name: m.elem.name, Scope: ScopeGlobal}
		for _, n := range sortedNames(m.elem.names) {
			if n == m.elem.name {
				continue
			}
			d.substitutable = append(d.substitutable, &ElementDecl{Name: n, Scope: ScopeGlobal})
		}
		return d
	}
	return buildWildcard(*m.wild)
}

func describeModelTerm(m modelTerm) string {
	if m.elem != nil {
		var subs []string
		for _, n := range sortedNames(m.elem.names) {
			if n != m.elem.name {
				subs = append(subs, qnameString(n))
			}
		}
		s := "element " + qnameString(m.elem.name)
		if len(subs) > 0 {
			s += " (substitutes: " + strings.Join(subs, " ") + ")"
		}
		return s
	}
	return "wildcard " + describeModelWildcard(*m.wild)
}

// TestUPACompetitionAgainstModel is steps 3 and 4: two terms are composed into
// a choice of two optional particles — the smallest model in which the two
// compete for the same element — and the model's overlap verdict is compared
// with the implementation's UPA decision.
//
// The composition matters. A choice of two optionals means any single child
// could be attributed to either branch, so the model is ambiguous exactly when
// the two terms overlap, and nothing else in checkUPA can mask the answer.
func TestUPACompetitionAgainstModel(t *testing.T) {
	for _, version := range []Version{Version10, Version11} {
		version := version
		t.Run(fmt.Sprintf("xsd%s", versionLabel(version)), func(t *testing.T) {
			rnd := rand.New(rand.NewSource(*wildcardSeed + int64(version)))
			for i := 0; i < wildcardCases; i++ {
				a, b := genTerm(rnd), genTerm(rnd)
				want := modelCompetes(a, b, version)
				got, err := upaRejects(buildTerm(a), buildTerm(b), version)
				if err != nil {
					t.Fatalf("seed %d case %d: compiling the pair: %v",
						*wildcardSeed, i, err)
				}
				if want != got {
					t.Fatalf("seed %d case %d, XSD %s:\n"+
						"  A: %s\n  B: %s\n"+
						"  model says they compete=%v (§3.8.6, Appendix H)\n"+
						"  checkUPA rejects the model=%v\n"+
						"replay with -wildcard.seed=%d",
						*wildcardSeed, i, versionLabel(version),
						describeModelTerm(a), describeModelTerm(b),
						want, got, *wildcardSeed)
				}
			}
		})
	}
}

func versionLabel(v Version) string {
	if v >= Version11 {
		return "1.1"
	}
	return "1.0"
}

// upaRejects composes the two terms into <choice><a? /><b? /></choice> and
// reports whether checkUPA rejects the result.
func upaRejects(a, b Term, v Version) (bool, error) {
	group := &ModelGroup{
		Compositor: CompositorChoice,
		Particles: []*Particle{
			{MinOccurs: 0, MaxOccurs: 1, Term: a},
			{MinOccurs: 0, MaxOccurs: 1, Term: b},
		},
	}
	m, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: 1, Term: group}, 0)
	if err != nil {
		return false, err
	}
	err = checkUPA(m, "the generated pair", CheckOptions{Version: v})
	if err == nil {
		return false, nil
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		return false, err
	}
	return true, nil
}

// TestUPACompetitionExamples pins the three competition cases by hand, for the
// same reason the keyword examples exist: a generated UPA failure says which
// seed broke, not which rule.
func TestUPACompetitionExamples(t *testing.T) {
	const nsA, nsB = "http://a", "http://b"
	qn := func(uri, local string) xdm.QName { return xdm.QName{URI: uri, Local: local} }
	elem := func(n xdm.QName, subs ...xdm.QName) modelTerm {
		e := modelElement{name: n, names: map[xdm.QName]bool{n: true}}
		for _, s := range subs {
			e.names[s] = true
		}
		return modelTerm{elem: &e}
	}
	wild := func(m modelWildcard) modelTerm {
		if m.namespaces == nil {
			m.namespaces = map[string]bool{}
		}
		if m.disallowed == nil {
			m.disallowed = map[xdm.QName]bool{}
		}
		return modelTerm{wild: &m}
	}

	cases := []struct {
		name   string
		a, b   modelTerm
		want10 bool
		want11 bool
		why    string
	}{{
		name:   "the same element name twice",
		a:      elem(qn(nsA, "x")),
		b:      elem(qn(nsA, "x")),
		want10: true, want11: true,
		why: "§3.8.6 — two declarations of one name",
	}, {
		name:   "two different element names",
		a:      elem(qn(nsA, "x")),
		b:      elem(qn(nsA, "y")),
		want10: false, want11: false,
		why: "disjoint name sets",
	}, {
		name:   "substitution group members intersect",
		a:      elem(qn(nsA, "x"), qn(nsA, "z")),
		b:      elem(qn(nsA, "y"), qn(nsA, "z")),
		want10: true, want11: true,
		why: "a list implicitly contains its members' substitutes",
	}, {
		name:   "##any against ##any",
		a:      wild(modelWildcard{variety: varAny}),
		b:      wild(modelWildcard{variety: varAny}),
		want10: true, want11: true,
		why: "both admit every namespace",
	}, {
		name:   "two disjoint enumerations",
		a:      wild(modelWildcard{variety: varEnum, namespaces: map[string]bool{nsA: true}}),
		b:      wild(modelWildcard{variety: varEnum, namespaces: map[string]bool{nsB: true}}),
		want10: false, want11: false,
		why: "no common namespace",
	}, {
		name:   "two negations always overlap",
		a:      wild(modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true}}),
		b:      wild(modelWildcard{variety: varNot, namespaces: map[string]bool{nsB: true}}),
		want10: true, want11: true,
		why: "the set of namespace names is unbounded, so one escapes both",
	}, {
		name:   "an enumeration escaping a negation",
		a:      wild(modelWildcard{variety: varEnum, namespaces: map[string]bool{nsB: true}}),
		b:      wild(modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true}}),
		want10: true, want11: true,
		why: "http://b survives the exclusion of http://a",
	}, {
		name:   "an enumeration entirely excluded",
		a:      wild(modelWildcard{variety: varEnum, namespaces: map[string]bool{nsA: true}}),
		b:      wild(modelWildcard{variety: varNot, namespaces: map[string]bool{nsA: true}}),
		want10: false, want11: false,
		why: "the only enumerated namespace is the excluded one",
	}, {
		name:   "element against a wildcard admitting it",
		a:      elem(qn(nsA, "x")),
		b:      wild(modelWildcard{variety: varAny}),
		want10: true, want11: false,
		why: "1.1 resolves element-vs-wildcard in favour of the element",
	}, {
		name:   "element against a wildcard refusing it",
		a:      elem(qn(nsA, "x")),
		b:      wild(modelWildcard{variety: varEnum, namespaces: map[string]bool{nsB: true}}),
		want10: false, want11: false,
		why: "the wildcard does not admit http://a",
	}, {
		name: "notQName does not defuse a 1.0 element/wildcard clash",
		a:    elem(qn(nsA, "x")),
		b: wild(modelWildcard{
			variety:    varAny,
			disallowed: map[xdm.QName]bool{qn(nsA, "x"): true},
		}),
		want10: true, want11: false,
		why: "Appendix H compares namespace constraints, not disallowed names",
	}}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range []struct {
				v    Version
				want bool
			}{{Version10, tc.want10}, {Version11, tc.want11}} {
				if got := modelCompetes(tc.a, tc.b, p.v); got != p.want {
					t.Fatalf("XSD %s: the model itself disagrees with the case: "+
						"the case expects compete=%v, the model says %v (%s)",
						versionLabel(p.v), p.want, got, tc.why)
				}
				got, err := upaRejects(buildTerm(tc.a), buildTerm(tc.b), p.v)
				if err != nil {
					t.Fatalf("XSD %s: compiling the pair: %v", versionLabel(p.v), err)
				}
				if got != p.want {
					t.Fatalf("XSD %s:\n  A: %s\n  B: %s\n"+
						"  want rejected=%v (%s)\n  checkUPA rejects=%v",
						versionLabel(p.v), describeModelTerm(tc.a),
						describeModelTerm(tc.b), p.want, tc.why, got)
				}
			}
		})
	}
}
