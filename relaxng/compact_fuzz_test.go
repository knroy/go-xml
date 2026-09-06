package relaxng

import (
	"strings"
	"testing"
)

// compactSeeds are the compact-syntax inputs the fuzzer starts from.
//
// Each targets an edge the parser has to get right rather than a schema worth
// having: the escapes and the quoting forms, because they are where a
// character can change meaning; the infix operators, because they are the one
// place the grammar refuses rather than chooses; and several inputs that are
// simply malformed, because the property being tested is that a refusal is an
// error and never a panic.
var compactSeeds = []string{
	`element foo { text }`,
	`start = element foo { empty }`,
	`element foo { attribute bar { xsd:int }, element baz { text }* }`,
	`default namespace = "http://example.com/"
	 element foo { empty }`,
	`namespace e = "http://example.com/"
	 element e:* - e:no { empty }`,
	`element * { empty }`,
	`foo |= element a { empty }`,
	`foo &= element a { empty }`,
	`element foo { xsd:string { pattern = "[\i-[:]]" } }`,
	`element foo { "a" ~ "b" }`,
	`element foo { """triple "quoted" """ }`,
	`element foo { "\x{41}" }`,
	`## documentation
	 start = element foo { empty }`,
	`[ a:x = "y" ] element foo { empty }`,
	`element foo { xsd:int >> a:documentation [ "why" ] }`,
	`div { start = element foo { empty } }`,
	`include "other.rnc" inherit = e`,
	`element foo { external "other.rnc" }`,
	`element foo { grammar { start = parent bar } }`,
	`\element = element foo { empty }`,
	// Malformed, and each in a different way.
	`element foo { text | empty, text }`,
	`element foo {`,
	`element foo { "unterminated }`,
	`element e:foo { empty }`,
	`element foo { "\x{zz}" }`,
	`[[[[[[`,
	`((((((`,
	``,
}

// ParseCompact must never panic, whatever it is given.
//
// A schema is untrusted input — it is the thing a caller was handed and wants
// checked — so a panic here is a denial of service in any program that
// validates something it did not write. The assertion is deliberately weak
// about *what* the parser decides and strict about how it says so: either a
// tree or an error, never both, never neither, and never an error a caller
// cannot report.
func FuzzParseCompactNoPanic(f *testing.F) {
	for _, s := range compactSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		// The bound is a cost control and not a correctness claim: the
		// parser is linear in its input, and the corpus test already runs it
		// over a 356KB schema.
		if len(src) > 4096 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseCompact(%q) panicked: %v", src, r)
			}
		}()
		doc, err := ParseCompact(src)
		if err != nil {
			if doc != nil {
				t.Fatalf("ParseCompact(%q) returned both a tree and an error %v", src, err)
			}
			// A refusal the caller cannot report is as unhelpful as no
			// refusal at all, and this parser's errors are also expected to
			// name a position, since a schema is often thousands of lines.
			if strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("ParseCompact(%q) failed with an empty message", src)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Fatalf("ParseCompact(%q) error %q names no position", src, err)
			}
			return
		}
		if doc == nil {
			t.Fatalf("ParseCompact(%q) returned no error and no tree", src)
		}
		// Whatever was produced must be a tree the compiler can be handed
		// without crashing. It need not compile — plenty of well-formed
		// schemas are invalid — but the failure must be an error.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile of the tree from %q panicked: %v", src, r)
			}
		}()
		if _, err := Compile(doc); err != nil && strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("Compile of the tree from %q failed with an empty message", src)
		}
	})
}
