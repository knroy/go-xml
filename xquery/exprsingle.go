package xquery

import (
	"sort"
	"strings"
)

// The XQuery-only expression forms.
//
// XQuery 3.1 [42] ExprSingle is XPath's ExprSingle plus a handful of
// alternatives the expression language does not have: switch, typeswitch,
// try/catch, validate, the extension expression, ordered and unordered, and
// the string constructor. Every one of them is recognised here rather than in
// xpath, for the reason the whole package exists: the expression parser is at
// 100% conformance and is not touched.
//
// They are recognised at the head of a query-body item, which is where the
// grammar puts them and where the tests put them: each is an ExprSingle, so it
// is a whole item of a comma-separated sequence, and the return clauses that
// follow are themselves ExprSingle and are parsed here in turn. What is *not*
// reached is one of these nested inside an ordinary XPath expression —
// "1 + (switch ...)" — because that inner text goes to xpath as a substring.
// A parenthesised sub-expression would have to be re-entered to fix that, and
// the suite does not ask: every case in prod-SwitchExpr, prod-TypeswitchExpr
// and prod-TryCatchExpr writes the construct at the head of an ExprSingle.

// parseXQueryOnly parses one of the XQuery-only expression forms if one starts
// at the current position, reporting whether it did.
//
// Each keyword is checked the way parseComputed checks its own: none of them
// is reserved, so "switch" alone is a legal name and "try(1)" is a legal
// function call. The keyword only commits when what follows it can only be the
// construct — a parenthesis for switch and typeswitch, a brace for try,
// ordered, unordered and validate.
func (p *parser) parseXQueryOnly() (node, bool, error) {
	if p.lookingAt("``[") {
		n, err := p.parseStringConstructor()
		return n, true, err
	}
	if p.lookingAt("(#") {
		n, err := p.parseExtension()
		return n, true, err
	}
	switch p.peekKeyword() {
	case "if":
		return p.parseIf()
	case "try":
		return p.parseTryCatch()
	case "switch":
		return p.parseSwitch()
	case "typeswitch":
		return p.parseTypeswitch()
	case "ordered", "unordered":
		return p.parseOrderedUnordered()
	case "validate":
		return p.parseValidate()
	case "function":
		// Only an inline function whose body this package has to read; see
		// parseInlineFunc, which hands the ordinary ones back to xpath.
		return p.parseInlineFunc()
	}
	return nil, false, nil
}

// scanExprSingle takes the run of source that forms one ExprSingle and hands
// it to the item parser.
//
// The boundary is what makes this work without a token stream: an ExprSingle
// ends at a top-level comma, and inside one of these constructs it also ends
// at the keyword that begins the next clause — "case", "default" and "catch".
// Those keywords cannot appear at nesting depth zero inside an expression:
// "case" is a reserved function name in XQuery, so nothing else at depth zero
// can be spelled that way. stops names the ones this caller has to stop at.
func (p *parser) scanExprSingle(stops ...string) (node, error) {
	p.skipSpaceAndComments()
	if n, ok, err := p.parseXQueryOnly(); ok || err != nil {
		return n, err
	}
	if n, ok, err := p.parseConstructorItem(); ok || err != nil {
		return n, err
	}
	start := p.pos
	end, err := p.scanToStop(stops)
	if err != nil {
		return nil, err
	}
	src := strings.TrimSpace(p.src[start:end])
	p.pos = end
	if src == "" {
		return nil, p.errorAt(start, "XPST0003: expected an expression")
	}
	if needsXQueryParser(src) {
		c, err := p.parseFromSource(src)
		if err != nil {
			return nil, err
		}
		return &enclosed{items: c.items}, nil
	}
	c, err := p.compileExpr(src)
	if err != nil {
		return nil, err
	}
	return &enclosed{expr: c}, nil
}

// parseConstructorHere parses the direct constructor beginning at the cursor,
// leaving the cursor after it. It is what a scanner uses to step over one:
// only a parse can find where a constructor ends.
func (p *parser) parseConstructorHere() (node, error) {
	switch {
	case p.lookingAt("<!--"):
		return p.parseDirComment()
	case p.lookingAt("<?"):
		return p.parseDirPI()
	case p.lookingAt("<"):
		return p.parseDirElement()
	}
	return nil, p.errorf("XPST0003: expected a direct constructor")
}

// parseConstructorItem parses a constructor if one starts here, so that a
// return clause may be one: "case 1 return <a/>" is ordinary.
func (p *parser) parseConstructorItem() (node, bool, error) {
	switch {
	case p.lookingAt("<!--"):
		n, err := p.parseDirComment()
		return n, true, err
	case p.lookingAt("<?"):
		n, err := p.parseDirPI()
		return n, true, err
	case p.lookingAt("<"):
		n, err := p.parseDirElement()
		return n, true, err
	}
	return p.parseComputed()
}

// scanToStop returns the offset at which the expression starting at p.pos
// ends: a top-level comma, one of the stop keywords at nesting depth zero, or
// the end of the source.
//
// Brackets, braces, parentheses, string literals and comments are all stepped
// over so that a comma or a keyword inside any of them is not a boundary.
// Direct constructors are the one thing this cannot step over with a depth
// count, because "<" is also less-than; they are handled by the caller, which
// reaches a constructor before it ever reaches here.
func (p *parser) scanToStop(stops []string) (int, error) {
	i := p.pos
	depth := 0
	// nest records which constructs are open at this point of the scan, and
	// in what order. A FLWOR is not one of the forms parseXQueryOnly parses
	// ahead of this scan, so an unparenthesised one is scanned over here, and
	// its "return" belongs to it rather than to whatever enclosed the caller;
	// likewise the "else" of a nested "if" is not the one that ends this
	// branch. Which of the two a keyword closes depends on which is innermost,
	// which a pair of counters cannot say -- see nesting.go.
	var nest nestStack
	// branchHead records that the last thing read was a "then" or an "else",
	// so that the word after it begins a branch. Only a binding keyword
	// consults it, and only to open the branch's own FLWOR rather than end
	// the scan.
	branchHead := false
	for i < len(p.src) {
		c := p.src[i]
		// A literal, a comment, a pragma or a string constructor holds no
		// stop keyword and no bracket this scan should count. A pragma's
		// contents in particular are unparsed text, so a comma or a quote
		// written inside one must not end the expression.
		if end, ok, err := skipNonSyntax(p.src, i); ok {
			if err != nil {
				return 0, err
			}
			i = end + 1
			branchHead = false
			continue
		}
		// Whitespace between an "else" and the branch it introduces is not a
		// thing read, so it alone leaves branchHead standing. A word clears
		// it after being handled -- the word the flag is about is the one
		// being read now -- and everything else clears it here.
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' && !isNameByte(c) {
			branchHead = false
		}
		switch c {
		case '(':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return i, nil
			}
		case '[', '{':
			depth++
		case ',':
			if depth == 0 && !nest.closesComma() {
				return i, nil
			}
			// With a FLWOR still open the comma separates that FLWOR's own
			// bindings rather than the items of an enclosing Expr: one clause
			// may bind several variables, as the sudoku demo writes
			//
			//	then let $index as xs:integer := ..,
			//	         $possibleValues as xs:integer* := ..
			//	     return ..
			//
			// Ending the scan here cut the branch mid-clause and reported
			// XPST0003 at the "return" left unread. The "return" that closes
			// the FLWOR is already counted below, so the comma follows the
			// same nesting rule the keywords do.
		default:
			if depth == 0 && len(stops) > 0 && isNameByte(c) &&
				(i == p.pos || !isNameByte(p.src[i-1])) {
				// The whole word is taken at once, so that a keyword's tail
				// is never rescanned as a word of its own.
				j := i
				for j < len(p.src) && isNameByte(p.src[j]) {
					j++
				}
				w := p.src[i:j]
				// head is branchHead for the word after this one: set only
				// by "then" and "else", cleared by every other word.
				head := false
				switch {
				// A binding keyword opens a FLWOR whose own "return" will
				// close it. startsBindingClause tells the clause from a call
				// to a function of the same name: none of the four is
				// reserved, so "let(1)" binds nothing and opens nothing. It
				// answers only about the text after the word, so the word
				// itself is checked here -- it takes "return $v" for a
				// binding otherwise.
				//
				// openBinding folds a clause onto a FLWOR already innermost
				// rather than opening a second level, because the clauses of
				// one FLWOR share one "return". The flat count this replaced
				// opened a level per clause, so "let $r := 1 let $e := 2
				// return $r" left a level open past the end of the branch.
				case bindingKeywords[w] && startsBindingClause(w, p.src[j:]):
					// With nothing open the keyword may still be a stop of
					// this caller's -- a clause of the enclosing FLWOR.
					//
					// Not at the head of an expression, though: a clause
					// continues a clause list, and at the start of the scan
					// or just after a "then" or an "else" there is no
					// expression before this one for it to continue. A FLWOR
					// written there opens the branch instead, which is how
					// the last branch of a conditional or a typeswitch is
					// commonly written -- "else let $c0 := 1 ... return ..",
					// RexParser's p:parse. Stopping at the start of the scan
					// returned an empty ExprSingle; stopping after an "else"
					// cut the branch and left the rest of it unread.
					if _, open := nest.innermost(); !open && i > p.pos &&
						!branchHead {
						for _, kw := range stops {
							if w == kw {
								return i, nil
							}
						}
					}
					nest.openBinding()
				// A "return" or a "satisfies" closes the innermost FLWOR
				// still open. Only when the innermost construct is not a
				// FLWOR -- or nothing is open -- does it belong to whatever
				// enclosed the caller, and end this expression.
				case (w == "return" || w == "satisfies") && nest.closeReturn():
				// A "then" opens a conditional whose own "else" closes it.
				// Without this the then-branch of an outer conditional ended
				// at the first "else" it met, which belongs to a nested "if":
				//
				//	then let $i := .. return if (..) then () else if (..) ..
				//
				// is the sudoku demo's populateValues, and the branch was cut
				// after "then ()", leaving the outer "else" unread and
				// reported as XPST0003. "then" needs no shape test of its own
				// the way the binding keywords do, because it can only ever
				// follow an "if (..)".
				case w == "then":
					nest.openThen()
					head = true
				case w == "else":
					// An "else" closes a conditional opened within this
					// scan. With none open it is an enclosing conditional's
					// and ends the expression, whether or not the caller
					// named it a stop -- a bare "else" cannot otherwise
					// follow a complete expression, so there is nothing else
					// it can be. Running on past it swallowed the caller's
					// own else branch, which is the "unexpected \"else\"
					// after expression" RexParser's p:parse reported.
					if !nest.closeElse() {
						return i, nil
					}
					head = true
				default:
					for _, kw := range stops {
						if w == kw {
							// A clause keyword is not reserved, so a bare one
							// may still be a name: "empty($stack)" is a call
							// to fn:empty, not the order-by modifier it is
							// spelled like, and RexParser's p:parse writes
							// exactly that. wordIsName judges by what follows
							// and needs the cursor on the word's end.
							//
							// Only the clause keywords are asked about. The
							// stops a caller adds -- "case", "default",
							// "catch" -- are the keywords of the construct
							// being parsed, and each is followed by an
							// expression or a type, so nothing after one
							// tells a keyword from a name; wordIsName has no
							// arm for them and its fallthrough, written for
							// the modifiers, took "case ()" for a call. What
							// settles them instead is that a bare one cannot
							// follow a complete expression at depth zero:
							// "case" is a reserved function name, so nothing
							// else there can be spelled that way.
							if stopWords[w] {
								probe := *p
								probe.pos = j
								if probe.wordIsName(w) {
									break
								}
							}
							return i, nil
						}
					}
				}
				branchHead = head
				i = j
				continue
			}
		}
		i++
	}
	return i, nil
}

// enclosingClauseStops are the keywords that end an ExprSingle because they
// begin a clause of an enclosing FLWOR, rather than anything belonging to the
// construct being parsed.
//
// The last branch of a typeswitch, a switch or an if has no sibling keyword
// after it -- there is no clause left inside the construct -- so it used to be
// scanned with no stops at all and ran to the end of the source. That is right
// only when the construct is the whole expression. Written as the value of a
// clause, as in
//
//	let $v as xs:string := typeswitch(...) ... default return "no" return $v
//
// the trailing "return $v" is the let's, and swallowing it left
// '"no" return $v' to be compiled as one expression, which is the XPST0003
// letexprwith-24 and whereClause-5 report.
//
// bindingKeywords are the four words that can open a FLWOR or a quantified
// expression by binding a variable.
//
// startsBindingClause is about the text after such a word and takes the word
// on trust, so a caller that has not already established which word it holds
// must say so here: "return $v" has the shape of a binding and is not one.
var bindingKeywords = map[string]bool{
	"for": true, "let": true, "some": true, "every": true,
}

// A "return" is the enclosing clause's only when the branch has not opened a
// FLWOR of its own. parseXQueryOnly does not handle a FLWOR, so one written
// unparenthesised in the branch --
//
//	typeswitch(1) case $i as xs:string return "s" default return let $q := 5 return $q
//
// reaches this scan itself, and its "return" closes its own "let". So the set
// is every stop word, and scanToStop decides per occurrence whether a word
// closes something the branch opened or belongs to the enclosing clause; only
// it knows the nesting, and it keeps a stack rather than a count so that it
// knows the order too. See nesting.go.
//
// The binding keywords used to be left out of this set, because either may
// open that nested FLWOR and a flat count could not tell the two uses apart.
// The stack can: with nothing open, a bare "let" at depth zero is the
// enclosing FLWOR's next clause and ends the branch. Leaving them out is what
// let the last branch of a conditional swallow the clause after it --
//
//	let $c1 := if (..) then 1 else if (..) then let $x := .. return $x else 0
//	let $current := ..
//
// is RexParser's p:transition, whose "else 0" ran on through the following
// "let" to the end of the function body.
var enclosingClauseStops = func() []string {
	stops := make([]string, 0, len(stopWords))
	for w := range stopWords {
		stops = append(stops, w)
	}
	sort.Strings(stops)
	return stops
}()

// consumeKeyword consumes a keyword if it is the next thing, whitespace and
// comments having been skipped first. A keyword is only a keyword when it is
// not the beginning of a longer name.
func (p *parser) consumeKeyword(kw string) bool {
	save := p.pos
	p.skipSpaceAndComments()
	if hasKeywordPrefix(p.src[p.pos:], kw) {
		p.pos += len(kw)
		return true
	}
	p.pos = save
	return false
}

// parseBracedExprSingle parses "{ Expr }" as an expression rather than as
// constructor content.
//
// try, catch, ordered, unordered and validate all take an EnclosedExpr, whose
// body is a whole Expr — a comma-separated sequence — and whose value is the
// value of that expression rather than a constructed tree. The braces are
// found the same way an enclosed expression's are, and the body is parsed as a
// query body so that it may itself hold constructors and further XQuery-only
// forms.
func (p *parser) parseBracedExprSingle() (*enclosed, error) {
	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		return nil, p.errorf("XPST0003: expected %q", "{")
	}
	end, err := findEnclosed(p.src, p.pos)
	if err != nil {
		return nil, err
	}
	body := p.src[p.pos+1 : end]
	p.pos = end + 1
	if strings.TrimSpace(body) == "" {
		return &enclosed{}, nil
	}
	inner := &parser{src: body, sc: p.sc, version: p.version}
	items, err := inner.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return &enclosed{items: items}, nil
}
