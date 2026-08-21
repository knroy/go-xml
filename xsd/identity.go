package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
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
	entries map[string]*xdm.Node
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
		tbl := v.buildNodeTable(el, ic)
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
				out[ic] = &nodeTable{entries: copyEntries(tbl.entries)}
				continue
			}
			for k, n := range tbl.entries {
				if _, clash := existing.entries[k]; clash {
					delete(existing.entries, k)
					continue
				}
				existing.entries[k] = n
			}
		}
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
func (v *validator) buildNodeTable(el *xdm.Node, ic *IdentityConstraint) *nodeTable {
	tbl := &nodeTable{entries: map[string]*xdm.Node{}}

	for _, target := range selectNodes(el, ic.Selector) {
		if v.inSkippedContent(target) {
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
	}
	return tbl
}

// checkKeyref resolves a keyref against the node table of the key it refers to.
func (v *validator) checkKeyref(el *xdm.Node, ic *IdentityConstraint, tables icTables) {
	if ic.Refer == nil {
		return
	}
	target := tables[ic.Refer]

	for _, node := range selectNodes(el, ic.Selector) {
		if v.inSkippedContent(node) {
			continue
		}
		seq, complete, ok := v.keySequence(node, ic)
		if !ok || !complete {
			// A keyref whose fields are absent simply does not
			// participate; only a key requires every field.
			continue
		}
		joined := strings.Join(seq, keySep)
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

// keySequence evaluates a constraint's fields against one target node.
//
// The three returns distinguish the three outcomes the spec treats
// differently: a complete sequence, a field that selected nothing (which
// disqualifies the node rather than failing), and a field that selected more
// than one node (which is a failure of clause 3).
func (v *validator) keySequence(target *xdm.Node, ic *IdentityConstraint) (seq []string, complete, ok bool) {
	for _, field := range ic.Fields {
		nodes := selectNodes(target, field)
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
		// The key sequence uses the [schema normalized value], so a
		// defaulted or fixed value participates. The annotation written
		// during validation is not enough to recover the type here, so
		// the string value is used with whitespace collapsed — which is
		// what every built-in except xs:string does anyway.
		seq = append(seq, strings.TrimSpace(n.StringValue()))
	}
	return seq, true, true
}

// selectNodes evaluates a selector or field path from a context node.
//
// The paths are the restricted subset of §3.11.6: child-axis steps, an optional
// leading ".//", an optional trailing attribute step, and "|" alternatives.
// Nothing here needs the XPath engine, and using it would accept expressions
// the spec forbids.
func selectNodes(context *xdm.Node, path *ICPath) []*xdm.Node {
	if path == nil {
		return nil
	}
	var out []*xdm.Node
	seen := map[*xdm.Node]bool{}

	for _, alt := range path.Alternatives {
		starts := []*xdm.Node{context}
		if alt.DescendantOrSelf {
			starts = descendantsOrSelf(context)
		}
		for _, start := range starts {
			for _, n := range walkSteps(start, alt) {
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
func walkSteps(start *xdm.Node, alt ICPathAlternative) []*xdm.Node {
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
		for _, a := range n.Attrs {
			if alt.AttributeWildcard {
				// "@*" selects every attribute, which is
				// grammatical even though a field using it can
				// only be valid when the element has exactly one.
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
