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
func cachedProgramTooBig(re *regexp.Regexp) bool {
	p, err := syntax.Parse(re.String(), syntax.Perl)
	return err == nil && progSize(p.Simplify(), map[*syntax.Regexp]int{}) > maxCachedProgram
}
