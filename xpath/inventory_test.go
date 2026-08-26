package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The function inventory is checked against the library that Builtins()
// actually returns, not against a grep of the source. An earlier audit of this
// package was done by grepping for register("...") literals and reported 31
// functions missing that were in fact present — they are registered through
// helper wrappers (dateComponent, qnamePart) whose names the grep never saw.
// Asking the library directly is the only form of this check that cannot lie
// in that direction.
//
// Each entry is "local-name/arity". A function required at two arities appears
// twice, because arity is part of the lookup key: fn:substring at 2 and at 3
// are genuinely different functions and one can exist without the other.
var fnRequired = strings.Fields(`
	node-name/1 nilled/1 string/0 string/1 data/1 base-uri/0 base-uri/1
	document-uri/1 error/0 error/1 error/2 trace/2

	abs/1 ceiling/1 floor/1 round/1 round-half-to-even/1 round-half-to-even/2
	number/0 number/1

	codepoints-to-string/1 string-to-codepoints/1 compare/2 compare/3
	codepoint-equal/2 concat/2 string-join/2 substring/2 substring/3
	string-length/0 string-length/1 normalize-space/0 normalize-space/1
	normalize-unicode/1 normalize-unicode/2 upper-case/1 lower-case/1
	translate/3 encode-for-uri/1 iri-to-uri/1 escape-html-uri/1

	contains/2 contains/3 starts-with/2 starts-with/3 ends-with/2 ends-with/3
	substring-before/2 substring-before/3 substring-after/2 substring-after/3

	matches/2 matches/3 replace/3 replace/4 tokenize/2 tokenize/3

	resolve-uri/1 resolve-uri/2 true/0 false/0 not/1

	years-from-duration/1 months-from-duration/1 days-from-duration/1
	hours-from-duration/1 minutes-from-duration/1 seconds-from-duration/1

	year-from-dateTime/1 month-from-dateTime/1 day-from-dateTime/1
	hours-from-dateTime/1 minutes-from-dateTime/1 seconds-from-dateTime/1
	timezone-from-dateTime/1

	year-from-date/1 month-from-date/1 day-from-date/1 timezone-from-date/1
	hours-from-time/1 minutes-from-time/1 seconds-from-time/1
	timezone-from-time/1

	adjust-dateTime-to-timezone/1 adjust-dateTime-to-timezone/2
	adjust-date-to-timezone/1 adjust-date-to-timezone/2
	adjust-time-to-timezone/1 adjust-time-to-timezone/2

	resolve-QName/2 QName/2 prefix-from-QName/1 local-name-from-QName/1
	namespace-uri-from-QName/1 namespace-uri-for-prefix/2 in-scope-prefixes/1

	name/0 name/1 local-name/0 local-name/1 namespace-uri/0 namespace-uri/1
	lang/1 lang/2 root/0 root/1

	boolean/1 index-of/2 index-of/3 empty/1 exists/1 distinct-values/1
	distinct-values/2 insert-before/3 remove/2 reverse/1 subsequence/2
	subsequence/3 unordered/1 zero-or-one/1 one-or-more/1 exactly-one/1
	deep-equal/2 deep-equal/3 count/1 avg/1 max/1 max/2 min/1 min/2
	sum/1 sum/2

	id/1 id/2 idref/1 idref/2 doc/1 doc-available/1 collection/0 collection/1

	position/0 last/0 current-dateTime/0 current-date/0 current-time/0
	implicit-timezone/0 default-collation/0 static-base-uri/0 dateTime/2
`)

// The XSLT 2.0 functions, which are not in the XPath library.
//
// fn:unparsed-text and the fn:format-date family are defined by the XSLT
// specification rather than the XPath one, so a plain XPath 2.0 processor is
// required to report XPST0017 for them. They are added by RegisterXSLTFuncs
// and so appear only in a stylesheet's library.
var xsltOnlyFunctions = strings.Fields(`
	unparsed-text/1 unparsed-text/2 unparsed-text-available/1
	unparsed-text-available/2
	format-dateTime/2 format-date/2 format-time/2
`)

// The constructor functions. Two are excluded: xs:NOTATION, which the spec
// forbids using as a constructor, and xs:QName, which is folded to a literal at
// parse time and so never reaches the runtime library — TestQNameConstructor
// covers it instead.
var xsRequired = strings.Fields(`
	string boolean decimal integer double float duration yearMonthDuration
	dayTimeDuration dateTime date time gYear gYearMonth gMonth gMonthDay gDay
	hexBinary base64Binary anyURI untypedAtomic
`)

func TestFunctionInventory(t *testing.T) {
	lib := Builtins()
	var missing []string

	for _, spec := range fnRequired {
		local, arity := splitArity(t, spec)
		if _, ok := lib.Lookup(xdm.QName{URI: xdm.NSFN, Local: local}, arity); !ok {
			missing = append(missing, "fn:"+spec)
		}
	}
	for _, local := range xsRequired {
		if _, ok := lib.Lookup(xdm.QName{URI: xdm.NSXS, Local: local}, 1); !ok {
			missing = append(missing, "xs:"+local+"/1")
		}
	}
	// The XSLT-only functions must be invisible to a 2.0 expression and
	// present once RegisterXSLTFuncs has run. A plain XPath 2.0 processor is
	// required to report XPST0017 for them, and a stylesheet needs them to
	// work, so the split is checked in both directions rather than assumed.
	//
	// The check is a version-aware lookup rather than a bare one because some
	// of these — the format-dateTime family — are also XPath 3.0 functions,
	// and so are registered in the builtin library marked Since XPath30. What
	// must stay true is that a *2.0* expression cannot reach them.
	ctx20 := NewContext(nil, Builtins())
	xsltLib := NewLibrary(Builtins())
	RegisterXSLTFuncs(xsltLib)
	for _, spec := range xsltOnlyFunctions {
		local, arity := splitArity(t, spec)
		qn := xdm.QName{URI: xdm.NSFN, Local: local}
		if _, ok := lookupFor(ctx20, qn, arity); ok {
			t.Errorf("fn:%s is visible to XPath 2.0; it is an XSLT function", spec)
		}
		if _, ok := xsltLib.Lookup(qn, arity); !ok {
			missing = append(missing, "fn:"+spec+" (XSLT)")
		}
	}

	if len(missing) > 0 {
		t.Errorf("%d required functions absent from the builtin library:\n\t%s",
			len(missing), strings.Join(missing, "\n\t"))
	}
}

// A lookup for an arity the function does not have must fail. Without this,
// a library that ignored arity entirely would pass TestFunctionInventory.
func TestFunctionInventoryRespectsArity(t *testing.T) {
	lib := Builtins()
	// fn:abs takes exactly one argument; there is no 3-argument form.
	if _, ok := lib.Lookup(xdm.QName{URI: xdm.NSFN, Local: "abs"}, 3); ok {
		t.Error("fn:abs#3 resolved, so arity is not part of the lookup key")
	}
	if _, ok := lib.Lookup(xdm.QName{URI: xdm.NSFN, Local: "no-such-fn"}, 1); ok {
		t.Error("an unregistered name resolved")
	}
}

func splitArity(t *testing.T, spec string) (string, int) {
	t.Helper()
	i := strings.LastIndex(spec, "/")
	if i < 0 {
		t.Fatalf("malformed inventory entry %q", spec)
	}
	n := 0
	for _, c := range spec[i+1:] {
		n = n*10 + int(c-'0')
	}
	return spec[:i], n
}

// xs:QName is deliberately absent from the runtime library: it is folded to a
// literal during parsing, because the prefix can only be resolved against the
// static context. This checks the behaviour rather than the registration.
func TestQNameConstructor(t *testing.T) {
	ns := testResolver{"p": "http://example.com/p", "xs": xdm.NSXS}
	expr, err := Parse(`xs:QName("p:local")`, ns)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lit, ok := expr.(*Literal)
	if !ok {
		t.Fatalf("expected a folded literal, got %T", expr)
	}
	q := lit.Val.QName()
	if q == nil || q.URI != "http://example.com/p" || q.Local != "local" {
		t.Fatalf("resolved to %+v, want {http://example.com/p}local", q)
	}
}

func TestQNameConstructorRejects(t *testing.T) {
	ns := testResolver{"p": "http://example.com/p", "xs": xdm.NSXS}
	for _, src := range []string{
		`xs:QName("zz:local")`, // unbound prefix
		`xs:QName(.)`,          // not a literal, so unresolvable
		`xs:QName("p:")`,       // empty local part
	} {
		if _, err := Parse(src, ns); err == nil {
			t.Errorf("%s parsed without error", src)
		}
	}
}

// testResolver binds a fixed set of prefixes.
type testResolver map[string]string

func (r testResolver) ResolvePrefix(p string) (string, bool) { u, ok := r[p]; return u, ok }
func (r testResolver) DefaultElementNamespace() string       { return "" }
func (r testResolver) DefaultFunctionNamespace() string      { return xdm.NSFN }

// The collation overloads must evaluate under the codepoint collation and
// refuse any other. Registering them at the wider arity without checking the
// argument would make fn:compare($a,$b,"...swedish") silently return ASCII
// order, which is the failure this guards against.
func TestCollationOverloads(t *testing.T) {
	const cp = `"` + CodepointCollation + `"`
	ns := testResolver{"xs": xdm.NSXS}

	ok := []struct{ src, want string }{
		{`compare("a", "b", ` + cp + `)`, "-1"},
		{`contains("hello", "ell", ` + cp + `)`, "true"},
		{`starts-with("hello", "he", ` + cp + `)`, "true"},
		{`ends-with("hello", "lo", ` + cp + `)`, "true"},
		{`substring-before("a:b", ":", ` + cp + `)`, "a"},
		{`substring-after("a:b", ":", ` + cp + `)`, "b"},
		{`index-of((1, 2, 3), 2, ` + cp + `)`, "2"},
		{`distinct-values((1, 1, 2), ` + cp + `)`, "1 2"},
		{`min((3, 1, 2), ` + cp + `)`, "1"},
		{`max((3, 1, 2), ` + cp + `)`, "3"},
	}
	for _, c := range ok {
		got, err := evalToString(t, c.src, ns)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}

	const sv = `"http://example.com/collation/swedish"`
	bad := []string{
		`compare("a", "b", ` + sv + `)`,
		`contains("hello", "ell", ` + sv + `)`,
		`starts-with("hello", "he", ` + sv + `)`,
		`ends-with("hello", "lo", ` + sv + `)`,
		`substring-before("a:b", ":", ` + sv + `)`,
		`substring-after("a:b", ":", ` + sv + `)`,
		`index-of((1, 2, 3), 2, ` + sv + `)`,
		`distinct-values((1, 1, 2), ` + sv + `)`,
		`min((3, 1, 2), ` + sv + `)`,
		`max((3, 1, 2), ` + sv + `)`,
	}
	for _, src := range bad {
		if _, err := evalToString(t, src, ns); err == nil {
			t.Errorf("%s accepted an unsupported collation instead of failing", src)
		}
	}
}

func evalToString(t *testing.T, src string, ns NamespaceResolver) (string, error) {
	t.Helper()
	expr, err := Parse(src, ns)
	if err != nil {
		return "", err
	}
	seq, err := expr.Eval(&Context{Funcs: Builtins()})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, it := range seq {
		parts = append(parts, it.(*xdm.Atomic).String())
	}
	return strings.Join(parts, " "), nil
}
