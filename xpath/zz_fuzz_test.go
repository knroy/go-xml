package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Compile must never panic, whatever the input. It is reachable from any
// stylesheet or query, so a panic is a denial of service for an embedder.
func FuzzCompileNoPanic(f *testing.F) {
	for _, s := range []string{
		`1 + 2`, `/a/b[c=1]`, `for $x in 1 to 3 return $x`,
		`if (a) then b else c`, `'s' cast as xs:QName`,
		`3 treat as item()`, `count(//*)`, `(: c :)1`,
		`a//b/@c`, `xs:integer('1')`, `matches('a','b','x')`,
		`some $x in (1,2) satisfies $x eq 1`, `1e5`, `.5`,
		`declare`, `element(a,b)`, `$v`, `'a' || 'b'`,
	} {
		f.Add(s)
	}
	ns := testResolver{"xs": xdm.NSXS}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 400 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile(%q) panicked: %v", src, r)
			}
		}()
		c, err := Compile(src, ns)
		if err != nil {
			// Every parse error must carry a spec code, not a bare message.
			if code := xdm.ErrorCode(err); code == "" {
				t.Fatalf("Compile(%q) error without a code: %v", src, err)
			}
			return
		}
		// A compiled expression must also survive evaluation without panic.
		ctx := NewContext(nil, Builtins())
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Eval(%q) panicked: %v", src, r)
				}
			}()
			_, _ = c.Eval(ctx)
		}()
		_ = strings.TrimSpace(src)
	})
}
