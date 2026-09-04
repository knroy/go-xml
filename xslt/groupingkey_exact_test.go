package xslt

import "testing"

// groupKeys groups a numeric sequence by value and reports each group's key
// and size, so a test can assert group *membership* rather than merely that
// the transform ran.
func groupKeys(t *testing.T, sel string) string {
	t.Helper()
	sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">
<xsl:output method="text"/>
<xsl:template match="/"><xsl:for-each-group select="` + sel + `" group-by=".">` +
		`[<xsl:value-of select="current-grouping-key()"/>:` +
		`<xsl:value-of select="count(current-group())"/>]</xsl:for-each-group></xsl:template>
</xsl:stylesheet>`
	out, err := runErr(t, sheet, `<r/>`)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return out
}

// TestGroupByKeyIsExactBeyondDouble is the load-bearing test for the grouping
// key of an xs:integer or xs:decimal.
//
// xsl:for-each-group finds a group by hashing the key to a string and looking
// it up; the erratum-E25 rescan that compares values exactly runs only when
// that lookup MISSES. A lookup that wrongly HITS is never re-verified, so a
// lossy key does not merely slow the grouping down, it silently puts two
// distinct values in one group and reports a count nobody can tell is wrong.
//
// The key formatted an integer or decimal through float64. Every xs:integer
// past 2^53 shares a double with its neighbour, and everything past roughly
// 1.8e308 formats as +Inf, so whole populations collapsed into one group.
// These are the values that separate an exact key from a lossy one; a test
// over small integers cannot see the difference.
func TestGroupByKeyIsExactBeyondDouble(t *testing.T) {
	e308 := "1"
	for i := 0; i < 400; i++ {
		e308 += "0"
	}
	tests := []struct {
		name string
		sel  string
		want string
	}{{
		// 2^53 and 2^53+1 are the same float64. As xs:integer they are two
		// values and must stay in two groups.
		name: "integers straddling 2^53",
		sel:  `(9007199254740991, 9007199254740992, 9007199254740993)`,
		want: `[9007199254740991:1][9007199254740992:1][9007199254740993:1]`,
	}, {
		// Two decimals that differ only past the 17 significant digits a
		// double carries.
		name: "decimals below double precision",
		sel: `(xs:decimal('1.00000000000000000001'),` +
			` xs:decimal('1.00000000000000000002'))`,
		want: `[1.00000000000000000001:1][1.00000000000000000002:1]`,
	}, {
		// Above the double range every value formats as +Inf, so a lossy key
		// gathered all three into a single group of three.
		name: "integers above the double range",
		sel: `(xs:integer('` + e308 + `'), xs:integer('2` + e308[1:] + `'),` +
			` xs:integer('3` + e308[1:] + `'))`,
		want: `[` + e308 + `:1][2` + e308[1:] + `:1][3` + e308[1:] + `:1]`,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupKeys(t, tt.sel); got != tt.want {
				t.Errorf("group-by:\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

// TestGroupByKeyStillCollidesAcrossNumericTypes guards the other direction.
//
// The exactness above is bought with a suffix on the key, and a suffix that
// separated values the specification calls EQUAL would be a worse bug than
// the one it fixes: 14.2 groups on the "eq" relation, under which an
// xs:integer, an xs:decimal, an xs:float and an xs:double of the same value
// are one key, and under which trailing zeroes in a decimal are not part of
// the value. Both must stay in a single group.
func TestGroupByKeyStillCollidesAcrossNumericTypes(t *testing.T) {
	tests := []struct{ name, sel, want string }{
		{"one value in four numeric types",
			`(1, xs:double('1.0'), xs:decimal('1.0'), xs:float('1.0'))`, `[1:4]`},
		{"decimal and double", `(xs:decimal('1.2'), xs:double('1.2'))`, `[1.2:2]`},
		{"trailing zeroes are not part of the value",
			`(xs:decimal('2.50'), xs:decimal('2.5'))`, `[2.5:2]`},
		{"integer and decimal spellings of one",
			`(xs:integer('1'), xs:decimal('1.0'), xs:decimal('1.00'))`, `[1:3]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupKeys(t, tt.sel); got != tt.want {
				t.Errorf("group-by:\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}
