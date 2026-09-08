package xslt

import (
	"strings"
	"testing"
)

// An array reaching content construction contributes its MEMBERS, not
// nothing.
//
// XPath 3.1 §3.11.1 makes an array a single item that holds a sequence of
// members, and XDM 3.1 defines atomization of an array as the atomization of
// its members, flattened -- data([1,2,3]) is (1,2,3). XSLT 3.0 §5.8.1 then
// casts each atomic value to a string and joins consecutive strings with a
// separator. XTDE0450 is reserved for a FUNCTION ITEM ("It is a dynamic error
// if the result sequence contains a function item"); an array of atomizable
// members is not one, so it must flatten rather than raise.
//
// Before the fix, constructedText and the xsl:copy-of switch matched only
// *xdm.Node and *xdm.Atomic. An *xdm.ArrayItem matched neither arm and fell
// off the end of the switch, so the array was dropped in silence and the
// instruction produced the empty sequence.
func TestValueOfFlattensArray(t *testing.T) {
	for _, tc := range []struct {
		name, sel, want string
	}{
		// The flat case: three members, each a single atomic value.
		{"flat", `[1, 2, 3]`, "<r>1 2 3</r>"},
		// Flattening is RECURSIVE. [[1,2],[3,4]] is two members, each a
		// two-item sequence; data() of it is (1,2,3,4), not two items.
		{"nested", `[[1, 2], [3, 4]]`, "<r>1 2 3 4</r>"},
		// A member may be a multi-item sequence written with the comma
		// operator inside parentheses.
		{"sequence member", `[(1, 2), 3]`, "<r>1 2 3</r>"},
		// An array beside ordinary items in one sequence: the array
		// contributes its members in place, in order.
		{"mixed with atomics", `('a', [1, 2], 'b')`, "<r>a 1 2 b</r>"},
		// An empty member contributes nothing at all, and must not leave a
		// stray separator behind.
		{"empty array", `[]`, "<r/>"},
		// Strings, so that the joining rule is visible rather than the
		// numeric formatting.
		{"strings", `['A', 'B']`, "<r>A B</r>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sheet := wrap30(`<xsl:template match="/"><r>` +
				`<xsl:value-of select="` + tc.sel + `"/>` +
				`</r></xsl:template>`)
			got := run(t, sheet, `<a/>`)
			if got != tc.want {
				t.Errorf("xsl:value-of select=%q\n got %q\nwant %q",
					tc.sel, got, tc.want)
			}
		})
	}
}

// The separator applies between the flattened members, because they are
// separate atomic values in the sequence being joined -- not one value that
// happens to contain spaces.
func TestValueOfArraySeparator(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/"><r>` +
		`<xsl:value-of select="[1, [2, 3]]" separator="|"/>` +
		`</r></xsl:template>`)
	if got, want := run(t, sheet, `<a/>`), "<r>1|2|3</r>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// xsl:copy-of has the same obligation and had the same two-arm switch.
//
// Its members are NODES here, so the array is not atomized: copy-of copies
// what the flattened sequence holds. That is the distinction between the two
// instructions -- value-of atomizes, copy-of does not -- and both must first
// flatten.
func TestCopyOfFlattensArray(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		sheet := wrap30(`<xsl:template match="/"><r>` +
			`<xsl:copy-of select="[//title]"/>` +
			`</r></xsl:template>`)
		want := "<r><title>Go</title><title>XML</title><title>XSLT</title></r>"
		if got := run(t, sheet, bookDoc); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	// Atomic members are appended as values, which the builder turns into
	// text with the section 5.8.1 joining rule.
	t.Run("atomics", func(t *testing.T) {
		sheet := wrap30(`<xsl:template match="/"><r>` +
			`<xsl:copy-of select="[1, [2, 3]]"/>` +
			`</r></xsl:template>`)
		if got, want := run(t, sheet, `<a/>`), "<r>1 2 3</r>"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// An attribute value template is simple content by the same §5.8.1 rule, so
// an array interpolated into one contributes its members.
func TestAttributeValueTemplateFlattensArray(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/">` +
		`<r v="{[1, [2, 3]]}"/>` +
		`</xsl:template>`)
	if got, want := run(t, sheet, `<a/>`), `<r v="1 2 3"/>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A FUNCTION ITEM is the thing XTDE0450 is actually about, and it must keep
// raising. Flattening arrays must not open the gate for function items: the
// error text is asserted, not merely that some error came back.
func TestFunctionItemStillRaisesXTDE0450(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/"><r>` +
		`<xsl:copy-of select="[function($x) {$x}]"/>` +
		`</r></xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil {
		t.Fatal("a function item inside element content must be an error, got none")
	}
	if !strings.Contains(err.Error(), "XTDE0450") &&
		!strings.Contains(err.Error(), "FOTY0013") {
		t.Errorf("want XTDE0450 or FOTY0013, got %v", err)
	}
}

// xsl:sequence is the third instruction with the same obligation, and it was
// the one still rejecting.
//
// value-of and copy-of were fixed by flattening at the instruction; xsl:sequence
// hands the item to the builder, which refused every non-node non-atomic item
// under an open element with XTDE0450. XTDE0450 is worded against a FUNCTION
// ITEM -- XSLT 3.0 §5.7.1: "It is a dynamic error if the result sequence
// contains a function item" -- and an array is not one for this rule. The
// suite says so outright: output-0713/0714/0715 are named "An array is
// flattened by the XML/HTML/dynamically-selected output method", and
// arrays-304/305 assert the member values of an array written into element
// content by xsl:sequence.
func TestSequenceFlattensArray(t *testing.T) {
	for _, tc := range []struct{ name, sel, want string }{
		{"atomics", `[1, 2, 3]`, "<r>1 2 3</r>"},
		// Recursive, exactly as data() is.
		{"nested", `[[1, 2], [3, 4]]`, "<r>1 2 3 4</r>"},
		// The case that motivated the earlier fix: an array in the MIDDLE of
		// a sequence contributes its members in place. Losing them was the
		// original defect, and a fix that raised instead of dropping would be
		// no better.
		{"array in the middle", `('a', [1, 2], 'b')`, "<r>a 1 2 b</r>"},
		// output-0713 in miniature: the members are NODES, and xsl:sequence
		// does not atomize, so they land as elements.
		{"nodes", `[//title]`,
			"<r><title>Go</title><title>XML</title><title>XSLT</title></r>"},
		// A node array in the middle of a sequence, so that neither the
		// leading nor the trailing item can mask a dropped member.
		{"nodes in the middle", `('x', [(//title)[1]], 'y')`,
			"<r>x<title>Go</title>y</r>"},
		{"empty", `[]`, "<r/>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sheet := wrap30(`<xsl:template match="/"><r>` +
				`<xsl:sequence select="` + tc.sel + `"/>` +
				`</r></xsl:template>`)
			if got := run(t, sheet, bookDoc); got != tc.want {
				t.Errorf("xsl:sequence select=%q\n got %q\nwant %q",
					tc.sel, got, tc.want)
			}
		})
	}
}

// At the TOP level an array stays an array. Flattening it there would break
// every xsl:variable declared as="array(*)" that a sequence constructor
// builds, so the fix is scoped to content under an open element.
func TestSequenceKeepsArrayAtTopLevel(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/">` +
		`<xsl:variable name="a" as="array(*)"><xsl:sequence select="[1, 2]"/></xsl:variable>` +
		`<r><xsl:value-of select="array:size($a)"/></r>` +
		`</xsl:template>`)
	if got, want := run(t, sheet, `<a/>`), "<r>2</r>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A function item or a map among an array's members is still XTDE0450: the
// array around it is unwrapped, and what it held is what the error is about.
func TestSequenceArrayOfFunctionItemsStillRaises(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/"><r>` +
		`<xsl:sequence select="[function($x) {$x}]"/>` +
		`</r></xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil {
		t.Fatal("a function item inside element content must be an error, got none")
	}
	if !strings.Contains(err.Error(), "XTDE0450") {
		t.Errorf("want XTDE0450, got %v", err)
	}
}

// xsl:apply-templates over an array processes its MEMBERS.
//
// arrays-301 (default text-only-copy) and arrays-302 (shallow-skip) select
// "array{$data}" over four elements and BOTH assert the same four <element>
// results. The two agree only if the array is unwrapped before rule matching:
// a built-in rule for the array item itself returns the empty sequence under
// shallow-skip (§6.7.5, "for atomic values and functions (including maps) is
// empty") and so could never produce the four.
func TestApplyTemplatesFlattensArray(t *testing.T) {
	const rules = `<xsl:template match="/"><out>` +
		`<xsl:apply-templates select="array{//title}"/>` +
		`</out></xsl:template>` +
		`<xsl:template match="title"><element name="{name()}"/></xsl:template>`
	want := `<out><element name="title"/><element name="title"/><element name="title"/></out>`

	t.Run("text-only-copy", func(t *testing.T) {
		if got := run(t, wrap30(rules), bookDoc); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	// The same result under shallow-skip is the half that proves the array is
	// unwrapped rather than matched by a built-in rule of its own.
	t.Run("shallow-skip", func(t *testing.T) {
		sheet := wrap30(`<xsl:mode on-no-match="shallow-skip"/>` + rules)
		if got := run(t, sheet, bookDoc); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// An EXPLICIT rule matching the array itself beats the built-in that unwraps
// it.
//
// square-array-019 writes match=".[. instance of array(*)]" and applies
// templates to an array expecting that rule to fire on the array as one item.
// The first attempt at the fix above flattened in xsl:apply-templates before
// rule matching, which made that rule unreachable and broke the case. The
// unwrapping therefore belongs in the BUILT-IN, reached only when no rule
// matched -- which is what this test and TestApplyTemplatesFlattensArray pin
// from the two sides.
func TestApplyTemplatesExplicitArrayRuleWins(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/"><out>` +
		`<xsl:apply-templates select="[//title]"/>` +
		`</out></xsl:template>` +
		`<xsl:template match=".[. instance of array(*)]">` +
		`<in><xsl:value-of select="array:size(.)"/></in>` +
		`</xsl:template>` +
		`<xsl:template match="title"><element/></xsl:template>`)
	// size 1, not 3: "[//title]" is a square constructor with ONE member
	// holding a three-item sequence. That the rule reports a size at all is
	// the point -- it fired on the array rather than on unwrapped members.
	if got, want := run(t, sheet, bookDoc), "<out><in>1</in></out>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
