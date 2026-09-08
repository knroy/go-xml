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
