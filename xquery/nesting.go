package xquery

// The nesting stack the two ExprSingle scanners share.
//
// An ExprSingle has no closing delimiter, so both scanners find its end by
// walking the source and stopping at the first bare clause keyword that is not
// part of something nested inside the expression. Deciding "not part of" is
// the whole problem: "return", "else" and a comma each close a different kind
// of construct, and which one they close depends on the ORDER the open
// constructs were entered in, not on how many of each are open.
//
// Flat counters cannot represent that order. The two scanners each carried a
// pair of them -- an "open FLWORs" count beside a "conditionals awaiting else"
// count in scanToStop, an "open FLWORs" count beside a one-word "the last word
// was then/else" flag in scanExprSingleSource -- and both misread the same
// interleavings. In
//
//	if ($a) then let $r := 1 let $e := 2 return $r else 9
//
// the branch flag is true at the first "let" and false at the second, because
// a word other than then/else came between them, so the second "let" read as a
// clause of an enclosing FLWOR and cut the branch there. In
//
//	let $q := 1 return if ($q) then let $r := 1 let $e := 2 return $r else 9
//
// the counters in the other scanner mis-paired the "return" for the same
// reason. Both are one bug: the scanner knew how many constructs were open but
// not which one was innermost.
//
// A stack records that. Each entry says which construct opened the level, so a
// keyword closes the innermost level when that level is of the kind it closes,
// and is otherwise a stop -- nothing nested is open to claim it. The one thing
// the stack cannot say is that an expression has not begun yet, at the start
// of a scan or just after a "then" or an "else"; a binding keyword there opens
// the branch's own FLWOR rather than continuing an enclosing clause list, and
// each scanner carries a "branchHead" flag for it.

// nestKind is the construct that opened one level of the nesting stack.
type nestKind uint8

const (
	// nestFLWOR is a FLWOR or a quantified expression opened by one of the
	// binding keywords. Its "return" (or "satisfies") closes it.
	//
	// Consecutive clauses of one FLWOR do not each open a level: "let $a := 1
	// let $b := 2 return .." is one FLWOR with one "return", so a binding
	// keyword only opens a level when no FLWOR level is already innermost.
	nestFLWOR nestKind = iota
	// nestCond is a conditional between its "then" and its "else". Its "else"
	// closes it.
	nestCond
)

// nestStack is the stack of constructs open at the current scan position.
type nestStack []nestKind

// innermost reports the kind of the innermost open construct, and whether
// there is one at all.
func (s nestStack) innermost() (nestKind, bool) {
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)-1], true
}

// push opens a level.
func (s *nestStack) push(k nestKind) { *s = append(*s, k) }

// openBinding records a binding keyword.
//
// It opens a FLWOR level only when one is not already innermost, because the
// clauses of a single FLWOR share one "return": "let $a := 1 let $b := 2
// return .." must leave one level open, not two, or the "return" closes only
// the inner of them and the scan runs past the end of the expression.
//
// This is what the old flat counters got wrong from the other side. They
// counted every binding keyword, so consecutive clauses over-counted; the
// branch flag was the patch that stopped them counting the second and later
// clauses, and it worked only while no other word intervened.
func (s *nestStack) openBinding() {
	if k, ok := s.innermost(); ok && k == nestFLWOR {
		return
	}
	s.push(nestFLWOR)
}

// closeReturn reports whether a "return" (or a "satisfies") here closes a
// FLWOR nested inside the expression rather than ending the expression.
//
// The innermost level has to be that FLWOR, not merely some level: with a
// conditional innermost the "return" is the enclosing clause's and ends the
// scan. In practice a "return" cannot be reached with a conditional innermost
// -- a then-branch holding one has opened a FLWOR of its own on top, and a
// "then" with a bare "return" after it is ungrammatical -- but the check is
// what makes the answer true by construction rather than by that argument.
func (s *nestStack) closeReturn() bool {
	if k, ok := s.innermost(); !ok || k != nestFLWOR {
		return false
	}
	*s = (*s)[:len(*s)-1]
	return true
}

// openThen records a "then", which opens a conditional awaiting its "else".
func (s *nestStack) openThen() { s.push(nestCond) }

// closeElse reports whether an "else" here closes a conditional opened within
// the scan rather than ending the expression.
//
// The conditional has to be innermost. A FLWOR opened in the then-branch is
// closed by its own "return" before the "else" is reached -- a branch is an
// ExprSingle, and "then let $r := 1 else" is not one -- so a FLWOR level
// above the conditional cannot survive to here.
//
// With no conditional innermost the "else" is an enclosing conditional's,
// whose "then" this scan never saw, and it ends the expression.
func (s *nestStack) closeElse() bool {
	if k, ok := s.innermost(); !ok || k != nestCond {
		return false
	}
	*s = (*s)[:len(*s)-1]
	return true
}

// closesComma reports whether a comma here separates the bindings of a FLWOR
// nested inside the expression rather than the items of the enclosing Expr.
//
// One clause may bind several variables -- "let $i := 1, $j := 2" -- so with a
// FLWOR open the comma is part of this expression. With none open, an
// ExprSingle by definition contains no top-level comma, so it is a boundary.
func (s nestStack) closesComma() bool {
	_, ok := s.innermost()
	return ok
}
