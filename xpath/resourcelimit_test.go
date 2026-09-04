package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The specs give no error code for "this processor gave up", so every resource
// limit in the engine borrows a semantic one -- and each of the codes below is
// a poor description of what actually happened:
//
//   - XPDY0001 means "no context item is defined". A recursion cap has a
//     context; it is deep.
//   - XPST0003 means "the expression is syntactically invalid". A type nested
//     past the parser's cap is perfectly well-formed.
//   - FORX0002 means "invalid regular expression". A pattern that exhausts the
//     backtracking budget is valid; the budget is what ran out.
//   - FOAR0002 means "numeric overflow or underflow". Nothing overflowed when
//     a range bound is refused; the enumeration was declined.
//
// The codes are nonetheless kept, because callers and the conformance suites
// read them and changing one is spec-visible. xdm.ErrResourceLimit is ADDED
// alongside, so a caller can tell the two conditions apart without reading
// prose. This test pins both halves at once: the sentinel must hold AND the
// code and the leading message must survive.
func TestResourceLimitsCarrySentinelAndKeepTheirCode(t *testing.T) {
	tests := []struct {
		name     string
		eval     func() error
		wantCode string
		wantText string
	}{
		{
			// xpath/context.go, Context.Descend.
			"evaluation depth", func() error {
				ctx := NewContext(nil, Builtins())
				ctx.MaxDepth = 2
				c, err := ctx.Descend()
				if err != nil {
					return err
				}
				if c, err = c.Descend(); err != nil {
					return err
				}
				_, err = c.Descend()
				return err
			},
			"XPDY0001", "recursion exceeded 2 levels",
		},
		{
			// xpath/parser_path.go, parseSequenceType. The exact twin of
			// the expression-nesting guard in parser.go, which 1140493
			// wrapped; a type nests through a path parseExprSingle's own
			// counter never sees.
			"type nesting", func() error {
				_, err := Parse("1 instance of "+
					strings.Repeat("(", 2000)+"item()"+
					strings.Repeat(")", 2000), nil)
				return err
			},
			"XPST0003", "type nesting exceeds",
		},
		{
			// xpath/regex_backtrack.go, errBacktrackBudget. The pattern
			// is valid and may well match; the engine simply declined to
			// keep enumerating choices.
			"regex backtracking budget", func() error {
				old := BacktrackingRegexEnabled()
				SetBacktrackingRegex(true)
				defer SetBacktrackingRegex(old)
				ctx := NewContext(nil, Builtins())
				ctx.Version = XPath31
				_, err := Eval(`matches("`+strings.Repeat("a", 60)+
					`", "(a*)*\1b")`, ctx, nil)
				return err
			},
			"FORX0002", "backtracking step budget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.eval()
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller "+
					"cannot tell this refusal from a fault in its input", err)
			}
			if code := xdm.ErrorCode(err); code != tt.wantCode {
				t.Errorf("code = %q, want %q; the wrap must ADD the sentinel, "+
					"never replace the code the suites read", code, tt.wantCode)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("message %q no longer contains %q; the ~30 sites that "+
					"match on message text would break", err, tt.wantText)
			}
		})
	}
}

// The FOAR0002 range-bound refusal is exercised through its own function
// rather than through an expression, because singleInteger currently has no
// caller: "to" reads its bounds through bigInteger, which keeps the arbitrary
// precision rather than narrowing. The site is wrapped anyway -- it is
// reachable the moment anything calls it again, and a limit that reports
// FOAR0002 without the sentinel is exactly the defect this contract exists to
// prevent from recurring.
func TestRangeBoundRefusalCarriesSentinel(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	huge, err := Parse("100000000000000000000000000", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = singleInteger(ctx, huge)
	if err == nil {
		t.Fatal("expected a refusal for a bound outside int64")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false", err)
	}
	if code := xdm.ErrorCode(err); code != "FOAR0002" {
		t.Errorf("code = %q, want FOAR0002", code)
	}
	if !strings.Contains(err.Error(), "is too large to enumerate") {
		t.Errorf("message %q lost its leading text", err)
	}
}

// A semantic fault must NOT carry the sentinel, or the distinction the
// sentinel exists to draw is worthless: a caller told "resource limit" about a
// genuine type error would retry a request that can never succeed.
func TestOrdinaryFaultsAreNotResourceLimits(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	for _, expr := range []string{
		`1 + "a"`,           // XPTY0004, a type error
		`xs:integer("zz")`,  // FORG0001, a failed cast
		`1 to (`,            // XPST0003, a genuinely malformed expression
		`matches("a", "(")`, // FORX0002, a genuinely invalid pattern
	} {
		_, err := Eval(expr, ctx, nil)
		if err == nil {
			t.Errorf("%s: expected an error", expr)
			continue
		}
		if errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: %v reports as a resource limit, but the input is "+
				"genuinely wrong; retrying it can never help", expr, err)
		}
	}
}
