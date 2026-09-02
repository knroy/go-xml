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
func (p *parser) scanExprSingleSource() (string, error) {
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
			// Where an operand cannot start, though, this is the less-than
			// operator and there is no markup to step over — "$x < 3" would
			// otherwise be read as an unterminated start tag.
			if !startsMarkup(p.src, p.pos, prev) {
				p.pos++
				prev = '<'
				continue
			}
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
// The test is per word, because the words differ in what may follow them.
// "count(" is a function call and "count#1" a function reference, neither of
// which is a CountClause, and "count" as a clause is always "count $v" — so
// for that word a following "(" settles it. "return" is not like that: it is
// followed by an expression, and "return (1, 2)" is the ordinary case, so a
// following "(" proves nothing at all. Treating every word alike is what made
// "for $i in (1,2), $j in (3,4) return ($i, $j)" fail, the second binding
// running past its "return" because the parenthesis after it looked like a
// call.
//
// The cursor is left where the caller put it.
func (p *parser) wordIsName(w string) bool {
	save := p.pos
	defer func() { p.pos = save }()
	p.skipSpaceAndComments()
	if p.eof() {
		// A word at the very end binds nothing and tests nothing, so it can
		// only be a name.
		return true
	}
	switch w {
	case "count", "for", "let":
		// Each introduces a variable, or — for "for" — a window.
		if p.lookingAt("$") {
			return false
		}
		if w == "for" {
			nw := p.peekWord()
			return nw != "sliding" && nw != "tumbling"
		}
		return true
	case "group", "order":
		return p.peekWord() != "by"
	case "stable":
		return p.peekWord() != "order"
	case "only":
		return p.peekWord() != "end"
	case "start", "end":
		// A window condition begins with a variable, "at", "previous",
		// "next" or "when"; anything else means the word is a name.
		if p.lookingAt("$") {
			return false
		}
		switch p.peekWord() {
		case "at", "previous", "next", "when":
			return false
		}
		return true
	case "return", "satisfies", "where", "when":
		// Each is followed by an expression, so nothing about the next
		// character distinguishes a keyword from a name. What does is that
		// a name here would have to be a function call or a reference, and
		// both are ruled out by the caller having found the word bare.
		return false
	}
	// The order-by modifiers. Each ends a clause or is followed by another
	// modifier, so a "(" or "#" after one makes it a call rather than a
	// keyword.
	switch p.src[p.pos] {
	case '(', '#':
		return true
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

// startsXQueryOnly reports whether one of the XQuery-only expression forms —
// typeswitch, switch, try, validate, ordered, unordered, the extension
// expression or the string constructor — begins at the cursor.
//
// A clause's expression is an ExprSingle and every one of these is an
// ExprSingle, so "for $x in E return typeswitch ($x) ..." is ordinary syntax.
// None is reserved, though, so the test is the one parseXQueryOnly itself
// makes: it trial-parses, and only a construct that parses commits.
func (p *parser) startsXQueryOnly() bool {
	save := p.pos
	defer func() { p.pos = save }()
	_, ok, _ := p.parseXQueryOnly()
	return ok
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
	if c, ok, err := p.parseOwnExpr(); ok || err != nil {
		return c, err
	}
	src, err := p.scanExprSingleSource()
	if err != nil {
		return nil, err
	}
	if !needsXQueryParser(src) {
		return p.compileExpr(src)
	}
	return p.parseFromSource(src)
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
	// The type can be folded into the source only when the expression goes to
	// xpath as a substring. Where it does not — a constructor, a nested FLWOR,
	// or anything holding one — there is no text to wrap, so the check is
	// recorded on the compiled expression and applied to the value instead.
	if c, ok, err := p.parseOwnExpr(); ok || err != nil {
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
	src, err := p.scanExprSingleSource()
	if err != nil {
		return nil, err
	}
	if needsXQueryParser(src) {
		c, err := p.parseFromSource(src)
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
	if perItem {
		return p.compileTypedFor(src, typ)
	}
	return p.compileTyped(src, typ)
}

// parseOwnExpr parses an expression this package must read itself when one
// begins at the cursor, and reports whether it did.
//
// It is the shared front half of parseClauseExpr and parseTypedClauseExpr:
// both have to recognise the same three constructs, and only what they do
// with the result differs.
func (p *parser) parseOwnExpr() (*compiledExpr, bool, error) {
	p.skipSpaceAndComments()
	if !p.startsConstructor() && !p.looksLikeFLWOR() &&
		!p.looksLikeQuantified() && !p.startsXQueryOnly() {
		return nil, false, nil
	}
	n, err := p.parseItem()
	if err != nil {
		return nil, false, err
	}
	return &compiledExpr{src: "xquery", items: []node{n}}, true, nil
}

// parseFromSource re-reads an expression from its own text with a parser
// sharing this one's static context.
//
// Reading from a substring rather than from the cursor keeps the extent the
// enclosing scan already decided, which is what the clause after it depends
// on: the cursor has moved past the expression and must stay there.
func (p *parser) parseFromSource(src string) (*compiledExpr, error) {
	sub := &parser{src: src, sc: p.sc, version: p.version, depth: p.depth}
	items, err := sub.parseNestedExpr()
	if err != nil {
		return nil, err
	}
	return &compiledExpr{src: src, items: items}, nil
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
		// The spec starts at the module's default empty-order, which
		// "declare default order empty greatest|least" (§4.7) sets, and
		// which an "empty greatest|least" written on this spec then
		// overrides. Defaulting to false here instead would silently make
		// every prolog that declared "greatest" mean "least".
		s := orderSpec{emptyGreatest: p.sc.emptyOrder == EmptyGreatest}
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
				s.emptyGreatest = false
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
			s.init, err = p.parseClauseExpr()
			if err != nil {
				return nil, err
			}
			if typ != "" {
				// The type is checked against the atomised value the
				// grouping rebinds, not against what the expression
				// returned, so it cannot be folded into that expression.
				s.check, err = p.compileTypeCheck(typ, false)
				if err != nil {
					return nil, err
				}
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
	return p.scanExprSingleSource()
}

// needsXQueryParser reports whether the expression starting at the cursor
// contains syntax the expression parser cannot read, and so must be parsed
// here.
//
// The three are constructors, FLWOR expressions and typed quantified
// expressions. A clause expression is normally handed to xpath as a
// substring, which works because every XPath expression is an XQuery
// expression — but not the other way round, and an expression holding one of
// these has to be taken apart by the parser that understands them.
//
// The scan is lexical and deliberately conservative: it may say yes to an
// expression xpath could have read (a "<" that is a less-than, a "for" that
// is a name), and the parser it defers to then reads that expression
// correctly anyway. Saying no where the answer is yes is the failure that
// matters, and that cannot happen: every construct listed here is introduced
// by one of the tokens sought.
func needsXQueryParser(src string) bool {
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return false
			}
			i = end
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return false
				}
				i = end
			}
		case '`':
			// [177] StringConstructor. Two backticks and a bracket are not
			// anything XPath's grammar can read — it has no backtick at all —
			// so the opener alone settles it wherever it appears.
			if strings.HasPrefix(src[i:], "``[") {
				return true
			}
		case '<':
			// "<" begins a direct constructor only where an operand may
			// start. After a name, a literal or a closing bracket it is the
			// less-than operator, and "$x < 3" is not a constructor however
			// the character after it looks. What follows must also look like
			// markup: a name, "!", "?" or "/".
			if startsMarkup(src, i, lastSignificantOperandAware(src[:i])) {
				return true
			}
		default:
			if !isNameStartByte(src[i]) {
				continue
			}
			// A word: only a keyword introducing XQuery-only syntax counts,
			// and only where it is bare rather than part of a name.
			if i > 0 && (isNameByte(src[i-1]) || src[i-1] == '$' ||
				src[i-1] == ':' || src[i-1] == '@') {
				// Skip to the end of the name so its tail is not rescanned.
				for i < len(src) && isNameByte(src[i]) {
					i++
				}
				i--
				continue
			}
			j := i
			for j < len(src) && isNameByte(src[j]) {
				j++
			}
			switch src[i:j] {
			case "for", "let", "some", "every":
				// These four are XPath's too, so the keyword alone proves
				// nothing; what proves it is a clause XPath's grammar lacks.
				if hasXQueryOnlyClause(src[j:]) {
					return true
				}
			case "element", "attribute", "document", "text", "comment",
				"processing-instruction", "namespace", "ordered", "unordered":
				// A computed constructor is the keyword followed by a name or
				// an expression in braces. A kind test is followed by "(",
				// and "namespace::" is the axis, which is refused elsewhere.
				if k := skipSpaceFrom(src, j); k < len(src) &&
					(src[k] == '{' || isNameStartByte(src[k])) {
					return true
				}
			case "try":
				// "try" commits only on a brace: it is not a reserved word,
				// so "try" alone is a name and "try(1)" is a function call.
				if k := skipSpaceFrom(src, j); k < len(src) && src[k] == '{' {
					return true
				}
			case "switch", "typeswitch":
				// Both take a parenthesised operand, and neither name is
				// reserved, so a following "(" is what distinguishes the
				// expression from a call to a function of that name. That is
				// ambiguous on its own, but a function named switch or
				// typeswitch would have to be declared to be called, and
				// parseXQueryOnly rejects the reading that does not parse.
				if k := skipSpaceFrom(src, j); k < len(src) && src[k] == '(' {
					return true
				}
			}
			i = j - 1
		}
	}
	return false
}

// hasXQueryOnlyClause reports whether what follows a "for", "let", "some" or
// "every" uses a clause XPath's cut-down forms do not have.
//
// XPath has "for $x in E return F" and "let $x := E return F" and the untyped
// quantified expressions, and reads all of them at 100% of the suite. What it
// does not have is every other FLWOR clause, "at", "allowing empty", and a
// type declaration on any of them — so an expression is only taken from xpath
// when one of those actually appears.
func hasXQueryOnlyClause(src string) bool {
	depth := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return false
			}
			i = end
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return false
				}
				i = end
				continue
			}
			depth++
		case '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return false
			}
		default:
			if !isNameStartByte(src[i]) || depth != 0 {
				continue
			}
			if i > 0 && (isNameByte(src[i-1]) || src[i-1] == '$' ||
				src[i-1] == ':' || src[i-1] == '@' || src[i-1] == '/') {
				for i < len(src) && isNameByte(src[i]) {
					i++
				}
				i--
				continue
			}
			j := i
			for j < len(src) && isNameByte(src[j]) {
				j++
			}
			switch src[i:j] {
			case "where", "order", "stable", "group", "count", "at",
				"allowing", "as", "window", "tumbling", "sliding":
				return true
			case "for", "let":
				// A second binding keyword before the "return". XPath's
				// ForExpr and LetExpr each bind a comma-separated list of one
				// kind, so "let $s := 1 for $d in ..." is a FLWOR with two
				// clauses and not an XPath expression at all. A "for" that
				// merely begins the binding's own value — "let $f := for $x
				// in ... return $x" — is XPath, and answering yes to it is
				// the conservative direction this scan is allowed to take.
				return true
			case "return", "satisfies":
				// The binding is over and nothing XQuery-only appeared in it.
				// A later clause would have been seen before this word.
				return false
			}
			i = j - 1
		}
	}
	return false
}

// startsMarkup reports whether the "<" at src[i] opens a direct constructor
// rather than being the less-than operator.
//
// Both readings are legal XQuery and neither character decides it alone. What
// decides it is grammatical position: "<" is markup only where an operand may
// begin, so after a name, a literal, a closing bracket or a wildcard it is
// the operator — "$x < 3" and "count(.) < 3" are comparisons whatever follows
// the "<". Where an operand may begin, the character after it must still look
// like markup: a name, "!", "?" or "/". This is the same test both the
// expression scan and the detector make, so they cannot disagree about where
// a constructor is.
//
// prev is the last significant byte, and for three of them that byte does not
// settle the question on its own. "-" and "*" each spell both a name
// character and a binary operator, and the last byte of a word operator is a
// name byte like any other, so "3 - <a/>", "1 * <a/>" and "3 eq <a/>" all
// look like a "<" after a name while every one of them is an operator with a
// constructor for its right operand. A caller that can tell the two apart
// says so by passing operatorPrev; one that cannot pass an unreduced byte,
// and is answered conservatively — which for this function means yes, since
// saying yes to an expression xpath could have read costs a reparse and
// saying no to a constructor loses it.
func startsMarkup(src string, i int, prev byte) bool {
	if i+1 >= len(src) {
		return false
	}
	switch c := src[i+1]; {
	case isNameStartByte(c), c == '!', c == '?', c == '/':
	default:
		return false
	}
	switch {
	case prev == 0, prev == operatorPrev:
		return true
	case isNameByte(prev):
		// A name ends where an operand ends, so "$x < 3" is a comparison —
		// unless the name is one of the keywords an operand *follows*, and
		// "if ($c) then <a/> else <b/>" is two constructors rather than four
		// comparisons of the word "then". The words are exactly those that
		// end a clause or an operator and hand the grammar back to an
		// ExprSingle; every other bare word before a "<" is an operand.
		return lastSignificantOperandAware(src[:i]) == operatorPrev
	case prev == ')', prev == ']', prev == '"', prev == '\'', prev == '*':
		return false
	}
	return true
}

// operatorPrev is what a scanner passes as startsMarkup's prev when it knows
// the byte before the "<" ended an operator rather than an operand.
//
// It is "+" because "+" is the one arithmetic operator whose spelling can be
// nothing else: it is not a name byte, not a wildcard, and not the head of a
// word. Using it rather than inventing a sentinel keeps the value inside the
// domain the rest of the switch is written over.
const operatorPrev = byte('+')

// lastSignificantOperandAware returns the last significant byte of s, reduced
// to operatorPrev when that byte ended a binary operator rather than a name.
//
// The three ambiguous spellings are "-", "*" and a word operator's final
// letter. Each is distinguishable from a name by what precedes it: a "-" or a
// "*" that continues a name has a name byte immediately before it, while one
// used as an operator is separated from its left operand — "a-b" is one name
// and "a - b" is a subtraction. A trailing word is an operator when it is one
// of the words §3.5 and §3.6 spell that way, which wordOperators lists.
func lastSignificantOperandAware(s string) byte {
	i := len(s)
	for i > 0 {
		switch s[i-1] {
		case ' ', '\t', '\r', '\n':
			i--
			continue
		}
		break
	}
	if i == 0 {
		return 0
	}
	c := s[i-1]
	switch c {
	case '-', '*':
		// Preceded by whitespace or by nothing, it cannot be continuing a
		// name, so it is the binary operator. "1 * <a/>" and "3 - <a/>" reach
		// here; "a-b < 3" and "*/x < 3" do not, because the byte before is a
		// name byte or a "/".
		if i == 1 {
			return operatorPrev
		}
		switch s[i-2] {
		case ' ', '\t', '\r', '\n':
			return operatorPrev
		}
		return c
	}
	if !isNameByte(c) {
		return c
	}
	j := i
	for j > 0 && isNameByte(s[j-1]) {
		j--
	}
	if wordOperators[s[j:i]] {
		return operatorPrev
	}
	return c
}

// lastSignificant returns the last non-space byte of s, or 0 when there is
// none. It decides whether a "<" is markup or a comparison.
func lastSignificant(s string) byte {
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
		default:
			return s[i]
		}
	}
	return 0
}

func skipSpaceFrom(src string, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}
