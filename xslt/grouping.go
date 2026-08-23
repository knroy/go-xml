package xslt

import (
	"fmt"
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
	groupBy         *xpath.Compiled
	groupAdjacent   *xpath.Compiled
	groupStartsWith *Pattern
	groupEndsWith   *Pattern
	// collation names the collation that compares grouping keys. It is an
	// attribute value template, so it is resolved per execution rather than
	// at compile time: collation="{$c}" is legal and names a collation the
	// stylesheet computes.
	collation *avt
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
		groups, err = groupByKey(rt, seq, i.groupBy, coll)
	case i.groupAdjacent != nil:
		var coll xpath.Collation
		if coll, err = i.resolveCollation(rt); err != nil {
			return err
		}
		groups, err = groupAdjacentKey(rt, seq, i.groupAdjacent, coll)
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
		// Sorting applies to the groups, with each group's first item as the
		// context, so a sort key can name the grouping value.
		var items xdm.Sequence
		byItem := map[xdm.Item]group{}
		for _, g := range groups {
			if len(g.items) == 0 {
				continue
			}
			items = append(items, g.items[0])
			byItem[g.items[0]] = g
		}
		sorted, err := applySorts(rt, items, i.sorts)
		if err != nil {
			return err
		}
		reordered := make([]group, 0, len(sorted))
		for _, it := range sorted {
			reordered = append(reordered, byItem[it])
		}
		groups = reordered
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
		sub := rt.withCurrent(focus, idx+1, size)
		sub = sub.withVar(currentGroupVar, g.items)
		sub = sub.withVar(currentGroupingKeyVar, g.key)
		if err := execSequence(i.body, sub, out); err != nil {
			return err
		}
	}
	return nil
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

	for idx, it := range seq {
		sub := rt.withFocus(it, idx+1, len(seq))
		vals, err := key.Eval(sub.ctx)
		if err != nil {
			return nil, err
		}
		// An item with multiple key values joins every corresponding group,
		// which is what makes group-by usable for many-to-many classification.
		for _, kv := range xdm.Atomize(vals) {
			// The group is found by the *collated* form of the key, so that
			// two keys the collation calls equal land together — while the
			// group keeps the key as written, which is what
			// current-grouping-key() returns.
			k := collationKey(coll, kv.(*xdm.Atomic).String())
			gi, ok := index[k]
			if !ok {
				index[k] = len(groups)
				groups = append(groups, group{key: xdm.One(kv)})
				gi = len(groups) - 1
			}
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
		atoms := xdm.Atomize(vals)
		if len(atoms) == 0 {
			continue
		}
		k := collationKey(coll, atoms[0].(*xdm.Atomic).String())
		if first || k != prev {
			groups = append(groups, group{key: xdm.One(atoms[0])})
			first, prev = false, k
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)
	}
	return groups, nil
}

// groupStartingWith begins a new group at each item matching the pattern.
func groupStartingWith(rt *runtime, seq xdm.Sequence, pat *Pattern) ([]group, error) {
	var groups []group
	for _, it := range seq {
		n, ok := it.(*xdm.Node)
		start := false
		if ok {
			m, err := pat.Matches(n, rt.ctx)
			if err != nil {
				return nil, err
			}
			start = m
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
	var groups []group
	open := false
	for _, it := range seq {
		if !open {
			groups = append(groups, group{})
			open = true
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)

		if n, ok := it.(*xdm.Node); ok {
			m, err := pat.Matches(n, rt.ctx)
			if err != nil {
				return nil, err
			}
			if m {
				open = false
			}
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

	if v := n.AttrValue("collation"); v != "" {
		if instr.collation, err = compileAVT(v, ns); err != nil {
			return nil, err
		}
	}

	count := 0
	if v := n.AttrValue("group-by"); v != "" {
		if instr.groupBy, err = xpath.Compile(v, ns); err != nil {
			return nil, err
		}
		count++
	}
	if v := n.AttrValue("group-adjacent"); v != "" {
		if instr.groupAdjacent, err = xpath.Compile(v, ns); err != nil {
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
			"xsl:for-each-group requires exactly one grouping attribute, found %d", count)
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
	input := stringJoin(seq, "")

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

	re, err := xpath.CompileRegexp(pattern, flags)
	if err != nil {
		return err
	}
	if re.MatchString("") {
		return fmt.Errorf("FORX0003: analyze-string pattern matches the empty string")
	}

	pos := 0
	for _, loc := range re.FindAllStringSubmatchIndex(input, -1) {
		if loc[0] > pos {
			if err := i.runBranch(rt, out, i.nonMatch, input[pos:loc[0]], nil, input); err != nil {
				return err
			}
		}
		if err := i.runBranch(rt, out, i.matching, input[loc[0]:loc[1]], loc, input); err != nil {
			return err
		}
		pos = loc[1]
	}
	if pos < len(input) {
		if err := i.runBranch(rt, out, i.nonMatch, input[pos:], nil, input); err != nil {
			return err
		}
	}
	return nil
}

// runBranch executes one branch with the run as the context item.
func (i *analyzeStringInstr) runBranch(rt *runtime, out *outputBuilder,
	body []Instruction, text string, loc []int, input string) error {
	if len(body) == 0 {
		return nil
	}
	sub := rt.withFocus(xdm.NewString(text), 1, 1)
	if loc != nil {
		// regex-group(n) reads the captured groups of the current match.
		groups := make([]string, 0, len(loc)/2)
		for g := 0; g < len(loc)/2; g++ {
			if loc[2*g] < 0 {
				groups = append(groups, "")
				continue
			}
			groups = append(groups, input[loc[2*g]:loc[2*g+1]])
		}
		sub = sub.withVar(regexGroupsVar, groupsToSeq(groups))
	}
	return execSequence(body, sub, out)
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
	regex, err := requiredAVT(n, "regex", ns)
	if err != nil {
		return nil, err
	}
	instr := &analyzeStringInstr{sel: sel, regex: regex}
	if v := n.AttrValue("flags"); v != "" {
		if instr.flags, err = compileAVT(v, ns); err != nil {
			return nil, err
		}
	}
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "matching-substring"):
			if instr.matching, err = c.compileSequence(ch, ch); err != nil {
				return nil, err
			}
		case isXSL(ch, "non-matching-substring"):
			if instr.nonMatch, err = c.compileSequence(ch, ch); err != nil {
				return nil, err
			}
		}
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
		if len(atoms) == 0 {
			return nil
		}
		nums := make([]int64, 0, len(atoms))
		for _, a := range atoms {
			at, ok := a.(*xdm.Atomic)
			if !ok {
				continue
			}
			conv, err := xpath.CastAtomic(at, xdm.TypeInteger)
			if err != nil {
				return err
			}
			nums = append(nums, conv.Int64())
		}
		out.appendText(formatNumberSeq(nums, format, opts))
		return nil
	}

	node, ok := rt.ctx.Item.(*xdm.Node)
	if !ok {
		return fmt.Errorf("xsl:number requires a node context or a value attribute")
	}

	numbers, err := i.countNode(rt, node)
	if err != nil {
		return err
	}
	if len(numbers) == 0 {
		return nil
	}
	out.appendText(formatNumberSeq(numbers, format, opts))
	return nil
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
		var nums []int64
		for cur := node; cur != nil; cur = cur.Parent {
			if cur.Kind == xdm.KindDocument {
				break
			}
			stop, err := i.matchesFrom(rt, cur)
			if err != nil {
				return nil, err
			}
			if stop {
				break
			}
			counted, err := i.matchesCount(rt, cur, node)
			if err != nil {
				return nil, err
			}
			if !counted {
				continue
			}
			n, err := i.positionAmongSiblings(rt, cur, node)
			if err != nil {
				return nil, err
			}
			// The walk is upward, so each level is prepended to keep the
			// outermost number first.
			nums = append([]int64{n}, nums...)
		}
		return nums, nil
	}

	// level="single": the nearest self-or-ancestor that the count pattern
	// selects is the node that gets numbered.
	for cur := node; cur != nil; cur = cur.Parent {
		if cur.Kind == xdm.KindDocument {
			break
		}
		stop, err := i.matchesFrom(rt, cur)
		if err != nil {
			return nil, err
		}
		if stop {
			break
		}
		counted, err := i.matchesCount(rt, cur, node)
		if err != nil {
			return nil, err
		}
		if !counted {
			continue
		}
		n, err := i.positionAmongSiblings(rt, cur, node)
		if err != nil {
			return nil, err
		}
		return []int64{n}, nil
	}
	return nil, nil
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
	root := node.Root()
	ancestors := map[*xdm.Node]bool{}
	for a := node.Parent; a != nil; a = a.Parent {
		ancestors[a] = true
	}

	var count int64
	var reached bool
	var walk func(cur *xdm.Node) error
	walk = func(cur *xdm.Node) error {
		if reached {
			return nil
		}
		// A @from match resets the count, so numbering restarts inside each
		// region the pattern delimits.
		if cur != node {
			restart, err := i.matchesFrom(rt, cur)
			if err != nil {
				return err
			}
			if restart {
				count = 0
			}
		}
		if !ancestors[cur] {
			counted, err := i.matchesCount(rt, cur, node)
			if err != nil {
				return err
			}
			if counted {
				count++
			}
		}
		if cur == node {
			// Everything after the target in document order is irrelevant.
			reached = true
			return nil
		}
		for _, ch := range cur.Children {
			if err := walk(ch); err != nil {
				return err
			}
			if reached {
				return nil
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return 0, err
	}
	if !reached {
		return 0, fmt.Errorf("xsl:number: the context node is not in the tree being walked")
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
func formatNumberSeq(nums []int64, format string, opts numberOptions) string {
	tokens, seps, prefix, suffix := splitFormat(format)
	if len(tokens) == 0 {
		tokens = []string{"1"}
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
		if instr.value, err = xpath.Compile(v, ns); err != nil {
			return nil, err
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
	} {
		if v := n.AttrValue(a.name); v != "" {
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

func formatNumber(n int64, format string, opts numberOptions) string {
	switch format {
	case "a":
		return alphaNumber(n, 'a')
	case "A":
		return alphaNumber(n, 'A')
	case "i":
		return strings.ToLower(romanNumber(n))
	case "I":
		return romanNumber(n)
	case "w":
		return spellNumber(n, opts.ordinal != "")
	case "W":
		return strings.ToUpper(spellNumber(n, opts.ordinal != ""))
	case "Ww":
		return titleCaseWords(spellNumber(n, opts.ordinal != ""))
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
			s += ordinalSuffix(n)
		}
		return s
	}
	return strconv.FormatInt(n, 10)
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
	if digitValue(last) != 1 {
		return 0, 0, false
	}
	zero = last - 1
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
func decimalIn(n int64, zero rune, width int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var ds []rune
	for {
		ds = append([]rune{zero + rune(n%10)}, ds...)
		n /= 10
		if n == 0 {
			break
		}
	}
	for len(ds) < width {
		ds = append([]rune{zero}, ds...)
	}
	if neg {
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
		return nil, nil
	}
	uri, err := i.collation.eval(rt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(uri) == "" {
		return nil, nil
	}
	return xpath.ResolveCollation(uri)
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
func collationKey(coll xpath.Collation, s string) string {
	if coll == nil {
		return s
	}
	if k, ok := coll.(interface{ Key(string) string }); ok {
		return k.Key(s)
	}
	return s
}
