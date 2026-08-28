package xslt

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// parseStartAt reads the effective value of xsl:number/@start-at.
//
// Section 12.1 gives the lexical form as -?[0-9]+(\s+-?[0-9]+)* and makes
// anything else an error. It is checked here rather than by a regular
// expression because the pieces are wanted as integers anyway, and a
// hand-written scan reports which piece was wrong.
func parseStartAt(v string) ([]int64, error) {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil, fmt.Errorf(
			"xsl:number/@start-at must hold at least one integer, got %q", v)
	}
	out := make([]int64, 0, len(fields))
	for _, f := range fields {
		// strconv.ParseInt accepts "+7", "0x10" and underscores, none of
		// which the production admits, so the shape is checked before the
		// value is read.
		digits := f
		if strings.HasPrefix(digits, "-") {
			digits = digits[1:]
		}
		if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
			return nil, fmt.Errorf(
				"xsl:number/@start-at: %q is not an integer", f)
		}
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"xsl:number/@start-at: %q is out of range", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// rebaseNumbers applies the start-at transformation of section 12.1 to values.
//
// The rule the specification writes as an XPath expression: the Nth start
// value re-bases the Nth number, and every number past the Nth is re-based by
// the last start value. A start sequence longer than the number sequence has
// its surplus ignored, which falls out of iterating over values rather than
// over starts.
func rebaseNumbers(values []*big.Int, starts []int64) []*big.Int {
	if len(starts) == 0 {
		return values
	}
	out := make([]*big.Int, len(values))
	for i, v := range values {
		s := starts[len(starts)-1]
		if i < len(starts) {
			s = starts[i]
		}
		out[i] = new(big.Int).Add(v, big.NewInt(s-1))
	}
	return out
}

// intsToBig lifts a list of counted level numbers into the arbitrary-precision
// values the formatter takes. Counting a tree cannot overflow an int64, but
// @value can hold any xs:integer, so the two meet in one type.
func intsToBig(ns []int64) []*big.Int {
	out := make([]*big.Int, len(ns))
	for i, n := range ns {
		out[i] = big.NewInt(n)
	}
	return out
}
