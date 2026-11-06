package xpath

import (
	"fmt"
	"regexp"
	"regexp/syntax"

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
func CompileBounded(expr string) (*regexp.Regexp, error) {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		// Let regexp.Compile report it, so the message is the usual one.
		return regexp.Compile(expr)
	}
	if n := progSize(re.Simplify(), map[*syntax.Regexp]int{}); n > MaxRegexpProgram {
		return nil, fmt.Errorf("regular expression compiles to more than %d instructions: %w",
			MaxRegexpProgram, xdm.ErrResourceLimit)
	}
	return regexp.Compile(expr)
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

// maxCachedProgram is the largest program, in estimated RE2 instructions, that
// the regex cache will retain.
const maxCachedProgram = 4096

// cachedProgramTooBig reports whether re is too large to cache. It re-parses
// the compiled source, which is cheap beside the compile that just ran.
//
// progSize counts a character class as one instruction, but each class keeps
// its own rune table: \w translates to a Unicode-wide class, so 504 copies of
// it (a 1 KB key) passed the gate and retained 4 MB per entry. The class
// payload is therefore added, at ten runes to an instruction (a rune is 4
// bytes, an instruction about 40).
//
// A shared class is counted once, as the ordinary program shares its table,
// except where regexp also builds a onepass program: that copies the table
// into every instruction that uses it, so there each use is counted. Counted
// once, ^\p{L}{990}$ passed the gate and retained 8 MB per entry, and 1024
// such entries could pin 8 GB in the process-wide cache.
func cachedProgramTooBig(re *regexp.Regexp) bool {
	p, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return false
	}
	s := p.Simplify()
	return progSize(s, map[*syntax.Regexp]int{})+
		classRunes(s, map[*syntax.Regexp]int{}, onepassEligible(s))/10 > maxCachedProgram
}

// onepassEligible reports whether regexp may build a onepass program for s:
// one anchored at the start of the text and under 1000 instructions, which
// are the conditions regexp's compileOnePass checks before it tries.
func onepassEligible(s *syntax.Regexp) bool {
	prog, err := syntax.Compile(s)
	return err == nil && len(prog.Inst) < 1000 &&
		prog.StartCond()&syntax.EmptyBeginText != 0
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
