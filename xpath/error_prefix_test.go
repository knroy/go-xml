package xpath

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// xdm.ErrCast and xdm.ErrType already carry their code, and Error() renders
// it once in front of the message. A call site that also spells the code in
// its format string renders it twice: "FORG0001: FORG0001: 999 is out of
// range for xs:byte". Every message must start with exactly one code.
func TestErrorCodeRenderedOnce(t *testing.T) {
	doubled := regexp.MustCompile(`^([A-Z]{4}[0-9]{4}): [A-Z]{4}[0-9]{4}: `)

	eval := func(expr string) error {
		t.Helper()
		ctx := NewContext(nil, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		_, err := Eval(expr, ctx, cardinalityNS{})
		return err
	}

	cases := []struct{ expr, code string }{
		{`xs:byte(999)`, "FORG0001"},
		{`xs:dateTimeStamp("2020-01-01T00:00:00")`, "FORG0001"},
		{`"2020-01-01T00:00:00" cast as xs:dateTimeStamp`, "FORG0001"},
		{`999 cast as xs:byte`, "FORG0001"},
		{`"1 2" cast as xs:NCName`, "FORG0001"},
		{`1(2)`, "XPTY0004"},
		{`"" cast as xs:NMTOKENS`, "FORG0001"},
		{`serialize((), map{'use-character-maps': 1})`, "XPTY0004"},
	}
	for _, c := range cases {
		err := eval(c.expr)
		if err == nil {
			t.Errorf("%s: expected an error", c.expr)
			continue
		}
		msg := err.Error()
		if got := xdm.ErrorCode(err); got != c.code {
			t.Errorf("%s: code %q, want %q (%s)", c.expr, got, c.code, msg)
		}
		if !strings.HasPrefix(msg, c.code+": ") {
			t.Errorf("%s: message %q does not start with %q", c.expr, msg, c.code)
		}
		if doubled.MatchString(msg) {
			t.Errorf("%s: code rendered twice: %q", c.expr, msg)
		}
	}
}

// The eight expressions above reach a handful of the sites; the shape has 29
// and any new helper call can add another. This scans the package source for
// a code spelled inside an ErrCast or ErrType format string, which is the one
// way the doubling can come back, so every site is pinned and not only the
// ones an expression happens to reach.
func TestNoErrorHelperSpellsItsOwnCode(t *testing.T) {
	doubled := regexp.MustCompile(`xdm\.Err(?:Cast|Type)\("[A-Z]{4}[0-9]{4}: `)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if doubled.MatchString(line) {
				t.Errorf("%s:%d spells a code the helper already adds: %s",
					f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
