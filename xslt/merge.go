package xslt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// xsl:merge and its three companion elements, XSLT 3.0 section 15.
//
// The instruction takes several already-sorted input sequences, interleaves
// them by a common merge key, and runs xsl:merge-action once per distinct
// composite key value. Each xsl:merge-source describes one *kind* of input:
// how to find the sequences (a plain select, or a select evaluated against
// each of a set of anchor items) and how to compute the key from an item.
//
// The merge itself is expressed here as a stable sort rather than an n-way
// interleave. The two are the same answer: the inputs are required to be
// sorted already, so ordering the union by the key and breaking ties by
// (source, anchor, position) reproduces exactly the interleaving section
// 15.6.1 describes — and it also gives sort-before-merge for free, since an
// input that was *not* sorted simply lands where its keys say. The separate
// XTDE2220 scan below is what keeps the unsorted case an error rather than a
// silent re-sort.
//
// Streaming is not implemented. streamable="yes" is a statement about memory,
// not about the answer: 19.10 leaves a processor that does not stream free to
// evaluate conventionally, and the sole visible consequence — that last() is
// absent inside the action — is honoured.

// mergeInstr is a compiled xsl:merge.
type mergeInstr struct {
	sources []*mergeSource
	action  []Instruction
	// streamed records that some xsl:merge-source asked for streaming, which
	// section 15.7 makes the context size absent inside the action. That is
	// observable — last() must raise XPDY0002 — so it is carried even though
	// nothing else about streaming is.
	streamed bool
}

// mergeSource is a compiled xsl:merge-source: one merge source definition.
type mergeSource struct {
	// name is @name, or the invented name allocated when the attribute is
	// absent. current-merge-group($source) matches against it, so an invented
	// name must be one no stylesheet can write; see compileMerge.
	name string
	// named records whether @name was written. An invented name is not a name
	// the stylesheet may ask for, so XTDE3490 must still be raised for it.
	named bool
	// sel is @select, evaluated once per anchor item (or once with the
	// xsl:merge focus when there is no anchor).
	sel *xpath.Compiled
	// anchors is @for-each-item, whose result items each yield one input
	// sequence.
	anchors *xpath.Compiled
	// sourceURIs is @for-each-source, whose result is a sequence of URIs each
	// of which is read as a document; the document nodes are the anchors.
	sourceURIs *xpath.Compiled
	// baseURI resolves a relative @for-each-source URI, which section 15.3
	// resolves against the base URI of the xsl:merge-source element rather
	// than of the stylesheet module.
	baseURI string
	// validation applies to documents read by @for-each-source only.
	validation validationSpec
	// accums is @use-accumulators: the accumulators 18.2.2 makes applicable
	// to the documents this source reads. nil means the attribute was absent,
	// which leaves every accumulator applicable.
	accums *modeAccumulators
	// streamed records streamable="yes", whose only visible consequence here
	// is that the context size inside the action is absent.
	streamed bool
	// sortBeforeMerge records sort-before-merge="yes", which both sorts the
	// input and suppresses XTDE2220 for it.
	sortBeforeMerge bool
	// keys are the xsl:merge-key children, in order. An xsl:merge-key is
	// syntactically an xsl:sort without @stable, so it compiles to the same
	// sortKey and reuses the same comparison machinery.
	keys []*sortKey
	// keyElems are the xsl:merge-key elements themselves, kept for the
	// XTDE2210 compatibility check, which compares attributes across sources
	// and so needs to know which were written at all.
	keyElems []*xdm.Node
}

// mergeEntry is one item of one input sequence, with its composite key.
type mergeEntry struct {
	item xdm.Item
	keys []sortValue
	// atoms is the key vector before makeSortValue reduced it, one entry per
	// merge key component and nil where the key was empty or not a single
	// atomic value. XTTE2230 is asked of the values as the "le" operator
	// would see them, and makeSortValue deliberately drops an
	// xs:untypedAtomic — the type the error is most often about — because an
	// xsl:sort promotes it to text rather than refusing it.
	atoms []*xdm.Atomic
	// src, anchor and pos are the tie-break, in the major-to-minor order
	// section 15.6.1 gives for the current merge group: the merge source's
	// position in the stylesheet, then the anchor item's position, then the
	// item's own position in its input sequence.
	src, anchor, pos int
}

// The current merge group and current merge key reach current-merge-group()
// and current-merge-key() through internal variable bindings, for the same
// reason the grouping state does: the xpath package cannot depend on this one.
//
// currentMergeSourcesVar carries the merge source names in document order, so
// that the zero-argument form can concatenate the groups in that order and the
// one-argument form can tell an unknown name (XTDE3490) from a known one whose
// group happens to be empty.
var (
	currentMergeGroupVar   = xdm.QName{URI: internalNS, Local: "current-merge-group"}
	currentMergeKeyVar     = xdm.QName{URI: internalNS, Local: "current-merge-key"}
	currentMergeSourcesVar = xdm.QName{URI: internalNS, Local: "current-merge-sources"}
)

// mergeGroupBinding is the current merge group: the items of one composite key
// value, split by the merge source that contributed them.
//
// It is passed through the context as an opaque item rather than as a map,
// because the map would have to be built for every group whether or not the
// action ever looks at it, and because the names are not all writable — an
// unnamed source's invented name must not be reachable from map:keys.
type mergeGroupBinding struct {
	// names are the merge source names in stylesheet document order.
	names []string
	// named parallels names, recording which were written by the stylesheet.
	named []bool
	// items parallels names: the group's contribution from each source.
	items []xdm.Sequence
}

func (b *mergeGroupBinding) all() xdm.Sequence {
	var out xdm.Sequence
	for _, seq := range b.items {
		out = append(out, seq...)
	}
	return out
}

// --- Compilation ------------------------------------------------------------

func (c *compiler) compileMerge(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &mergeInstr{}
	var actionElem *xdm.Node
	seenAction := false
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "merge-source"):
			if seenAction {
				return nil, fmt.Errorf(
					"XTSE0010: xsl:merge-source follows xsl:merge-action; the " +
						"content of xsl:merge is (xsl:merge-source+, " +
						"xsl:merge-action, xsl:fallback*)")
			}
			src, err := c.compileMergeSource(ch, len(instr.sources))
			if err != nil {
				return nil, err
			}
			instr.sources = append(instr.sources, src)
		case isXSL(ch, "merge-action"):
			if seenAction {
				return nil, fmt.Errorf(
					"XTSE0010: xsl:merge may have only one xsl:merge-action child")
			}
			seenAction = true
			actionElem = ch
		case isXSL(ch, "fallback"):
			// 15.2: fallback children are ignored by a 3.0 processor. They
			// must still come last, which is what the model says.
			if !seenAction {
				return nil, fmt.Errorf(
					"XTSE0010: xsl:fallback precedes xsl:merge-action; the " +
						"content of xsl:merge is (xsl:merge-source+, " +
						"xsl:merge-action, xsl:fallback*)")
			}
		}
	}
	if len(instr.sources) == 0 {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:merge requires at least one xsl:merge-source child")
	}
	if !seenAction {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:merge requires an xsl:merge-action child")
	}

	// XTSE2200 and XTSE3190 are both about the sources as a set, so they are
	// checked once the whole list is known.
	nkeys := len(instr.sources[0].keys)
	seenNames := map[string]bool{}
	for _, src := range instr.sources {
		if len(src.keys) != nkeys {
			return nil, fmt.Errorf(
				"XTSE2200: every xsl:merge-source of an xsl:merge must have the "+
					"same number of xsl:merge-key children (%d against %d)",
				len(src.keys), nkeys)
		}
		if src.named {
			if seenNames[src.name] {
				return nil, fmt.Errorf(
					"XTSE3190: two sibling xsl:merge-source elements are named %q",
					src.name)
			}
			seenNames[src.name] = true
		}
		if src.streamable() {
			instr.streamed = true
		}
	}

	// XTDE2210 compares the key attributes across sources. The attributes are
	// attribute value templates, so a value written with braces cannot be
	// compared until the instruction runs; only the literal ones are decided
	// here, which the error's own wording permits ("may be reported statically
	// if it is detected statically").
	if err := checkMergeKeyCompatibility(instr.sources); err != nil {
		return nil, err
	}

	action, err := c.compileSequence(actionElem, actionElem)
	if err != nil {
		return nil, err
	}
	instr.action = action
	return instr, nil
}

// streamable reports whether the source asked for streamed evaluation.
func (s *mergeSource) streamable() bool { return s.streamed }

func (c *compiler) compileMergeSource(n *xdm.Node, idx int) (*mergeSource, error) {
	ns := newNSResolver(n, "")
	src := &mergeSource{baseURI: n.BaseURI}

	if a := n.Attr("", "name"); a != nil {
		name := strings.TrimSpace(a.Value)
		// The summary types @name as an ncname, so a value that is not one is
		// XTSE0020 — the same code the table check gives an attribute whose
		// value is outside the set the summary allows.
		if !xdm.IsNCName(name) {
			return nil, fmt.Errorf(
				"XTSE0020: xsl:merge-source/@name must be an NCName, got %q", name)
		}
		src.name, src.named = name, true
	} else {
		// 15.3: an absent name gets "an implementation-dependent name,
		// different from all explicitly specified names". A space makes it
		// unwritable as an NCName, so current-merge-group() can never be
		// handed it by accident.
		src.name = fmt.Sprintf("merge source %d", idx)
	}

	hasItem := n.Attr("", "for-each-item") != nil
	hasSource := n.Attr("", "for-each-source") != nil
	hasAccum := n.Attr("", "use-accumulators") != nil
	if v := strings.TrimSpace(n.AttrValue("streamable")); v != "" {
		b, ok := parseMergeBoolean(v)
		if !ok {
			return nil, fmt.Errorf(
				"XTSE0020: xsl:merge-source/@streamable must be a boolean, got %q", v)
		}
		src.streamed = b
	}
	// XTSE3195 states three constraints in one code. for-each-item excludes
	// both of the streaming attributes and for-each-source; use-accumulators
	// requires for-each-source.
	switch {
	case hasItem && hasSource:
		return nil, fmt.Errorf(
			"XTSE3195: xsl:merge-source may not have both for-each-item and " +
				"for-each-source")
	case hasItem && hasAccum:
		return nil, fmt.Errorf(
			"XTSE3195: xsl:merge-source may not have both for-each-item and " +
				"use-accumulators")
	case hasItem && n.Attr("", "streamable") != nil:
		return nil, fmt.Errorf(
			"XTSE3195: xsl:merge-source may not have both for-each-item and " +
				"streamable")
	case hasAccum && !hasSource:
		return nil, fmt.Errorf(
			"XTSE3195: xsl:merge-source/@use-accumulators requires " +
				"for-each-source")
	}

	if hasAccum {
		set, err := parseUseAccumulators(n)
		if err != nil {
			return nil, err
		}
		src.accums = set
	}

	spec, err := compileValidation(n, "")
	if err != nil {
		return nil, err
	}
	// 15.3: "If the for-each-source attribute is absent, then the validation
	// and type attributes must both be absent." The documents those
	// attributes describe are the ones for-each-source reads; with any other
	// selection there is nothing for them to apply to.
	if !hasSource && !spec.isDefault() {
		// XTSE0020 rather than XTSE0090: the attributes are ones
		// xsl:merge-source has, so the error is in the value being written at
		// all in this combination rather than in the name being unknown.
		// merge-054 is the case, and names XTSE0020.
		return nil, fmt.Errorf(
			"XTSE0020: xsl:merge-source/@validation and @type are only allowed " +
				"alongside for-each-source")
	}
	src.validation = spec

	if v := strings.TrimSpace(n.AttrValue("sort-before-merge")); v != "" {
		b, ok := parseMergeBoolean(v)
		if !ok {
			return nil, fmt.Errorf(
				"XTSE0020: xsl:merge-source/@sort-before-merge must be a "+
					"boolean, got %q", v)
		}
		src.sortBeforeMerge = b
	}

	if hasItem {
		if src.anchors, err = compileExpr(n.AttrValue("for-each-item"), ns); err != nil {
			return nil, fmt.Errorf("in xsl:merge-source/@for-each-item: %w", err)
		}
	}
	if hasSource {
		if src.sourceURIs, err = compileExpr(n.AttrValue("for-each-source"), ns); err != nil {
			return nil, fmt.Errorf("in xsl:merge-source/@for-each-source: %w", err)
		}
	}
	// The summary makes @select required, but an absent one is treated as "."
	// rather than refused: with no anchor there is nothing else it could
	// select, and the suite's own examples rely on the element grammar to
	// report a missing one.
	sel := n.AttrValue("select")
	if sel == "" {
		sel = "."
	}
	if src.sel, err = compileExpr(sel, ns); err != nil {
		return nil, fmt.Errorf("in xsl:merge-source/@select: %w", err)
	}

	for _, ch := range n.ChildElements() {
		if !isXSL(ch, "merge-key") {
			continue
		}
		k, err := c.compileMergeKey(ch)
		if err != nil {
			return nil, err
		}
		src.keys = append(src.keys, k)
		src.keyElems = append(src.keyElems, ch)
	}
	if len(src.keys) == 0 {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:merge-source requires at least one xsl:merge-key child")
	}
	return src, nil
}

// compileMergeKey builds one xsl:merge-key.
//
// 15.5 says the syntax and semantics are "closely based on the rules for the
// xsl:sort element (the only exception being the absence of the stable
// attribute)", so the whole of compileSort is reused rather than restated. The
// two differences that matter are handled by the caller and by evalMergeKey:
// the element table gives xsl:merge-key no @stable, and the key is evaluated
// with a singleton focus rather than the position in the unsorted sequence.
func (c *compiler) compileMergeKey(n *xdm.Node) (*sortKey, error) {
	// 15.5 says xsl:merge-key follows xsl:sort's summary with "the only
	// exception being the absence of the stable attribute". The table check
	// would normally report that, but a version="3.0" stylesheet is in
	// forwards-compatible mode as far as this engine's threshold is
	// concerned, so an attribute the summary does not define is ignored
	// there. That leniency is meant for elements of a *later* version, and
	// xsl:merge-key belongs to this one: a 2.0 processor never sees the
	// element at all, so nothing is gained by pretending not to understand
	// its attributes. merge-010 requires the error.
	if a := n.Attr("", "stable"); a != nil {
		return nil, fmt.Errorf(
			"XTSE0090: attribute \"stable\" is not allowed on xsl:merge-key; " +
				"an xsl:merge-key declares an existing order rather than " +
				"causing a sort")
	}

	// XTSE3200 is XTSE1015's counterpart for xsl:merge-key. Both say a select
	// attribute and content are mutually exclusive; only the code differs, so
	// the condition is tested here rather than letting compileSort report the
	// sort element's code for a merge key.
	if n.AttrValue("select") != "" {
		for _, ch := range n.Children {
			switch ch.Kind {
			case xdm.KindElement:
				return nil, fmt.Errorf(
					"XTSE3200: an xsl:merge-key element with a select attribute " +
						"must be empty")
			case xdm.KindText:
				if !xdm.IsXMLWhitespace(ch.Value) {
					return nil, fmt.Errorf(
						"XTSE3200: an xsl:merge-key element with a select " +
							"attribute must be empty")
				}
			}
		}
	}
	return c.compileSort(n)
}

// parseMergeBoolean reads the four lexical forms of xs:boolean the 3.0
// summaries admit wherever they type an attribute as a boolean.
func parseMergeBoolean(v string) (bool, bool) {
	switch v {
	case "yes", "true", "1":
		return true, true
	case "no", "false", "0":
		return false, true
	}
	return false, false
}

// checkMergeKeyCompatibility enforces XTDE2210 over the literal attributes.
//
// The rule is that corresponding xsl:merge-key elements — the Nth child of
// each xsl:merge-source — must agree on lang, order, collation, case-order and
// data-type, where "agree" includes both being absent. An attribute written as
// an attribute value template has no value until the instruction runs, so a
// pair where either side is computed is left to checkMergeKeysAtRuntime.
func checkMergeKeyCompatibility(sources []*mergeSource) error {
	if len(sources) < 2 {
		return nil
	}
	attrs := []string{"lang", "order", "collation", "case-order", "data-type"}
	for j := range sources[0].keyElems {
		first := sources[0].keyElems[j]
		for _, src := range sources[1:] {
			other := src.keyElems[j]
			for _, name := range attrs {
				a, b := first.Attr("", name), other.Attr("", name)
				if (a == nil) != (b == nil) {
					return fmt.Errorf(
						"XTDE2210: corresponding xsl:merge-key elements differ "+
							"in the %s attribute: present on one and absent on "+
							"the other", name)
				}
				if a == nil {
					continue
				}
				av, bv := strings.TrimSpace(a.Value), strings.TrimSpace(b.Value)
				if strings.Contains(av, "{") || strings.Contains(bv, "{") {
					continue // computed; decided at run time
				}
				if av != bv {
					return fmt.Errorf(
						"XTDE2210: corresponding xsl:merge-key elements differ "+
							"in the %s attribute: %q against %q", name, av, bv)
				}
			}
		}
	}
	return nil
}

// --- Execution --------------------------------------------------------------

func (i *mergeInstr) Execute(rt *runtime, out *outputBuilder) error {
	// The key attributes are resolved per source before anything is selected,
	// because XTDE2210 is about the *effective* values and a computed one is
	// only knowable now. Resolving them once here rather than per item also
	// matches what applySorts does for an ordinary sort.
	resolved := make([][]*sortKey, len(i.sources))
	colls := make([][]xpath.Collation, len(i.sources))
	for s, src := range i.sources {
		ks := make([]*sortKey, len(src.keys))
		for k, sk := range src.keys {
			r, err := sk.resolve(rt)
			if err != nil {
				return err
			}
			ks[k] = r
		}
		cs, err := resolveSortCollations(rt, ks)
		if err != nil {
			return err
		}
		resolved[s], colls[s] = ks, cs
	}
	if err := checkMergeKeysAtRuntime(resolved); err != nil {
		return err
	}

	var entries []mergeEntry
	for s, src := range i.sources {
		got, err := src.collect(rt, s, resolved[s], colls[s])
		if err != nil {
			return err
		}
		entries = append(entries, got...)
	}

	if err := checkMergeKeysComparable(entries, len(i.sources)); err != nil {
		return err
	}

	// Ordering the union by key and breaking ties by (source, anchor,
	// position) is the merge. The sort is stable, so entries whose whole key
	// vector compares equal keep exactly the order the tie-break gives them.
	var cmpErr error
	sort.SliceStable(entries, func(a, b int) bool {
		ea, eb := &entries[a], &entries[b]
		for k := range ea.keys {
			if k >= len(eb.keys) {
				break
			}
			// The order attribute is taken from the first source's key: they
			// are required to agree, and XTDE2210 above has already refused
			// the case where they do not.
			cmp := compareSortValues(ea.keys[k], eb.keys[k])
			if cmp == sortIncomparable {
				if cmpErr == nil {
					cmpErr = fmt.Errorf(
						"XTTE2230: merge key values %s and %s cannot be "+
							"compared with the le operator",
						ea.keys[k].typeName(), eb.keys[k].typeName())
				}
				return false
			}
			if cmp == 0 {
				continue
			}
			if resolved[0][k].order == "descending" {
				cmp = -cmp
			}
			return cmp < 0
		}
		if ea.src != eb.src {
			return ea.src < eb.src
		}
		if ea.anchor != eb.anchor {
			return ea.anchor < eb.anchor
		}
		return ea.pos < eb.pos
	})
	if cmpErr != nil {
		return cmpErr
	}

	// A group is a maximal run of entries whose composite key values are all
	// equal. Because the sequence is now ordered by that key, the runs are
	// adjacent and one pass finds them.
	var groups [][]mergeEntry
	for start := 0; start < len(entries); {
		end := start + 1
		for end < len(entries) && mergeKeysEqual(entries[start].keys, entries[end].keys) {
			end++
		}
		groups = append(groups, entries[start:end])
		start = end
	}

	names := make([]string, len(i.sources))
	named := make([]bool, len(i.sources))
	nameSeq := make(xdm.Sequence, 0, len(i.sources))
	for s, src := range i.sources {
		names[s], named[s] = src.name, src.named
		if src.named {
			nameSeq = append(nameSeq, xdm.NewString(src.name))
		}
	}

	size := len(groups)
	if i.streamed {
		// 15.7: with any streamable source the context size is absent, so
		// last() inside the action raises XPDY0002. A size of zero is how
		// this engine spells an absent one.
		size = 0
	}
	for g, grp := range groups {
		binding := &mergeGroupBinding{names: names, named: named,
			items: make([]xdm.Sequence, len(i.sources))}
		for _, e := range grp {
			binding.items[e.src] = append(binding.items[e.src], e.item)
		}
		all := binding.all()
		// 15.7 puts the first item of the group in focus. The current merge
		// key is the first item's key "after atomization and casting of
		// xs:untypedAtomic values to xs:string", which is what the sortValue
		// already holds.
		var focus xdm.Item
		if len(all) > 0 {
			focus = all[0]
		}
		sub := rt.withCurrent(focus, g+1, size)
		sub = sub.withVar(currentMergeGroupVar,
			xdm.One(&xdm.Opaque{Value: binding}))
		sub = sub.withVar(currentMergeKeyVar, mergeKeySequence(grp[0].keys))
		sub = sub.withVar(currentMergeSourcesVar, nameSeq)
		if err := execSequence(i.action, sub, out); err != nil {
			return err
		}
	}
	return nil
}

// checkMergeKeysComparable is XTTE2230: a merge key value from one input
// sequence that cannot be compared with "le" against the corresponding key
// from another.
//
// The sort machinery cannot answer this on its own. For an xsl:sort an
// xs:untypedAtomic key is promoted to the other operand's type and ordered as
// text when that fails, which is right there and wrong here: "le" over an
// untyped value promotes it by *casting*, and a date string against an
// xs:dateTime does not cast. So the untyped keys of each source are tested
// against the typed keys of the others, which is the pairing the error is
// written about — within one source the keys are all computed the same way
// and the type check the sort already makes covers them.
func checkMergeKeysComparable(entries []mergeEntry, nsrc int) error {
	if nsrc < 2 {
		return nil
	}
	// One representative typed value and one representative untyped value per
	// (source, key position) is enough: every item of one input sequence has
	// its key computed by the same expression, so a second sample of the same
	// source adds nothing the first did not say.
	type sample struct{ typed, untyped *xdm.Atomic }
	byKey := map[int]map[int]*sample{}
	for _, e := range entries {
		for k, a := range e.atoms {
			if a == nil {
				continue
			}
			m := byKey[k]
			if m == nil {
				m = map[int]*sample{}
				byKey[k] = m
			}
			sm := m[e.src]
			if sm == nil {
				sm = &sample{}
				m[e.src] = sm
			}
			if isUntyped(a) {
				if sm.untyped == nil {
					sm.untyped = a
				}
			} else if sm.typed == nil {
				sm.typed = a
			}
		}
	}
	for _, m := range byKey {
		for src, sm := range m {
			if sm.untyped == nil {
				continue
			}
			for other, om := range m {
				if other == src || om.typed == nil {
					continue
				}
				// "le" casts the untyped operand to the typed one's type. A
				// cast that fails is the error; a numeric target is the
				// exception the operator makes, casting to xs:double.
				target := om.typed.Type
				if target.IsNumeric() {
					target = xdm.TypeDouble
				}
				if _, err := xpath.CastAtomic(sm.untyped, target); err != nil {
					return fmt.Errorf(
						"XTTE2230: the merge key %q of one input sequence "+
							"cannot be compared with the %s key of another",
						sm.untyped.String(), om.typed.TypeName())
				}
			}
		}
	}
	return nil
}

// checkMergeKeysAtRuntime is the half of XTDE2210 that a computed attribute
// leaves until now: with the attribute value templates resolved, corresponding
// keys must have agreed on every ordering attribute.
func checkMergeKeysAtRuntime(resolved [][]*sortKey) error {
	if len(resolved) < 2 {
		return nil
	}
	for j := range resolved[0] {
		first := resolved[0][j]
		for _, ks := range resolved[1:] {
			if j >= len(ks) {
				continue
			}
			other := ks[j]
			switch {
			case first.order != other.order:
				return fmt.Errorf(
					"XTDE2210: corresponding xsl:merge-key elements have "+
						"order %q and %q", first.order, other.order)
			case first.dataType != other.dataType:
				return fmt.Errorf(
					"XTDE2210: corresponding xsl:merge-key elements have "+
						"data-type %q and %q", first.dataType, other.dataType)
			case first.caseOrder != other.caseOrder:
				return fmt.Errorf(
					"XTDE2210: corresponding xsl:merge-key elements have "+
						"case-order %q and %q", first.caseOrder, other.caseOrder)
			}
		}
	}
	return nil
}

// mergeKeySequence is the current merge key: the composite key of the group,
// as a sequence of atomic values.
func mergeKeySequence(keys []sortValue) xdm.Sequence {
	out := make(xdm.Sequence, 0, len(keys))
	for _, k := range keys {
		switch {
		case k.empty:
			continue
		case k.cmpAtom != nil:
			out = append(out, k.cmpAtom)
		case k.numeric:
			out = append(out, xdm.NewDouble(k.num))
		default:
			out = append(out, xdm.NewString(k.str))
		}
	}
	return out
}

// mergeKeysEqual reports whether two composite key values are the same group.
//
// 15.7 makes them distinct when any corresponding pair does not compare equal
// under "eq" with the merge key's collation, which compareSortValues already
// answers for the pair. An incomparable pair is not equal — the error for it
// was raised by the sort above, which sees every adjacent pair.
func mergeKeysEqual(a, b []sortValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if compareSortValues(a[k], b[k]) != 0 {
			return false
		}
	}
	return true
}

// collect evaluates one merge source into entries carrying their keys.
func (s *mergeSource) collect(rt *runtime, idx int, keys []*sortKey,
	colls []xpath.Collation) ([]mergeEntry, error) {
	anchors, err := s.anchorItems(rt)
	if err != nil {
		return nil, err
	}

	var out []mergeEntry
	for a, anchor := range anchors {
		sub := rt
		if anchor != nil {
			// 15.3: the select expression sees the anchor item as the context
			// item, its position among the anchors, and their number.
			sub = rt.withCurrent(anchor, a+1, len(anchors))
		}
		seq, err := s.sel.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		entries := make([]mergeEntry, len(seq))
		for p, it := range seq {
			// 15.5: the key is evaluated "with a singleton focus based on J",
			// so position() and last() are both 1 — deliberately unlike an
			// xsl:sort key, which sees the position in the unsorted sequence.
			kctx := rt.withCurrent(it, 1, 1)
			kv := make([]sortValue, len(keys))
			ka := make([]*xdm.Atomic, len(keys))
			for k, sk := range keys {
				v, err := sk.evalKey(kctx)
				if err != nil {
					return nil, err
				}
				if at := xdm.Atomize(v); len(at) == 1 {
					if a, ok := at[0].(*xdm.Atomic); ok {
						ka[k] = a
					}
				}
				sv, err := makeSortValue(v, sk, colls[k],
					rt.ctx.ImplicitTimezone, rt.sheet.output.Version10Implicit)
				if err != nil {
					// XTTE1020 is the sort-key code; for a merge key the
					// suite accepts either it or XTTE2230, and the message
					// keeps the sort code so the two paths stay one.
					return nil, err
				}
				kv[k] = sv
			}
			entries[p] = mergeEntry{item: it, keys: kv, atoms: ka,
				src: idx, anchor: a, pos: p}
		}
		if s.sortBeforeMerge {
			// The input was not promised to be sorted, so sort it. Doing it
			// per input sequence rather than over the whole source matters:
			// the sequences stay individually ordered, which is what the
			// merge below assumes.
			if err := sortMergeEntries(entries, keys); err != nil {
				return nil, err
			}
			for p := range entries {
				entries[p].pos = p
			}
		} else if err := checkMergeInputSorted(entries, keys); err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

// anchorItems produces the anchor items of a merge source, or a single nil
// anchor when the source names none.
//
// A nil anchor means "evaluate select with the focus of the xsl:merge itself",
// which 15.3 gives as the behaviour when neither for-each-item nor
// for-each-source is present.
func (s *mergeSource) anchorItems(rt *runtime) ([]xdm.Item, error) {
	switch {
	case s.anchors != nil:
		seq, err := s.anchors.Eval(rt.ctx)
		if err != nil {
			return nil, err
		}
		return []xdm.Item(seq), nil
	case s.sourceURIs != nil:
		seq, err := s.sourceURIs.Eval(rt.ctx)
		if err != nil {
			return nil, err
		}
		// 15.3: "The expected type of the expression is xs:string*, and the
		// actual result of the expression is converted to this type using the
		// function conversion rules." An integer is not convertible to
		// xs:string under those rules, which is what makes merge-043's
		// "1 to 5" XPTY0004 rather than five nonexistent filenames.
		var items []xdm.Item
		for _, a := range xdm.Atomize(seq) {
			at, ok := a.(*xdm.Atomic)
			if !ok {
				return nil, fmt.Errorf(
					"XPTY0004: xsl:merge-source/@for-each-source must return " +
						"strings")
			}
			if at.Type != xdm.TypeUntypedAtomic && at.Type != xdm.TypeString &&
				at.Type != xdm.TypeAnyURI {
				return nil, fmt.Errorf(
					"XPTY0004: xsl:merge-source/@for-each-source returned a "+
						"%s, which the function conversion rules do not convert "+
						"to xs:string", at.TypeName())
			}
			root, err := s.load(rt, at.String())
			if err != nil {
				return nil, err
			}
			items = append(items, root)
		}
		return items, nil
	}
	return []xdm.Item{nil}, nil
}

// load reads one for-each-source document and applies the source's validation.
//
// It mirrors xsl:source-document's load for the same reason that one copies:
// the resolver caches its trees, so validating in place would change what a
// later fn:doc of the same URI sees.
func (s *mergeSource) load(rt *runtime, href string) (*xdm.Node, error) {
	docs := rt.ctx.Docs
	if docs == nil {
		return nil, fmt.Errorf(
			"FODC0002: document access is disabled (no resolver configured): %q",
			href)
	}
	base := s.baseURI
	if base == "" {
		base = rt.ctx.StaticBaseURI
	}
	tree, err := docs.ResolveDocument(href, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", href, err)
	}
	if s.accums != nil {
		// 18.2.2: use-accumulators names the accumulators applicable to the
		// documents this source reads, and only those. An accumulator the
		// list omits is inapplicable to the tree, which XTDE3362 makes a
		// dynamic error to read — including, as merge-067 does, one reached
		// indirectly by another accumulator's rule.
		rt.treeAccums[tree.Root] = s.accums
	}
	if s.validation.isDefault() {
		return tree.Root, nil
	}
	copied := xdm.NewTree()
	copied.Root.BaseURI = tree.Root.BaseURI
	for _, ch := range tree.Root.Children {
		copied.Root.AppendChild(deepCopy(ch))
	}
	copied.Finalize()
	if s.accums != nil {
		rt.treeAccums[copied.Root] = s.accums
	}
	if err := s.validation.assess(rt, copied.Root); err != nil {
		return nil, err
	}
	return copied.Root, nil
}

// parseUseAccumulators reads a use-accumulators attribute into the same set
// xsl:mode/@use-accumulators compiles to, since 18.2.2 gives both the same
// meaning and the same "#all" and "#default" tokens.
func parseUseAccumulators(n *xdm.Node) (*modeAccumulators, error) {
	set := &modeAccumulators{names: map[string]bool{}}
	for _, tok := range strings.Fields(n.AttrValue("use-accumulators")) {
		switch tok {
		case "#all":
			set.all = true
			continue
		case "#default":
			// Meaningful only inside a package that sets a default, which
			// this processor does not model.
			continue
		}
		qn, err := resolveQNameAttr(n, tok)
		if err != nil {
			return nil, err
		}
		set.names[xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()] = true
	}
	return set, nil
}

// sortMergeEntries orders one input sequence, for sort-before-merge="yes".
func sortMergeEntries(entries []mergeEntry, keys []*sortKey) error {
	var sortErr error
	sort.SliceStable(entries, func(a, b int) bool {
		for k, sk := range keys {
			cmp := compareSortValues(entries[a].keys[k], entries[b].keys[k])
			if cmp == sortIncomparable {
				if sortErr == nil {
					sortErr = fmt.Errorf(
						"XTTE2230: merge key values %s and %s cannot be "+
							"compared with the le operator",
						entries[a].keys[k].typeName(),
						entries[b].keys[k].typeName())
				}
				return false
			}
			if cmp == 0 {
				continue
			}
			if sk.order == "descending" {
				cmp = -cmp
			}
			return cmp < 0
		}
		return false
	})
	return sortErr
}

// checkMergeInputSorted is XTDE2220: an input sequence that is not already in
// merge key order, where sort-before-merge did not license one.
//
// The comparison is the same one the merge itself uses, so an input that
// passes here is one the merge can interleave without reordering it.
func checkMergeInputSorted(entries []mergeEntry, keys []*sortKey) error {
	for i := 1; i < len(entries); i++ {
		for k, sk := range keys {
			cmp := compareSortValues(entries[i-1].keys[k], entries[i].keys[k])
			if cmp == sortIncomparable {
				return fmt.Errorf(
					"XTTE2230: merge key values %s and %s cannot be compared "+
						"with the le operator",
					entries[i-1].keys[k].typeName(), entries[i].keys[k].typeName())
			}
			if cmp == 0 {
				continue
			}
			if sk.order == "descending" {
				cmp = -cmp
			}
			if cmp > 0 {
				return fmt.Errorf(
					"XTDE2220: an input sequence to xsl:merge is not sorted on "+
						"its merge keys (%s follows %s)",
					entries[i].keys[k].typeName(), entries[i-1].keys[k].typeName())
			}
			break
		}
	}
	return nil
}

// --- The two functions ------------------------------------------------------

// registerMergeFuncs adds fn:current-merge-group and fn:current-merge-key.
//
// Both read the merge state from internal variable bindings, exactly as
// current-group() does. Both are XSLT 3.0 additions, so both are gated on
// XPath 3.1 — the version a 3.0 stylesheet compiles its expressions as — and a
// 2.0 stylesheet calling one gets XPST0017 rather than a working call.
func registerMergeFuncs(l *xpath.Library) {
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-merge-group"}, Arity: 0,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			b, err := currentMergeGroup(ctx)
			if err != nil {
				return nil, err
			}
			return b.all(), nil
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-merge-group"}, Arity: 1,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			b, err := currentMergeGroup(ctx)
			if err != nil {
				return nil, err
			}
			want := stringArg(args[0])
			for i, n := range b.names {
				// An invented name is not one the stylesheet may ask for:
				// 15.6.1 says the key is "the value of the name attribute",
				// and an unnamed source has none, so asking for its items by
				// name is XTDE3490 like any other unknown name.
				if b.named[i] && n == want {
					return b.items[i], nil
				}
			}
			return nil, fmt.Errorf(
				"XTDE3490: %q does not name any xsl:merge-source of the current "+
					"merge operation", want)
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-merge-key"}, Arity: 0,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			// The key is present exactly when the group is, and an
			// invocation construct clears both together. Asking the group
			// rather than testing the key sequence for emptiness is what
			// keeps a genuinely empty composite key distinguishable from an
			// absent one.
			if _, err := currentMergeGroup(ctx); err != nil {
				return nil, fmt.Errorf(
					"XTDE3510: current-merge-key() was called when the current " +
						"merge key is absent")
			}
			seq, _ := ctx.LookupVar(currentMergeKeyVar)
			return seq, nil
		},
	})
}

// currentMergeGroup reads the binding, or reports XTDE3480.
//
// The binding is absent — rather than empty — everywhere outside an
// xsl:merge-action, which is what makes the error reachable: 15.6 has every
// invocation construct clear it, so a template or function called from the
// action sees nothing.
func currentMergeGroup(ctx *xpath.Context) (*mergeGroupBinding, error) {
	seq, ok := ctx.LookupVar(currentMergeGroupVar)
	if !ok || len(seq) == 0 {
		return nil, fmt.Errorf(
			"XTDE3480: current-merge-group() was called when the current merge " +
				"group is absent")
	}
	o, ok := seq[0].(*xdm.Opaque)
	if !ok {
		return nil, fmt.Errorf(
			"XTDE3480: current-merge-group() was called when the current merge " +
				"group is absent")
	}
	b, ok := o.Value.(*mergeGroupBinding)
	if !ok {
		return nil, fmt.Errorf(
			"XTDE3480: current-merge-group() was called when the current merge " +
				"group is absent")
	}
	return b, nil
}

// clearMergeContext removes the current merge group and key.
//
// 15.6: "All invocation constructs set the current merge group and current
// merge key to absent." A template or function called from inside an
// xsl:merge-action must therefore not see them, which is what makes
// merge-087, merge-088, merge-100 and merge-101 the XTDE3480/XTDE3510 tests
// they are rather than working stylesheets.
func (rt *runtime) clearMergeContext() *runtime {
	sub := rt.withVar(currentMergeGroupVar, nil)
	sub = sub.withVar(currentMergeKeyVar, nil)
	return sub.withVar(currentMergeSourcesVar, nil)
}
