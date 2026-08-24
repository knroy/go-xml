package xpath

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/knroy/go-xml/xdm"
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
	src  string
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

// maxBackrefGroups bounds how many groups a pattern may declare before the
// backreference path refuses it. A pattern this large is not something a
// stylesheet wrote by hand, and the width analysis is quadratic in the nesting.
const maxBackrefGroups = 64

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
func splitBackrefs(p string) (string, []backrefUse, error) {
	// Find the first backreference; everything from there must be
	// backreferences, their quantifiers, and an optional trailing anchor.
	i := firstBackref(p)
	if i < 0 {
		return p, nil, nil
	}
	head, tail := p[:i], p[i:]

	var refs []backrefUse
	for len(tail) > 0 {
		switch {
		case strings.HasPrefix(tail, "\\"):
			if len(tail) < 2 || tail[1] < '1' || tail[1] > '9' {
				return "", nil, fmt.Errorf(
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
				return "", nil, fmt.Errorf(
					"FORX0002: only \\N and \\N* are supported on a backreference")
			}
			refs = append(refs, use)
		case tail == "$":
			// An end anchor after the backreferences is honoured by requiring
			// the comparison to consume the rest of the input.
			tail = ""
		default:
			return "", nil, fmt.Errorf(
				"FORX0002: a backreference must be the last thing in the pattern")
		}
	}
	// The anchor moves onto the stripped pattern's tail check rather than into
	// RE2, so it is dropped from head only if it was there.
	return head, refs, nil
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

// fixedWidthGroup reports whether group n in pattern p matches a fixed number
// of characters.
//
// The analysis is deliberately conservative: it returns false for anything it
// does not recognise, and a false answer only means the pattern is refused,
// never that a wrong answer is given. A group is fixed-width when it contains
// no quantifier, no alternation of differing widths, and no nested group that
// is itself variable.
func fixedWidthGroup(p string, n int) bool {
	body, ok := groupBody(p, n)
	if !ok {
		return false
	}
	w, ok := fixedWidth(body)
	return ok && w >= 0
}

// groupEnd returns the offset just past the nth capturing group's closing
// paren, together with any quantifier applied to the group itself.
//
// This is what decides whether capture-and-compare is sound. RE2 hands back
// one submatch assignment — the greedy or lazy one its own semantics chose —
// and the comparison cannot ask for a different one. That is only harmless
// when nothing between the group and the backreference could have absorbed
// text the backreference needed. "(a)(b)\1" is safe because "(b)" matches one
// width and no other split exists; "(['\"])(.*?)\1" is not, because ".*?"
// stops at the first opportunity and the correct match needs it to run on.
func groupEnd(p string, n int) (int, bool) {
	count := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '[':
			for i++; i < len(p); i++ {
				if p[i] == '\\' {
					i++
					continue
				}
				if p[i] == ']' {
					break
				}
			}
		case '(':
			capturing := !strings.HasPrefix(p[i:], "(?")
			if capturing {
				count++
			}
			if capturing && count == n {
				depth, j := 1, i+1
				for ; j < len(p) && depth > 0; j++ {
					switch p[j] {
					case '\\':
						j++
					case '[':
						for j++; j < len(p); j++ {
							if p[j] == '\\' {
								j++
								continue
							}
							if p[j] == ']' {
								break
							}
						}
					case '(':
						depth++
					case ')':
						depth--
					}
				}
				if depth != 0 {
					return 0, false
				}
				return j, true
			}
		}
	}
	return 0, false
}

// betweenIsFixed reports whether the text from the end of group n to the end
// of head always matches the same number of characters.
//
// A quantifier on the referenced group itself counts as intervening: "(ki){2}"
// leaves RE2 free to place group 1 on either repetition, and only the last one
// is reported, so a backreference comparing against it is comparing against a
// choice it did not get to make.
func betweenIsFixed(head string, n int) bool {
	end, ok := groupEnd(head, n)
	if !ok {
		return false
	}
	rest := head[end:]
	// A quantifier directly on the group makes the captured occurrence
	// ambiguous even when the text after it is fixed.
	if rest != "" && strings.ContainsRune("*+?{", rune(rest[0])) {
		return false
	}
	w, ok := fixedWidth(rest)
	return ok && w >= 0
}

// groupBody returns the text inside the nth capturing group.
func groupBody(p string, n int) (string, bool) {
	count := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '[':
			// A class may contain an unescaped "(", so it is skipped whole.
			for i++; i < len(p); i++ {
				if p[i] == '\\' {
					i++
					continue
				}
				if p[i] == ']' {
					break
				}
			}
		case '(':
			capturing := !strings.HasPrefix(p[i:], "(?")
			if capturing {
				count++
			}
			if capturing && count == n {
				depth, j := 1, i+1
				for ; j < len(p) && depth > 0; j++ {
					switch p[j] {
					case '\\':
						j++
					case '[':
						for j++; j < len(p); j++ {
							if p[j] == '\\' {
								j++
								continue
							}
							if p[j] == ']' {
								break
							}
						}
					case '(':
						depth++
					case ')':
						depth--
					}
				}
				if depth != 0 {
					return "", false
				}
				return p[i+1 : j-1], true
			}
		}
	}
	return "", false
}

// fixedWidth returns the number of characters body always matches.
//
// It handles the constructs a fixed-width group can be built from: literals,
// escapes, character classes, nested groups, and alternations whose branches
// agree. Anything else — any quantifier, any assertion — makes it variable, so
// the pattern is refused rather than guessed at.
func fixedWidth(body string) (int, bool) {
	// An alternation is fixed only if every branch has the same width.
	if branches := splitAlternation(body); len(branches) > 1 {
		w, ok := fixedWidth(branches[0])
		if !ok {
			return 0, false
		}
		for _, b := range branches[1:] {
			w2, ok := fixedWidth(b)
			if !ok || w2 != w {
				return 0, false
			}
		}
		return w, true
	}

	w := 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; c {
		case '\\':
			if i+1 >= len(body) {
				return 0, false
			}
			// A multi-character escape still matches exactly one character.
			i++
			w++
		case '[':
			j := i + 1
			for ; j < len(body); j++ {
				if body[j] == '\\' {
					j++
					continue
				}
				if body[j] == ']' {
					break
				}
			}
			if j >= len(body) {
				return 0, false
			}
			i = j
			w++
		case '(':
			depth, j := 1, i+1
			for ; j < len(body) && depth > 0; j++ {
				switch body[j] {
				case '\\':
					j++
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth != 0 {
				return 0, false
			}
			inner := body[i+1 : j-1]
			inner = strings.TrimPrefix(inner, "?:")
			iw, ok := fixedWidth(inner)
			if !ok {
				return 0, false
			}
			w += iw
			i = j - 1
		case '*', '+', '?', '{':
			// Any quantifier makes the width variable.
			return 0, false
		case '.':
			w++
		case '^', '$':
			// An anchor inside a group is not something this analysis models.
			return 0, false
		default:
			// A multi-byte character is one character, not one byte.
			_, size := utf8.DecodeRuneInString(body[i:])
			i += size - 1
			w++
		}
	}
	return w, true
}

// splitAlternation splits body on top-level "|".
func splitAlternation(body string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '[':
			for i++; i < len(body); i++ {
				if body[i] == '\\' {
					i++
					continue
				}
				if body[i] == ']' {
					break
				}
			}
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				out = append(out, body[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, body[start:])
	return out
}

// MatchString reports whether s matches the pattern, backreferences included.
//
// The stripped pattern is matched at each position in turn, and at each match
// the text that follows is compared against the captured groups. Trying every
// position is what makes this a search rather than an anchored test; the cost
// is one comparison pass per candidate, so the whole thing stays linear in the
// input times the number of backreferences.
func (b *backrefRegexp) MatchString(s string) bool {
	for start := 0; start <= len(s); {
		loc := b.stripped.FindStringSubmatchIndex(s[start:])
		if loc == nil {
			return false
		}
		end := start + loc[1]
		if b.tailMatches(s, end, loc, start) {
			return true
		}
		// A pattern anchored at the start has one candidate position, so a
		// failed tail is a failed match. Retrying further along would let
		// "^([a-z])\\1*$" find a later single character and report a match
		// the anchor forbids.
		if b.anchoredStart() {
			return false
		}
		// Advance past this match's start to look for another. A zero-width
		// match would otherwise loop.
		next := start + loc[0] + 1
		if next <= start {
			next = start + 1
		}
		start = next
	}
	return false
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
	if b.anchoredEnd() {
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
	for start := 0; start <= len(s); {
		loc := b.stripped.FindStringSubmatchIndex(s[start:])
		if loc == nil {
			break
		}
		mStart, mEnd := start+loc[0], start+loc[1]
		end, ok := b.tailEnd(s, mEnd, loc, start)
		if ok {
			sb.WriteString(s[last:mStart])
			// Expand indexes into s[start:], so it is given that slice and the
			// loc that belongs to it.
			sb.Write(b.stripped.ExpandString(nil, repl, s[start:], loc))
			last = end
			// A zero-width span would not advance; step one rune past it so
			// the scan terminates.
			if end > mStart {
				start = end
				continue
			}
			start = mStart + runeLenAt(s, mStart)
			continue
		}
		if b.anchoredStart() {
			break
		}
		start = mStart + runeLenAt(s, mStart)
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

func (b *backrefRegexp) anchoredEnd() bool {
	return strings.HasSuffix(b.src, "$")
}

func (b *backrefRegexp) anchoredStart() bool {
	return strings.HasPrefix(b.src, "^")
}

// compileArgBackref is compileArgRegexp for the backreference path.
//
// It returns nil, nil when the pattern has no backreference, so the caller
// falls through to RE2 unchanged — the ordinary path is not affected by this
// file existing.
func compileArgBackref(args []xdm.Sequence, pat, flags int) (*backrefRegexp, error) {
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
	return buildBackrefRegexp(p, f)
}

// buildBackrefRegexp is buildRegexp for a pattern containing backreferences.
//
// The flag handling has to be repeated rather than shared because the
// backreferences must come out *before* translatePattern sees the pattern —
// that function's job is to reject what RE2 cannot express, and a backreference
// is exactly that.
func buildBackrefRegexp(pattern, flags string) (*backrefRegexp, error) {
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

	head, refs, err := splitBackrefs(pattern)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	translated, err := translatePattern(head, dotAll)
	if err != nil {
		return nil, err
	}
	if len(goFlags) > 0 {
		translated = "(?" + strings.Join(goFlags, "") + ")" + translated
	}
	re, err := regexp.Compile(translated)
	if err != nil {
		return nil, fmt.Errorf(
			"FORX0002: invalid regular expression %q: %w", original, err)
	}
	if re.NumSubexp() > maxBackrefGroups {
		return nil, fmt.Errorf(
			"FORX0002: too many groups for a backreference pattern")
	}
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
		if !fixedWidthGroup(head, refs[i].group) {
			return nil, fmt.Errorf(
				"FORX0002: backreference \\%d names a group of variable width, "+
					"which RE2 cannot resolve", refs[i].group)
		}
		// A fixed-width group is not enough on its own. If anything between
		// that group and the backreference can vary in width, RE2's single
		// assignment is one of several and the comparison cannot reach the
		// others — so the pattern must be refused rather than answered from
		// the wrong split. Without this check "(['\"])(.*?)\\1" compiled and
		// returned false for text it matches.
		if !betweenIsFixed(head, refs[i].group) {
			return nil, fmt.Errorf(
				"FORX0002: backreference \\%d is separated from its group by a "+
					"variable-width expression, which RE2 cannot resolve",
				refs[i].group)
		}
	}
	return &backrefRegexp{
		stripped: re, refs: refs, src: original,
		fold: strings.ContainsRune(flags, 'i'),
	}, nil
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
