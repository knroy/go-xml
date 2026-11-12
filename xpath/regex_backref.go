package xpath

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/knroy/go-xml/v2/xdm"
)

// Backreferences are XPath 2.0's one regular-expression construct with no RE2
// equivalent, and RE2's linear-time guarantee is worth more than the handful of
// patterns that use them. This file implements them anyway, for the subset
// where doing so costs nothing.
//
// The observation is that a backreference is only hard when the group it names
// can match more than one width. RE2 returns a single submatch assignment — the
// greedy one — and cannot enumerate alternatives, so for "(a*)\1" against "aa"
// it reports the group as "aa", leaving nothing for the backreference, and a
// comparison against that answers false where the correct answer is true (the
// split is "a" + "a"). No amount of comparing fixes that: the information
// needed was discarded before the comparison ran.
//
// But when every group a backreference names has a *fixed* width, the greedy
// assignment is the only assignment. There is nothing to enumerate, so
// capture-and-compare is not an approximation — it is exact, and it runs in
// RE2's linear time with one extra pass per candidate position.
//
// So the split is by what can be decided rather than by what the caller asked
// for: a fixed-width backreference is handled, a variable-width one is refused
// with FORX0002 as before. That is why this needs no option to enable. An
// engine that answers correctly or says it cannot is safe to have on always;
// one that guesses is not safe at any setting.
//
// Of the twelve QT3 cases that use backreferences, eleven are fixed-width.

// backrefRegexp is a pattern whose backreferences are resolved by comparison
// rather than by the automaton.
//
// stripped is the pattern with each backreference removed, so RE2 can compile
// it; refs records which group each removed backreference named, in the order
// they appeared. Matching runs stripped, then checks at each candidate position
// that the text following the match equals the captured groups in sequence.
type backrefRegexp struct {
	stripped *regexp.Regexp
	// fold is set by the "i" flag. The comparison has to fold too: RE2 folds
	// inside the automaton, but the backreference is checked by string
	// comparison, and "(a)\\1" against "aA" under "i" must match.
	fold bool
	// segments alternate: a matched prefix, then the backreferences that
	// follow it. Only the trailing form is supported, which is what the
	// fixed-width restriction leaves.
	refs []backrefUse
	// width is the number of characters every match of stripped spans. The
	// matcher is admitted only when that number is fixed, so the match at a
	// start position, and every group's place in it, is the only one.
	width int
	// atStart and atEnd record a leading "^" and a trailing "$". Without them
	// stripped holds no anchor at all, which is what lets it be run on a slice
	// of the input without a slice boundary posing as the start of the text.
	atStart, atEnd bool
}

// backrefUse is one \N occurrence and the repetition applied to it.
type backrefUse struct {
	group int
	// literal is the digits that followed the group number when "\\11" turned
	// out to mean group 1 followed by a literal "1".
	literal string
	// star is true for "\N*", which matches zero or more copies. Only "*" is
	// supported because it is the only quantifier the suite exercises and the
	// only one whose greedy reading cannot be wrong: each copy is the same
	// fixed width, so consuming as many as fit is the unique maximal match and
	// nothing later can need fewer.
	star bool
}

func hasBackref(p string) bool {
	for i := 0; i+1 < len(p); i++ {
		if p[i] != '\\' {
			continue
		}
		if p[i+1] >= '1' && p[i+1] <= '9' {
			return true
		}
		i++ // skip the escaped character
	}
	return false
}

// splitBackrefs removes the trailing run of backreferences from a pattern.
//
// Only backreferences at the end are handled — after them may come "$" and
// nothing else. A backreference in the middle would need the comparison to
// feed back into the automaton, which is the backtracking this avoids.
func splitBackrefs(p string) (string, []backrefUse, bool, error) {
	// Find the first backreference; everything from there must be
	// backreferences, their quantifiers, and an optional trailing anchor.
	i := firstBackref(p)
	if i < 0 {
		return p, nil, false, nil
	}
	atEnd := false
	head, tail := p[:i], p[i:]

	var refs []backrefUse
	for len(tail) > 0 {
		switch {
		case strings.HasPrefix(tail, "\\"):
			if len(tail) < 2 || tail[1] < '1' || tail[1] > '9' {
				return "", nil, false, fmt.Errorf(
					"FORX0002: backreference is not supported here")
			}
			// A backreference number is greedy: "\11" is group 11 when eleven
			// groups exist, which the caller checks, and group 1 followed by a
			// literal "1" otherwise. Reading the longest run and letting the
			// group-count check decide matches what the suite expects.
			j := 1
			for j < len(tail) && tail[j] >= '0' && tail[j] <= '9' {
				j++
			}
			n := 0
			for _, c := range tail[1:j] {
				n = n*10 + int(c-'0')
			}
			use := backrefUse{group: n}
			tail = tail[j:]
			if strings.HasPrefix(tail, "*") {
				use.star = true
				tail = tail[1:]
			} else if len(tail) > 0 && strings.ContainsRune("+?{", rune(tail[0])) {
				return "", nil, false, fmt.Errorf(
					"FORX0002: only \\N and \\N* are supported on a backreference")
			}
			refs = append(refs, use)
		case tail == "$":
			// An end anchor after the backreferences is honoured by requiring
			// the comparison to consume the rest of the input.
			tail = ""
			atEnd = true
		default:
			return "", nil, false, fmt.Errorf(
				"FORX0002: a backreference must be the last thing in the pattern")
		}
	}
	// The anchor moves onto the stripped pattern's tail check rather than into
	// RE2, so it is dropped from head only if it was there.
	return head, refs, atEnd, nil
}

func firstBackref(p string) int {
	for i := 0; i+1 < len(p); i++ {
		if p[i] != '\\' {
			continue
		}
		if p[i+1] >= '1' && p[i+1] <= '9' {
			return i
		}
		i++
	}
	return -1
}

// MatchString reports whether s matches the pattern, backreferences included.
//
// Every match of stripped spans exactly width characters, so each start
// position has one candidate window and one submatch assignment. The scan
// slides that window one character at a time, and each step runs stripped,
// anchored, over the window alone plus one comparison per backreference: linear
// in the input times the pattern, with no rescan of the remaining text.
func (b *backrefRegexp) MatchString(s string) bool {
	q, ok := advanceRunes(s, 0, b.width)
	for p := 0; ok; p, q, ok = b.next(s, p, q) {
		if _, _, matched := b.matchAt(s, p, q); matched {
			return true
		}
	}
	return false
}

// next slides the window [p, q) one character to the right. It stops after the
// first window when the pattern began with "^", and at the end of the input.
func (b *backrefRegexp) next(s string, p, q int) (int, int, bool) {
	if b.atStart || q >= len(s) {
		return 0, 0, false
	}
	return p + runeLenAt(s, p), q + runeLenAt(s, q), true
}

// matchAt tries the pattern at window [p, q): loc is stripped's submatch
// against s[p:q] and end is where the backreferences' text stops.
func (b *backrefRegexp) matchAt(s string, p, q int) ([]int, int, bool) {
	loc := b.stripped.FindStringSubmatchIndex(s[p:q])
	if loc == nil {
		return nil, 0, false
	}
	end, ok := b.tailEnd(s, q, loc, p)
	return loc, end, ok
}

// advanceRunes returns the offset n characters after i, and false when the
// input ends first.
func advanceRunes(s string, i, n int) (int, bool) {
	for ; n > 0; n-- {
		if i >= len(s) {
			return 0, false
		}
		i += runeLenAt(s, i)
	}
	return i, true
}

// tailMatches checks the captured groups against the text after the match.
func (b *backrefRegexp) tailMatches(s string, at int, loc []int, base int) bool {
	_, ok := b.tailEnd(s, at, loc, base)
	return ok
}

// tailEnd is tailMatches with the end of the consumed text.
//
// replace() needs the span the backreferences covered, not just whether they
// matched: the text they consumed is part of the match and must be replaced
// along with it. matches() discards the position, which is why the boolean
// wrapper above exists.
func (b *backrefRegexp) tailEnd(s string, at int, loc []int, base int) (int, bool) {
	pos := at
	for _, r := range b.refs {
		gi := 2 * r.group
		if gi+1 >= len(loc) || loc[gi] < 0 {
			// An unparticipating group matches the empty string, which is
			// what a backreference to it must match too.
			continue
		}
		text := s[base+loc[gi] : base+loc[gi+1]]
		if r.star {
			// Each copy is the same fixed width, so consuming as many as fit
			// is the unique maximal match.
			for text != "" && b.hasPrefix(s[pos:], text) {
				pos += len(text)
			}
			continue
		}
		if !b.hasPrefix(s[pos:], text) {
			return 0, false
		}
		pos += len(text)
		if r.literal != "" {
			if !b.hasPrefix(s[pos:], r.literal) {
				return 0, false
			}
			pos += len(r.literal)
		}
	}
	// A pattern that ended with "$" required the whole input; one that did not
	// is a containment test and may leave a tail.
	if b.atEnd {
		return pos, pos == len(s)
	}
	return pos, true
}

// NumSubexp is the group count of the stripped pattern, which is the same as
// the original's: stripping removes backreferences, never groups.
func (b *backrefRegexp) NumSubexp() int { return b.stripped.NumSubexp() }

// ReplaceAllString substitutes repl for every non-overlapping match.
//
// The match is the stripped pattern's span *plus* the text its backreferences
// consumed, because a backreference is part of the pattern and the text it
// matched is part of what replace() must remove. Scanning is left to right and
// non-overlapping, as fn:replace requires: after a replacement the search
// resumes at the end of the whole span, never inside it.
//
// repl is already in Go's "${n}" form — translateReplacement produced it — so
// expansion is delegated to RE2's own Expand against the stripped pattern's
// submatch indices. The backreference tail contributes no group of its own, so
// no index needs adjusting.
func (b *backrefRegexp) ReplaceAllString(s, repl string) string {
	var sb strings.Builder
	last := 0
	q, ok := advanceRunes(s, 0, b.width)
	for p := 0; ok; {
		loc, end, matched := b.matchAt(s, p, q)
		if !matched {
			p, q, ok = b.next(s, p, q)
			continue
		}
		sb.WriteString(s[last:p])
		// Expand indexes into the window, so it is given that slice and the
		// loc that belongs to it.
		sb.Write(b.stripped.ExpandString(nil, repl, s[p:q], loc))
		last = end
		if b.atStart {
			break
		}
		// A zero-width span would not advance; step one rune past it so the
		// scan terminates.
		if end == p {
			end += runeLenAt(s, p)
		}
		p = end
		q, ok = advanceRunes(s, p, b.width)
		ok = ok && p <= len(s)
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// runeLenAt is the width of the rune at i, and 1 past the end so a scan that
// reaches the end still advances rather than spinning.
func runeLenAt(s string, i int) int {
	if i >= len(s) {
		return 1
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	if size < 1 {
		return 1
	}
	return size
}

// compileArgBackref is compileArgRegexp for the backreference path.
//
// It returns nil, nil when the pattern has no backreference, so the caller
// falls through to RE2 unchanged — the ordinary path is not affected by this
// file existing.
func compileArgBackref(args []xdm.Sequence, pat, flags int, v Version) (*backrefRegexp, error) {
	p, err := argStringRequired(args, pat)
	if err != nil {
		return nil, err
	}
	if !hasBackref(p) {
		return nil, nil
	}
	f := ""
	if flags < len(args) {
		if f, err = argFlags(args, flags); err != nil {
			return nil, err
		}
	}
	return buildBackrefRegexp(p, f, v)
}

// buildBackrefRegexp is buildRegexp for a pattern containing backreferences.
//
// The flag handling has to be repeated rather than shared because the
// backreferences must come out *before* translatePattern sees the pattern —
// that function's job is to reject what RE2 cannot express, and a backreference
// is exactly that.
func buildBackrefRegexp(pattern, flags string, v Version) (*backrefRegexp, error) {
	original := pattern
	var goFlags []string
	dotAll := false
	for _, f := range flags {
		switch f {
		case 'i':
			goFlags = append(goFlags, "i")
		case 's':
			dotAll = true
		case 'm':
			goFlags = append(goFlags, "m")
		case 'x':
			pattern = stripPatternWhitespace(pattern)
		case 'q':
			// A literal pattern has no backreference left to resolve.
			return nil, nil
		default:
			return nil, fmt.Errorf(
				"FORX0001: unknown regular expression flag %q", string(f))
		}
	}

	head, refs, atEnd, err := splitBackrefs(pattern)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	translated, err := translatePattern(head, dotAll, v)
	if err != nil {
		return nil, err
	}
	if len(goFlags) > 0 {
		translated = "(?" + strings.Join(goFlags, "") + ")" + translated
	}
	// stripped runs anchored on one window at a time; see MatchString.
	re, err := CompileBounded("^(?:" + translated + ")")
	if err != nil {
		return nil, regexCompileError(original, err)
	}
	// There is deliberately no ceiling on the group count: 65 groups is valid
	// under both regex grammars. Admission is decided once, on the parsed
	// tree below, in time linear in the pattern.
	refd := make(map[int]bool, len(refs))
	for i := range refs {
		// A backreference number is greedy: "\11" is group 11 when eleven
		// groups exist, and group 1 followed by a literal "1" otherwise.
		if refs[i].group > re.NumSubexp() {
			g, lit := splitGreedyRef(refs[i].group, re.NumSubexp())
			if g < 1 {
				return nil, fmt.Errorf(
					"FORX0002: backreference \\%d names no group", refs[i].group)
			}
			refs[i].group = g
			refs[i].literal = lit
		}
		refd[refs[i].group] = true
	}
	// Every referenced group must have one width and one place, and so must
	// everything around it: "(['\"])(.*?)\\1" needs .*? to run on, and
	// "a*(a)\\1" against "aa" needs a* to give one "a" back, and RE2's one
	// assignment reaches neither. The whole head must have one width and
	// every referenced group one place in it, or the pattern goes to the
	// backtracker or is refused.
	tree, err := syntax.Parse(translated, syntax.Perl)
	if err != nil {
		return nil, regexCompileError(original, err)
	}
	atStart := startsWithBeginText(tree)
	for i, r := range refs {
		// A "\\N*" is greedy-exact only as the last thing: a later
		// backreference may need fewer copies. Unanchored with "$", every start
		// would re-run it to the end of the input, so that shape goes too.
		if r.star && (i != len(refs)-1 || r.literal != "" || (atEnd && !atStart)) {
			return nil, fmt.Errorf(
				"FORX0002: backreference \\%d* is not decided by the fixed-width "+
					"matcher in this position", r.group)
		}
	}
	if atStart {
		tree = dropLeadingBeginText(tree)
	}
	// Under "m" a trailing "$" also matches before a newline, which the
	// end-of-input check in tailEnd does not model.
	width, ok := fixedShape(tree, refd, false)
	if !ok || (atEnd && strings.ContainsRune(flags, 'm')) {
		return nil, fmt.Errorf(
			"FORX0002: backreference in a pattern whose groups can match in " +
				"more than one place, which RE2 cannot resolve")
	}
	return &backrefRegexp{
		stripped: re, refs: refs, width: width,
		atStart: atStart, atEnd: atEnd,
		fold: strings.ContainsRune(flags, 'i'),
	}, nil
}

// startsWithBeginText reports whether tree begins with "^" (not multi-line).
func startsWithBeginText(tree *syntax.Regexp) bool {
	if tree.Op == syntax.OpConcat && len(tree.Sub) > 0 {
		tree = tree.Sub[0]
	}
	return tree.Op == syntax.OpBeginText
}

// dropLeadingBeginText removes the "^" startsWithBeginText found.
func dropLeadingBeginText(tree *syntax.Regexp) *syntax.Regexp {
	if tree.Op == syntax.OpConcat {
		c := *tree
		c.Sub = c.Sub[1:]
		return &c
	}
	return &syntax.Regexp{Op: syntax.OpEmptyMatch}
}

// fixedShape returns the number of characters re always matches, and false
// when that number can vary, when re holds an anchor, or when a group in refd
// sits under an alternation or a repetition. Under those every match of re
// has the same length and each referenced group the same offset and the same
// participation, so RE2's one assignment is the only one. guarded is true below
// an alternation or a repetition.
func fixedShape(re *syntax.Regexp, refd map[int]bool, guarded bool) (int, bool) {
	switch re.Op {
	case syntax.OpEmptyMatch:
		return 0, true
	case syntax.OpLiteral:
		return len(re.Rune), true
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1, true
	case syntax.OpCapture:
		if guarded && refd[re.Cap] {
			return 0, false
		}
		return fixedShape(re.Sub[0], refd, guarded)
	case syntax.OpConcat:
		w := 0
		for _, sub := range re.Sub {
			sw, ok := fixedShape(sub, refd, guarded)
			if !ok {
				return 0, false
			}
			w += sw
		}
		return w, true
	case syntax.OpAlternate:
		w := -1
		for _, sub := range re.Sub {
			sw, ok := fixedShape(sub, refd, true)
			if !ok || (w >= 0 && sw != w) {
				return 0, false
			}
			w = sw
		}
		return w, w >= 0
	case syntax.OpRepeat:
		if re.Min != re.Max {
			return 0, false
		}
		sw, ok := fixedShape(re.Sub[0], refd, true)
		return sw * re.Min, ok
	}
	// Star, plus, quest, anchors and no-match.
	return 0, false
}

// splitGreedyRef re-reads "\NM" as group N followed by the literal digits M
// when there are fewer than NM groups.
func splitGreedyRef(n, groups int) (int, string) {
	s := fmt.Sprint(n)
	for cut := len(s) - 1; cut >= 1; cut-- {
		g := 0
		for _, c := range s[:cut] {
			g = g*10 + int(c-'0')
		}
		if g >= 1 && g <= groups {
			return g, s[cut:]
		}
	}
	return 0, ""
}

// hasPrefix is strings.HasPrefix, folding case when the "i" flag is in force.
//
// Folding is per-rune rather than over the whole string because the two may
// differ in byte length: the Kelvin sign folds to "k", which is one byte where
// the original is three, and comparing lowered copies would then report a
// prefix that consumes the wrong number of bytes.
func (b *backrefRegexp) hasPrefix(s, prefix string) bool {
	if !b.fold {
		return strings.HasPrefix(s, prefix)
	}
	for _, pr := range prefix {
		if s == "" {
			return false
		}
		sr, size := utf8.DecodeRuneInString(s)
		if !runeFoldEqual(sr, pr) {
			return false
		}
		s = s[size:]
	}
	return true
}

func runeFoldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	return unicode.SimpleFold(a) == b || unicode.SimpleFold(b) == a ||
		unicode.ToLower(a) == unicode.ToLower(b)
}
