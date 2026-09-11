package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONOptionKeyTypes covers which key types NAME a JSON option.
//
// The option maps of fn:parse-json, fn:json-doc and fn:json-to-xml are
// ordinary maps, so an option name arrives with whatever type the caller's
// data carried. A key read out of an unvalidated document atomises to
// xs:untypedAtomic rather than xs:string, and jsonOptionsFrom used to demand
// xdm.TypeString exactly -- so every option written with the untyped spelling
// fell through the "unknown options are ignored" branch and was silently
// dropped. duplicates, escape, validate and fallback were all disabled that
// way, while the sibling paths (fn:xml-to-json's "indent", map:merge's
// "duplicates") looked theirs up through MapKeyOf and accepted it.
//
// The rule being asserted is the one xdm/maparray.go documents for
// typeFamilyOf: xs:string, xs:anyURI and xs:untypedAtomic are one key family
// because map:get applies the function conversion rules and casts an untyped
// key to xs:string, while that cast is to string and NEVER to a number -- so
// an integer key must still name nothing. F&O 3.1 is not vendored here and the
// vendored specs carry no FOJS material, so that documented conversion rule,
// plus the two sibling paths, is the evidence.
//
// Each case is written so that the honoured and ignored states differ
// OBSERVABLY, rather than both landing on the same value: an assertion that
// cannot tell the two apart would pass against the unfixed code.
func TestJSONOptionKeyTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		// expr has one %s, the expression naming the option key.
		expr string
		// honoured reports that the option took effect; ignored reports the
		// default behaviour. Asserting both directions is what keeps a case
		// from passing when the option is silently dropped.
		honoured func(got string, err error) bool
	}{
		{
			// duplicates='reject' turns a duplicate key into FOJS0003;
			// ignored, the map is simply built and there is no error.
			name: "duplicates",
			expr: `parse-json('{"a":1,"a":2}', map{%s:'reject'})`,
			honoured: func(got string, err error) bool {
				return err != nil && strings.Contains(err.Error(), "FOJS0003")
			},
		},
		{
			// escape=true leaves the \n as the two-character escape it was
			// written as; ignored, it is unescaped to a real newline.
			name: "escape",
			expr: `parse-json('["a\nb"]', map{%s:true()})?1`,
			honoured: func(got string, err error) bool {
				return err == nil && got == `a\nb`
			},
		},
		{
			// validate=true on fn:json-to-xml flips the duplicates default
			// from "retain" to "reject", so the duplicate becomes an error;
			// ignored, the default stays "retain" and the tree is built.
			name: "validate",
			expr: `serialize(json-to-xml('{"a":1,"a":2}', map{%s:true()}))`,
			honoured: func(got string, err error) bool {
				return err != nil && strings.Contains(err.Error(), "FOJS")
			},
		},
		{
			// The fallback replaces a character the result cannot hold;
			// ignored, the default U+FFFD substitution applies instead. The
			// trigger is the one the existing fallback test uses.
			name: "fallback",
			expr: `parse-json('"\uFFFF"', map{%s:function($s){'[BAD]'}})`,
			honoured: func(got string, err error) bool {
				return err == nil && got == "[BAD]"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The string spelling is the control: it worked before this fix
			// and must keep working.
			got, err := evalJSONOptionKey(t, tc.expr, `'`+tc.name+`'`)
			if !tc.honoured(got, err) {
				t.Errorf("string-spelled %s was not honoured: got %q, err %v", tc.name, got, err)
			}
			// The untyped spelling is the regression this test exists for:
			// same key family, so it must name the same option.
			got, err = evalJSONOptionKey(t, tc.expr, `xs:untypedAtomic('`+tc.name+`')`)
			if !tc.honoured(got, err) {
				t.Errorf("untyped-spelled %s was not honoured: got %q, err %v", tc.name, got, err)
			}
			// xs:anyURI is the third member of that family.
			got, err = evalJSONOptionKey(t, tc.expr, `xs:anyURI('`+tc.name+`')`)
			if !tc.honoured(got, err) {
				t.Errorf("anyURI-spelled %s was not honoured: got %q, err %v", tc.name, got, err)
			}
			// The boundary: the cast is to string, never to a number, so an
			// integer key names no option and the option must stay unset.
			// Widening the gate to "any atomic" would make these take effect
			// and this assertion would catch it.
			got, err = evalJSONOptionKey(t, tc.expr, `12`)
			if tc.honoured(got, err) {
				t.Errorf("an integer key must not name the %s option, but it took effect: got %q, err %v",
					tc.name, got, err)
			}
		})
	}
}

// evalJSONOptionKey evaluates expr with its %s replaced by the key expression.
func evalJSONOptionKey(t *testing.T, expr, key string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	seq, err := Eval(strings.Replace(expr, "%s", key, 1), ctx, nil)
	if err != nil {
		return "", err
	}
	if len(seq) == 0 {
		return "", nil
	}
	a, ok := seq[0].(*xdm.Atomic)
	if !ok {
		return "", nil
	}
	return a.String(), nil
}
