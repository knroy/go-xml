package xslt

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// forEachGroupInstr implements xsl:for-each-group.
//
// Grouping is the headline addition of XSLT 2.0 and the reason many rule sets
// require it: expressing "group consecutive rows by their key" in 1.0 needed
// the Muenchian key trick, which is unreadable and slow.
type forEachGroupInstr struct {
	sel *xpath.Compiled
	// exactly one of these grouping modes is set
	groupBy       *xpath.Compiled
	groupAdjacent *xpath.Compiled
	// defaultCollation is the default collation in force at the instruction,
	// used when no @collation is given.
	defaultCollation string
	groupStartsWith  *Pattern
	groupEndsWith    *Pattern
	// collation names the collation that compares grouping keys. It is an
	// attribute value template, so it is resolved per execution rather than
	// at compile time: collation="{$c}" is legal and names a collation the
	// stylesheet computes.
	collation *avt
	// composite records composite="yes": the whole atomized key sequence is
	// one grouping key rather than a set of them. XSLT 3.0 section 14.2; see
	// grouping_composite.go.
	composite bool
	sorts     []*sortKey
	body      []Instruction
}

// group is one population group, carrying the key that formed it.
type group struct {
	key   xdm.Sequence
	items xdm.Sequence
}

func (i *forEachGroupInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}

	var groups []group
	switch {
	case i.groupBy != nil:
		var coll xpath.Collation
		if coll, err = i.resolveCollation(rt); err != nil {
			return err
		}
		if i.composite {
			groups, err = groupByCompositeKey(rt, seq, i.groupBy, coll)
		} else {
			groups, err = groupByKey(rt, seq, i.groupBy, coll)
		}
	case i.groupAdjacent != nil:
		var coll xpath.Collation
		if coll, err = i.resolveCollation(rt); err != nil {
			return err
		}
		if i.composite {
			groups, err = groupAdjacentCompositeKey(rt, seq, i.groupAdjacent, coll)
		} else {
			groups, err = groupAdjacentKey(rt, seq, i.groupAdjacent, coll)
		}
	case i.groupStartsWith != nil:
		groups, err = groupStartingWith(rt, seq, i.groupStartsWith)
	case i.groupEndsWith != nil:
		groups, err = groupEndingWith(rt, seq, i.groupEndsWith)
	default:
		return fmt.Errorf("xsl:for-each-group requires a grouping attribute")
	}
	if err != nil {
		return err
	}

	if len(i.sorts) > 0 {
		if groups, err = i.sortGroups(rt, groups); err != nil {
			return err
		}
	}

	size := len(groups)
	for idx, g := range groups {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		// Inside the body, the context item is the group's first item and
		// current-group()/current-grouping-key() expose the rest.
		var focus xdm.Item
		if len(g.items) > 0 {
			focus = g.items[0]
		}
		sub := rt.withCurrent(focus, idx+1, size).clearCurrentRule()
		sub = sub.withGroupingScope(g.items, g.key)
		sub = sub.withGroupingKeyPresence(i.groupBy != nil || i.groupAdjacent != nil)
		if err := execSequence(i.body, sub, out); err != nil {
			return err
		}
	}
	return nil
}

// resolveSortCollations evaluates each sort key's @collation attribute value
// template, giving the collation its text comparisons use.
//
// It runs once per sort rather than once per comparison: the attribute cannot
// vary between the items being sorted, and re-parsing the URI n log n times
// was measurable.
func resolveSortCollations(rt *runtime, sorts []*sortKey) ([]xpath.Collation, error) {
	resolved := make([]xpath.Collation, len(sorts))
	for k, s := range sorts {
		resolved[k] = s.strColl
		if s.strColl == nil && s.collAVT != nil {
			uri, err := s.collAVT.eval(rt)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(uri) != "" {
				c, err := xpath.ResolveCollation(uri)
				if err != nil {
					// Section 13.1.3 fixes the code: a collation URI the
					// implementation does not recognise is XTDE1035, not the
					// FOCH0002 that the function library raises for the same
					// condition.
					return nil, fmt.Errorf(
						"XTDE1035: xsl:sort/@collation %q is not a recognized collation", uri)
				}
				resolved[k] = c
			}
		}
	}
	return resolved, nil
}

// sortGroups orders the groups by the instruction's xsl:sort children.
//
// The sort keys are evaluated with the *grouping* context in place: section
// 14 puts the current group and the current grouping key in scope for them,
// and <xsl:sort select="current-grouping-key()"/> is the ordinary way to
// order groups. Sorting a bare sequence of first items instead left
// current-grouping-key() unbound, so the sort key was empty for every group
// and the groups stayed in population order.
//
// This does not go through applySorts because that function establishes the
// focus itself, from the sequence it is given; here the focus and the two
// grouping bindings have to be built per group. The comparison machinery it
// uses — makeSortValue and compareSortValues — is shared.
func (i *forEachGroupInstr) sortGroups(rt *runtime, groups []group) ([]group, error) {
	sorts := make([]*sortKey, len(i.sorts))
	for k, sk := range i.sorts {
		r, err := sk.resolve(rt)
		if err != nil {
			return nil, err
		}
		sorts[k] = r
	}
	colls, err := resolveSortCollations(rt, sorts)
	if err != nil {
		return nil, err
	}
	if len(groups) < 2 {
		return groups, nil
	}

	type entry struct {
		g    group
		keys []sortValue
		idx  int
	}
	entries := make([]entry, len(groups))
	for n, g := range groups {
		var focus xdm.Item
		if len(g.items) > 0 {
			focus = g.items[0]
		}
		sub := rt.withFocus(focus, n+1, len(groups))
		sub = sub.withGroupingScope(g.items, g.key)
		sub = sub.withGroupingKeyPresence(i.groupBy != nil || i.groupAdjacent != nil)
		e := entry{g: g, idx: n, keys: make([]sortValue, len(sorts))}
		for k, sk := range sorts {
			v, err := sk.evalKey(sub)
			if err != nil {
				return nil, err
			}
			sv, err := makeSortValue(v, sk, colls[k], rt.ctx.ImplicitTimezone,
				rt.sheet.output.Version10Implicit)
			if err != nil {
				return nil, err
			}
			e.keys[k] = sv
		}
		entries[n] = e
	}

	var sortErr error
	sort.SliceStable(entries, func(a, b int) bool {
		for k, sk := range sorts {
			cmp := compareSortValues(entries[a].keys[k], entries[b].keys[k])
			if cmp == sortIncomparable {
				if sortErr == nil {
					sortErr = fmt.Errorf(
						"XTDE1030: two sort key values cannot be compared "+
							"with the lt operator (%s and %s)",
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
		return entries[a].idx < entries[b].idx
	})
	if sortErr != nil {
		return nil, sortErr
	}
	out := make([]group, len(entries))
	for n, e := range entries {
		out[n] = e.g
	}
	return out, nil
}

// The grouping state is passed to current-group() and current-grouping-key()
// through variable bindings, for the same reason the runtime is: the xpath
// package cannot depend on this one.
var (
	currentGroupVar       = xdm.QName{URI: internalNS, Local: "current-group"}
	currentGroupingKeyVar = xdm.QName{URI: internalNS, Local: "current-grouping-key"}
)

// groupByKey groups items by the value of a key expression, preserving the
// order in which each key was first seen.
func groupByKey(rt *runtime, seq xdm.Sequence, key *xpath.Compiled,
	coll xpath.Collation) ([]group, error) {
	index := map[string]int{}
	var groups []group
	// Indices of the groups whose key is numeric, and the numeric types seen
	// so far. Both feed the erratum-E25 rescan below, which only has to run
	// when a grouping actually mixes numeric types.
	var numericGroups []int
	numericTypes := map[xdm.TypeCode]bool{}

	for idx, it := range seq {
		sub := rt.withFocus(it, idx+1, len(seq))
		vals, err := key.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		// Section 14.2 forms the groups from the *distinct* key values of an
		// item, so an item whose key expression yields the same value twice
		// still joins that group once. group-by="number(@pop),
		// string-length(@name)" on a city named "milan" with pop=5 produces
		// 5 twice, and appending it twice put the same element in one group
		// two times over.
		joined := map[int]bool{}
		// An item with multiple key values joins every corresponding group,
		// which is what makes group-by usable for many-to-many classification.
		for _, kv := range xdm.Atomize(vals) {
			// The group is found by the *collated* form of the key, so that
			// two keys the collation calls equal land together — while the
			// group keeps the key as written, which is what
			// current-grouping-key() returns.
			k, err := groupingKey(rt, kv.(*xdm.Atomic), coll)
			if err != nil {
				return nil, err
			}
			if a := kv.(*xdm.Atomic); a.Type.IsNumeric() {
				numericTypes[a.Type] = true
			}
			gi, ok := index[k]
			if !ok {
				// The hash missed, but the value comparison grouping uses is
				// not transitive across the numeric types — erratum E25
				// spells this out. A value can be equal to a group's key
				// without hashing to it: xs:decimal("1.0000000000100000000001")
				// equals both xs:float("1.0") and xs:double("1.00000000001"),
				// which are not equal to each other. So before opening a new
				// group, compare against the key each existing group was
				// opened with, in population order, and join the first match.
				//
				// The scan is quadratic, so it runs only where it can change
				// the answer: over numeric groups, and only once the
				// population has produced more than one numeric type. A
				// grouping whose keys are all strings, or all xs:integer,
				// keeps its single map lookup.
				a := kv.(*xdm.Atomic)
				if a.Type.IsNumeric() && len(numericTypes) > 1 {
					for _, gj := range numericGroups {
						gk, isAtomic := groups[gj].key[0].(*xdm.Atomic)
						if !isAtomic {
							continue
						}
						if xpath.GroupingEqual(a, gk, coll,
							rt.ctx.ImplicitTimezone) {
							gi, ok = gj, true
							break
						}
					}
				}
			}
			if !ok {
				index[k] = len(groups)
				groups = append(groups, group{key: xdm.One(kv)})
				gi = len(groups) - 1
				if a := kv.(*xdm.Atomic); a.Type.IsNumeric() {
					numericGroups = append(numericGroups, gi)
				}
			}
			if joined[gi] {
				continue
			}
			joined[gi] = true
			groups[gi].items = append(groups[gi].items, it)
		}
	}
	return groups, nil
}

// groupAdjacentKey starts a new group whenever the key value changes, so
// non-consecutive items with the same key form separate groups.
func groupAdjacentKey(rt *runtime, seq xdm.Sequence, key *xpath.Compiled,
	coll xpath.Collation) ([]group, error) {
	var groups []group
	var prev string
	first := true

	for idx, it := range seq {
		sub := rt.withFocus(it, idx+1, len(seq))
		vals, err := key.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		// XTTE1100: a group-adjacent key must be exactly one item. An empty
		// sequence leaves the item in no group at all and more than one
		// leaves it ambiguous, so both are errors rather than something to
		// silently pick from.
		atoms := xdm.Atomize(vals)
		if len(atoms) != 1 {
			return nil, fmt.Errorf(
				"XTTE1100: the group-adjacent key produced %d items, want exactly one",
				len(atoms))
		}
		k, err := groupingKey(rt, atoms[0].(*xdm.Atomic), coll)
		if err != nil {
			return nil, err
		}
		if first || k != prev {
			groups = append(groups, group{key: xdm.One(atoms[0])})
			first, prev = false, k
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)
	}
	return groups, nil
}

// groupStartingWith begins a new group at each item matching the pattern.
// requireNodePopulation enforces XTTE1120.
//
// Positional grouping asks a pattern whether each item starts or ends a
// group, and a pattern only ever matches a node. An atomic value in the
// population therefore has no defined answer; treating it silently as "does
// not match" put every atomic value in the first group instead of reporting
// the type error the spec requires.
//
// XSLT 3.0 changes the answer for one pattern form: ".[E]" matches an atomic
// value, so a population holding one is not automatically an error. The
// pattern is asked whether it can match a value at all, and only a pattern
// that cannot still raises XTTE1120.
//
// XSLT 3.0 drops the error entirely: XTTE1120 has no entry in the 3.0 error
// summary, and §14.1 says only "if an item matches the pattern", which an
// atomic value simply does not. So the check is on the processor's version,
// not the module's -- for-each-group-046 and -046a run the very same
// version="2.0" stylesheet and differ only in the spec they are scoped to.
func requireNodePopulation(rt *runtime, seq xdm.Sequence, attr string, pat *Pattern) error {
	if rt != nil && rt.sheet != nil && (rt.sheet.maxVersion == 0 || rt.sheet.maxVersion >= 3.0) {
		return nil
	}
	if pat != nil && pat.matchesAtomicValues() {
		return nil
	}
	for _, it := range seq {
		if _, ok := it.(*xdm.Node); !ok {
			return fmt.Errorf(
				"XTTE1120: xsl:for-each-group/@%s requires a population of "+
					"nodes, but the select expression produced an atomic value",
				attr)
		}
	}
	return nil
}

func groupStartingWith(rt *runtime, seq xdm.Sequence, pat *Pattern) ([]group, error) {
	if err := requireNodePopulation(rt, seq, "group-starting-with", pat); err != nil {
		return nil, err
	}
	rt = rt.clearRegexGroups()
	var groups []group
	for _, it := range seq {
		start, err := patternMatchesItem(pat, it, rt.ctx)
		if err != nil {
			return nil, err
		}
		if start || len(groups) == 0 {
			groups = append(groups, group{})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)
	}
	return groups, nil
}

// groupEndingWith closes a group after each item matching the pattern.
func groupEndingWith(rt *runtime, seq xdm.Sequence, pat *Pattern) ([]group, error) {
	if err := requireNodePopulation(rt, seq, "group-ending-with", pat); err != nil {
		return nil, err
	}
	rt = rt.clearRegexGroups()
	var groups []group
	open := false
	for _, it := range seq {
		if !open {
			groups = append(groups, group{})
			open = true
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)

		m, err := patternMatchesItem(pat, it, rt.ctx)
		if err != nil {
			return nil, err
		}
		if m {
			open = false
		}
	}
	return groups, nil
}

// compileForEachGroup compiles xsl:for-each-group.
func (c *compiler) compileForEachGroup(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	sel, err := requiredExpr(n, "select", ns)
	if err != nil {
		return nil, err
	}
	instr := &forEachGroupInstr{sel: sel}
	if r, ok := ns.(*nsResolver); ok {
		instr.defaultCollation = r.collation
	}

	if v := n.AttrValue("collation"); v != "" {
		if instr.collation, err = compileAVT(v, ns); err != nil {
			return nil, err
		}
	}

	count := 0
	if v := n.AttrValue("group-by"); v != "" {
		if instr.groupBy, err = compileExpr(v, ns); err != nil {
			return nil, err
		}
		count++
	}
	if v := n.AttrValue("group-adjacent"); v != "" {
		if instr.groupAdjacent, err = compileExpr(v, ns); err != nil {
			return nil, err
		}
		count++
	}
	if v := n.AttrValue("group-starting-with"); v != "" {
		if instr.groupStartsWith, err = CompilePattern(v, ns); err != nil {
			return nil, err
		}
		count++
	}
	if v := n.AttrValue("group-ending-with"); v != "" {
		if instr.groupEndsWith, err = CompilePattern(v, ns); err != nil {
			return nil, err
		}
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf(
			"XTSE1080: xsl:for-each-group requires exactly one grouping attribute, found %d", count)
	}
	// XTSE1090: "it is an error to specify the collation attribute if neither
	// the group-by attribute nor group-adjacent attribute is specified." The
	// two pattern-based forms group by position rather than by key, so there
	// is no key for a collation to compare and naming one is a mistake about
	// what the instruction does.
	if instr.collation != nil && instr.groupBy == nil && instr.groupAdjacent == nil {
		return nil, fmt.Errorf(
			"XTSE1090: xsl:for-each-group/@collation requires either " +
				"group-by or group-adjacent")
	}
	// XTSE1090 names @composite in the same breath, for the same reason: a
	// positional grouping has no key for the attribute to describe.
	if v := n.AttrValue("composite"); v != "" {
		if instr.groupBy == nil && instr.groupAdjacent == nil {
			return nil, fmt.Errorf(
				"XTSE1090: xsl:for-each-group/@composite requires either " +
					"group-by or group-adjacent")
		}
		instr.composite = isYes(v)
	}

	_, sorts, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	instr.sorts = sorts

	body, err := c.compileNodes(nonSortChildren(n), n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// analyzeStringInput applies the function conversion rules for xs:string to
// the value of xsl:analyze-string/@select.
//
// Section 15.1 says the select expression's result is "converted to a string
// by applying the function conversion rules", and the target type is the
// required xs:string — a single item, not a sequence. Joining the sequence
// instead accepted an integer, a three-item sequence and the empty sequence
// alike, so three tests that require XPTY0004 quietly produced output.
//
// Only untypedAtomic is cast and only anyURI is promoted; xs:integer is
// neither, which is what makes select="22" an error rather than the string
// "22".
//
// XSLT 3.0 added one exception ahead of the conversion: "if the result of
// evaluating the select expression is an empty sequence, it is treated as a
// zero-length string". It is decided when the instruction runs, so it follows
// the processor rather than the module -- and it is only the empty sequence,
// not the general laxity the joined form had.
func analyzeStringInput(seq xdm.Sequence, xslt30 bool) (string, error) {
	if len(seq) == 0 && xslt30 {
		return "", nil
	}
	atoms := xdm.Atomize(seq)
	if len(atoms) != 1 {
		return "", fmt.Errorf(
			"XPTY0004: xsl:analyze-string/@select must be a single string, "+
				"but the value has %d items", len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return "", fmt.Errorf(
			"XPTY0004: xsl:analyze-string/@select must be a single string")
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic, xdm.TypeAnyURI:
		return a.String(), nil
	}
	return "", fmt.Errorf(
		"XPTY0004: xsl:analyze-string/@select must be a string, not %s",
		a.Type)
}

// evalKey computes one sort key value with the focus already set to the item
// being sorted.
//
// Section 13.1 gives the key either as a select expression or as the
// element's content, and says the value is "the sequence constructor's
// result", atomized. The result is the sequence itself and not a temporary
// tree: <xsl:sort><xsl:sequence select="round(.)"/></xsl:sort> must compare
// doubles, and wrapping them in a document node would have atomized every key
// to an untypedAtomic string and sorted 100 before 99.
func (s *sortKey) evalKey(rt *runtime) (xdm.Sequence, error) {
	if s.sel != nil {
		seq, err := s.sel.Eval(rt.ctx)
		if err != nil {
			return nil, err
		}
		// 3.8: a sort key under backwards compatibility is a single value.
		// XSLT 1.0 sorted on string() or number() of the key expression, both
		// of which read a node-set as its first node, so a multi-item key is
		// truncated rather than being XTTE1020. backwards-012 sorts by
		// "(-., 'banana')" and expects the sort to run on the "-." alone.
		if s.sel.CompatMode() {
			seq = seq[:min(len(seq), 1)]
		}
		return seq, nil
	}
	sub := rt.temporaryOutput()
	out := newOutputBuilder()
	if err := execSequence(s.body, sub, out); err != nil {
		return nil, err
	}
	return out.sequence(), nil
}

// --- xsl:analyze-string -----------------------------------------------------

// analyzeStringInstr implements xsl:analyze-string, which splits a string by a
// regular expression and processes matching and non-matching runs separately.
type analyzeStringInstr struct {
	sel      *xpath.Compiled
	regex    *avt
	flags    *avt
	matching []Instruction
	nonMatch []Instruction
}

func (i *analyzeStringInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}
	input, err := analyzeStringInput(seq, rt.sheet == nil || rt.sheet.maxVersion == 0 || rt.sheet.maxVersion >= 3.0)
	if err != nil {
		return err
	}

	pattern, err := i.regex.eval(rt)
	if err != nil {
		return err
	}
	flags := ""
	if i.flags != nil {
		if flags, err = i.flags.eval(rt); err != nil {
			return err
		}
	}

	// The dialect follows the processor, not the module, for the same reason
	// fn:matches does: @regex is a string read when the instruction runs
	// rather than by the parser, so nothing about a 3.0 construct in it needs
	// a 3.0 module to have been parsed. Compiling at the 2.0 dialect refused
	// non-capturing groups and the "q" flag even in a version="3.0"
	// stylesheet -- analyze-string-036 and -037 are exactly that.
	re, err := xpath.CompileRegexpVersion(pattern, flags, regexDialect(rt.ctx))
	if err != nil {
		// xsl:analyze-string has its own error codes for the two ways the
		// regex can be rejected, and a caller matching on the code needs
		// them rather than the function library's FORX0001/FORX0002: an
		// unusable flag is XTDE1145 and an unusable pattern XTDE1140.
		if strings.Contains(err.Error(), "FORX0001") {
			return fmt.Errorf(
				"XTDE1145: invalid xsl:analyze-string/@flags %q: %w", flags, err)
		}
		return fmt.Errorf(
			"XTDE1140: invalid xsl:analyze-string/@regex %q: %w", pattern, err)
	}
	zeroLen := re.MatchString("")
	// A backtracking pattern reports an exhausted step budget out of band,
	// through Err(), because the Regexp interface returns a bare bool. An
	// unchecked bool is then indistinguishable from a genuine non-match, so
	// the instruction would answer with silence on exactly the inputs where
	// the answer was hardest to get — which is the guess this package
	// declines to make everywhere else. See xpath.RegexpErr.
	if err := xpath.RegexpErr(re); err != nil {
		return fmt.Errorf(
			"XTDE1140: invalid xsl:analyze-string/@regex %q: %w", pattern, err)
	}
	xslt30 := rt.sheet == nil || rt.sheet.maxVersion == 0 || rt.sheet.maxVersion >= 3.0
	if zeroLen && !xslt30 {
		// XTDE1150: xsl:analyze-string's own error for a regex that matches
		// a zero-length string. FORX0003 is fn:tokenize's; the instruction
		// has its own code and a caller matching on one needs the right one.
		//
		// XSLT 3.0 dropped the error: XTDE1150 has no entry in its error
		// summary, and §17.1 gives an algorithm that says what a zero-length
		// match means instead. So the refusal follows the processor.
		return fmt.Errorf(
			"XTDE1150: the xsl:analyze-string regex matches a zero-length string")
	}

	// The substrings are collected before any is processed, because the
	// context size is the number of matching *and* non-matching substrings
	// and that is not known until the whole input has been scanned.
	runs, err := analyzeStringRuns(re, input)
	if err != nil {
		return fmt.Errorf(
			"XTDE1140: invalid xsl:analyze-string/@regex %q: %w", pattern, err)
	}

	for n, r := range runs {
		body := i.nonMatch
		if r.match {
			body = i.matching
		}
		if err := i.runBranch(rt, out, body, r.text, r.loc, input, n+1, len(runs)); err != nil {
			return err
		}
	}
	return nil
}

// analyzeStringRun is one matching or non-matching substring of the input.
type analyzeStringRun struct {
	text  string
	loc   []int // the submatch indices; nil for a non-matching run
	match bool
}

// analyzeStringRuns splits the input the way §17.1 describes.
//
// The section words the scan as a loop over inter-character positions rather
// than as "find every match", because the two differ once the pattern can
// match nothing. They differ less than the wording suggests: after a
// zero-length match the spec has the following character join the current
// non-matching substring and resumes one character later, which is the same
// advance Go's scanner makes, so the set of matches is the same and the gaps
// between them are exactly the non-matching substrings. What XSLT 3.0 really
// changed is that such a pattern is allowed at all -- 2.0 refused it as
// XTDE1150 before the scan began.
//
// The scan runs over the whole input in one call rather than restarting on
// each remaining suffix, because ^ must keep meaning "the start of the input":
// analyze-string-092 writes (?:^|,) and a per-suffix scan would let it match
// at every resumption point.
func analyzeStringRuns(re xpath.Regexp, input string) ([]analyzeStringRun, error) {
	var runs []analyzeStringRun
	pos := 0
	all := re.FindAllStringSubmatchIndex(input, -1)
	// A budget exhausted part way through the scan returns the matches found
	// so far, which is a truncated answer rather than a wrong-shaped one and
	// so is even easier to mistake for the truth.
	if err := xpath.RegexpErr(re); err != nil {
		return nil, err
	}
	for _, loc := range all {
		if loc[0] > pos {
			runs = append(runs, analyzeStringRun{text: input[pos:loc[0]]})
		}
		runs = append(runs, analyzeStringRun{
			text: input[loc[0]:loc[1]], loc: loc, match: true})
		pos = loc[1]
	}
	if pos < len(input) {
		runs = append(runs, analyzeStringRun{text: input[pos:]})
	}
	return runs, nil
}

// runBranch executes one branch with the run as the context item.
//
// pos and size are the substring's position within the whole sequence of
// matching and non-matching substrings and that sequence's length, which is
// what section 15.1 makes the context position and size. Using 1 of 1 made
// position() report 1 for every substring.
func (i *analyzeStringInstr) runBranch(rt *runtime, out *outputBuilder,
	body []Instruction, text string, loc []int, input string, pos, size int) error {
	if len(body) == 0 {
		return nil
	}
	// Section 16.6.1 defines current() as the context item at the point the
	// expression was invoked from the stylesheet, and section 15.1 makes the
	// substring the context item here — so it is the current item too, and
	// withFocus leaves current() pointing at whatever the enclosing
	// instruction was processing.
	sub := rt.withCurrent(xdm.NewString(text), pos, size).clearCurrentRule()
	// Section 15.2: the captured substrings are set for xsl:matching-substring
	// and set to the empty sequence for xsl:non-matching-substring. Leaving
	// the outer binding in place let regex-group() inside a non-matching run
	// read the groups of the preceding match.
	groups := make([]string, 0, len(loc)/2)
	for g := 0; g < len(loc)/2; g++ {
		if loc[2*g] < 0 {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, input[loc[2*g]:loc[2*g+1]])
	}
	sub = sub.withVar(regexGroupsVar, groupsToSeq(groups))
	return execSequence(body, sub, out)
}

// clearFunctionContext removes the context components that section 5.4's
// table says a call on a stylesheet function clears: the current group, the
// current grouping key and the current captured substrings.
//
// They have dynamic scope, so without this a function called from inside
// xsl:matching-substring saw the caller's captured substrings and
// regex-group(1) returned the match instead of the required empty string.
func (rt *runtime) clearFunctionContext() *runtime {
	sub := rt.withVar(regexGroupsVar, nil).withoutGroupingScope()
	// 15.6 adds the merging pair to the same list of what an invocation
	// construct clears, so a stylesheet function called from an
	// xsl:merge-action sees neither of them.
	return sub.clearMergeContext()
}

// clearRegexGroups empties the current captured substrings.
//
// Section 5.4's table of context components says the current captured
// substrings are absent while a pattern is being matched, so a pattern written
// inside xsl:matching-substring — group-starting-with="//item[@attr =
// string(regex-group(1))]" — sees the empty string rather than the enclosing
// match's group.
func (rt *runtime) clearRegexGroups() *runtime {
	return rt.withVar(regexGroupsVar, nil)
}

var regexGroupsVar = xdm.QName{URI: internalNS, Local: "regex-groups"}

func groupsToSeq(groups []string) xdm.Sequence {
	out := make(xdm.Sequence, 0, len(groups))
	for _, g := range groups {
		out = append(out, xdm.NewString(g))
	}
	return out
}

func (c *compiler) compileAnalyzeString(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	sel, err := requiredExpr(n, "select", ns)
	if err != nil {
		return nil, err
	}
	// requiredAVT treats an empty value as a missing attribute, but an
	// explicit regex="" is present and legal to compile: it is a regex that
	// matches the zero-length string, and that is the run-time error
	// XTDE1150, not a compile-time complaint about a missing attribute.
	regexAttr := n.Attr("", "regex")
	if regexAttr == nil {
		return nil, fmt.Errorf("%s requires a regex attribute", n.Name.Lexical())
	}
	regex, err := compileAVT(regexAttr.Value, ns)
	if err != nil {
		return nil, err
	}
	instr := &analyzeStringInstr{sel: sel, regex: regex}
	if v := n.AttrValue("flags"); v != "" {
		if instr.flags, err = compileAVT(v, ns); err != nil {
			return nil, err
		}
	}
	sawSubstring := false
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "matching-substring"):
			if instr.matching, err = c.compileSequence(ch, ch); err != nil {
				return nil, err
			}
			sawSubstring = true
		case isXSL(ch, "non-matching-substring"):
			if instr.nonMatch, err = c.compileSequence(ch, ch); err != nil {
				return nil, err
			}
			sawSubstring = true
		}
	}
	// XTSE1130: at least one of the two substring elements is required.
	// Without either, the instruction can produce nothing, which is more
	// likely a mistake than an intention.
	if !sawSubstring {
		return nil, fmt.Errorf(
			"XTSE1130: xsl:analyze-string needs an xsl:matching-substring or " +
				"an xsl:non-matching-substring child")
	}
	return instr, nil
}

// --- xsl:number -------------------------------------------------------------

// numberInstr implements xsl:number.
//
// Two levels are supported. "single" counts the node among its like-named
// preceding siblings; "multiple" walks up the ancestor chain and emits one
// number per level, which is what produces the "3.1.4" style path a
// Schematron report uses to point at an element.
//
// "any" counts every node the count pattern selects that precedes this one in
// document order, regardless of depth, restarting after the nearest preceding
// node matching @from.
type numberInstr struct {
	value *xpath.Compiled
	// selectExpr is @select, which names the node to number instead of
	// taking the context node. It is not @value: the node it selects is
	// still counted by @level, @count and @from.
	selectExpr *xpath.Compiled
	// count selects which nodes are counted; nil means "nodes with the same
	// name and kind as the context node", the spec's default.
	count *Pattern
	// from bounds the upward walk: numbering restarts below the nearest
	// ancestor matching it.
	from   *Pattern
	format *avt
	level  string
	// ordinal, lang, groupingSep and groupingSize are the remaining
	// number-to-string conversion attributes of section 12.3. All four are
	// attribute value templates, so none can be resolved at compile time.
	ordinal      *avt
	lang         *avt
	groupingSep  *avt
	groupingSize *avt
	// startAt is @start-at, which re-bases the numbers before they are
	// formatted (section 12.1). It is an attribute value template like the
	// conversion attributes, so its integers are parsed per execution.
	startAt *avt
}

func (i *numberInstr) Execute(rt *runtime, out *outputBuilder) error {
	format := "1"
	if i.format != nil {
		f, err := i.format.eval(rt)
		if err != nil {
			return err
		}
		if f != "" {
			format = f
		}
	}
	opts, err := i.conversionOptions(rt)
	if err != nil {
		return err
	}
	starts, err := i.startValues(rt)
	if err != nil {
		return err
	}

	// An explicit value bypasses counting entirely.
	if i.value != nil {
		seq, err := i.value.Eval(rt.ctx)
		if err != nil {
			return err
		}
		// The value attribute is a *sequence* of numbers, not one number
		// (section 12.3): xsl:number value="1 to 3" is the three of them,
		// separated by the format's own separators. Taking only the first
		// silently dropped the rest.
		atoms := xdm.Atomize(seq)

		// 3.8: under backwards compatibility @value is a single number, not a
		// sequence of them, and the conversion is number() -- which yields NaN
		// rather than an error for anything that is not a number, and formats
		// as the string "NaN". XSLT 1.0 defined it that way and had no
		// XTDE0980 to raise. backwards-015 numbers the empty sequence and
		// backwards-016 numbers "apples"; both want NaN.
		if i.value.CompatMode() {
			if len(atoms) == 0 {
				out.appendText("NaN")
				return nil
			}
			at, ok := atoms[0].(*xdm.Atomic)
			if !ok {
				out.appendText("NaN")
				return nil
			}
			num, cerr := xpath.CastAtomic(at, xdm.TypeDouble)
			if cerr != nil || math.IsNaN(num.Float64()) ||
				math.IsInf(num.Float64(), 0) {
				out.appendText("NaN")
				return nil
			}
			n := int64(math.Floor(num.Float64() + 0.5))
			if n < 0 {
				out.appendText("NaN")
				return nil
			}
			out.appendText(formatNumberSeq([]*big.Int{big.NewInt(n)}, format, opts))
			return nil
		}

		nums := make([]*big.Int, 0, len(atoms))
		for _, a := range atoms {
			at, ok := a.(*xdm.Atomic)
			if !ok {
				continue
			}
			n, err := numberValueOf(at)
			if err != nil {
				return err
			}
			// The second half of XTDE0980: "or if the resulting integer is
			// less than 0 (zero)". There is no numbering scheme for a
			// negative position, and formatting one produced a plausible
			// string rather than an error.
			if n.Sign() < 0 {
				return fmt.Errorf(
					"XTDE0980: the value %q converts to %s, which is less "+
						"than zero", at.String(), n)
			}
			nums = append(nums, n)
		}
		out.appendText(formatNumberSeq(rebaseNumbers(nums, starts), format, opts))
		return nil
	}

	// @select names the node to number; without it the context node is used.
	var node *xdm.Node
	if i.selectExpr != nil {
		seq, err := i.selectExpr.Eval(rt.ctx)
		if err != nil {
			return err
		}
		// XTTE1000: the select expression must yield exactly one node.
		if len(seq) != 1 {
			return fmt.Errorf(
				"XTTE1000: xsl:number/@select returned %d items, want exactly one node",
				len(seq))
		}
		n, ok := seq[0].(*xdm.Node)
		if !ok {
			return fmt.Errorf(
				"XTTE1000: xsl:number/@select returned an item that is not a node")
		}
		node = n
	} else {
		n, ok := rt.ctx.Item.(*xdm.Node)
		if !ok {
			return fmt.Errorf(
				"XTTE0990: xsl:number requires a node context or a value attribute")
		}
		node = n
	}

	numbers, err := i.countNode(rt, node)
	if err != nil {
		return err
	}
	// An empty number list is not an empty result. Section 12.3 says the
	// characters before the first format token and after the last are output
	// literally, and that holds whether or not any number was found: the
	// format "*1*" applied to nothing produces "**", not "". Returning early
	// here dropped the prefix and suffix of every unnumbered node.
	out.appendText(formatNumberSeq(rebaseNumbers(intsToBig(numbers), starts), format, opts))
	return nil
}

// numberValueOf converts one item of xsl:number/@value to the integer that is
// numbered.
//
// Section 12.1 gives the conversion exactly: xs:integer(round(number($V))).
// Doing that through a float64 is wrong for a value an xs:double cannot hold.
// xs:integer is unbounded, and @value routinely carries a computed one --
// 1234567890 cubed is 1881676371789154860897069003, which a double rounds to
// 1.8816763717891548e27 and an int64 cannot hold at all, so the number came
// out as the int64 limits rather than as itself.
//
// big.Rat is the exact value of every numeric type in the data model, so the
// round is done there and the result kept as a big.Int.
func numberValueOf(at *xdm.Atomic) (*big.Int, error) {
	// The cast to xs:double is still what decides *convertibility*: it is the
	// fn:number() of the specification's expression, and it is the step that
	// rejects "apples". Its result is then discarded in favour of the exact
	// value, except for the two doubles that have no exact value at all.
	num, err := xpath.CastAtomic(at, xdm.TypeDouble)
	if err != nil {
		// "If any value in the sequence cannot be converted to an integer
		// ... a dynamic error occurs": the failure to convert is XTDE0980
		// here, not the cast's own FORG0001.
		return nil, fmt.Errorf(
			"XTDE0980: the value %q cannot be converted to an integer",
			at.String())
	}
	if f := num.Float64(); math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf(
			"XTDE0980: the value %q cannot be converted to an integer",
			at.String())
	}

	// The value to round is the item's own exact one where it has one.
	// Rat is non-nil exactly for xs:integer and xs:decimal, the two types
	// that can hold a value no double can; for everything else -- a double,
	// a float, a string, an untypedAtomic -- the double *is* the value, and
	// SetFloat64 is exact over it.
	r := at.Rat()
	if r == nil {
		r = new(big.Rat).SetFloat64(num.Float64())
	}
	if r == nil {
		return nil, fmt.Errorf(
			"XTDE0980: the value %q cannot be converted to an integer",
			at.String())
	}
	return roundHalfUp(r), nil
}

// roundHalfUp is fn:round over an exact value: the nearest integer, and on a
// tie the one closer to positive infinity.
func roundHalfUp(r *big.Rat) *big.Int {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() == 0 {
		return q
	}
	// Twice the remainder against the denominator says which side of the
	// midpoint the value falls on; a tie goes up, which for a negative value
	// means towards zero and so leaves the truncated quotient alone.
	twice := new(big.Int).Abs(new(big.Int).Lsh(rem, 1))
	switch cmp := twice.Cmp(r.Denom()); {
	case cmp > 0:
		if r.Sign() < 0 {
			return q.Sub(q, big.NewInt(1))
		}
		return q.Add(q, big.NewInt(1))
	case cmp == 0:
		if r.Sign() > 0 {
			return q.Add(q, big.NewInt(1))
		}
		return q
	}
	return q
}

// startValues evaluates @start-at, which is an attribute value template and
// so may only be checked once the transform is running. A value written
// literally was already rejected at compile time under XTSE0020; a computed
// one that is malformed is the dynamic counterpart, XTDE0030.
func (i *numberInstr) startValues(rt *runtime) ([]int64, error) {
	if i.startAt == nil {
		return nil, nil
	}
	v, err := i.startAt.eval(rt)
	if err != nil {
		return nil, err
	}
	starts, err := parseStartAt(v)
	if err != nil {
		return nil, fmt.Errorf("XTDE0030: %w", err)
	}
	return starts, nil
}

// conversionOptions evaluates the number-to-string conversion attributes.
//
// All of them are attribute value templates, so they are resolved per
// execution rather than at compile time: xsl:number ordinal="{$o}" is legal
// and changes the answer from one call to the next.
func (i *numberInstr) conversionOptions(rt *runtime) (numberOptions, error) {
	var o numberOptions
	for _, a := range []struct {
		src *avt
		dst *string
	}{
		{i.ordinal, &o.ordinal},
		{i.lang, &o.lang},
		{i.groupingSep, &o.groupingSep},
	} {
		if a.src == nil {
			continue
		}
		v, err := a.src.eval(rt)
		if err != nil {
			return o, err
		}
		// XTDE0030: the effective value of an attribute value template, in a
		// position where one is permitted, that is not one of the permitted
		// values for that attribute. number-0826 computes lang="{$lang}"
		// from a parameter defaulting to 42.
		if a.src == i.lang && !isLanguageTag(v) {
			return o, fmt.Errorf(
				"XTDE0030: xsl:number/@lang evaluated to %q, which is not "+
					"a valid language identifier", v)
		}
		*a.dst = v
	}
	if i.groupingSize != nil {
		v, err := i.groupingSize.eval(rt)
		if err != nil {
			return o, err
		}
		if v != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return o, fmt.Errorf(
					"in xsl:number/@grouping-size: %q is not an integer", v)
			}
			o.groupingSize = n
		}
	}
	return o, nil
}

// countNode produces the sequence of numbers for a node, one per level.
func (i *numberInstr) countNode(rt *runtime, node *xdm.Node) ([]int64, error) {
	if i.level == "any" {
		n, err := i.countAny(rt, node)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			// The node itself is not selected by the count pattern, so there
			// is nothing to number and xsl:number produces no output.
			return nil, nil
		}
		return []int64{n}, nil
	}
	if i.level == "multiple" {
		// Section 12.2:
		//
		//   $A  = $S/ancestor-or-self::node()[matches-count(.)]
		//   $F  = $S/ancestor-or-self::node()[matches-from(.)][1]
		//   $AF = $A[ancestor-or-self::node()[. is $F]]
		//
		// $F is the *innermost* ancestor-or-self matching @from, and $AF is
		// those count-matches lying within its subtree — which includes $F
		// itself when it also matches @count. Stopping the upward walk at the
		// first @from match dropped that node's own number, and stopping
		// before testing @count meant a @from ancestor never contributed one.
		// ancestor-or-self::node() includes the document node, so the walk
		// runs to the root rather than stopping short of it.
		var chain []*xdm.Node
		for cur := node; cur != nil; cur = cur.Parent {
			chain = append(chain, cur)
		}
		// chain is innermost-first, so the first @from match in it is the
		// innermost one, and everything at or below that index is in its
		// subtree.
		// $F is the innermost ancestor-or-self matching @from, and matches-from
		// is true at the root whether or not @from was given — so $F always
		// exists and the walk has a definite stopping point. Everything at or
		// below $F in the chain is inside its subtree and so is a candidate
		// for $AF, $F itself included when it also matches @count.
		limit := len(chain) - 1
		for k, cur := range chain {
			ok, err := i.matchesFrom(rt, cur)
			if err != nil {
				return nil, err
			}
			if ok {
				limit = k
				break
			}
		}
		var nums []int64
		for k := 0; k <= limit; k++ {
			counted, err := i.matchesCount(rt, chain[k], node)
			if err != nil {
				return nil, err
			}
			if !counted {
				continue
			}
			n, err := i.positionAmongSiblings(rt, chain[k], node)
			if err != nil {
				return nil, err
			}
			// The walk is innermost-first, so each level is prepended to keep
			// the outermost number first.
			nums = append([]int64{n}, nums...)
		}
		return nums, nil
	}

	// level="single": the nearest self-or-ancestor that the count pattern
	// selects is the node that gets numbered.
	//
	// The axis the spec names is ancestor-or-self::node(), which includes the
	// document node. Breaking before testing it meant xsl:number produced
	// nothing at all when the node being numbered was the root — which is the
	// context a simplified stylesheet starts in.
	// The specification computes $A and $F independently and only then asks
	// whether $A lies in the subtree rooted at $F:
	//
	//   $A  = $S/ancestor-or-self::node()[matches-count(.)][1]
	//   $F  = $S/ancestor-or-self::node()[matches-from(.)][1]
	//   $AF = $A[ancestor-or-self::node()[. is $F]]
	//
	// Walking up and stopping at the first @from match computed something
	// else: it never looked for a counted node *above* the @from node, and
	// so returned nothing where the spec returns a number.
	var chain []*xdm.Node
	for cur := node; cur != nil; cur = cur.Parent {
		chain = append(chain, cur)
	}

	aIdx := -1
	for k, cur := range chain {
		ok, err := i.matchesCount(rt, cur, node)
		if err != nil {
			return nil, err
		}
		if ok {
			aIdx = k
			break
		}
	}
	if aIdx < 0 {
		return nil, nil
	}

	fIdx := -1
	for k, cur := range chain {
		ok, err := i.matchesFrom(rt, cur)
		if err != nil {
			return nil, err
		}
		if ok {
			fIdx = k
			break
		}
	}
	// chain is innermost-first, so $A is inside the subtree rooted at $F
	// exactly when $F is at or above it — that is, at a larger index.
	if fIdx < 0 || aIdx > fIdx {
		return nil, nil
	}

	n, err := i.positionAmongSiblings(rt, chain[aIdx], node)
	if err != nil {
		return nil, err
	}
	return []int64{n}, nil
}

// matchesCount reports whether n is a node the count pattern selects. With no
// count attribute the default is "same node kind and name as the node being
// numbered".
func (i *numberInstr) matchesCount(rt *runtime, n, target *xdm.Node) (bool, error) {
	if i.count == nil {
		return n.Kind == target.Kind &&
			n.Name.URI == target.Name.URI &&
			n.Name.Local == target.Name.Local, nil
	}
	return i.count.Matches(n, rt.ctx)
}

func (i *numberInstr) matchesFrom(rt *runtime, n *xdm.Node) (bool, error) {
	// Section 12.2 states the base case explicitly: matches-from returns true
	// "if the given node matches the pattern given in the from attribute, or
	// if $node is the root node of a tree". The root is a match whether or
	// not @from was given, which is what guarantees $F always exists and so
	// that counting has somewhere to start.
	if n.Parent == nil {
		return true, nil
	}
	if i.from == nil {
		return false, nil
	}
	return i.from.Matches(n, rt.ctx)
}

// countAny implements level="any": one number, counting every node the count
// pattern selects at or before this node in document order, at any depth.
//
// The walk is over the whole tree rather than the ancestor chain, which is why
// this cannot reuse positionAmongSiblings. Ancestors are excluded from the
// count: the spec counts nodes that *precede* the target, and an ancestor
// contains it rather than preceding it — counting them would inflate every
// number by the depth of the node.
//
// @from restarts the numbering: only nodes after the last preceding node
// matching it are counted, which is what makes "number the footnotes within
// each chapter" work.
func (i *numberInstr) countAny(rt *runtime, node *xdm.Node) (int64, error) {
	// Section 12.2 defines this exactly, and the definition is easier to
	// follow than to paraphrase:
	//
	//   $A  = $S/(preceding::node()|ancestor-or-self::node())[matches-count(.)]
	//   $F  = $S/(preceding::node()|ancestor-or-self::node())[matches-from(.)][last()]
	//   $AF = $A[. is $F or . >> $F]
	//   result = count($AF), or () when $AF is empty
	//
	// Ancestors are *in* the candidate set. Excluding them — which is the
	// intuitive reading, since an ancestor does not precede its descendant in
	// the usual sense — made "count" patterns naming an ancestor element
	// return nothing at all: the H2 that numbers an H4 is one of its
	// ancestors, and the H4 got no number for it.
	root := node.Root()

	// An attribute or namespace node is not on its parent's child axis, so
	// the walk below can never arrive at it. For such a node the candidate
	// set is the one belonging to its parent element — preceding::node() of
	// an attribute is the same as that of the element carrying it — with the
	// node itself appended as the last member of ancestor-or-self.
	stop, extra := node, (*xdm.Node)(nil)
	if node.Kind == xdm.KindAttribute || node.Kind == xdm.KindNamespace {
		if node.Parent == nil {
			return 0, fmt.Errorf(
				"xsl:number: the context node is not in the tree being walked")
		}
		stop, extra = node.Parent, node
	}

	// The candidate set in document order: everything from the root up to
	// and including the node, minus the descendants of the node itself, is
	// exactly preceding::node() plus ancestor-or-self::node().
	var candidates []*xdm.Node
	var reached bool
	var walk func(cur *xdm.Node)
	walk = func(cur *xdm.Node) {
		if reached {
			return
		}
		candidates = append(candidates, cur)
		if cur == stop {
			reached = true
			return
		}
		for _, ch := range cur.Children {
			walk(ch)
			if reached {
				return
			}
		}
	}
	walk(root)
	if !reached {
		return 0, fmt.Errorf(
			"xsl:number: the context node is not in the tree being walked")
	}
	if extra != nil {
		candidates = append(candidates, extra)
	}

	// $F: the last of them matching @from. With no @from every candidate
	// qualifies, which is the same as starting at the beginning.
	from := -1
	if i.from != nil {
		for k, c := range candidates {
			ok, err := i.matchesFrom(rt, c)
			if err != nil {
				return 0, err
			}
			if ok {
				from = k
			}
		}
		if from < 0 {
			// No node matches @from at all. Read literally, $F is then empty
			// and so is $AF, which would make the result the empty sequence.
			// Both the conformance suite and this package's own tests say
			// otherwise: counting runs from the start of the document, as if
			// @from were absent. Taking the literal reading cost five suite
			// cases and broke a unit test that was right.
			from = 0
		}
	}

	var count int64
	for k, c := range candidates {
		if k < from {
			continue
		}
		ok, err := i.matchesCount(rt, c, node)
		if err != nil {
			return 0, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}

// positionAmongSiblings counts how many preceding siblings the count pattern
// also selects, plus one.
func (i *numberInstr) positionAmongSiblings(rt *runtime, n, target *xdm.Node) (int64, error) {
	if n.Parent == nil {
		return 1, nil
	}
	var count int64
	for _, sib := range n.Parent.Children {
		ok, err := i.matchesCount(rt, sib, target)
		if err != nil {
			return 0, err
		}
		if ok {
			count++
		}
		if sib == n {
			return count, nil
		}
	}
	return count, nil
}

// formatNumberSeq renders a sequence of level numbers, reusing the picture's
// separator between them.
//
// A format like "1.1.1" gives "." as the separator; a single-token format like
// "1" repeats that token for every level and joins with ".", which is what
// makes "<xsl:number level='multiple' format='1'/>" produce "2.1.3".
func formatNumberSeq(nums []*big.Int, format string, opts numberOptions) string {
	tokens, seps, prefix, suffix := splitFormat(format)
	if len(tokens) == 0 {
		// A picture with no format token at all still has to number
		// something, so the default token "1" is used — and the literal that
		// makes up the whole picture surrounds it on both sides, because it
		// is at once everything before the first token and everything after
		// the last. format="*" on the number 1 gives "*1*".
		tokens = []string{"1"}
		suffix = prefix
	}
	var sb strings.Builder
	// Section 12.3: any characters before the first token and after the last
	// are emitted as they stand. Dropping them turned the common "(1)" and
	// "[1]" formats into a bare number.
	sb.WriteString(prefix)
	for i, n := range nums {
		if i > 0 {
			sep := "."
			if i-1 < len(seps) {
				sep = seps[i-1]
			} else if len(seps) > 0 {
				sep = seps[len(seps)-1]
			}
			sb.WriteString(sep)
		}
		tok := tokens[len(tokens)-1]
		if i < len(tokens) {
			tok = tokens[i]
		}
		sb.WriteString(formatNumber(n, tok, opts))
	}
	sb.WriteString(suffix)
	return sb.String()
}

// splitFormat separates a picture into alphanumeric format tokens and the
// literal separators between them.
func splitFormat(format string) (tokens, seps []string, prefix, suffix string) {
	runes := []rune(format)
	i := 0
	for i < len(runes) {
		if isFormatToken(runes[i]) {
			j := i
			for j < len(runes) && isFormatToken(runes[j]) {
				j++
			}
			tokens = append(tokens, string(runes[i:j]))
			i = j
			continue
		}
		j := i
		for j < len(runes) && !isFormatToken(runes[j]) {
			j++
		}
		run := string(runes[i:j])
		switch {
		case len(tokens) == 0:
			// Before the first token: a prefix.
			prefix = run
		case j >= len(runes):
			// After the last token: a suffix. It is not a separator, since
			// there is no following number for it to separate.
			suffix = run
		default:
			seps = append(seps, run)
		}
		i = j
	}
	return tokens, seps, prefix, suffix
}

// isFormatToken reports whether r may appear in a format token.
//
// Section 12.3 defines this by Unicode category — "Nd, Nl, No, Lu, Ll, Lt, Lm
// or Lo" — not by ASCII range. Restricting it to ASCII split a picture written
// in any other script into separators, so an Arabic-Indic or Greek format
// token was never recognised as a token at all.
func isFormatToken(r rune) bool {
	return unicode.IsDigit(r) || unicode.IsNumber(r) || unicode.IsLetter(r)
}

func (c *compiler) compileNumber(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &numberInstr{level: n.AttrValue("level")}
	if instr.level == "" {
		instr.level = "single"
	}
	switch instr.level {
	case "single", "multiple":
	case "any":
	default:
		return nil, fmt.Errorf("invalid xsl:number level %q", instr.level)
	}

	var err error
	if v := n.AttrValue("value"); v != "" {
		if instr.value, err = compileExpr(v, ns); err != nil {
			return nil, err
		}
	}
	if v := n.AttrValue("select"); v != "" {
		if instr.selectExpr, err = compileExpr(v, ns); err != nil {
			return nil, fmt.Errorf("in xsl:number/@select: %w", err)
		}
	}
	if v := n.AttrValue("count"); v != "" {
		if instr.count, err = CompilePattern(v, ns); err != nil {
			return nil, fmt.Errorf("in xsl:number/@count: %w", err)
		}
	}
	if v := n.AttrValue("from"); v != "" {
		if instr.from, err = CompilePattern(v, ns); err != nil {
			return nil, fmt.Errorf("in xsl:number/@from: %w", err)
		}
	}
	if v := n.AttrValue("format"); v != "" {
		if instr.format, err = compileAVT(v, ns); err != nil {
			return nil, err
		}
	}
	for _, a := range []struct {
		name string
		dst  **avt
	}{
		{"ordinal", &instr.ordinal},
		{"lang", &instr.lang},
		{"grouping-separator", &instr.groupingSep},
		{"grouping-size", &instr.groupingSize},
		{"start-at", &instr.startAt},
	} {
		if v := n.AttrValue(a.name); v != "" {
			// XTSE0020 is about an attribute whose *fixed* value is not one
			// of the permitted values. A value written with curly brackets
			// is excluded by the error's own wording and is checked when it
			// is evaluated instead, under XTDE0030. number-0825 writes
			// lang="#####" literally and expects the static error.
			if a.name == "lang" && !strings.Contains(v, "{") &&
				!isLanguageTag(v) {
				return nil, fmt.Errorf(
					"XTSE0020: xsl:number/@lang value %q is not a valid "+
						"language identifier", v)
			}
			// Same rule for @start-at: a fixed value that does not match
			// section 12.1's production is XTSE0020, which is what
			// number-0109 asserts of start-at="1..2".
			if a.name == "start-at" && !strings.Contains(v, "{") {
				if _, perr := parseStartAt(v); perr != nil {
					return nil, fmt.Errorf("XTSE0020: %w", perr)
				}
			}
			if *a.dst, err = compileAVT(v, ns); err != nil {
				return nil, fmt.Errorf("in xsl:number/@%s: %w", a.name, err)
			}
		}
	}
	return instr, nil
}

// formatNumber renders one level number in a numbering style.
// numberOptions carries the number-to-string conversion attributes other than
// format, which section 12.3 defines alongside it.
type numberOptions struct {
	// ordinal requests ordinal rather than cardinal numbering when it is a
	// non-empty string. Its *value* may also select a variant in inflected
	// languages; English has none, so any non-empty value means the same
	// thing here.
	ordinal string
	// lang selects the language for the spelled-out sequences. Only English
	// is implemented; section 12.3 requires an unsupported language to fall
	// back to the default rather than to fail.
	lang string
	// groupingSep and groupingSize insert a separator every groupingSize
	// digits of a decimal sequence, which is how "1,000" is written.
	groupingSep  string
	groupingSize int
}

// isLanguageTag reports whether v is a well-formed xs:language value, which
// is what xsl:number/@lang and xsl:sort/@lang are declared to hold.
//
// The lexical space is the BCP 47 shape XML Schema fixes as
// [a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*: subtags of one to eight alphanumerics,
// the first of them alphabetic. That is a lexical test, not a registry
// lookup — an unregistered but well-formed tag is a *valid* value the
// processor falls back to English for, per section 12.3, while "#####" or
// "42" are not values of the type at all and are errors.
func isLanguageTag(v string) bool {
	if v == "" {
		return false
	}
	for i, sub := range strings.Split(v, "-") {
		if len(sub) < 1 || len(sub) > 8 {
			return false
		}
		for j := 0; j < len(sub); j++ {
			c := sub[j]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			case c >= '0' && c <= '9':
				// Only the first subtag is restricted to letters; the rest
				// may be alphanumeric, which is how "en-US" and "zh-Hant"
				// and the numeric region subtags of "es-419" all fit.
				if i == 0 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// spellNumberLang spells n in the language @lang asks for.
//
// Section 12.3: "if the processor does not support numbering in the language
// requested, it must use the language it would use if the lang attribute were
// omitted", so an unrecognised language is answered in English rather than
// refused. The tag is matched on its primary subtag alone, per BCP 47, so
// "de-AT" and "de-CH" reach the same table as "de"; German has no ordinal
// support here, and an ordinal request falls back to English for the reason
// spellNumberDE records.
func spellNumberLang(n int64, opts numberOptions) string {
	ordinal := opts.ordinal != ""
	primary := opts.lang
	if i := strings.IndexAny(primary, "-_"); i >= 0 {
		primary = primary[:i]
	}
	if strings.EqualFold(primary, "de") && !ordinal {
		return spellNumberDE(n)
	}
	return spellNumber(n, ordinal)
}

func formatNumber(n *big.Int, format string, opts numberOptions) string {
	// The alphabetic, roman and spelled-out schemes are defined over
	// positions in a document, which no counting walk can push past an
	// int64. A value large enough to need more has no roman numeral and no
	// English name, so it falls through to the decimal rule below rather
	// than being truncated to something that reads like an answer.
	if small, ok := smallNumber(n); ok {
		switch format {
		case "a":
			return alphaNumber(small, 'a')
		case "A":
			return alphaNumber(small, 'A')
		case "i":
			return strings.ToLower(romanNumber(small))
		case "I":
			return romanNumber(small)
		case "w":
			return spellNumberLang(small, opts)
		case "W":
			return strings.ToUpper(spellNumberLang(small, opts))
		case "Ww":
			return titleCaseWords(spellNumberLang(small, opts))
		}
	}
	// The decimal rule, section 12.3: "any token where the last character has
	// a decimal digit value of 1, and the Unicode value of preceding
	// characters is one less than the Unicode value of the last character".
	//
	// That is a much wider rule than a run of zeros. "001" and "0001" match
	// it, but so does any digit family — Arabic-Indic "١" numbers in
	// Arabic-Indic digits — and the width is the *token's* length, so "0100"
	// is not a valid token at all while "001" pads to three. Matching only
	// zeros made every non-zero-padded token fall through to plain decimal.
	if digits, width, ok := decimalToken(format); ok {
		s := decimalIn(n, digits, width)
		if opts.groupingSep != "" && opts.groupingSize > 0 {
			s = groupDigits(s, opts.groupingSep, opts.groupingSize)
		}
		if opts.ordinal != "" {
			// The ordinal suffix is decided by the last one or two digits,
			// which every value has however large it is.
			s += ordinalSuffix(lastTwoDigits(n))
		}
		return s
	}
	return n.String()
}

// smallNumber returns n as an int64 when it fits in one.
func smallNumber(n *big.Int) (int64, bool) {
	if !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

// lastTwoDigits returns n mod 100, which is all fn:ordinal-suffix looks at.
func lastTwoDigits(n *big.Int) int64 {
	m := new(big.Int).Mod(new(big.Int).Abs(n), big.NewInt(100))
	return m.Int64()
}

// groupDigits inserts sep every size digits from the right.
func groupDigits(s, sep string, size int) string {
	r := []rune(s)
	var out []rune
	for i, c := range r {
		if i > 0 && (len(r)-i)%size == 0 {
			out = append(out, []rune(sep)...)
		}
		out = append(out, c)
	}
	return string(out)
}

// decimalToken recognises a decimal format token and returns the zero digit of
// its digit family together with the minimum width it requires.
func decimalToken(format string) (zero rune, width int, ok bool) {
	runes := []rune(format)
	if len(runes) == 0 {
		return 0, 0, false
	}
	last := runes[len(runes)-1]
	// The last character must be the "one" of some decimal digit family, and
	// its family's zero is one below it.
	if !unicode.IsDigit(last) {
		return 0, 0, false
	}
	switch digitValue(last) {
	case 1:
		zero = last - 1
	case 0:
		// A token that is all zeros of one family names no numbering
		// sequence of its own, and 12.3 says what to do then: "If an
		// implementation does not support a numbering sequence represented
		// by the given token, it must use a format token of 1." Taking that
		// literally down to the ASCII "1" would also throw away the digit
		// family the author wrote, so the substitution is made within the
		// family the token names -- "0" numbers in ASCII digits and the
		// Arabic-Indic zero in Arabic-Indic ones, each to the token's own
		// width, which is the reading number-0111 asserts.
		zero = last
	default:
		return 0, 0, false
	}
	// Every preceding character must be that family's zero, so that the
	// token's own length is the width. "0100" fails here, which is why the
	// specification's own example of it is not a decimal token.
	for _, r := range runes[:len(runes)-1] {
		if r != zero {
			return 0, 0, false
		}
	}
	return zero, len(runes), true
}

// digitValue returns the decimal digit value of a Unicode digit, or -1.
func digitValue(r rune) int {
	// Every decimal digit family is a contiguous run of ten codepoints
	// starting at its zero, so the value is the distance back to the first
	// codepoint that is still a digit. unicode.IsDigit has already
	// established that r is in such a run.
	for d := 0; d <= 9; d++ {
		if !unicode.IsDigit(r - rune(d) - 1) {
			return d
		}
	}
	return -1
}

// decimalIn renders n in the digit family whose zero is the given rune, padded
// to at least width digits.
func decimalIn(n *big.Int, zero rune, width int) string {
	digits := new(big.Int).Abs(n).String()
	ds := make([]rune, 0, len(digits)+width)
	for len(ds)+len(digits) < width {
		ds = append(ds, zero)
	}
	for _, c := range digits {
		ds = append(ds, zero+(c-'0'))
	}
	if n.Sign() < 0 {
		ds = append([]rune{'-'}, ds...)
	}
	return string(ds)
}

// titleCaseWords upper-cases the first letter of each word.
func titleCaseWords(s string) string {
	out := []rune(s)
	start := true
	for i, r := range out {
		if start && unicode.IsLetter(r) {
			out[i] = unicode.ToUpper(r)
			start = false
		}
		if r == ' ' || r == '-' {
			start = true
		}
	}
	return string(out)
}

// alphaNumber renders 1 as "a", 26 as "z", 27 as "aa", in a bijective base-26
// system that has no zero digit.
func alphaNumber(n int64, base rune) string {
	if n <= 0 {
		return "0"
	}
	var sb []rune
	for n > 0 {
		n--
		sb = append([]rune{base + rune(n%26)}, sb...)
		n /= 26
	}
	return string(sb)
}

func romanNumber(n int64) string {
	if n <= 0 || n > 3999 {
		return strconv.FormatInt(n, 10)
	}
	vals := []int64{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	return sb.String()
}

// resolveCollation evaluates the collation attribute for this execution.
func (i *forEachGroupInstr) resolveCollation(rt *runtime) (xpath.Collation, error) {
	if i.collation == nil {
		// No @collation: the default collation in force where the
		// instruction was written applies, which is what
		// [xsl:]default-collation sets.
		if i.defaultCollation == "" {
			return nil, nil
		}
		return resolveGroupCollation(i.defaultCollation)
	}
	uri, err := i.collation.eval(rt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(uri) == "" {
		return nil, nil
	}
	return resolveGroupCollation(uri)
}

// resolveGroupCollation is xpath.ResolveCollation with xsl:for-each-group's
// own error code.
//
// Section 14.2: "it is a non-recoverable dynamic error if the collation URI
// specified to xsl:for-each-group ... is a collation that is not recognized by
// the implementation". FOCH0002 is fn:compare's code for the same condition,
// and reporting it here named the wrong instruction.
func resolveGroupCollation(uri string) (xpath.Collation, error) {
	coll, err := xpath.ResolveCollation(uri)
	if err != nil {
		return nil, fmt.Errorf(
			"XTDE1110: xsl:for-each-group/@collation %q is not a recognized "+
				"collation", uri)
	}
	return coll, nil
}

// collationKey is the string a grouping key is indexed by.
//
// Two keys belong in the same group when the collation calls them equal, so
// the index is keyed by a form the collation makes identical rather than by
// the key as written. For the ASCII case-insensitive collation that is the
// folded string; for codepoint it is the string itself.
//
// Only equality matters here, not order, so a collation that cannot produce a
// key falls back to comparing — which for the collations this implements is
// the same answer by a slower route.
// groupingKey is the key two items must share to land in the same group.
//
// Section 14: grouping keys are compared by value, using the rules of the "eq"
// operator, not by their string form. Keying on the string put two
// xs:dateTime values naming the same instant in different groups whenever
// their lexical timezones differed, and kept an xs:integer apart from the
// equal xs:double.
func groupingKey(rt *runtime, a *xdm.Atomic, coll xpath.Collation) (string, error) {
	return xpath.GroupingKey(a, coll, rt.ctx.ImplicitTimezone)
}

func collationKey(coll xpath.Collation, s string) string {
	if coll == nil {
		return s
	}
	if k, ok := coll.(interface{ Key(string) string }); ok {
		return k.Key(s)
	}
	return s
}
