package xquery

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// TestFormatNumberPrefixFromConstructor covers the prefix in fn:format-number's
// third argument being bound by a direct element constructor rather than by the
// prolog.
//
// §3.9.1.3 puts an xmlns declaration attribute into the statically known
// namespaces of the element's content, and §4.7 resolves the format name
// against those. The function is registered as a closure over the Query, so by
// the time it runs the constructor's static context has gone out of scope and
// only the module's prefixes remain -- which is why the suite's eqname-007
// came back "the prefix ex is not bound to a namespace" for a prefix the
// constructor enclosing the call does bind.
//
// The prolog-declared spelling is tested alongside it because it always
// worked: it is what shows the failure was the constructor binding being lost
// and not the format lookup itself.
func TestFormatNumberPrefixFromConstructor(t *testing.T) {
	const decl = `declare decimal-format ` +
		`Q{http://www.example.com/ns}format grouping-separator="'"; `

	tests := []struct {
		name  string
		query string
	}{{
		// eqname-007 itself.
		name: "prefix bound by the enclosing constructor",
		query: decl + `<a xmlns:ex="http://www.example.com/ns">` +
			`{format-number(1e9, "#'###'###'##0.00", 'ex:format')}</a>`,
	}, {
		name: "prefix bound by the prolog",
		query: `declare namespace ex = "http://www.example.com/ns"; ` +
			decl + `<a>{format-number(1e9, "#'###'###'##0.00", 'ex:format')}</a>`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Compile(tt.query, Options{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			// The declared format groups with an apostrophe, so reaching it
			// is visible in the result: the default format would have used a
			// comma, and a missing one is an error rather than a value.
			const want = "1'000'000'000.00"
			if len(seq) != 1 {
				t.Fatalf("got %d items, want 1", len(seq))
			}
			// The result is the constructed <a> element, so its string value
			// is what the enclosed format-number produced.
			el, ok := seq[0].(*xdm.Node)
			if !ok {
				t.Fatalf("got %T, want an element node", seq[0])
			}
			got := el.StringValue()
			if !strings.Contains(got, want) {
				t.Errorf("format-number = %q, want a result containing %q",
					got, want)
			}
		})
	}
}

// TestFormatNumberUnboundPrefixStillFails is the other side of the fallback:
// recording every constructor prefix in the module must not make an genuinely
// unbound prefix resolve. A prefix nothing binds is still FODF1280.
func TestFormatNumberUnboundPrefixStillFails(t *testing.T) {
	q, err := Compile(`declare decimal-format `+
		`Q{http://www.example.com/ns}format grouping-separator="'"; `+
		`<a xmlns:ex="http://www.example.com/ns">`+
		`{format-number(1e9, "#'###'###'##0.00", 'nope:format')}</a>`,
		Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := q.Eval(xpath.NewContext(nil, xpath.Builtins())); err == nil {
		t.Fatal("an unbound prefix resolved, want FODF1280")
	} else if !strings.Contains(err.Error(), "FODF1280") {
		t.Errorf("err = %v, want FODF1280", err)
	}
}
