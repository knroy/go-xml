package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// xsl:number/@value converts by xs:integer(round(number($V))) (section 12.1).
// xs:integer is unbounded, so the conversion must be carried out on the
// value's own exact representation. Routing it through a float64 to decide
// whether it is convertible reports every finite xs:integer above the float64
// range -- roughly 1.8e308 -- as infinite, and XTDE0980 was raised for a value
// the data model holds exactly.
//
// The two guards that did this sat on the 2.0+ path (numberValueOf) and on the
// backwards-compatible 1.0 path. Both are exercised here.

// runNumber compiles a stylesheet body at the given version and runs it
// against <a><b/><b/><b/></a>, returning the serialized result or the error.
func runNumberVer(t *testing.T, ver, body string) (string, error) {
	t.Helper()
	sheet := `<xsl:stylesheet version="` + ver + `"` +
		` xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xsl:output omit-xml-declaration="yes"/>` + body + `</xsl:stylesheet>`
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	dtree, err := xdm.ParseString(`<a><b/><b/><b/></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

// numberValue runs <xsl:number value="{expr}"/> at XSLT 3.0.
func numberValue(t *testing.T, expr string) (string, error) {
	t.Helper()
	return runNumberVer(t, "3.0",
		`<xsl:template match="/"><xsl:number value="`+expr+`"/></xsl:template>`)
}

// pow10 is the exact decimal digits of 10^n.
func pow10(n int) string { return "1" + strings.Repeat("0", n) }

// --- Site 1: the 2.0+ @value path, numberValueOf ---------------------------

// The digits are asserted, not merely the absence of an error: narrowing an
// xs:integer to a float64 and back loses low-order digits silently, so a test
// that only checked for err == nil would pass against the broken conversion
// for every value inside the float64 range.
func TestNumberValueExactInteger(t *testing.T) {
	for _, tc := range []struct {
		name, expr, want string
	}{
		// Just inside the float64 range: worked before, must keep working.
		{"10^308", `xs:integer('` + pow10(308) + `')`, pow10(308)},
		// The first power of ten a double cannot hold. This raised XTDE0980.
		{"10^309", `xs:integer('` + pow10(309) + `')`, pow10(309)},
		{"10^310", `xs:integer('` + pow10(310) + `')`, pow10(310)},
		{"10^400", `xs:integer('` + pow10(400) + `')`, pow10(400)},
		{"10^4096", `xs:integer('` + pow10(4096) + `')`, pow10(4096)},
		// Above 2^53 a double can no longer represent consecutive integers,
		// so these two catch a narrowing that raises no error at all.
		{"2^53", `xs:integer('9007199254740992')`, "9007199254740992"},
		{"2^53+1", `xs:integer('9007199254740993')`, "9007199254740993"},
		{"zero", `xs:integer('0')`, "0"},
		// A big xs:decimal has an exact value too, and rounds half up.
		{"decimal 10^320", `xs:decimal('` + pow10(320) + `')`, pow10(320)},
		{"decimal .5 rounds up", `xs:decimal('9007199254740993.5')`, "9007199254740994"},
		// Computed rather than cast, so the big value is produced by
		// arithmetic in the data model rather than by a literal.
		{"computed 10^310", `xs:integer('` + pow10(155) + `') * xs:integer('` + pow10(155) + `')`, pow10(310)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := numberValue(t, tc.expr)
			if err != nil {
				t.Fatalf("xsl:number value=%q raised %v, want the number %s...",
					tc.expr, err, tc.want[:min(len(tc.want), 12)])
			}
			if got != tc.want {
				t.Errorf("xsl:number value=%q\n got %s\nwant %s",
					tc.expr, digest(got), digest(tc.want))
			}
		})
	}
}

// XTDE0980's other half: a negative value has no numbering scheme. It must
// still be an error, and the error must not depend on the float64 range.
func TestNumberValueNegativeExactInteger(t *testing.T) {
	for _, expr := range []string{
		`xs:integer('-1')`,
		`xs:integer('-` + pow10(310) + `')`,
		`xs:decimal('-` + pow10(320) + `')`,
	} {
		got, err := numberValue(t, expr)
		if err == nil {
			t.Errorf("xsl:number value=%q produced %s, want XTDE0980: a "+
				"negative number cannot be numbered", expr, digest(got))
			continue
		}
		if !strings.Contains(err.Error(), "XTDE0980") {
			t.Errorf("xsl:number value=%q raised %v, want XTDE0980", expr, err)
		}
	}
}

// The regression the fix must not cause. Only xs:double and xs:float can be
// NaN or infinite, and for those XTDE0980 is still required: they have no
// integer value at all. Deleting the guards rather than gating them on the
// type would make these silently produce a number.
func TestNumberValueDoubleInfinityStillRejected(t *testing.T) {
	for _, expr := range []string{
		`xs:double('INF')`,
		`xs:double('-INF')`,
		`xs:double('NaN')`,
		`xs:float('INF')`,
		`xs:float('NaN')`,
		// Arrived at by arithmetic rather than by a literal.
		`xs:double('1e308') * xs:double('10')`,
		`xs:double('0') div xs:double('0')`,
		// Not a number at all: the cast itself fails, and that too is
		// XTDE0980 rather than the cast's FORG0001.
		`'apples'`,
	} {
		got, err := numberValue(t, expr)
		if err == nil {
			t.Errorf("xsl:number value=%q produced %q, want XTDE0980: an "+
				"infinite or non-numeric value has no integer", expr, got)
			continue
		}
		if !strings.Contains(err.Error(), "XTDE0980") {
			t.Errorf("xsl:number value=%q raised %v, want XTDE0980", expr, err)
		}
	}
}

// A finite double is still numbered through its own value, so the fix must not
// change what an ordinary double does.
func TestNumberValueFiniteDouble(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`xs:double('3.7')`, "4"},
		{`xs:double('3.5')`, "4"},
		{`xs:double('2.4')`, "2"},
		{`1 to 3`, "1.2.3"},
	} {
		got, err := numberValue(t, tc.expr)
		if err != nil {
			t.Fatalf("xsl:number value=%q raised %v", tc.expr, err)
		}
		if got != tc.want {
			t.Errorf("xsl:number value=%q = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// --- Site 2: the backwards-compatible 1.0 @value path ----------------------

// Under version="1.0" the conversion is XSLT 1.0's -- number() then round --
// and anything that is not a number formats as the string "NaN" rather than
// raising XTDE0980, which 1.0 did not have. That does not license narrowing a
// value the data model holds exactly: count() yields an xs:integer, and
// multiplying two of them stays an xs:integer, so an exact value above the
// float64 range reaches this path from a real 1.0 transform.
func TestNumberValueCompatExactInteger(t *testing.T) {
	// The value must reach the instruction without passing through 1.0
	// arithmetic: under XPath 1.0 compatibility the operands of + - * div are
	// cast to xs:double unconditionally (B.1), so "count(//b) * $big" really
	// is a double there and losing its digits is correct 1.0 behaviour. A
	// bare constructor or a typed variable keeps the xs:integer.
	for _, tc := range []struct{ name, expr, want string }{
		{"count", `count(//b)`, "3"},
		// Exact xs:integers above the float64 range. These formatted as
		// "NaN" because the guard asked a float64 projection of them.
		{"10^310", `xs:integer('` + pow10(310) + `')`, pow10(310)},
		{"10^400", `xs:integer('` + pow10(400) + `')`, pow10(400)},
		// Inside the float64 range but above 2^53: this lost its last digit,
		// formatting as 9007199254740992, with no error at all.
		{"2^53+1", `xs:integer('9007199254740993')`, "9007199254740993"},
		// Inside the float64 range but beyond int64: this clamped to the
		// int64 ceiling, 9223372036854775807.
		{"10^24", `xs:integer('` + pow10(24) + `')`, pow10(24)},
		{"decimal rounds half up", `xs:decimal('2.5')`, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runNumberVer(t, "1.0",
				`<xsl:template match="/"><xsl:number value="`+tc.expr+`"/></xsl:template>`)
			if err != nil {
				t.Fatalf("xsl:number value=%q raised %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("xsl:number value=%q\n got %s\nwant %s",
					tc.expr, digest(got), digest(tc.want))
			}
		})
	}
}

// The same value carried by a typed variable, so the big integer is not a
// literal in the instruction's own expression.
func TestNumberValueCompatTypedVariable(t *testing.T) {
	got, err := runNumberVer(t, "1.0",
		`<xsl:variable name="v" as="xs:integer" select="xs:integer('`+pow10(310)+`')"/>`+
			`<xsl:template match="/"><xsl:number value="$v"/></xsl:template>`)
	if err != nil {
		t.Fatalf("raised %v", err)
	}
	if got != pow10(310) {
		t.Errorf("\n got %s\nwant %s", digest(got), digest(pow10(310)))
	}
}

// The 1.0 path's own regression guard. XSLT 1.0 had no XTDE0980, so a double
// infinity, a NaN, a negative and a non-number all format as "NaN" -- and must
// keep doing so. backwards-015 and backwards-016 in the W3C suite depend on it.
func TestNumberValueCompatNaN(t *testing.T) {
	for _, expr := range []string{
		`1 div 0`,      // xs:double INF under 1.0 arithmetic
		`-1 div 0`,     // -INF
		`0 div 0`,      // NaN
		`'apples'`,     // not a number at all
		`//nosuchnode`, // the empty sequence
		`-1`,           // negative
		`-count(//b)`,
		`1e400`, // a double literal that overflows to INF on parse
	} {
		got, err := runNumberVer(t, "1.0",
			`<xsl:template match="/"><xsl:number value="`+expr+`"/></xsl:template>`)
		if err != nil {
			t.Errorf("xsl:number value=%q raised %v; XSLT 1.0 has no "+
				"XTDE0980, it wants the string NaN", expr, err)
			continue
		}
		if got != "NaN" {
			t.Errorf("xsl:number value=%q = %q, want \"NaN\"", expr, got)
		}
	}
}

// digest abbreviates a very long run of digits so a failure message stays
// readable while still showing the ends, where a narrowing shows first.
func digest(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:24] + "..." + s[len(s)-16:] + " (" + itoa(len(s)) + " chars)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
