package xslt

import "strings"
import "testing"

// evalXSL evaluates a single XPath expression inside a stylesheet and returns
// the text result and any error.
func evalXSL(t *testing.T, expr string) (string, error) {
	t.Helper()
	sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">
<xsl:output method="text"/>
<xsl:template match="/"><xsl:value-of select="` + expr + `"/></xsl:template>
</xsl:stylesheet>`
	return runErr(t, sheet, `<r/>`)
}

// TestStringArgIsASingleton pins the cardinality of every XSLT function whose
// first argument stringArg reads.
//
// Each of these parameters is declared "as xs:string" in XSLT 3.0 -- no
// occurrence indicator -- so under the function conversion rules a sequence of
// two is XPTY0004 and the empty sequence is too. stringArg took atoms[0] and
// discarded the rest, so system-property(('xsl:version','xsl:vendor'))
// answered "3.0" as though the second item had not been written. That is a
// wrong answer rather than a refused one, and nothing in the result says so.
//
// The test asserts the error CODE, because absence-of-answer is not the
// property at issue: a truncating implementation also returns no error.
func TestStringArgIsASingleton(t *testing.T) {
	twoItem := []struct{ name, expr string }{
		{"system-property", `system-property(('xsl:version','xsl:vendor'))`},
		{"function-available", `function-available(('a','b'))`},
		{"type-available", `type-available(('xs:string','xs:int'))`},
		{"element-available", `element-available(('xsl:if','xsl:when'))`},
		{"unparsed-entity-uri", `unparsed-entity-uri(('a','b'))`},
		{"unparsed-entity-public-id", `unparsed-entity-public-id(('a','b'))`},
	}
	for _, tt := range twoItem {
		t.Run(tt.name+"/two items", func(t *testing.T) {
			out, err := evalXSL(t, tt.expr)
			if err == nil {
				t.Fatalf("%s: got %q and no error, want XPTY0004", tt.expr, out)
			}
			if !strings.Contains(err.Error(), "XPTY0004") {
				t.Errorf("%s: got %v, want XPTY0004", tt.expr, err)
			}
		})
	}

	// The empty sequence is the other half of "exactly one" and fails the
	// same way; a parameter declared "xs:string?" would accept it, and none
	// of these are.
	t.Run("system-property/empty", func(t *testing.T) {
		out, err := evalXSL(t, `system-property(())`)
		if err == nil {
			t.Fatalf("got %q and no error, want XPTY0004", out)
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("got %v, want XPTY0004", err)
		}
	})

	// The singleton call still works: the check must reject the extra item,
	// not the argument.
	t.Run("system-property/one item still answers", func(t *testing.T) {
		got, err := evalXSL(t, `system-property('xsl:version')`)
		if err != nil {
			t.Fatalf("system-property('xsl:version'): %v", err)
		}
		if got != "3.0" {
			t.Errorf("got %q, want %q", got, "3.0")
		}
	})
}
