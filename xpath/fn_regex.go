package xpath

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/knroy/go-xml/xdm"
)

// registerRegexFuncs adds fn:matches, fn:replace and fn:tokenize.
//
// Schematron rule sets lean on these heavily — every format constraint on an
// identifier is a fn:matches — so their fidelity matters more than their
// breadth.
func registerRegexFuncs(l *Library) {
	l.registerFn("matches", []int{2, 3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		flags, err := argFlags(args, 2)
		if err != nil {
			return nil, err
		}
		re, err := compileArgRegexp(args, 1, 2)
		if err != nil {
			return nil, err
		}
		return boolSeq(re.MatchString(matchInput(s, flags))), nil
	})

	l.registerFn("replace", []int{3, 4}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		re, err := compileArgRegexp(args, 1, 3)
		if err != nil {
			return nil, err
		}
		// The replacement is declared xs:string, not xs:string?, so an empty
		// sequence is a type error rather than an empty replacement.
		repl, err := argStringRequired(args, 2)
		if err != nil {
			return nil, err
		}
		// A pattern that matches the empty string would loop forever in some
		// engines and produce surprising output here; the spec makes it an
		// error outright.
		if re.MatchString("") {
			return nil, fmt.Errorf("FORX0003: pattern matches the empty string")
		}
		goRepl, err := translateReplacement(repl, re.NumSubexp())
		if err != nil {
			return nil, err
		}
		return strSeq(re.ReplaceAllString(s, goRepl)), nil
	})

	l.registerFn("tokenize", []int{2, 3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		re, err := compileArgRegexp(args, 1, 2)
		if err != nil {
			return nil, err
		}
		if re.MatchString("") {
			return nil, fmt.Errorf("FORX0003: pattern matches the empty string")
		}
		if s == "" {
			// Tokenizing the empty string gives the empty sequence, not one
			// empty token.
			return xdm.Empty, nil
		}
		parts := re.Split(s, -1)
		out := make(xdm.Sequence, 0, len(parts))
		for _, p := range parts {
			out = append(out, xdm.NewString(p))
		}
		return out, nil
	})
}

// compileArgRegexp compiles the pattern at index pat with optional flags at
// index flags.
func compileArgRegexp(args []xdm.Sequence, pat, flags int) (*regexp.Regexp, error) {
	p, err := argStringRequired(args, pat)
	if err != nil {
		return nil, err
	}
	f := ""
	if flags < len(args) {
		if f, err = argFlags(args, flags); err != nil {
			return nil, err
		}
	}
	return compileXPathRegexp(p, f)
}

// regexCache memoises compiled patterns.
//
// Schematron applies the same handful of patterns to every node of a large
// document, and regexp.Compile is expensive enough that recompiling per call
// dominates the transform.
//
// The cache is bounded. An earlier version was not, on the reasoning that
// patterns come from the stylesheet and so form a fixed set — which is wrong:
// "matches($s, $node/@pattern)" compiles a pattern that came from document
// data, and a long-running process validating many documents would retain one
// compiled regexp per distinct pattern it had ever seen. Measured at 17.6 MB
// retained after 20,000 distinct patterns, growing without limit.
//
// When the cache is full it is cleared rather than evicted one entry at a
// time. A true LRU would need a linked list and a lock on every *read*, which
// costs more than it saves for the access pattern here: the working set is a
// handful of stylesheet patterns that are re-cached immediately after a clear.
const regexCacheMax = 1024

var regexCache sync.Map // key: flags + "\x00" + pattern -> *regexp.Regexp or error
var regexCacheSize atomic.Int64

// compileXPathRegexp translates an XML Schema / XPath 2.0 regular expression
// into Go's syntax and compiles it.
//
// The two flavours are close but not identical. RE2 lacks backreferences and
// lookaround, which XPath 2.0 also lacks, so the gap is smaller than it looks;
// what does differ is that XPath's "." excludes newlines by default in the
// opposite way, and that XPath adds \i, \c, \I, \C for XML name characters and
// supports character-class subtraction. Those are translated here; a pattern
// using a construct with no RE2 equivalent is rejected rather than silently
// mis-compiled.
func compileXPathRegexp(pattern, flags string) (*regexp.Regexp, error) {
	key := flags + "\x00" + pattern
	if v, ok := regexCache.Load(key); ok {
		switch t := v.(type) {
		case *regexp.Regexp:
			return t, nil
		case error:
			return nil, t
		}
	}

	re, err := buildRegexp(pattern, flags)
	if err != nil {
		storeRegex(key, err)
		return nil, err
	}
	storeRegex(key, re)
	return re, nil
}

// storeRegex adds an entry, clearing the cache first if it is full.
func storeRegex(key string, v any) {
	if regexCacheSize.Load() >= regexCacheMax {
		// Two goroutines can both decide to clear; that is harmless, since a
		// cleared cache is only a performance loss and every entry is
		// reproducible from its key.
		regexCache.Range(func(k, _ any) bool {
			regexCache.Delete(k)
			return true
		})
		regexCacheSize.Store(0)
	}
	if _, loaded := regexCache.LoadOrStore(key, v); !loaded {
		regexCacheSize.Add(1)
	}
}

// argFlags reads the flags argument of a regex function.
//
// The parameter is declared xs:string rather than xs:string?, so an explicitly
// supplied empty sequence is a type error rather than "no flags" —
// matches("input", "pattern", ()) is XPTY0004. Omitting the argument
// altogether is still fine.
func argFlags(args []xdm.Sequence, i int) (string, error) {
	if i >= len(args) {
		return "", nil
	}
	return argStringRequired(args, i)
}

func buildRegexp(pattern, flags string) (*regexp.Regexp, error) {
	var goFlags []string
	dotAll := false
	for _, f := range flags {
		switch f {
		case 'i':
			goFlags = append(goFlags, "i")
		case 's':
			// "." matches every character including the newlines. Since "."
			// expands to an explicit class rather than relying on RE2's, the
			// flag has to be passed down to translatePattern rather than
			// prefixed onto the compiled pattern — "(?s)" cannot reach inside
			// a character class.
			dotAll = true
		case 'm':
			// "^" and "$" match at line boundaries.
			goFlags = append(goFlags, "m")
		case 'x':
			// Whitespace in the pattern is ignored. RE2 has no such flag, so
			// it is applied by stripping unescaped whitespace here.
			pattern = stripPatternWhitespace(pattern)
		case 'q':
			// The pattern is a literal string.
			pattern = regexp.QuoteMeta(pattern)
		default:
			return nil, fmt.Errorf("FORX0001: unknown regular expression flag %q", string(f))
		}
	}

	translated, err := translatePattern(pattern, dotAll)
	if err != nil {
		return nil, err
	}
	if len(goFlags) > 0 {
		translated = "(?" + strings.Join(goFlags, "") + ")" + translated
	}

	// XPath's fn:matches is a *containment* test, unlike XML Schema's pattern
	// facet which is anchored. Go's MatchString is also containment, so no
	// anchoring is added here.
	re, err := regexp.Compile(translated)
	if err != nil {
		// A repeat count RE2 will not accept is still a valid XML Schema
		// pattern — "a{2147483647}" simply matches nothing any input can
		// satisfy. Reporting FORX0002 for it said the pattern was malformed
		// when it was merely enormous, so the count is clamped to one past
		// RE2's limit, which matches exactly what the original would.
		if clamped, ok := clampRepeatCounts(translated); ok {
			if re2, err2 := regexp.Compile(clamped); err2 == nil {
				return re2, nil
			}
		}
		return nil, fmt.Errorf("FORX0002: invalid regular expression %q: %w", pattern, err)
	}
	return re, nil
}

// validEscapes are the single characters that may follow a backslash, beyond
// the \i \I \c \C \p \P handled separately above.
//
// XML Schema's set is closed: the metacharacters, the shorthand classes, and
// the three control abbreviations. Notably absent are \0, \a, \e and an
// escaped space, all of which other regex dialects accept.
// dotClass is what "." expands to. XPath excludes both newline characters
// from it, where RE2's "." excludes only \n.
const dotClass = `[^\n\r]`

const validEscapes = `nrt\\|.?*+(){}[]^$-sSdDwW`

// unicodeEscape rewrites the escapes whose XML Schema meaning is wider than
// RE2's, and reports whether it did.
//
// Appendix F defines \d as \p{Nd} and \w by subtracting the punctuation,
// separator and other categories from everything — both are Unicode-wide, while
// RE2 reads them as ASCII. \s is the same in both, so it is left alone.
func unicodeEscape(esc byte) (string, bool) {
	switch esc {
	case 'd':
		return `\p{Nd}`, true
	case 'D':
		return `\P{Nd}`, true
	case 'w':
		return `[^\p{P}\p{Z}\p{C}]`, true
	case 'W':
		return `[\p{P}\p{Z}\p{C}]`, true
	}
	return "", false
}

// classUnicodeEscape is unicodeEscape's form for use inside a character class,
// where a bracketed alternative cannot nest.
//
// \w has no single property that names it — it is defined by subtraction — so
// inside a class it stays as RE2's ASCII \w rather than becoming something
// syntactically invalid. That is a narrowing, and the only one: \d and \D are
// property references and carry their full Unicode meaning either way.
func classUnicodeEscape(esc byte) (string, bool) {
	switch esc {
	case 'd':
		return `\p{Nd}`, true
	case 'D':
		return `\P{Nd}`, true
	}
	return "", false
}

// translatePattern rewrites the XPath-specific escapes into RE2 syntax.
func translatePattern(p string, dotAll bool) (string, error) {
	var sb strings.Builder
	inClass := false

	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '\\' && i+1 < len(p) {
			i++
			esc := p[i]
			switch esc {
			case 'i':
				// An XML name start character.
				sb.WriteString(classNameStart(inClass))
			case 'I':
				if inClass {
					return "", fmt.Errorf("FORX0002: \\I is not supported inside a character class")
				}
				sb.WriteString("[^" + nameStartBody + "]")
			case 'c':
				sb.WriteString(classNameChar(inClass))
			case 'C':
				if inClass {
					return "", fmt.Errorf("FORX0002: \\C is not supported inside a character class")
				}
				sb.WriteString("[^" + nameCharBody + "]")
			case 'p', 'P':
				// A block name such as \p{IsBasicLatin} is an XML Schema
				// construct that RE2 does not know; a category such as \p{L}
				// it does. Translate the former into its codepoint range and
				// pass the latter through.
				if body, rest, ok := takeBlockName(p[i+1:]); ok {
					if !strings.HasPrefix(body, "Is") {
						// A category, not a block: RE2 knows it, but the case
						// sensitivity has to be pinned. A global "i" flag
						// otherwise reaches inside \p{Lu} and makes it match
						// lowercase, which is not what asking for an uppercase
						// letter means.
						sb.WriteString("(?-i:\\" + string(esc) + "{" + body + "})")
						i += len(p[i+1:]) - len(rest)
						continue
					}
					if r, known := unicodeBlockRange(body); known {
						if esc == 'p' {
							sb.WriteString("[" + r + "]")
						} else {
							sb.WriteString("[^" + r + "]")
						}
						i += len(p[i+1:]) - len(rest)
						continue
					}
				}
				sb.WriteByte('\\')
				sb.WriteByte(esc)
			default:
				// XML Schema defines a closed set of escapes, so anything
				// outside it is an invalid pattern rather than a literal.
				// Passing them through let "\\0" and "\\ " compile — RE2 reads
				// the first as a NUL byte and the second as a space — where
				// the spec requires FORX0002.
				//
				// A digit is the one case worth naming separately: it is a
				// backreference, which is valid XML Schema but which RE2 does
				// not implement at all.
				if esc >= '1' && esc <= '9' {
					return "", fmt.Errorf(
						"FORX0002: backreference \\%c is not supported", esc)
				}
				if !strings.ContainsRune(validEscapes, rune(esc)) {
					return "", fmt.Errorf(
						"FORX0002: invalid escape %q", `\`+string(esc))
				}
				// \d and \w mean more in XML Schema than they do in
				// RE2. Appendix F defines \d as \p{Nd} — every
				// decimal digit in Unicode, not just 0-9 — and \w as
				// everything outside the punctuation, separator and
				// other categories. RE2 reads both as ASCII, so a
				// pattern of "\d" silently rejected the Arabic-Indic,
				// Mongolian and Khmer digits the spec accepts.
				if repl, ok := unicodeEscape(esc); ok {
					// Inside a class the bracketed forms of \w
					// and \W cannot nest, so the escape is kept
					// as a property reference. \d and \D are
					// already property references and nest
					// fine. The subtraction handler re-reads the
					// original text, so this rewrite does not
					// reach it.
					if inClass {
						if repl2, ok2 := classUnicodeEscape(esc); ok2 {
							sb.WriteString(repl2)
							continue
						}
					} else {
						sb.WriteString(repl)
						continue
					}
				}
				sb.WriteByte('\\')
				sb.WriteByte(esc)
			}
			continue
		}

		switch c {
		case '.':
			if inClass {
				// Inside a class "." is an ordinary character.
				sb.WriteByte(c)
				break
			}
			// XPath's "." excludes both newline characters; RE2's excludes
			// only \n, so a carriage return matched where it should not have.
			// The s flag, which makes "." match everything, is applied by
			// replacing this class rather than by a global flag.
			if dotAll {
				sb.WriteString(`(?s:.)`)
				break
			}
			sb.WriteString(dotClass)
		case '[':
			if !inClass {
				inClass = true
			}
			sb.WriteByte(c)
		case ']':
			inClass = false
			sb.WriteByte(c)
		case '-':
			// Character-class subtraction, "[a-z-[aeiou]]", has no RE2 syntax,
			// so the two classes are expanded into codepoint ranges and the
			// difference is emitted as an ordinary class. Whatever has been
			// written for the current class so far is the left operand.
			if inClass && i+1 < len(p) && p[i+1] == '[' {
				out, consumed, err := applySubtraction(sb.String(), p[i:])
				if err != nil {
					return "", err
				}
				sb.Reset()
				sb.WriteString(out)
				i += consumed - 1
				inClass = false
				continue
			}
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String(), nil
}

// The XML NameStartChar and NameChar productions, as RE2 class bodies.
const (
	nameStartBody = `A-Za-z_:\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}` +
		`\x{370}-\x{37D}\x{37F}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}` +
		`\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}`
	nameCharExtra = `\-.0-9\x{B7}\x{300}-\x{36F}\x{203F}-\x{2040}`
	nameCharBody  = nameStartBody + nameCharExtra
)

func classNameStart(inClass bool) string {
	if inClass {
		return nameStartBody
	}
	return "[" + nameStartBody + "]"
}

func classNameChar(inClass bool) string {
	if inClass {
		return nameCharBody
	}
	return "[" + nameCharBody + "]"
}

// stripPatternWhitespace implements the "x" flag by removing whitespace that
// is neither escaped nor inside a character class.
func stripPatternWhitespace(p string) string {
	var sb strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		// The x flag removes whitespace *before* escapes are interpreted, so a
		// backslash does not protect the character after it: "hello\ sworld"
		// becomes "hello\sworld", which is what the suite asserts. Copying the
		// escape pair verbatim kept the space and then rejected "\ " as an
		// invalid escape.
		if c == '\\' {
			sb.WriteByte(c)
			// Skip any whitespace between the backslash and what it escapes.
			for i+1 < len(p) && isPatternSpace(p[i+1]) {
				i++
			}
			if i+1 < len(p) {
				i++
				sb.WriteByte(p[i])
			}
			continue
		}
		switch c {
		case '[':
			inClass = true
		case ']':
			inClass = false
		case ' ', '\t', '\n', '\r':
			if !inClass {
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// translateReplacement converts an XPath replacement string to Go's syntax.
//
// XPath writes group references as "$1" and escapes a literal dollar as "\$";
// Go writes them as "${1}" and a literal dollar as "$$". Passing the XPath
// form to Go directly mostly works and then fails on "$1x", which Go reads as
// group "1x" — an unnamed group that does not exist, so the substitution
// silently becomes empty.
func translateReplacement(r string, groups int) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch c {
		case '\\':
			if i+1 >= len(r) {
				return "", fmt.Errorf("FORX0004: replacement string ends with a backslash")
			}
			i++
			switch r[i] {
			case '$':
				sb.WriteString("$$")
			case '\\':
				sb.WriteByte('\\')
			default:
				return "", fmt.Errorf(
					"FORX0004: invalid escape %q in replacement string", `\`+string(r[i]))
			}
		case '$':
			j := i + 1
			for j < len(r) && r[j] >= '0' && r[j] <= '9' {
				j++
			}
			if j == i+1 {
				return "", fmt.Errorf("FORX0004: '$' must be followed by a digit in a replacement string")
			}
			// The group reference takes as many digits as name a group that
			// exists; the rest are literal text. "$1520" against a pattern
			// with fifteen groups is group 15 followed by "20", not group
			// 1520 — taking every digit made the whole reference vanish,
			// because no such group could ever match.
			end := j
			for end > i+1 {
				n, err := strconv.Atoi(r[i+1 : end])
				if err == nil && n <= groups {
					break
				}
				end--
			}
			if end == i+1 {
				// Not even one digit names a group: the reference is empty,
				// which the spec substitutes as the empty string.
				end = i + 2
				sb.WriteString("${" + r[i+1:end] + "}")
				sb.WriteString(r[end:j])
				i = j - 1
				continue
			}
			sb.WriteString("${" + r[i+1:end] + "}")
			sb.WriteString(r[end:j])
			i = j - 1
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String(), nil
}

// CompileRegexp exposes the XPath-to-Go regular expression translation for the
// XSLT layer, which needs it for xsl:analyze-string. The compiled result is
// cached exactly as it is for fn:matches.
func CompileRegexp(pattern, flags string) (*regexp.Regexp, error) {
	return compileXPathRegexp(pattern, flags)
}

// matchInput adapts the subject string for fn:matches in multi-line mode.
//
// Go and XML Schema disagree about one position. Both let "$" match before a
// newline, but Go additionally treats the position *after* a trailing newline
// as the start of an empty final line, so "^$" finds a match in "abcd\ndefg\n"
// where XML Schema finds none. Expressing the difference in the pattern would
// need lookahead, which RE2 does not have, so the one trailing newline is
// dropped from the subject instead.
//
// This is safe only because fn:matches answers a yes/no question: the same
// trick applied to fn:replace or fn:tokenize would silently eat a character
// from their output, which is why it lives here rather than in the shared
// compile path.
func matchInput(s, flags string) string {
	if !strings.ContainsRune(flags, 'm') {
		return s
	}
	return strings.TrimSuffix(s, "\n")
}

// applySubtraction rewrites a class subtraction into a plain RE2 class.
//
// built is everything emitted for the pattern so far, ending in the open
// bracket and body of the left-hand class; rest begins at the "-" that starts
// the subtraction. It returns the replacement for built and how many bytes of
// rest were consumed, including the closing bracket of the outer class.
func applySubtraction(built, rest string) (string, int, error) {
	open := strings.LastIndexByte(built, '[')
	if open < 0 {
		return "", 0, fmt.Errorf("FORX0002: malformed character class subtraction")
	}
	prefix, left := built[:open], built[open+1:]

	// rest is "-[right]]" — find the bracket that closes the inner class.
	depth := 0
	end := -1
	for i := 1; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", 0, fmt.Errorf("FORX0002: unterminated character class subtraction")
	}
	right := rest[2:end]

	// The outer class must still be closed after the inner one.
	consumed := end + 1
	if consumed >= len(rest) || rest[consumed] != ']' {
		return "", 0, fmt.Errorf("FORX0002: malformed character class subtraction")
	}
	consumed++

	body, ok := subtractClasses(left, right)
	if !ok {
		// Shorthand classes such as \d or \p{L} would need the Unicode
		// tables that define them to expand exactly, so those are still
		// refused rather than approximated.
		return "", 0, fmt.Errorf(
			"FORX0002: character class subtraction is only supported for " +
				"literal characters and ranges")
	}
	return prefix + "[" + body + "]", consumed, nil
}

// takeBlockName reads a "{IsName}" block reference following \p or \P.
//
// Only the Is-prefixed form is a block; \p{L} and friends are Unicode
// categories, which RE2 handles itself.
func takeBlockName(s string) (name, rest string, ok bool) {
	// The body may name either an XML Schema block ("IsBasicLatin") or a
	// Unicode category ("Lu"). Telling them apart is the caller's job, since
	// the two are handled differently.
	if len(s) < 2 || s[0] != '{' {
		return "", s, false
	}
	end := strings.IndexByte(s, '}')
	if end < 0 {
		return "", s, false
	}
	return s[1:end], s[end+1:], true
}

// unicodeBlockRange maps an XML Schema Unicode block name to the RE2 class
// body for its codepoint range.
//
// Only the blocks that occur in practice are listed. An unknown block is an
// error rather than a silently empty class, because a pattern that matches
// nothing is worse than one that refuses to compile.
func unicodeBlockRange(name string) (string, bool) {
	blocks := map[string]string{
		"IsBasicLatin":                         `\x{0}-\x{7F}`,
		"IsLatin-1Supplement":                  `\x{80}-\x{FF}`,
		"IsLatinExtended-A":                    `\x{100}-\x{17F}`,
		"IsLatinExtended-B":                    `\x{180}-\x{24F}`,
		"IsGreek":                              `\x{370}-\x{3FF}`,
		"IsCyrillic":                           `\x{400}-\x{4FF}`,
		"IsHebrew":                             `\x{590}-\x{5FF}`,
		"IsArabic":                             `\x{600}-\x{6FF}`,
		"IsArabicPresentationForms-A":          `\x{FB50}-\x{FDFF}`,
		"IsArabicPresentationForms-B":          `\x{FE70}-\x{FEFF}`,
		"IsHiragana":                           `\x{3040}-\x{309F}`,
		"IsKatakana":                           `\x{30A0}-\x{30FF}`,
		"IsUnifiedCanadianAboriginalSyllabics": `\x{1400}-\x{167F}`,
		"IsYiSyllables":                        `\x{A000}-\x{A48F}`,
		"IsYiRadicals":                         `\x{A490}-\x{A4CF}`,
		"IsCJKUnifiedIdeographs":               `\x{4E00}-\x{9FFF}`,
		"IsSpecials":                           `\x{FFF0}-\x{FFFF}`,
	}
	r, ok := blocks[name]
	return r, ok
}

func isPatternSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// clampRepeatCounts lowers any {n} or {n,m} bound above RE2's limit.
//
// RE2 caps a repeat count at 1000 and refuses to compile past it. A larger
// count is legal in XML Schema, and clamping is exact for the purpose: no
// input can satisfy a thousand-and-one repetitions of anything the caller is
// matching against either, so the clamped pattern matches the same strings.
func clampRepeatCounts(p string) (string, bool) {
	const re2Max = 1000
	var sb strings.Builder
	changed := false
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			sb.WriteByte(p[i])
			continue
		}
		end := strings.IndexByte(p[i:], '}')
		if end < 0 {
			sb.WriteByte(p[i])
			continue
		}
		body := p[i+1 : i+end]
		out, ok := clampBound(body, re2Max)
		if !ok {
			sb.WriteString(p[i : i+end+1])
			i += end
			continue
		}
		changed = true
		sb.WriteString("{" + out + "}")
		i += end
	}
	return sb.String(), changed
}

// clampBound rewrites one {n}, {n,} or {n,m} body, reporting whether it
// changed.
func clampBound(body string, max int) (string, bool) {
	lo, hi, hasComma := strings.Cut(body, ",")
	clamp := func(s string) (string, bool) {
		if s == "" {
			return s, false
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= max {
			return s, false
		}
		return strconv.Itoa(max), true
	}
	newLo, loChanged := clamp(lo)
	if !hasComma {
		return newLo, loChanged
	}
	newHi, hiChanged := clamp(hi)
	return newLo + "," + newHi, loChanged || hiChanged
}

// TranslateSchemaRegexp rewrites an XML Schema regular expression into RE2
// syntax, without anchoring it.
//
// The XML Schema flavour and the XPath flavour share a grammar — Part 2
// Appendix F defines the one, and XPath's fn:matches extends it — so the
// translation is the same in both directions: the multi-character escapes \i
// and \c, the block and category escapes, and character class subtraction, none
// of which RE2 accepts as written.
//
// The result is deliberately unanchored, because the two flavours differ
// exactly there. fn:matches is a containment test, while a pattern facet must
// span the whole value. A caller using this for a pattern facet has to wrap the
// result — see the xsd package, which does so with \A(?:...)\z.
func TranslateSchemaRegexp(pattern string) (string, error) {
	return translatePattern(pattern, false)
}
