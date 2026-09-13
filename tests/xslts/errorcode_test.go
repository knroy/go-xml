package xslts

import (
	"errors"
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestErrorCodeJudgingPrefersTheOutermostCode pins the three shapes the
// <error> branch of judgeIn has to tell apart, and the reason it cannot simply
// compare xdm.ErrorCode(terr) against the expected code.
//
// The shapes are not hypothetical: each row is taken from a case in the XSLT
// 3.0 suite, and the middle one is why a strict swap drops 356 cases.
func TestErrorCodeJudgingPrefersTheOutermostCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		// want is the code a correct judge must read out of err.
		want string
		// structured is what xdm.ErrorCode alone answers, recorded to show
		// where the two part company.
		structured string
	}{{
		// The ordinary shape: one structured error, no wrapping.
		name:       "structured",
		err:        xdm.Errorf("XPTY0004", "wrong type"),
		want:       "XPTY0004",
		structured: "XPTY0004",
	}, {
		// as-1602, variable-0115, sequence-0132, avt-3201, error-0340c. The
		// engine's verdict is the OUTER code; the inner cause is still an
		// *xdm.Error, so errors.As reaches it first and ErrorCode answers with
		// the wrong one. Judging on ErrorCode alone fails a correct engine.
		name: "outer code wrapping an inner cause",
		err: fmt.Errorf("%s: %s: %w", "XTTE0505", "result of template temp",
			xdm.Errorf("FORG0001", "invalid xs:double value %q", "hello")),
		want:       "XTTE0505",
		structured: "FORG0001",
	}, {
		// The 351-case family: fmt.Errorf with the code in a trailing
		// parenthetical and no structured code anywhere. ErrorCode returns "".
		name:       "code only as a trailing parenthetical",
		err:        errors.New(`attribute "as" is not allowed on xsl:call-template (XTSE0090)`),
		want:       "",
		structured: "",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := xdm.ErrorCode(c.err); got != c.structured {
				t.Fatalf("xdm.ErrorCode = %q, want %q (the premise of this test moved)",
					got, c.structured)
			}
			// The judge's own rule: the leading code wins when there is one.
			var got string
			if code, _, ok := cut(c.err.Error()); ok && isErrorCode(code) {
				got = code
			}
			if got != c.want {
				t.Errorf("leading code = %q, want %q", got, c.want)
			}
		})
	}
}

func cut(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
