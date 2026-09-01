package xquery

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// looksLikeFLWOR reports whether a FLWOR expression starts here.
//
// "for" and "let" are not reserved words: "let" is a legal element name and
// "for(1)" is a legal function call, so the keyword alone does not decide it.
// What decides it is that a FLWOR's initial clause binds a variable, so the
// keyword must be followed by "$" — or, for "for", by "sliding"/"tumbling",
// which is the window form. Saxon and BaseX both make the same test.
func (p *parser) looksLikeFLWOR() bool {
	save := p.pos
	defer func() { p.pos = save }()
	kw := p.peekWord()
	switch kw {
	case "for":
		p.pos += len(kw)
		p.skipSpaceAndComments()
		if p.lookingAt("$") {
			return true
		}
		w := p.peekWord()
		return w == "sliding" || w == "tumbling"
	case "let":
		p.pos += len(kw)
		p.skipSpaceAndComments()
		return p.lookingAt("$")
	}
	return false
}

// peekWord returns the NCName at the cursor without consuming it.
func (p *parser) peekWord() string {
	save := p.pos
	w := p.scanNCName()
	p.pos = save
	return w
}

// consumeWord consumes the keyword kw when it is the next word, and reports
// whether it did.
//
// It matches whole words only: "letter" does not begin a "let" clause, and
// "ordered" is not "order". Every FLWOR keyword is a soft one, so this is the
// only safe way to test for any of them.
func (p *parser) consumeWord(kw string) bool {
	save := p.pos
	p.skipSpaceAndComments()
	if p.peekWord() == kw {
		p.pos += len(kw)
		return true
	}
	p.pos = save
	return false
}

// consumeWords consumes a run of keywords, all or nothing. "empty greatest"
// and "group by" are each two words with whitespace and comments allowed
// between them.
func (p *parser) consumeWords(kws ...string) bool {
	save := p.pos
	for _, kw := range kws {
		if !p.consumeWord(kw) {
			p.pos = save
			return false
		}
	}
	return true
}

// peekWordAt returns the keyword at the cursor after skipping whitespace,
// leaving the cursor where it was.
func (p *parser) peekWordAt() string {
	save := p.pos
	p.skipSpaceAndComments()
	w := p.peekWord()
	p.pos = save
	return w
}

// parseVarName reads "$" followed by a QName and resolves it.
//
// A variable name is not an element name, so an unprefixed one is in no
// namespace whatever the default element namespace says — §3.1.2. Using
// resolveAttributeName here is not a coincidence of implementation: the two
// name classes share exactly the rule that the default does not apply.
func (p *parser) parseVarName() (xdm.QName, error) {
	p.skipSpaceAndComments()
	if !p.consume("$") {
		return xdm.QName{}, p.errorf("XPST0003: expected %q", "$")
	}
	prefix, local, err := p.parseQName()
	if err != nil {
		return xdm.QName{}, err
	}
	return p.sc.resolveAttributeName(prefix, local)
}

// stopWords are the words that end an ExprSingle written inside a FLWOR or a
// quantified expression.
//
// The set is one rather than one per position because an ExprSingle cannot
// contain any of them as a bare word in the first place: every one is either
// a clause keyword or a modifier, and where such a word is legitimately part
// of an expression — "$x/order", "count(1)" — it is not bare, which is what
// canStartClause and wordIsName test for. A per-position set would be more
// precise and would buy nothing, because a word from the wrong position is a
// syntax error either way; it would only change which parser reports it.
//
// "satisfies", "when", "start" and "end" are here for the quantified and
// window forms, which are not FLWOR clauses but delimit their expressions in
// the same way.
var stopWords = map[string]bool{
	// Clause keywords.
	"for": true, "let": true, "where": true, "order": true, "stable": true,
	"group": true, "count": true, "return": true,
	// Order-by modifiers, which follow the ordering key.
	"ascending": true, "descending": true, "empty": true, "collation": true,
	// The quantified form.
	"satisfies": true,
	// The window form.
	"start": true, "end": true, "only": true, "when": true,
}

// scanExprSingle returns the source of one ExprSingle starting at the cursor,
// stopping before the keyword that begins the next clause.
//
// This is the boundary between the two parsers, and it is where a FLWOR
// implementation is most easily got wrong. An ExprSingle has no closing
// delimiter: "for $x in $a/b return $x" ends its binding expression at
// "return", and nothing in the text says so except that "return" is a word
// that cannot continue "$a/b". So the scan tracks bracket depth, steps over
// string literals and comments, and stops at the first bare keyword at depth
// zero.
//
// Two things make that insufficient on its own, and both are handled here:
//
// A nested FLWOR inside the expression has keywords of its own —
// "let $y := for $z in $s return $z" would stop at that inner "return" and
// hand "for $z in $s" to the expression parser. So a nested FLWOR is scanned
// recursively rather than by keyword, which is the whole reason this shares
// the recursive-descent parser's cursor instead of being a lexical scan.
//
// A keyword may be a name. "$x/for" is a path step, "order" is a legal
// element name, and "count(...)" is a function call, so a word is only a
// clause keyword when what precedes it could end an expression: after "/",
// "::", "@", "$" or "(" it is a name, and a word followed by "(" or "#" is a
// function name rather than a keyword.
func (p *parser) scanExprSingle() (string, error) {
	start := p.pos
	depth := 0
	// prev is the last significant character seen, which decides whether a
	// word here can be a keyword at all.
	prev := byte(0)
	for !p.eof() {
		c := p.src[p.pos]
		switch {
		case c == '\'' || c == '"':
			end, err := skipString(p.src, p.pos)
			if err != nil {
				return "", err
			}
			p.pos = end + 1
			prev = '"'
			continue

		case c == '(' && p.pos+1 < len(p.src) && p.src[p.pos+1] == ':':
			end, err := skipComment(p.src, p.pos)
			if err != nil {
				return "", err
			}
			p.pos = end + 1
			continue

		case c == '(' || c == '[' || c == '{':
			depth++
			p.pos++
			prev = c
			continue

		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				// A closing bracket we never opened belongs to whatever
				// encloses this expression — an enclosed expression, or a
				// parenthesis around the whole FLWOR.
				goto done
			}
			depth--
			p.pos++
			prev = c
			continue

		case c == ',':
			if depth == 0 {
				// A comma at depth zero separates the items of the enclosing
				// Expr, and an ExprSingle by definition contains none.
				goto done
			}
			p.pos++
			continue

		case c == '<':
			// A direct constructor inside the expression: its content is XML
			// and any keyword in it is text, so it is stepped over whole.
			if err := p.skipDirConstructor(); err != nil {
				return "", err
			}
			prev = '>'
			continue

		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.pos++
			continue
		}

		if !isNameStartByte(c) {
			prev = c
			p.pos++
			continue
		}

		// A word. Decide whether it ends the expression.
		wordStart := p.pos
		w := p.scanNCName()
		if depth == 0 && stopWords[w] && canStartClause(prev) &&
			!p.wordIsName(w) {
			p.pos = wordStart
			goto done
		}
		prev = 'x'
	}
done:
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return "", p.errorf("XPST0003: expected an expression")
	}
	return src, nil
}

// canStartClause reports whether a clause keyword may follow the character
// before it.
//
// After a path separator, an axis, an "@", a "$" or an opening parenthesis, a
// word is a name: "$x/order" selects children named "order" and does not
// begin an "order by" clause. Everything else — the end of a literal, a
// closing bracket, a name — could end an expression, so a keyword there is a
// keyword.
func canStartClause(prev byte) bool {
	switch prev {
	case '/', ':', '@', '$', '(', '[', ',', 0:
		return false
	}
	return true
}

// wordIsName reports whether the word just scanned is being used as a name
// rather than as a keyword, judged by what follows it.
//
// "count(" is a function call and "count#1" a function reference, neither of
// which is a CountClause; a CountClause is always "count $v". The cursor is
// left where the caller put it.
func (p *parser) wordIsName(w string) bool {
	save := p.pos
	defer func() { p.pos = save }()
	p.skipSpaceAndComments()
	if p.eof() {
		return false
	}
	switch p.src[p.pos] {
	case '(', '#':
		return true
	}
	// "count" and "let" and "for" introduce a variable; a word not followed
	// by one is being used as something else. The two-word clauses are
	// checked on their second word instead.
	switch w {
	case "count", "for", "let":
		return !p.lookingAt("$") && p.peekWord() != "sliding" &&
			p.peekWord() != "tumbling"
	case "group", "order":
		return p.peekWord() != "by"
	case "stable":
		return p.peekWord() != "order"
	}
	return false
}

func isNameStartByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		c >= 0x80
}

// skipDirConstructor steps the cursor over a direct constructor without
// building anything, for the scan that only needs to know where it ends.
//
// It reparses the constructor, which is wasteful and correct: the alternative
// is a second, looser scanner for XML, and two scanners that must agree about
// where a constructor ends is exactly the kind of duplication that produces
// parse errors a long way from their cause. The constructor is parsed again
// for real when the expression it is in is compiled.
func (p *parser) skipDirConstructor() error {
	switch {
	case p.lookingAt("<!--"):
		_, err := p.parseDirComment()
		return err
	case p.lookingAt("<?"):
		_, err := p.parseDirPI()
		return err
	case p.lookingAt("<"):
		_, err := p.parseDirElement()
		return err
	}
	p.pos++
	return nil
}

// parseFLWOR parses a FLWOR expression: §3.10, productions [41]-[69].
//
// The grammar is a sequence of clauses ending in "return", with the one
// constraint that the first must be "for" or "let" — a FLWOR that began with
// "where" would have nothing to filter.
func (p *parser) parseFLWOR() (*flwor, error) {
	f := &flwor{}
	first := true
	for {
		p.skipSpaceAndComments()
		w := p.peekWord()
		var err error
		var cs []clause
		switch {
		case w == "for" && p.forIsWindow():
			cs, err = p.parseWindowClause()
		case w == "for":
			cs, err = p.parseForClause()
		case w == "let":
			cs, err = p.parseLetClause()
		case w == "where" && !first:
			cs, err = p.parseWhereClause()
		case (w == "order" || w == "stable") && !first:
			cs, err = p.parseOrderByClause()
		case w == "group" && !first:
			cs, err = p.parseGroupByClause()
		case w == "count" && !first:
			cs, err = p.parseCountClause()
		case w == "return" && !first:
			p.consumeWord("return")
			return p.finishFLWOR(f)
		default:
			if first {
				return nil, p.errorf(
					"XPST0003: a FLWOR expression must begin with %q or %q",
					"for", "let")
			}
			return nil, p.errorf(
				"XPST0003: expected a FLWOR clause or %q, got %q",
				"return", firstToken(p.src[p.pos:]))
		}
		if err != nil {
			return nil, err
		}
		f.clauses = append(f.clauses, cs...)
		first = false
	}
}

// finishFLWOR parses the return expression.
//
// A constructor may be the return expression, and the expression parser
// cannot read one, so the two cases are kept apart here rather than being
// forced through one path.
func (p *parser) finishFLWOR(f *flwor) (*flwor, error) {
	c, err := p.parseClauseExpr()
	if err != nil {
		return nil, err
	}
	f.ret = c
	return f, nil
}

// startsConstructor reports whether a direct or computed constructor begins
// at the cursor.
func (p *parser) startsConstructor() bool {
	if p.lookingAt("<") {
		return true
	}
	switch p.peekWord() {
	case "element", "attribute", "document", "text", "comment",
		"processing-instruction", "namespace":
		// Only when it is a constructor rather than a kind test or a function
		// call: "element(a)" is a test and "element a {...}" a constructor.
		save := p.pos
		defer func() { p.pos = save }()
		_, ok, _ := p.parseComputed()
		return ok
	}
	return false
}

// parseClauseExpr parses the ExprSingle a clause binds or tests, compiling it
// with xpath.
//
// Three things it may be cannot be handed to xpath as a substring, and each
// is taken here instead. A constructor, because the expression parser cannot
// read XML. A nested FLWOR, because its own clauses would otherwise be
// scanned as though they were this one's — "let $y := for $z in $s return $z"
// would end $y's expression at the inner "return" and leave a dangling
// binding. And a quantified expression with a type declaration, which XPath's
// grammar does not admit.
func (p *parser) parseClauseExpr() (*compiledExpr, error) {
	p.skipSpaceAndComments()
	if p.startsConstructor() {
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		return &compiledExpr{src: "constructor", items: []node{n}}, nil
	}
	if p.looksLikeFLWOR() || p.looksLikeQuantified() {
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		return &compiledExpr{src: "flwor", items: []node{n}}, nil
	}
	src, err := p.scanExprSingle()
	if err != nil {
		return nil, err
	}
	return p.compileExpr(src)
}

// parseTypedClauseExpr is parseClauseExpr with a declared type applied to the
// value, for the clauses that admit one.
//
// A nested FLWOR or a constructor cannot have the type folded into the
// expression the way a plain one can, because there is no source text to wrap;
// its check is recorded on the compiled expression and applied to the value
// instead.
func (p *parser) parseTypedClauseExpr(typ string, perItem bool) (*compiledExpr, error) {
	p.skipSpaceAndComments()
	if p.startsConstructor() || p.looksLikeFLWOR() || p.looksLikeQuantified() {
		c, err := p.parseClauseExpr()
		if err != nil {
			return nil, err
		}
		if typ != "" {
			c.check, err = p.compileTypeCheck(typ, perItem)
			if err != nil {
				return nil, err
			}
		}
		return c, nil
	}
	src, err := p.scanExprSingle()
	if err != nil {
		return nil, err
	}
	if perItem {
		return p.compileTypedFor(src, typ)
	}
	return p.compileTyped(src, typ)
}

// parseForClause parses "for" and its comma-separated bindings: [45]-[48].
func (p *parser) parseForClause() ([]clause, error) {
	if !p.consumeWord("for") {
		return nil, p.errorf("XPST0003: expected %q", "for")
	}
	var out []clause
	for {
		c := &forClause{}
		name, err := p.parseVarName()
		if err != nil {
			return nil, err
		}
		c.name = name
		if err := p.parseOptionalType(); err != nil {
			return nil, err
		}
		typ := p.lastType
		// §3.10.2 orders these: the type declaration, then "allowing empty",
		// then the positional variable, then "in".
		if p.consumeWords("allowing", "empty") {
			c.allowingEmpty = true
		}
		if p.consumeWord("at") {
			pos, err := p.parseVarName()
			if err != nil {
				return nil, err
			}
			if pos == c.name {
				return nil, p.errorf(
					"XQST0089: the positional variable $%s repeats the "+
						"binding variable", pos.Lexical())
			}
			c.pos, c.hasPos = pos, true
		}
		if !p.consumeWord("in") {
			return nil, p.errorf("XPST0003: expected %q after $%s",
				"in", c.name.Lexical())
		}
		// The declared type constrains each *item* the variable is bound to,
		// not the sequence iterated, so the check cannot be applied to the
		// binding expression as a whole. It is applied per item instead.
		c.seq, err = p.parseTypedClauseExpr(typ, true)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		if !p.consumeAtDepthZero(",") {
			break
		}
	}
	return out, nil
}

// parseLetClause parses "let" and its comma-separated bindings: [49]-[50].
func (p *parser) parseLetClause() ([]clause, error) {
	if !p.consumeWord("let") {
		return nil, p.errorf("XPST0003: expected %q", "let")
	}
	var out []clause
	for {
		name, err := p.parseVarName()
		if err != nil {
			return nil, err
		}
		if err := p.parseOptionalType(); err != nil {
			return nil, err
		}
		typ := p.lastType
		p.skipSpaceAndComments()
		if !p.consume(":=") {
			return nil, p.errorf("XPST0003: expected %q after $%s",
				":=", name.Lexical())
		}
		// A "let" binds the whole sequence, so its declared type constrains
		// the sequence and the check is the ordinary "treat as".
		c, err := p.parseTypedClauseExpr(typ, false)
		if err != nil {
			return nil, err
		}
		out = append(out, &letClause{name: name, seq: c})
		if !p.consumeAtDepthZero(",") {
			break
		}
	}
	return out, nil
}

// parseWhereClause parses "where": [51].
func (p *parser) parseWhereClause() ([]clause, error) {
	p.consumeWord("where")
	c, err := p.parseClauseExpr()
	if err != nil {
		return nil, err
	}
	return []clause{&whereClause{test: c}}, nil
}

// parseCountClause parses "count $v": [59].
func (p *parser) parseCountClause() ([]clause, error) {
	p.consumeWord("count")
	name, err := p.parseVarName()
	if err != nil {
		return nil, err
	}
	return []clause{&countClause{name: name}}, nil
}

// parseOrderByClause parses "order by" and "stable order by": [60]-[62].
func (p *parser) parseOrderByClause() ([]clause, error) {
	c := &orderByClause{}
	if p.consumeWord("stable") {
		c.stable = true
	}
	if !p.consumeWords("order", "by") {
		return nil, p.errorf("XPST0003: expected %q", "order by")
	}
	for {
		var s orderSpec
		key, err := p.parseClauseExpr()
		if err != nil {
			return nil, err
		}
		s.key = key
		if p.consumeWord("ascending") {
			// The default, written for clarity.
		} else if p.consumeWord("descending") {
			s.descending = true
		}
		if p.consumeWord("empty") {
			switch {
			case p.consumeWord("greatest"):
				s.emptyGreatest = true
			case p.consumeWord("least"):
			default:
				return nil, p.errorf("XPST0003: expected %q or %q after %q",
					"greatest", "least", "empty")
			}
		}
		if p.consumeWord("collation") {
			uri, err := p.parseStringLiteral()
			if err != nil {
				return nil, err
			}
			s.collation = uri
		}
		c.specs = append(c.specs, s)
		if !p.consumeAtDepthZero(",") {
			break
		}
	}
	return []clause{c}, nil
}

// parseGroupByClause parses "group by": [63]-[65].
func (p *parser) parseGroupByClause() ([]clause, error) {
	if !p.consumeWords("group", "by") {
		return nil, p.errorf("XPST0003: expected %q", "group by")
	}
	c := &groupByClause{}
	for {
		var s groupSpec
		name, err := p.parseVarName()
		if err != nil {
			return nil, err
		}
		s.name = name
		// "group by $x := E" both binds and groups, which is the shorthand
		// for a "let" immediately before the clause.
		if err := p.parseOptionalType(); err != nil {
			return nil, err
		}
		typ := p.lastType
		p.skipSpaceAndComments()
		if p.consume(":=") {
			s.init, err = p.parseTypedClauseExpr(typ, false)
			if err != nil {
				return nil, err
			}
		} else if typ != "" {
			return nil, p.errorf(
				"XPST0003: a type on a grouping variable requires %q", ":=")
		}
		if p.consumeWord("collation") {
			uri, err := p.parseStringLiteral()
			if err != nil {
				return nil, err
			}
			s.collation = uri
		}
		c.specs = append(c.specs, s)
		if !p.consumeAtDepthZero(",") {
			break
		}
	}
	return []clause{c}, nil
}

// consumeAtDepthZero consumes a separator that belongs to the clause rather
// than to an expression inside it.
func (p *parser) consumeAtDepthZero(s string) bool {
	save := p.pos
	p.skipSpaceAndComments()
	if p.consume(s) {
		return true
	}
	p.pos = save
	return false
}

// parseStringLiteral reads a quoted literal, which is what "collation" and
// the prolog's URIs are written as.
func (p *parser) parseStringLiteral() (string, error) {
	p.skipSpaceAndComments()
	if p.eof() || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
		return "", p.errorf("XPST0003: expected a string literal")
	}
	quote := p.src[p.pos]
	end, err := skipString(p.src, p.pos)
	if err != nil {
		return "", err
	}
	raw := p.src[p.pos+1 : end]
	p.pos = end + 1
	// A doubled quote inside is one quote.
	return strings.ReplaceAll(raw, string([]byte{quote, quote}),
		string(quote)), nil
}

// bindingSource scans the ExprSingle a clause binds, tests or orders by.
func (p *parser) bindingSource() (string, error) {
	p.skipSpaceAndComments()
	return p.scanExprSingle()
}
