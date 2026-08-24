package xpath

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// A backtracking matcher for the XPath 2.0 / XML Schema regular expression
// language, covering the backreferences RE2 cannot express at all.
//
// regex_backref.go handles the subset where a backreference can be decided by
// capture-and-compare, which is exact and runs in RE2's linear time. This file
// handles the rest — a lazy quantifier between a group and its reference, a
// quantified group, a reference in the middle of a pattern — and it does so the
// only way anyone knows how: by enumerating the choices, which is exponential
// in the worst case.
//
// That cost is why BacktrackingRegex is OFF by default, and why turning it on
// is a decision the embedder makes rather than one this package makes for them:
//
//   - RE2 guarantees a match runs in time linear in the input. A backtracker
//     guarantees nothing. "(a*)*\1" against a few dozen non-matching characters
//     is exponential, and that is not an exotic pattern — it is two stars and a
//     backreference.
//   - The patterns this engine evaluates do not all come from the stylesheet.
//     "matches($s, $node/@pattern)" compiles a pattern that came from document
//     data, so a server that enabled this by default would let a document it
//     was merely validating choose how long the validation takes. Catastrophic
//     backtracking is a denial of service with a one-line payload.
//
// So the default stays on the engine that cannot be made to hang, and this one
// exists for the caller who knows their patterns are trusted and needs the
// general case.
//
// Even enabled, the budget below is not optional. Every match attempt is
// counted, and exhausting the budget is an *error* — never a false. The
// principle from regex_backref.go carries over unchanged: an engine that
// answers correctly or says it cannot is safe; one that guesses is not safe at
// any setting. A budget that returned "no match" on exhaustion would be
// guessing, and would do it precisely on the inputs where the answer was
// hardest to get.

// backtrackingRegex enables the backtracking matcher for the backreference
// patterns RE2 and the fixed-width analysis cannot decide.
//
// It is false by default. See the file comment for why: this engine has no
// linear-time guarantee, and patterns can come from document data. Turn it on
// only when the patterns being evaluated are trusted.
//
// Changing it is safe at any time — the compiled-pattern cache is keyed on the
// setting, so a toggle does not serve a stale compilation — but it is a
// process-wide switch, not a per-evaluation one.
var backtrackingRegex atomic.Bool

// SetBacktrackingRegex turns the backtracking matcher on or off. See
// BacktrackingRegex.
//
// It is safe to call while other goroutines are evaluating patterns: the
// setting is read atomically, and the compiled-pattern cache is keyed on it, so
// neither a stale compilation nor a torn read is possible. What is not
// guaranteed is which setting a call already in flight observes.
func SetBacktrackingRegex(on bool) { backtrackingRegex.Store(on) }

// BacktrackingRegexEnabled reports the current setting.
func BacktrackingRegexEnabled() bool { return backtrackingRegex.Load() }

// backtrackBudget is the number of match steps a single MatchString,
// ReplaceAllString or FindAll call may take before it gives up with an error.
//
// A "step" is one attempt to match one node against one position, so the count
// tracks work done rather than input consumed — which is the quantity that
// blows up, and the only one worth bounding.
//
// The figure is measured rather than guessed, from the two ends of the range:
//
//   - regex-032, fifteen lazy groups and a \14 against 180 characters, is the
//     textbook exponential shape and the hardest pattern either conformance
//     suite contains. It answers in 525 steps — the laziness that makes the
//     pattern look exponential is what saves it, since each group stops at the
//     first space rather than exploring every split. Every other pattern in the
//     XSLT and QT3 suites is smaller still, so the honest workload sits five
//     orders of magnitude below the ceiling.
//   - "(a*)*\1b" against sixty "a"s is the pathological shape. It exhausts the
//     full budget in about 200ms on the machine this was measured on.
//
// So the ceiling is a fifth of a second of wasted work, not the heat death the
// same pattern reaches without one, and no pattern anyone means to write comes
// anywhere near it.
const backtrackBudget = 40_000_000

// errBacktrackBudget is returned when a match exhausts the budget.
//
// It is deliberately not a false: the pattern may well match, and reporting
// that it does not would be an answer this engine did not compute. FORX0002 is
// the code the rest of the regex layer uses for "this pattern cannot be
// evaluated here", and that is exactly what has happened.
var errBacktrackBudget = fmt.Errorf(
	"FORX0002: regular expression exceeded the backtracking step budget; " +
		"the pattern may be exponential in the length of the input")

// ---------------------------------------------------------------------------
// AST
// ---------------------------------------------------------------------------

// btNode is one node of the parsed pattern.
//
// The tree is deliberately small. Character classes are *not* modelled: a leaf
// that matches a single character keeps its original XSD source text and is
// compiled by RE2, so class semantics — subtraction, \p{IsGreek}, \i, \c, the
// Unicode-wide reading of \d and \w — are owned by translatePattern and
// applied in exactly one place. Duplicating them here would guarantee the two
// paths drifted apart, and a divergence between them is a silent wrong answer
// rather than a visible failure.
type btNode interface{ isBTNode() }

// btAlt is an alternation. Branches are tried left to right, which is the
// order the language requires.
type btAlt struct{ branches []btNode }

// btSeq is a concatenation.
type btSeq struct{ items []btNode }

// btRepeat is a quantified sub-expression. max < 0 means unbounded.
type btRepeat struct {
	node     btNode
	min, max int
	lazy     bool
}

// btGroup is a parenthesised sub-expression. index is 0 for a non-capturing
// group, otherwise its 1-based capture number.
type btGroup struct {
	index int
	node  btNode
}

// btChar is a leaf that matches exactly one character, compiled by RE2 from
// its original XSD source.
type btChar struct {
	re  *regexp.Regexp
	src string
}

// btLiteral is a run of ordinary characters, kept whole so the common case
// costs one comparison rather than one per character.
type btLiteral struct {
	text []rune
	fold bool
}

// btBackref is a \N reference. literal holds the digits that turned out not to
// be part of the number, under the same greedy rule splitBackrefs uses:
// "\12" is group 12 when twelve groups exist and group 1 followed by "2"
// otherwise.
type btBackref struct {
	group   int
	literal []rune
	fold    bool
}

// btAnchor is "^" or "$".
type btAnchor struct{ end bool }

// btEmpty matches the empty string. An empty alternation branch parses to it.
type btEmpty struct{}

func (*btAlt) isBTNode()     {}
func (*btSeq) isBTNode()     {}
func (*btRepeat) isBTNode()  {}
func (*btGroup) isBTNode()   {}
func (*btChar) isBTNode()    {}
func (*btLiteral) isBTNode() {}
func (*btBackref) isBTNode() {}
func (*btAnchor) isBTNode()  {}
func (*btEmpty) isBTNode()   {}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// btParser parses the XSD regular expression language into a btNode tree.
//
// It parses the *original* pattern rather than the RE2 translation, because
// the translation has already thrown away the structure this needs: "\1" does
// not survive translatePattern at all — that function rejects it — and a class
// rewritten into explicit ranges is harder to bound than the bracketed source
// it came from. Single-character leaves are handed back to translatePattern
// one at a time instead, which is what keeps the class semantics shared.
type btParser struct {
	src    string
	pos    int
	groups int
	// closed is the set of capturing groups whose ")" has already been read.
	// It is what a backreference may name, and the reason the resolution
	// happens during the parse rather than after it: Appendix F's backReference
	// is "'\\' [1-9][0-9]*" constrained to a group that *precedes* it, so
	// "\\1(abc)" and "(a\\1)" are malformed patterns, not patterns that match
	// nothing. Resolving afterwards, when every group is known, accepted both.
	closed map[int]bool
	dotAll bool
	fold   bool
	// multiline is the "m" flag, which decides whether ^ and $ match at line
	// boundaries as well as at the ends of the input.
	multiline bool
}

func (p *btParser) errf(format string, a ...any) error {
	return fmt.Errorf("FORX0002: "+format, a...)
}

// parseAlternation is the top level: branch ("|" branch)*.
func (p *btParser) parseAlternation() (btNode, error) {
	first, err := p.parseSequence()
	if err != nil {
		return nil, err
	}
	if p.pos >= len(p.src) || p.src[p.pos] != '|' {
		return first, nil
	}
	branches := []btNode{first}
	for p.pos < len(p.src) && p.src[p.pos] == '|' {
		p.pos++
		b, err := p.parseSequence()
		if err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return &btAlt{branches: branches}, nil
}

// parseSequence reads pieces until "|" or ")" or the end.
func (p *btParser) parseSequence() (btNode, error) {
	var items []btNode
	for p.pos < len(p.src) {
		if c := p.src[p.pos]; c == '|' || c == ')' {
			break
		}
		n, err := p.parsePiece()
		if err != nil {
			return nil, err
		}
		if n == nil {
			continue
		}
		items = append(items, n)
	}
	switch len(items) {
	case 0:
		return &btEmpty{}, nil
	case 1:
		return items[0], nil
	}
	return &btSeq{items: items}, nil
}

// parsePiece is one atom with its optional quantifier.
func (p *btParser) parsePiece() (btNode, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	min, max, lazy, ok, err := p.parseQuantifier()
	if err != nil {
		return nil, err
	}
	if !ok {
		return atom, nil
	}
	// A quantifier binds to the last *character* of a literal run, not to the
	// whole run: "abc*" is "ab" then "c*". The run is split rather than
	// re-lexed so the common unquantified case still coalesces.
	if lit, isLit := atom.(*btLiteral); isLit && len(lit.text) > 1 {
		last := &btLiteral{text: lit.text[len(lit.text)-1:], fold: lit.fold}
		head := &btLiteral{text: lit.text[:len(lit.text)-1], fold: lit.fold}
		return &btSeq{items: []btNode{
			head, &btRepeat{node: last, min: min, max: max, lazy: lazy},
		}}, nil
	}
	if _, isAnchor := atom.(*btAnchor); isAnchor {
		return nil, p.errf("a quantifier may not follow an anchor")
	}
	return &btRepeat{node: atom, min: min, max: max, lazy: lazy}, nil
}

// parseQuantifier reads "*", "+", "?" or "{n,m}", each optionally followed by
// "?" for the lazy form.
func (p *btParser) parseQuantifier() (min, max int, lazy, ok bool, err error) {
	if p.pos >= len(p.src) {
		return 0, 0, false, false, nil
	}
	switch p.src[p.pos] {
	case '*':
		min, max = 0, -1
	case '+':
		min, max = 1, -1
	case '?':
		min, max = 0, 1
	case '{':
		n, m, end, e := p.parseBraces(p.pos)
		if e != nil {
			return 0, 0, false, false, e
		}
		min, max = n, m
		p.pos = end - 1 // the shared advance below steps past "}"
	default:
		return 0, 0, false, false, nil
	}
	p.pos++
	if p.pos < len(p.src) && p.src[p.pos] == '?' {
		lazy = true
		p.pos++
	}
	return min, max, lazy, true, nil
}

// parseBraces reads "{n}", "{n,}" or "{n,m}" starting at the "{".
func (p *btParser) parseBraces(at int) (min, max, end int, err error) {
	close := strings.IndexByte(p.src[at:], '}')
	if close < 0 {
		return 0, 0, 0, p.errf("unterminated quantifier")
	}
	body := p.src[at+1 : at+close]
	end = at + close + 1
	lo, hi, found := strings.Cut(body, ",")
	min, err = parseCount(lo)
	if err != nil {
		return 0, 0, 0, p.errf("invalid quantifier {%s}", body)
	}
	if !found {
		return min, min, end, nil
	}
	if hi == "" {
		return min, -1, end, nil
	}
	max, err = parseCount(hi)
	if err != nil || max < min {
		return 0, 0, 0, p.errf("invalid quantifier {%s}", body)
	}
	return min, max, end, nil
}

func parseCount(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
		if n > maxBacktrackRepeat {
			// A count this large cannot be satisfied by any input a
			// backtracker will reach, and letting it through would let the
			// bound itself allocate. It is clamped rather than refused, which
			// is what the RE2 path does with an oversized count too.
			n = maxBacktrackRepeat
		}
	}
	return n, nil
}

// maxBacktrackRepeat clamps an explicit repeat count. Nothing an input of any
// realistic length can match needs more, and the budget would stop a genuine
// attempt long before this anyway.
const maxBacktrackRepeat = 1 << 20

// parseAtom reads one atom: a group, a class, an escape, a dot, an anchor, or
// a run of literal characters.
func (p *btParser) parseAtom() (btNode, error) {
	c := p.src[p.pos]
	switch c {
	case '(':
		return p.parseGroup()
	case '[':
		return p.parseClass()
	case '.':
		p.pos++
		return p.compileCharAtom(".")
	case '^':
		p.pos++
		return &btAnchor{end: false}, nil
	case '$':
		p.pos++
		return &btAnchor{end: true}, nil
	case '\\':
		return p.parseEscape()
	case '*', '+', '?':
		return nil, p.errf("quantifier %q has nothing to repeat", string(c))
	case '{':
		// A brace that does not open a valid quantifier is a literal in some
		// dialects; XML Schema's grammar makes "{" a metacharacter that must
		// be escaped, and checkRegexGrammar has already had its say, so
		// reaching here means a quantifier with nothing before it.
		return nil, p.errf("quantifier has nothing to repeat")
	}
	return p.parseLiteralRun()
}

// parseLiteralRun coalesces ordinary characters into one node, stopping one
// character short if a quantifier follows so the quantifier binds correctly.
func (p *btParser) parseLiteralRun() (btNode, error) {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if strings.IndexByte(`()[].^$\|*+?{`, c) >= 0 {
			break
		}
		_, size := utf8.DecodeRuneInString(p.src[p.pos:])
		p.pos += size
	}
	if p.pos == start {
		return nil, p.errf("unexpected character %q", string(p.src[start]))
	}
	return &btLiteral{text: []rune(p.src[start:p.pos]), fold: p.fold}, nil
}

// parseGroup reads "(" ... ")", capturing unless it begins "(?:".
func (p *btParser) parseGroup() (btNode, error) {
	p.pos++ // "("
	index := 0
	if strings.HasPrefix(p.src[p.pos:], "?:") {
		p.pos += 2
	} else if p.pos < len(p.src) && p.src[p.pos] == '?' {
		// XML Schema has no other "(?" construct. checkRegexGrammar refuses
		// lookaround and named groups already; this is the belt to its braces.
		return nil, p.errf("group modifier \"(?\" is not in the language")
	} else {
		p.groups++
		index = p.groups
		if p.groups > maxBacktrackGroups {
			return nil, p.errf("too many capturing groups")
		}
	}
	inner, err := p.parseAlternation()
	if err != nil {
		return nil, err
	}
	if p.pos >= len(p.src) || p.src[p.pos] != ')' {
		return nil, p.errf("unbalanced parenthesis")
	}
	p.pos++
	if index > 0 {
		if p.closed == nil {
			p.closed = map[int]bool{}
		}
		p.closed[index] = true
	}
	return &btGroup{index: index, node: inner}, nil
}

// maxBacktrackGroups bounds the capture count. The capture vector is copied on
// every group entry, so the cost of a pattern is linear in this.
const maxBacktrackGroups = 128

// parseClass reads a bracketed character class whole, including a subtraction's
// nested class, and hands the source text to RE2 for compilation.
func (p *btParser) parseClass() (btNode, error) {
	start := p.pos
	p.pos++ // "["
	if p.pos < len(p.src) && p.src[p.pos] == '^' {
		p.pos++
	}
	// A "]" in first position is a literal in some dialects but not in XML
	// Schema's grammar, so the scan does not special-case it.
	depth := 1
	for p.pos < len(p.src) && depth > 0 {
		switch p.src[p.pos] {
		case '\\':
			p.pos++
		case '[':
			// Only a subtraction can nest, and it has already been validated.
			depth++
		case ']':
			depth--
			if depth == 0 {
				p.pos++
				return p.compileCharAtom(p.src[start:p.pos])
			}
		}
		p.pos++
	}
	return nil, p.errf("unterminated character class")
}

// parseEscape reads a backslash escape: a backreference, or anything else,
// which is a single-character matcher RE2 can compile.
func (p *btParser) parseEscape() (btNode, error) {
	if p.pos+1 >= len(p.src) {
		return nil, p.errf("trailing backslash")
	}
	esc := p.src[p.pos+1]
	if esc >= '1' && esc <= '9' {
		return p.parseBackref()
	}
	start := p.pos
	p.pos += 2
	// \p and \P take a braced property name, which is part of the atom.
	if esc == 'p' || esc == 'P' {
		if p.pos < len(p.src) && p.src[p.pos] == '{' {
			end := strings.IndexByte(p.src[p.pos:], '}')
			if end < 0 {
				return nil, p.errf("unterminated property name")
			}
			p.pos += end + 1
		}
	}
	return p.compileCharAtom(p.src[start:p.pos])
}

// parseBackref reads "\N", taking the longest run of digits. Whether all of
// them belong to the number is not knowable until the group count is final, so
// the resolution is deferred to resolveBacktrackRefs.
func (p *btParser) parseBackref() (btNode, error) {
	p.pos++ // "\"
	j := p.pos
	for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
		j++
	}
	n := 0
	for _, c := range p.src[p.pos:j] {
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return nil, p.errf("backreference number is too large")
		}
	}
	p.pos = j
	if n < 1 {
		return nil, p.errf("backreference \\0 names no group")
	}
	// The number is resolved here, against the groups closed so far, for two
	// reasons that are really one. A reference to a group that is not yet
	// complete is malformed and must be FORX0002 — that is 0820 through 0938 in
	// the suite, and deferring the check until the whole pattern was parsed
	// accepted every one of them. And the greedy rule that decides whether
	// "\\12" is group 12 or group 1 followed by "2" has to be applied against
	// the same count, or the two answers disagree about which groups exist.
	if p.closed[n] {
		return &btBackref{group: n, fold: p.fold}, nil
	}
	// A group that has been opened but not yet closed cannot be renumbered by
	// the greedy split: erratum FO.E24 makes "\\10" inside the tenth group a
	// malformed pattern, not "\\1" followed by "0". Splitting is only right
	// when the full number names no group at all.
	if n <= p.groups {
		return nil, p.errf(
			"backreference \\%d names a group that is not yet closed", n)
	}
	g, lit := splitGreedyRefClosed(n, p.closed)
	if g < 1 {
		return nil, p.errf(
			"backreference \\%d names no group that precedes it", n)
	}
	return &btBackref{group: g, literal: []rune(lit), fold: p.fold}, nil
}

// splitGreedyRefClosed is splitGreedyRef against a set of groups rather than a
// count, because "already closed" is not a prefix of "exists": in
// "(a)(\2b)(c)" group 2 is open where the reference stands and group 1 is not,
// so a count would have to say "1" and lose the distinction.
func splitGreedyRefClosed(n int, closed map[int]bool) (int, string) {
	s := fmt.Sprint(n)
	for cut := len(s) - 1; cut >= 1; cut-- {
		g := 0
		for _, c := range s[:cut] {
			g = g*10 + int(c-'0')
		}
		if closed[g] {
			return g, s[cut:]
		}
	}
	return 0, ""
}

// compileCharAtom translates one single-character construct through the
// ordinary XSD-to-RE2 path and compiles it anchored, so matching it is
// "does RE2 accept exactly this one character".
//
// Going through translatePattern is the whole point: every class rule the rest
// of the package implements — subtraction, the Unicode-wide \d and \w, \i \c
// \I \C, the Appendix G block names, the case-pinning of \p{Lu} — applies here
// without being written twice.
func (p *btParser) compileCharAtom(src string) (btNode, error) {
	key := charAtomKey{src: src, dotAll: p.dotAll, fold: p.fold}
	if v, ok := charAtomCache.Load(key); ok {
		switch t := v.(type) {
		case *regexp.Regexp:
			return &btChar{re: t, src: src}, nil
		case error:
			return nil, t
		}
	}
	re, err := buildCharAtom(src, p.dotAll, p.fold)
	if err != nil {
		charAtomCache.Store(key, err)
		return nil, err
	}
	charAtomCache.Store(key, re)
	return &btChar{re: re, src: src}, nil
}

func buildCharAtom(src string, dotAll, fold bool) (*regexp.Regexp, error) {
	translated, err := translatePattern(src, dotAll)
	if err != nil {
		return nil, err
	}
	prefix := "(?s)"
	if fold {
		prefix = "(?is)"
	}
	// "(?s)" is safe to add unconditionally: "." never reaches RE2 as a dot —
	// translatePattern expands it to an explicit class — so the flag can only
	// affect a dot the pattern escaped, which is a literal either way. What it
	// does buy is that "\n" is matched by a negated class rather than falling
	// foul of RE2's line handling.
	re, err := regexp.Compile(prefix + `\A(?:` + translated + `)\z`)
	if err != nil {
		return nil, fmt.Errorf("FORX0002: invalid regular expression %q: %w", src, err)
	}
	return re, nil
}

type charAtomKey struct {
	src          string
	dotAll, fold bool
}

// charAtomCache memoises the single-character leaves. A class like
// "[\p{IsBasicLatin}-[aeiou]]" expands to hundreds of ranges, and a pattern
// that uses it inside a loop would otherwise pay for that expansion on every
// compilation.
var charAtomCache sync.Map

// ---------------------------------------------------------------------------
// Matcher
// ---------------------------------------------------------------------------

// btMachine is one match attempt over one input.
//
// The engine is continuation-passing: match(node, pos, k) tries every way node
// can consume input from pos and calls k with each resulting position, in the
// order the language requires alternatives to be tried. The continuation is
// what makes a backreference work at all — it is the only structure in which
// "the rest of the pattern" is available to be retried after a different
// earlier choice — and it is also what makes the engine exponential, since a
// failing continuation is re-entered once per choice above it.
//
// caps is the capture vector, two rune offsets per group, -1 for a group that
// has not participated. It is mutated in place and restored on backtracking
// rather than copied, because copying it at every group entry is the
// difference between a pattern that finishes and one that does not.
type btMachine struct {
	in     []rune
	caps   []int
	steps  int
	budget int
	// multiline is the "m" flag: ^ and $ also match at line boundaries.
	multiline bool
	// overrun records that the budget ran out, so the caller can tell a
	// genuine "no match" from a match that was never finished.
	overrun bool
}

// step charges one unit against the budget and reports whether there is any
// left. Every entry to match() charges, so the count measures the work the
// engine did rather than the input it consumed — which is the quantity that
// blows up.
func (m *btMachine) step() bool {
	m.steps++
	if m.steps > m.budget {
		m.overrun = true
		return false
	}
	return true
}

// btCont is a continuation: given the position after a node matched, decide
// whether the rest of the pattern can match from there.
type btCont func(pos int) bool

func (m *btMachine) match(n btNode, pos int, k btCont) bool {
	if m.overrun || !m.step() {
		return false
	}
	switch t := n.(type) {
	case *btEmpty:
		return k(pos)

	case *btAnchor:
		if t.end {
			if pos == len(m.in) || (m.multiline && m.in[pos] == '\n') {
				return k(pos)
			}
			return false
		}
		if pos == 0 || (m.multiline && m.in[pos-1] == '\n') {
			return k(pos)
		}
		return false

	case *btLiteral:
		if pos+len(t.text) > len(m.in) {
			return false
		}
		for i, r := range t.text {
			if !runeEq(m.in[pos+i], r, t.fold) {
				return false
			}
		}
		return k(pos + len(t.text))

	case *btChar:
		if pos >= len(m.in) {
			return false
		}
		if !t.re.MatchString(string(m.in[pos])) {
			return false
		}
		return k(pos + 1)

	case *btSeq:
		return m.matchSeq(t.items, pos, k)

	case *btAlt:
		for _, b := range t.branches {
			if m.match(b, pos, k) {
				return true
			}
			if m.overrun {
				return false
			}
		}
		return false

	case *btGroup:
		if t.index == 0 {
			return m.match(t.node, pos, k)
		}
		gs, ge := 2*t.index, 2*t.index+1
		oldS, oldE := m.caps[gs], m.caps[ge]
		ok := m.match(t.node, pos, func(end int) bool {
			// The capture is set before the continuation runs, so a
			// backreference later in the pattern sees this attempt's value,
			// and restored if the continuation fails, so the next attempt does
			// not inherit it.
			prevS, prevE := m.caps[gs], m.caps[ge]
			m.caps[gs], m.caps[ge] = pos, end
			if k(end) {
				return true
			}
			m.caps[gs], m.caps[ge] = prevS, prevE
			return false
		})
		if !ok {
			m.caps[gs], m.caps[ge] = oldS, oldE
		}
		return ok

	case *btBackref:
		gs, ge := 2*t.group, 2*t.group+1
		s, e := m.caps[gs], m.caps[ge]
		if s < 0 {
			// A group that has not participated captures nothing, and a
			// reference to it matches the empty string. That is what makes
			// "matches('', '()\\1')" true.
			s, e = 0, 0
		}
		n := e - s
		if pos+n > len(m.in) {
			return false
		}
		for i := 0; i < n; i++ {
			if !runeEq(m.in[pos+i], m.in[s+i], t.fold) {
				return false
			}
		}
		pos += n
		// The digits that were not part of the number are ordinary literal
		// text following the reference.
		if len(t.literal) > 0 {
			if pos+len(t.literal) > len(m.in) {
				return false
			}
			for i, r := range t.literal {
				if !runeEq(m.in[pos+i], r, t.fold) {
					return false
				}
			}
			pos += len(t.literal)
		}
		return k(pos)

	case *btRepeat:
		return m.matchRepeat(t, pos, 0, k)
	}
	return false
}

func (m *btMachine) matchSeq(items []btNode, pos int, k btCont) bool {
	if len(items) == 0 {
		return k(pos)
	}
	return m.match(items[0], pos, func(next int) bool {
		return m.matchSeq(items[1:], next, k)
	})
}

// matchRepeat tries the repetitions of t, having already made count of them.
//
// The one subtlety is the zero-width guard. "(a*)*" can repeat its body
// matching nothing, forever; the guard is that a repetition which consumed no
// input may not be followed by another, which is the standard reading and the
// one that makes "(){0,}" terminate. It is checked by comparing positions
// rather than by a visited set, which costs nothing and is exact for this
// purpose: a body that consumed nothing leaves the position unchanged, and a
// second identical iteration could only do the same.
func (m *btMachine) matchRepeat(t *btRepeat, pos, count int, k btCont) bool {
	if m.overrun || !m.step() {
		return false
	}
	canStop := count >= t.min
	canMore := t.max < 0 || count < t.max

	more := func() bool {
		if !canMore {
			return false
		}
		return m.match(t.node, pos, func(next int) bool {
			if next == pos {
				// A zero-width iteration. It is allowed only while it is still
				// needed to reach the minimum; past that it can only spin.
				if count+1 < t.min {
					return m.matchRepeat(t, next, count+1, k)
				}
				return false
			}
			return m.matchRepeat(t, next, count+1, k)
		})
	}

	if t.lazy {
		// Lazy: prefer stopping, then try one more. This is the ordering
		// regex-019 needs — "(.*?)" must stop at the first quote the
		// backreference can match, not run to the last.
		if canStop && k(pos) {
			return true
		}
		if m.overrun {
			return false
		}
		return more()
	}
	if more() {
		return true
	}
	if m.overrun {
		return false
	}
	return canStop && k(pos)
}

func runeEq(a, b rune, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	return runeFoldEqual(a, b)
}

// ---------------------------------------------------------------------------
// The compiled pattern
// ---------------------------------------------------------------------------

// btRegexp is a compiled pattern matched by backtracking.
//
// It carries the same shape of API as *regexp.Regexp for the operations the
// regex layer needs, so callers can hold either. What it cannot do is report
// the budget error through those signatures — MatchString returns a bool and
// has nowhere to put one — so the error is stashed and the callers that can
// report it (fn:matches, fn:replace, xsl:analyze-string) read it back with
// Err() after the call. Answering false on exhaustion without that check would
// be the guess this package refuses to make, which is why every call site is
// obliged to look.
type btRegexp struct {
	root   btNode
	groups int
	// anchoredEnd is not modelled specially — "$" is a node like any other —
	// but the source is kept for error messages.
	src       string
	multiline bool

	// err records a budget exhaustion from the most recent operation. It is
	// per-pattern rather than per-call, so a btRegexp must not be shared
	// across goroutines; the cache hands out one per (pattern, flags, mode),
	// so this is enforced by cloning on lookup rather than by locking.
	err error
}

// NumSubexp is the number of capturing groups, matching regexp.Regexp's method
// of the same name.
func (b *btRegexp) NumSubexp() int { return b.groups }

// Err returns the budget error from the most recent operation, or nil.
func (b *btRegexp) Err() error { return b.err }

// newMachine builds a fresh machine with a fresh budget.
func (b *btRegexp) newMachine(s []rune) *btMachine {
	return &btMachine{
		in:        s,
		caps:      newCaps(b.groups),
		budget:    backtrackBudget,
		multiline: b.multiline,
	}
}

func newCaps(groups int) []int {
	caps := make([]int, 2*(groups+1))
	for i := range caps {
		caps[i] = -1
	}
	return caps
}

// findFrom finds the leftmost match starting at or after from, returning the
// capture vector in rune offsets, or nil.
//
// Leftmost-first is the language's rule and the one RE2's own FindString
// follows: the earliest starting position wins, and among matches at that
// position the one the alternation and quantifier orderings pick. The engine
// therefore tries start positions in order and takes the first that succeeds,
// rather than looking for the longest.
func (b *btRegexp) findFrom(m *btMachine, from int) []int {
	for start := from; start <= len(m.in); start++ {
		for i := range m.caps {
			m.caps[i] = -1
		}
		end := -1
		if m.match(b.root, start, func(pos int) bool { end = pos; return true }) {
			out := make([]int, len(m.caps))
			copy(out, m.caps)
			out[0], out[1] = start, end
			return out
		}
		if m.overrun {
			return nil
		}
	}
	return nil
}

// MatchString reports whether s contains a match.
//
// On budget exhaustion it returns false *and* sets Err(); the caller must check
// Err() rather than trusting the bool.
func (b *btRegexp) MatchString(s string) bool {
	b.err = nil
	in := []rune(s)
	m := b.newMachine(in)
	loc := b.findFrom(m, 0)
	if m.overrun {
		b.err = errBacktrackBudget
		return false
	}
	return loc != nil
}

// FindAllStringSubmatchIndex is regexp.Regexp's method of the same name, in
// *byte* offsets, which is what xsl:analyze-string and the rest of the package
// index with. The engine works in runes, so the offsets are mapped back on the
// way out.
func (b *btRegexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	b.err = nil
	in := []rune(s)
	byteAt := runeToByteOffsets(s, in)
	m := b.newMachine(in)

	var out [][]int
	pos := 0
	for pos <= len(in) {
		if n >= 0 && len(out) >= n {
			break
		}
		loc := b.findFrom(m, pos)
		if m.overrun {
			b.err = errBacktrackBudget
			return nil
		}
		if loc == nil {
			break
		}
		out = append(out, mapOffsets(loc, byteAt))
		if loc[1] > loc[0] {
			pos = loc[1]
			continue
		}
		// A zero-width match does not advance on its own; step one rune past
		// it so the scan terminates, as RE2's own FindAll does.
		pos = loc[1] + 1
	}
	return out
}

// ReplaceAllString substitutes repl for every non-overlapping match. repl is
// already in Go's "${n}" form, which translateReplacement produced.
func (b *btRegexp) ReplaceAllString(s, repl string) string {
	locs := b.FindAllStringSubmatchIndex(s, -1)
	if b.err != nil {
		return ""
	}
	var sb strings.Builder
	last := 0
	for _, loc := range locs {
		if loc[0] < last {
			// Overlap cannot happen with the scan above, but a defensive skip
			// costs nothing and keeps the output well-formed if it ever did.
			continue
		}
		sb.WriteString(s[last:loc[0]])
		sb.Write(expandBacktrack(nil, repl, s, loc))
		last = loc[1]
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// Split is regexp.Regexp's method of the same name, which fn:tokenize needs.
func (b *btRegexp) Split(s string, n int) []string {
	locs := b.FindAllStringSubmatchIndex(s, -1)
	if b.err != nil {
		return nil
	}
	out := make([]string, 0, len(locs)+1)
	last := 0
	for _, loc := range locs {
		if loc[0] < last {
			continue
		}
		out = append(out, s[last:loc[0]])
		last = loc[1]
	}
	return append(out, s[last:])
}

// expandBacktrack is regexp.Regexp.ExpandString for a capture vector this
// engine produced.
//
// RE2's own Expand cannot be borrowed: it is a method on the *regexp.Regexp
// whose groups the vector describes, and there is no such object here. The
// replacement has already been normalised by translateReplacement into the
// "${n}" form with "$$" for a literal dollar, so what is left to do is small
// and exactly specified.
func expandBacktrack(dst []byte, repl, src string, loc []int) []byte {
	for i := 0; i < len(repl); i++ {
		if repl[i] != '$' {
			dst = append(dst, repl[i])
			continue
		}
		if i+1 < len(repl) && repl[i+1] == '$' {
			dst = append(dst, '$')
			i++
			continue
		}
		if i+1 >= len(repl) || repl[i+1] != '{' {
			// translateReplacement never emits a bare "$", so this is a
			// literal one that arrived some other way.
			dst = append(dst, '$')
			continue
		}
		close := strings.IndexByte(repl[i+2:], '}')
		if close < 0 {
			dst = append(dst, '$')
			continue
		}
		name := repl[i+2 : i+2+close]
		i += 2 + close
		g, ok := atoiGroup(name)
		if !ok {
			continue
		}
		if 2*g+1 < len(loc) && loc[2*g] >= 0 {
			dst = append(dst, src[loc[2*g]:loc[2*g+1]]...)
		}
	}
	return dst
}

func atoiGroup(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0, false
		}
	}
	return n, true
}

// runeToByteOffsets maps each rune index to the byte offset it starts at, with
// one extra entry for the end.
func runeToByteOffsets(s string, in []rune) []int {
	out := make([]int, len(in)+1)
	b := 0
	for i, r := range in {
		out[i] = b
		b += utf8.RuneLen(r)
	}
	out[len(in)] = len(s)
	return out
}

func mapOffsets(loc, byteAt []int) []int {
	out := make([]int, len(loc))
	for i, v := range loc {
		if v < 0 {
			out[i] = -1
			continue
		}
		out[i] = byteAt[v]
	}
	return out
}

// ---------------------------------------------------------------------------
// Compilation
// ---------------------------------------------------------------------------

// compileBacktrack parses and prepares a pattern for the backtracking engine.
//
// It is only ever reached when the switch is on *and* the fixed-width path has
// already declined the pattern, so it never displaces an exact answer with a
// slower one.
func compileBacktrack(pattern, flags string) (*btRegexp, error) {
	original := pattern
	p := &btParser{}
	for _, f := range flags {
		switch f {
		case 'i':
			p.fold = true
		case 's':
			p.dotAll = true
		case 'm':
			p.multiline = true
		case 'x':
			pattern = stripPatternWhitespace(pattern)
		case 'q':
			// A literal pattern has no backreference to resolve, so it is not
			// this engine's business. The caller falls back.
			return nil, nil
		default:
			return nil, fmt.Errorf(
				"FORX0001: unknown regular expression flag %q", string(f))
		}
	}
	// The grammar check is the same one the RE2 path runs, and it runs first
	// for the same reason: a construct the language does not define must be
	// refused rather than translated, and this engine is if anything more
	// willing to accept Perl syntax than RE2 is.
	if err := checkRegexGrammar(pattern); err != nil {
		return nil, err
	}
	p.src = pattern
	root, err := p.parseAlternation()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.src) {
		return nil, fmt.Errorf(
			"FORX0002: invalid regular expression %q: unbalanced parenthesis", original)
	}
	return &btRegexp{
		root: root, groups: p.groups, src: original, multiline: p.multiline,
	}, nil
}

// clone returns a copy safe for one goroutine to use.
//
// Only the err field is mutable, so the tree is shared and the header copied.
// The cache stores one btRegexp per key and hands out clones, which is what
// lets Err() be a field rather than a return value without making concurrent
// use of the same cached pattern a data race.
func (b *btRegexp) clone() *btRegexp {
	c := *b
	c.err = nil
	return &c
}
