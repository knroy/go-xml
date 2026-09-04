package xsd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"math/big"
)

// Identity constraint evaluation (§3.11.4, §3.11.5).
//
// The shape of this is dictated by one rule that is easy to miss and expensive
// to get wrong: node tables are "assembled strictly recursively from the node
// tables of descendants", so a keyref resolves against the subtree rooted at
// the element carrying the constraint — not against the document. Implementing
// keyref as a flat document-wide map is the standard shortcut and it accepts
// references that should fail, because a key defined in a sibling subtree is
// not in scope.

// nodeTable holds the key sequences found for one identity constraint within
// one subtree.
type nodeTable struct {
	// entries maps a key sequence to the element it was found on. The
	// sequence is joined with a separator that cannot appear in a value,
	// so that ("a", "b") and ("a b") are distinct keys.
	//
	// A sequence that two SIBLING subtrees both define is absent: the
	// merge deletes it, because an ancestor's keyref cannot say which of
	// them it should resolve to. That deletion is why entries alone cannot
	// answer "did this sequence occur below me", which targets does.
	entries map[string]*xdm.Node

	// targets records every node this constraint selected in the subtree,
	// with the key sequence it produced. Unlike entries it is not merged
	// away on a clash, so an enclosing scope can decide duplicates from it
	// without walking the subtree again.
	//
	// The two are kept apart because they answer different questions.
	// entries is what a keyref resolves against and must drop ambiguity;
	// targets is what a duplicate check counts and must not.
	targets map[*xdm.Node]string

	// ambiguous records the sequences that two or more sibling subtrees
	// each defined, and which were therefore removed from entries.
	//
	// Deleting from entries is not enough on its own: a key is absent both
	// before it is first seen and after it has been dropped as ambiguous,
	// and the merge cannot tell those apart from entries alone. Three
	// siblings defining the same sequence used to leave it RESOLVABLE —
	// the second deleted it and the third found nothing there and put it
	// back — so an ancestor's keyref resolved against a key no reading of
	// the document makes unique. It oscillated with the count: wrong at
	// three siblings and five, right at two and four.
	ambiguous map[string]bool
}

// mergeEntry folds one sibling's entry into this table.
//
// Every merge goes through here so the three states stay distinct: absent, a
// unique node, and ambiguous. Ambiguity is terminal — once two subtrees have
// defined a sequence, nothing a later sibling contributes can make it
// resolvable again.
func (t *nodeTable) mergeEntry(k string, n *xdm.Node) {
	if t.ambiguous[k] {
		return
	}
	if prev, exists := t.entries[k]; exists && prev != n {
		delete(t.entries, k)
		if t.ambiguous == nil {
			t.ambiguous = map[string]bool{}
		}
		t.ambiguous[k] = true
		return
	}
	t.entries[k] = n
}

// keySep separates the fields of a key sequence.
//
// It is a unit separator rather than a space because a field value may contain
// spaces: joining ["a b"] and ["a", "b"] on a space would make them equal and
// silently merge two different keys.
const keySep = "\x1f"

// icTables is the set of node tables for one element, keyed by the constraint
// they belong to.
type icTables map[*IdentityConstraint]*nodeTable

// icStats counts the work an identity-constraint evaluation actually does.
//
// Elapsed time says a run was slow; it does not say the same node was examined
// once per enclosing scope, which is the specific thing wrong here. These
// counters do, and they are what a redesign has to move: a one-pass evaluator
// should hold SelectorEvals near NodesVisited, where the current one holds it
// near NodesVisited times depth.
//
// It is nil in ordinary use and allocated only by a test that asks for it, so
// the counting costs a nil check on paths that are already doing map work.
type icStats struct {
	// NodesVisited counts nodes reached by a selector or field walk,
	// including those a step rejects.
	NodesVisited uint64
	// SelectorEvals counts calls to selectNodes for a constraint's
	// selector — one per scope in the current design.
	SelectorEvals uint64
	// FieldEvals counts calls to selectNodes for a field.
	FieldEvals uint64
	// Targets counts selected nodes a key sequence was built for.
	Targets uint64
	// Seeded counts targets whose key sequence was reused from the
	// subtree's table rather than rebuilt.
	Seeded uint64
	// TableOps counts entry reads and writes on a node table.
	TableOps uint64
}

// icStatsHook lets a measurement test attach counters to the next validation.
// It is nil in every ordinary build and set only by identity_stats_test.go,
// which is why the counters cost a nil check rather than an option.
var icStatsHook func() *icStats

// checkIdentityConstraints evaluates the constraints declared on an element and
// returns the node tables for its subtree.
//
// The tables returned include those of every descendant, merged, which is what
// makes the recursion in the spec's definition work: an ancestor's keyref can
// see a key defined anywhere below it, and nothing outside.
func (v *validator) checkIdentityConstraints(el *xdm.Node, decl *ElementDecl, children []icTables) icTables {
	merged := mergeTables(children)

	if decl == nil || len(decl.IdentityConstraints) == 0 {
		return merged
	}

	// key and unique are evaluated before keyref, because a keyref on the
	// same element may refer to a key on that element.
	for _, ic := range decl.IdentityConstraints {
		if ic.Kind == ICKeyref {
			continue
		}
		tbl := v.buildNodeTable(el, ic, merged[ic])
		merged[ic] = tbl
	}
	for _, ic := range decl.IdentityConstraints {
		if ic.Kind != ICKeyref {
			continue
		}
		v.checkKeyref(el, ic, merged)
	}
	return merged
}

// mergeTables combines the node tables of an element's children.
//
// The spec says entries that conflict are dropped entirely rather than one
// being chosen: two descendants may each define the same key value, and the
// merged table cannot say which the ancestor's keyref should resolve to. That
// is only reachable through a unique or key that is itself scoped below, so a
// conflict here is not a validity failure at this level.
func mergeTables(children []icTables) icTables {
	out := icTables{}
	for _, child := range children {
		for ic, tbl := range child {
			existing, ok := out[ic]
			if !ok {
				out[ic] = &nodeTable{
					entries:   copyEntries(tbl.entries),
					targets:   copyTargets(tbl.targets),
					ambiguous: copyAmbiguous(tbl.ambiguous),
				}
				continue
			}
			// A sequence already ambiguous in the child stays ambiguous
			// here: the ancestor's scope contains that child's whole
			// subtree, so both occurrences are inside it.
			for k := range tbl.ambiguous {
				if existing.ambiguous == nil {
					existing.ambiguous = map[string]bool{}
				}
				existing.ambiguous[k] = true
				delete(existing.entries, k)
			}
			for k, n := range tbl.entries {
				existing.mergeEntry(k, n)
			}
			// targets is a union: a node selected in one subtree is
			// still selected in the ancestor's, and a sequence two
			// siblings share must survive here even though entries
			// drops it, because the ancestor's scope contains both
			// occurrences and has to see them.
			for n, k := range tbl.targets {
				existing.targets[n] = k
			}
		}
	}
	return out
}

func copyAmbiguous(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyTargets(in map[*xdm.Node]string) map[*xdm.Node]string {
	out := make(map[*xdm.Node]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyEntries(in map[string]*xdm.Node) map[string]*xdm.Node {
	out := make(map[string]*xdm.Node, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// buildNodeTable evaluates a key or unique constraint over an element's
// subtree.
func (v *validator) buildNodeTable(el *xdm.Node, ic *IdentityConstraint, below *nodeTable) *nodeTable {
	tbl := &nodeTable{
		entries: map[string]*xdm.Node{},
		targets: map[*xdm.Node]string{},
	}

	// A per-element check in validateElement does not reach this loop: one
	// constraint at the top of a deeply recursive document selects the whole
	// subtree, so the single outermost call is where most of the quadratic
	// cost documented in docs/security.md is actually spent. The loop is over
	// selected targets and each iteration builds a key sequence, so a check
	// per target is cheap against the work it guards.
	//
	// below is the table this same constraint already built for the subtree,
	// merged from the children. Its targets are a subset of the ones selected
	// here — the same constraint on a nested element selects out of a smaller
	// subtree — so their key sequences are already computed and are seeded
	// rather than recomputed. The scan still runs over every target, because
	// a duplicate is only a failure within the scope that contains both
	// occurrences and the inner scope cannot have reported it, but seeding
	// lets the sequence work be skipped for anything already keyed.
	// below is the table this same constraint already built for the subtree,
	// merged up from the children, and its targets carry the key sequence
	// each one produced. Those are seeded rather than recomputed.
	//
	// It is not enough on its own to skip the walk. The tables that merge up
	// come from every element child, but only a child that DECLARES this
	// constraint contributes one for it, so a target sitting under a child
	// that does not declare it is reachable from here and absent from below.
	// The walk therefore still runs, and prunes: descending stops at any
	// element that declares this same constraint, because everything under
	// it is already in below.targets. On the recursive shape that turns the
	// per-scope subtree walk into a walk of the gap between one scope and
	// the next.
	// The subtree's targets are carried in wholesale, keeping the sequence
	// each one already produced. They are seeded into the table BEFORE the
	// walk rather than being matched against it: the pruned walk returns
	// only what this scope newly reaches, so a target from below is never
	// visited here and would otherwise never enter the table at all. That
	// is the cross-level duplicate case — a key at depth 1 and the same key
	// at depth 2 collide in the scope that contains both, and the inner
	// scope cannot have reported it.
	if below != nil {
		for n, k := range below.targets {
			tbl.targets[n] = k
			if prev, dup := tbl.entries[k]; dup && prev != n {
				code := "cvc-identity-constraint.4.1"
				if ic.Kind == ICKey {
					code = "cvc-identity-constraint.4.2.2"
				}
				v.fail(n, code,
					"%s %q: duplicate key sequence", ic.Kind, ic.Name.Local)
				continue
			}
			tbl.entries[k] = n
		}
	}

	for _, target := range v.selectNodesPruned(el, ic) {
		if v.checkCancelled() {
			return tbl
		}
		if v.inSkippedContent(target) {
			continue
		}
		if v.icStats != nil {
			v.icStats.Targets++
		}
		if _, already := tbl.targets[target]; already {
			// Carried in from below; its sequence is known and the
			// collision check has already run for it.
			if v.icStats != nil {
				v.icStats.Seeded++
			}
			continue
		}
		seq, complete, ok := v.keySequence(target, ic)
		if !ok {
			// A field selected more than one node, which clause 3
			// makes a hard failure whatever the category.
			continue
		}
		if !complete {
			// A field selected nothing. For unique that removes the
			// node from the qualified set; for key it is a failure,
			// because every target must be qualified.
			if ic.Kind == ICKey {
				v.fail(target, "cvc-identity-constraint.4.2.1",
					"key %q: a field selects no value", ic.Name.Local)
			}
			continue
		}

		joined := strings.Join(seq, keySep)
		if v.icStats != nil {
			v.icStats.TableOps++
		}
		if prev, dup := tbl.entries[joined]; dup && prev != target {
			code := "cvc-identity-constraint.4.1"
			if ic.Kind == ICKey {
				code = "cvc-identity-constraint.4.2.2"
			}
			v.fail(target, code,
				"%s %q: duplicate key sequence", ic.Kind, ic.Name.Local)
			continue
		}
		tbl.entries[joined] = target
		tbl.targets[target] = joined
	}
	return tbl
}

// checkKeyref resolves a keyref against the node table of the key it refers to.
func (v *validator) checkKeyref(el *xdm.Node, ic *IdentityConstraint, tables icTables) {
	if ic.Refer == nil {
		return
	}
	target := tables[ic.Refer]

	// Same reasoning as buildNodeTable: one keyref at the top of a recursive
	// document walks the whole subtree in this one call.
	for _, node := range v.selectNodes(el, ic.Selector) {
		if v.checkCancelled() {
			return
		}
		if v.inSkippedContent(node) {
			continue
		}
		joined, complete, ok := v.cachedKeySequence(node, ic)
		if !ok || !complete {
			// A keyref whose fields are absent simply does not
			// participate; only a key requires every field.
			continue
		}
		if target == nil {
			v.fail(node, "cvc-identity-constraint.4.3",
				"keyref %q: no %s is in scope", ic.Name.Local, ic.Refer.Name.Local)
			return
		}
		if _, ok := target.entries[joined]; !ok {
			v.fail(node, "cvc-identity-constraint.4.3",
				"keyref %q: no matching %s for %q",
				ic.Name.Local, ic.Refer.Name.Local,
				strings.ReplaceAll(joined, keySep, ", "))
		}
	}
}

// cachedKeySequence is keySequence with the result kept per (node, constraint).
//
// Unlike a key or unique, a keyref cannot prune: it produces no table for an
// ancestor to seed from, and a node under a nested keyref scope is a target of
// that scope AND of every enclosing one, each resolving against its own key
// table. Both checks are required, so the number of checks really is nodes
// times enclosing scopes and no traversal change can remove it.
//
// What is repeated needlessly is the field extraction. A node's key sequence
// does not depend on which scope is asking, and computing it means running the
// field paths — the expensive half of the loop. Caching it leaves the
// quadratic count of cheap map lookups and removes the quadratic count of
// selectNodes calls.
func (v *validator) cachedKeySequence(node *xdm.Node, ic *IdentityConstraint) (joined string, complete, ok bool) {
	type key struct {
		n  *xdm.Node
		ic *IdentityConstraint
	}
	k := key{node, ic}
	if v.keySeqCache == nil {
		v.keySeqCache = map[any]cachedSeq{}
	}
	if c, hit := v.keySeqCache[k]; hit {
		return c.joined, c.complete, c.ok
	}
	seq, complete, ok := v.keySequence(node, ic)
	if ok && complete {
		joined = strings.Join(seq, keySep)
	}
	v.keySeqCache[k] = cachedSeq{joined: joined, complete: complete, ok: ok}
	return joined, complete, ok
}

// cachedSeq is one memoised key sequence.
type cachedSeq struct {
	joined   string
	complete bool
	ok       bool
}

// keySequence evaluates a constraint's fields against one target node.
//
// The three returns distinguish the three outcomes the spec treats
// differently: a complete sequence, a field that selected nothing (which
// disqualifies the node rather than failing), and a field that selected more
// than one node (which is a failure of clause 3).
func (v *validator) keySequence(target *xdm.Node, ic *IdentityConstraint) (seq []string, complete, ok bool) {
	for _, field := range ic.Fields {
		if v.icStats != nil {
			v.icStats.FieldEvals++
			v.icStats.SelectorEvals-- // counted as a field, not a selector
		}
		nodes := v.selectNodes(target, field)
		switch len(nodes) {
		case 0:
			return nil, false, true
		case 1:
		default:
			v.fail(target, "cvc-identity-constraint.3",
				"%s %q: a field selects %d nodes, want at most one",
				ic.Kind, ic.Name.Local, len(nodes))
			return nil, false, false
		}

		n := nodes[0]
		// §3.11.4 clause 3: the single selected node "must have a
		// simple type". An element assessed against a complex type
		// with empty, element-only or mixed content has no
		// ·type-determined value·, so there is no ·key-sequence· to
		// build and the constraint fails outright — for unique as much
		// as for key, since clause 3 is above the case split in clause
		// 4. idG006 (key over a field naming an empty complex type
		// carrying an attribute) and idK012 (unique over a field
		// naming an element-only complex type) are the pair; both are
		// expected invalid and both were accepted, because the key
		// string quietly fell back to the node's string value.
		if v.complexTyped[n] {
			v.fail(target, "cvc-identity-constraint.3",
				"%s %q: a field selects an element with a complex type, "+
					"which has no simple value",
				ic.Kind, ic.Name.Local)
			return nil, false, false
		}
		// The key sequence uses the [schema normalized value], so a
		// defaulted or fixed value participates. For most primitives
		// the normalized lexical form is canonical, so collapsing
		// whitespace makes the string comparison a value comparison.
		// The temporal families are the exception — 24:00:00Z and
		// 00:00:00Z are one time, PT29H and P1DT5H one duration — and
		// their canonical form is recovered from what validation
		// recorded, since the constraint sees only nodes.
		seq = append(seq, v.keyString(n))
	}
	return seq, true, true
}

// selectNodes evaluates a selector or field path from a context node.
//
// The paths are the restricted subset of §3.11.6: child-axis steps, an optional
// leading ".//", an optional trailing attribute step, and "|" alternatives.
// Nothing here needs the XPath engine, and using it would accept expressions
// the spec forbids.
// selectNodesPruned selects a constraint's targets from el, skipping any
// subtree an inner scope of the SAME constraint has already accounted for.
//
// A descendant element that declares this constraint is itself a scope: its
// own table already holds every target beneath it, merged up as below. Walking
// into it again is the repeated work that makes the whole evaluation
// quadratic, and everything it would find is already keyed.
//
// Only a leading ".//" is pruned. Without one the path is anchored at el and
// reaches a bounded number of nodes, so there is nothing to save and the
// ordinary selection is used unchanged.
func (v *validator) selectNodesPruned(el *xdm.Node, ic *IdentityConstraint) []*xdm.Node {
	path := ic.Selector
	if path == nil {
		return nil
	}
	anyDescendant := false
	for _, alt := range path.Alternatives {
		if alt.DescendantOrSelf {
			anyDescendant = true
			break
		}
	}
	if !anyDescendant {
		return v.selectNodes(el, path)
	}

	// The frontier is el plus every element beneath it that is not inside a
	// nested scope of this constraint. Selecting from the frontier with the
	// ".//" already accounted for gives the same set the unpruned walk
	// produces, minus what below already holds.
	var out []*xdm.Node
	seen := map[*xdm.Node]bool{}
	for _, start := range v.frontier(el, ic) {
		for _, alt := range path.Alternatives {
			var hits []*xdm.Node
			if alt.DescendantOrSelf {
				hits = v.walkSteps(start, alt)
			} else if start == el {
				hits = v.walkSteps(start, alt)
			}
			for _, n := range hits {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// frontier returns el and the elements beneath it, stopping at any element
// other than el that declares ic — that element's own scope covers what lies
// below it.
func (v *validator) frontier(el *xdm.Node, ic *IdentityConstraint) []*xdm.Node {
	out := []*xdm.Node{el}
	for i := 0; i < len(out); i++ {
		for _, c := range out[i].ChildElements() {
			if v.declaresConstraint(c, ic) {
				continue
			}
			out = append(out, c)
		}
	}
	if v.icStats != nil {
		v.icStats.NodesVisited += uint64(len(out))
	}
	return out
}

// hasIdentityConstraints reports whether any declaration in the schema carries
// an identity constraint, so that the per-element bookkeeping the walk needs
// can be skipped entirely for the schemas that have none.
func (s *Schema) hasIdentityConstraints() bool {
	for _, d := range s.Elements {
		if d != nil && len(d.IdentityConstraints) > 0 {
			return true
		}
	}
	for _, t := range s.Types {
		ct, ok := t.(*ComplexType)
		if !ok || ct.Particle == nil {
			continue
		}
		found := false
		walkParticleElements(ct.Particle, map[*Particle]bool{}, func(d *ElementDecl) {
			if len(d.IdentityConstraints) > 0 {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

// declaresConstraint reports whether the declaration governing el carries ic.
func (v *validator) declaresConstraint(el *xdm.Node, ic *IdentityConstraint) bool {
	decl := v.declFor[el]
	if decl == nil {
		return false
	}
	for _, d := range decl.IdentityConstraints {
		if d == ic {
			return true
		}
	}
	return false
}

func (v *validator) selectNodes(context *xdm.Node, path *ICPath) []*xdm.Node {
	if path == nil {
		return nil
	}
	if v.icStats != nil {
		v.icStats.SelectorEvals++
	}
	var out []*xdm.Node
	seen := map[*xdm.Node]bool{}

	for _, alt := range path.Alternatives {
		starts := []*xdm.Node{context}
		if alt.DescendantOrSelf {
			starts = descendantsOrSelf(context)
		}
		if v.icStats != nil {
			v.icStats.NodesVisited += uint64(len(starts))
		}
		for _, start := range starts {
			for _, n := range v.walkSteps(start, alt) {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// walkSteps applies one alternative's steps from a starting node.
func (v *validator) walkSteps(start *xdm.Node, alt ICPathAlternative) []*xdm.Node {
	current := []*xdm.Node{start}
	for _, step := range alt.Steps {
		var next []*xdm.Node
		for _, n := range current {
			for _, c := range n.ChildElements() {
				if stepMatches(step, c) {
					next = append(next, c)
				}
			}
		}
		current = next
		if len(current) == 0 {
			return nil
		}
	}

	if alt.Attribute == nil {
		return current
	}

	var attrs []*xdm.Node
	for _, n := range current {
		// An attribute the type supplied by default is part of the
		// infoset as much as a written one, so a field selects it. It
		// is not in n.Attrs, because validation does not rewrite the
		// caller's tree; the value recorded at the time it was applied
		// stands in, carried on a node that exists only for this
		// comparison.
		if !alt.AttributeWildcard && alt.Attribute != nil {
			if val, ok := v.defaultedAttrs[defaultedAttr{
				el:   n,
				name: xdm.QName{URI: alt.Attribute.URI, Local: alt.Attribute.Local},
			}]; ok && !hasWrittenAttr(n, alt.Attribute) {
				syn := &xdm.Node{
					Kind:   xdm.KindAttribute,
					Name:   xdm.QName{Local: alt.Attribute.Local},
					Value:  val.normalized,
					Parent: n,
				}
				// The synthetic node needs the same key entry a
				// written attribute would have, or it compares
				// by raw string against keys that compare by
				// primitive and never matches.
				if v.keyValues == nil {
					v.keyValues = map[*xdm.Node]keyValue{}
				}
				v.keyValues[syn] = keyValue{
					normalized: val.normalized,
					primitive:  val.primitive,
				}
				attrs = append(attrs, syn)
				continue
			}
		}
		for _, a := range n.Attrs {
			if alt.AttributeWildcard {
				// "@*" selects every attribute, which is
				// grammatical even though a field using it can
				// only be valid when the element has exactly one.
				//
				// "@prefix:*" is the narrower form and selects
				// only the attributes in that namespace. idL102
				// pins the difference: its field is "@myNS:*"
				// over elements carrying both myNS:row and
				// xsi:nil, so ignoring the prefix selected two
				// attributes and failed a key that holds.
				if alt.Attribute != nil && alt.Attribute.Prefix != "" &&
					!attrNamespaceMatches(a, alt.Attribute) {
					continue
				}
				attrs = append(attrs, a)
				continue
			}
			if a.Name.Local == alt.Attribute.Local &&
				attrNamespaceMatches(a, alt.Attribute) {
				attrs = append(attrs, a)
			}
		}
	}
	return attrs
}

// hasWrittenAttr reports whether the document itself carried the attribute a
// field names, in which case no default was applied.
func hasWrittenAttr(n *xdm.Node, want *xdm.QName) bool {
	for _, a := range n.Attrs {
		if a.Name.Local == want.Local && attrNamespaceMatches(a, want) {
			return true
		}
	}
	return false
}

// attrNamespaceMatches compares an attribute's namespace with a field's.
//
// The prefix in the path is resolved against the schema document, not the
// instance, so an unprefixed name means the absent namespace — which is where
// unqualified attributes live.
func attrNamespaceMatches(a *xdm.Node, want *xdm.QName) bool {
	if want.URI != "" {
		return a.Name.URI == want.URI
	}
	if want.Prefix == "" {
		return a.Name.URI == ""
	}
	// A prefix that never resolved: match on the local name alone rather
	// than silently selecting nothing.
	return true
}

// stepMatches reports whether an element satisfies one path step.
func stepMatches(step ICStep, el *xdm.Node) bool {
	if step.Wildcard {
		return true
	}
	if el.Name.Local != step.Name.Local {
		return false
	}
	if step.Name.URI != "" {
		return el.Name.URI == step.Name.URI
	}
	// An unprefixed name in a selector refers to the absent namespace,
	// unless the prefix simply failed to resolve.
	if step.Name.Prefix == "" {
		return el.Name.URI == ""
	}
	return true
}

// descendantsOrSelf returns an element and every element beneath it, in
// document order.
func descendantsOrSelf(el *xdm.Node) []*xdm.Node {
	out := []*xdm.Node{el}
	for i := 0; i < len(out); i++ {
		out = append(out, out[i].ChildElements()...)
	}
	return out
}

// inSkippedContent reports whether a node lies inside content matched by a
// processContents="skip" wildcard.
//
// An identity constraint selects nodes out of the PSVI, and skipped content was
// never assessed — it has no schema-normalized values and no type annotations,
// so there is nothing there for a field to select. Reaching into it makes a key
// report a duplicate for an element the schema explicitly said not to look at,
// or a missing field for one that was never validated.
//
// The walk is upward from the node rather than a mark on every descendant,
// because a skip wildcard may cover an arbitrarily large subtree and marking it
// wholesale would cost more than the constraints that consult it.
func (v *validator) inSkippedContent(n *xdm.Node) bool {
	if len(v.skipped) == 0 {
		return false
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if v.skipped[cur] {
			return true
		}
	}
	return false
}

// keyString returns the value a node contributes to a key sequence.
//
// Two values that are equal must produce the same string, because the table is
// keyed by the joined sequence. For the temporal families that means a
// canonical form rather than the spelling: a keyref written 05:00:00+05:00 has
// to find a key written 00:00:00Z, which is the same time.
func (v *validator) keyString(n *xdm.Node) string {
	kv, ok := v.keyValues[n]
	if !ok {
		return strings.TrimSpace(n.StringValue())
	}
	if c, ok := canonicalTemporal(kv.normalized, kv.primitive); ok {
		return c
	}
	if c, ok := canonicalValue(kv.normalized, kv.primitive); ok {
		return c
	}
	// A QName's value is its namespace URI and local name; the prefix is only
	// a way of spelling the URI. Two prefixes bound to one namespace therefore
	// denote the same value, so the prefix is resolved away here rather than
	// compared literally. sunData IdentityTestSuite/002 test.2.v pins this: the
	// key is written p:abc and the keyref q:abc, with both prefixes bound to
	// "abc". Resolution needs the node's in-scope namespaces, which is why this
	// lives here and not in canonicalValue.
	if kv.primitive == "QName" || kv.primitive == "NOTATION" {
		local := kv.normalized
		prefix := ""
		if i := strings.IndexByte(local, ':'); i >= 0 {
			prefix, local = local[:i], local[i+1:]
		}
		// An unresolvable prefix cannot arise from a value that validated, but
		// falling back to the lexical form keeps this total.
		if uri, ok := n.LookupPrefix(prefix); ok {
			return kv.primitive + "/{" + uri + "}" + local
		}
		if prefix == "" {
			return kv.primitive + "/{}" + local
		}
	}
	// The primitive is part of the key. Values drawn from different
	// primitives are never equal, whatever their spellings do: idF012 has
	// the boolean 1 beside the decimal 1 and expects no duplicate.
	//
	// Types that share a primitive still compare by value, which is what
	// keeps xs:int 1 equal to xs:integer 1 — both are decimal.
	return kv.primitive + "/" + kv.normalized
}

// canonicalValue renders a non-temporal value in a form that is the same for
// every literal denoting it.
//
// The temporal families are handled by canonicalTemporal; this covers the rest
// of the primitives whose lexical space has more than one spelling per value.
// A key sequence compares values, so a keyref written 5.0 has to find a key
// written 5 — sunData's identity suite pins exactly that pair, and the numeric
// primitives are where the spelling varies most.
func canonicalValue(normalized, primitive string) (string, bool) {
	switch primitive {
	case "decimal", "float", "double":
		// A rational is the canonical form for all three: 5, 5.0 and
		// 05.00 share one, and INF and NaN have no rational so they
		// fall through to the lexical comparison, which is right —
		// each has a single spelling.
		if r, ok := new(big.Rat).SetString(normalized); ok {
			return primitive + "/" + r.RatString(), true
		}
		// The specials have no rational. Each is its own canonical
		// form, except that +INF and INF are two spellings of one
		// value.
		switch normalized {
		case "+INF", "INF":
			return primitive + "/INF", true
		case "-INF", "NaN":
			return primitive + "/" + normalized, true
		}
	case "boolean":
		// "1" and "true" are the same value, as are "0" and "false".
		switch normalized {
		case "1", "true":
			return "boolean/true", true
		case "0", "false":
			return "boolean/false", true
		}
	}
	return "", false
}

// canonicalTemporal renders a date, time or duration in a form that is the same
// for every literal denoting the same value.
//
// The seconds-from-epoch the comparison already computes is exactly such a
// form, so this reuses it rather than inventing a second notion of equality
// that could disagree with compareTemporal. A value with no timezone is kept
// distinct from one with, because they are not equal — the order between them
// is indeterminate, and an indeterminate pair is not a match.
func canonicalTemporal(normalized, primitive string) (string, bool) {
	if primitive == "duration" {
		d, ok := parseDuration(normalized)
		if !ok || d.seconds == nil {
			return "", false
		}
		// Two durations are equal exactly when their months and their
		// seconds both agree — compareDuration's fast path — so the
		// pair is itself a canonical key. P1M and P30D differ here, and
		// rightly: they are incomparable, not equal.
		return fmt.Sprintf("duration/%d/%s", d.months, d.seconds.RatString()), true
	}
	t, ok := parseTemporal(normalized, primitive)
	if !ok || t.seconds == nil {
		return "", false
	}
	tz := "-"
	if t.hasTZ {
		tz = "Z"
	}
	return primitive + "/" + tz + "/" + t.seconds.RatString(), true
}
