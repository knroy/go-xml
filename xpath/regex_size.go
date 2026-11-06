package xpath

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"sync/atomic"

	"github.com/knroy/go-xml/v2/xdm"
)

// MaxRegexpProgram bounds the compiled size of a pattern, in RE2 instructions.
//
// Go refuses a nested repeat product over 1000 but not a sequence of them, so
// \pL{1000} written a hundred times (904 bytes) compiled to a program that
// retained 5 MB, and an 18 KB pattern needed 462 MB to compile. The cap is
// checked on the parsed tree before regexp.Compile builds anything.
const MaxRegexpProgram = 100000

// CompileBounded is regexp.Compile that refuses a pattern whose program would
// exceed MaxRegexpProgram instructions. The refusal wraps xdm.ErrResourceLimit.
// It is shared by the XPath and XSD pattern compilers.
//
// Where regexp also builds a onepass program, which copies a class's rune
// table into every instruction that uses it, those copies are charged too, at
// ten runes to an instruction as compileBounded counts them. An XSD facet is
// always anchored, so \A(?:\pL{990})\z passed at about 1000 instructions and
// retained 8 MB per compiled facet.
func CompileBounded(expr string) (*regexp.Regexp, error) {
	re, _, err := compileBounded(expr)
	return re, err
}

// regexCompiles counts the programs compileBounded builds: each
// regexp.Compile, and each syntax.Compile onepassEligible needs. The tests
// read it to show a pattern is compiled once, not once per call.
var regexCompiles atomic.Int64

// regexBytesPerInst is the retained size of one RE2 instruction, about 40
// bytes, and a rune of a class table is 4: hence ten runes to an instruction.
const regexBytesPerInst = 40

// compileBounded is CompileBounded that also returns the estimated bytes the
// compiled program retains, which the regex cache weighs the entry by. The
// estimate comes from the same parse as the size check, so storing a pattern
// costs no second compile.
//
// A class shared by the copies of x{n} is counted once, as the ordinary
// program shares its table, except where regexp also builds a onepass
// program: that copies the table into every instruction that uses it, so
// there each use is counted. \w translates to a Unicode-wide class, so 504
// copies of it (a 1 KB key) retain 4 MB, and ^\p{L}{990}$ retained 8 MB.
func compileBounded(expr string) (*regexp.Regexp, int, error) {
	p, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		// regexp.Compile fails at this same parse, with this same error.
		return nil, 0, err
	}
	s := p.Simplify()
	n := progSize(s, map[*syntax.Regexp]int{})
	onepass := n <= MaxRegexpProgram && onepassEligible(s, n)
	runes := classRunes(s, map[*syntax.Regexp]int{}, onepass) / 10
	if n > MaxRegexpProgram || onepass && n+runes > MaxRegexpProgram {
		return nil, 0, fmt.Errorf("regular expression compiles to more than %d instructions: %w",
			MaxRegexpProgram, xdm.ErrResourceLimit)
	}
	regexCompiles.Add(1)
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, 0, err
	}
	return re, (n + runes) * regexBytesPerInst, nil
}

// progSize estimates the instruction count of a simplified tree. Simplify
// shares the operand of x{n}, so results are memoised by node to keep the walk
// linear in the pattern rather than in the expansion. Sums are clamped so a
// hostile pattern cannot overflow them.
func progSize(re *syntax.Regexp, memo map[*syntax.Regexp]int) int {
	if n, ok := memo[re]; ok {
		return n
	}
	n := 1
	switch re.Op {
	case syntax.OpLiteral:
		n = len(re.Rune)
	case syntax.OpConcat, syntax.OpAlternate:
		for _, s := range re.Sub {
			n += progSize(s, memo)
			if n > MaxRegexpProgram*2 {
				n = MaxRegexpProgram * 2
				break
			}
		}
	case syntax.OpCapture, syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		n += 1 + progSize(re.Sub[0], memo)
	}
	memo[re] = n
	return n
}

// onepassMaxInst is the program length from which regexp's makeOnePass
// declines to build a onepass program.
const onepassMaxInst = 1000

// onepassEligible reports whether regexp may build a onepass program for s,
// whose progSize is size: one anchored at the start of the text and under
// onepassMaxInst instructions, which are the conditions regexp's
// compileOnePass checks before it tries.
//
// Two cheap tests come first, so that only a candidate is compiled. progSize
// counts at most three per instruction the program holds (two for a
// repetition that compiles to one, one for a concatenation, which compiles to
// none and has two operands), so a size of three times the limit is a program
// over it. And compileOnePass needs the first instruction to be \A, which
// only a tree that begins with OpBeginText can compile to.
func onepassEligible(s *syntax.Regexp, size int) bool {
	if size >= 3*onepassMaxInst || !leadsWithBeginText(s) {
		return false
	}
	regexCompiles.Add(1)
	prog, err := syntax.Compile(s)
	return err == nil && len(prog.Inst) < onepassMaxInst &&
		prog.StartCond()&syntax.EmptyBeginText != 0
}

// leadsWithBeginText reports whether the first instruction s compiles to may
// be \A. It follows the operand a program starts with: the first of a
// concatenation, a plus, which compiles its operand first, and a capture,
// which is conservative (its first instruction is the capture itself).
func leadsWithBeginText(s *syntax.Regexp) bool {
	for {
		switch s.Op {
		case syntax.OpBeginText:
			return true
		case syntax.OpConcat, syntax.OpCapture, syntax.OpPlus:
			if len(s.Sub) == 0 {
				return false
			}
			s = s.Sub[0]
		default:
			return false
		}
	}
}

// classRunes sums the rune tables of the classes in re. With perUse false a
// node that Simplify shares between the copies of x{n} is counted once;
// with perUse true it is counted at every use.
func classRunes(re *syntax.Regexp, memo map[*syntax.Regexp]int, perUse bool) int {
	if n, ok := memo[re]; ok {
		if perUse {
			return n
		}
		return 0
	}
	n := 0
	if re.Op == syntax.OpCharClass {
		n = len(re.Rune)
	}
	for _, s := range re.Sub {
		n += classRunes(s, memo, perUse)
	}
	memo[re] = n
	return n
}
