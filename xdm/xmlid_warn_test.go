package xdm

import (
	"strings"
	"testing"
)

// xml:id §4 and §6: a duplicate value and a value that is not an NCName are
// xml:id errors, "not fatal, but should be reported". ParseOptions.Warn is
// where they are reported, and the parse succeeds either way.
func TestParseWarnXMLIDErrors(t *testing.T) {
	var got []string
	tree, err := ParseString(`<r><a xml:id=" x "/><b xml:id="x"/><c xml:id="9z"/><d xml:id="ok"/></r>`,
		ParseOptions{Warn: func(e error) {
			if ErrorCode(e) != "XQDY0091" {
				t.Errorf("code %q, want XQDY0091", ErrorCode(e))
			}
			got = append(got, e.Error())
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0], `duplicate xml:id value "x"`) ||
		!strings.Contains(got[1], `xml:id value "9z" is not an NCName`) {
		t.Fatalf("warnings = %q", got)
	}
	// The non-NCName value is still kept, normalised, as the attribute's value.
	if v := tree.Root.FirstChild().LastChild().PrevSibling().Attr(NSXML, "id"); v == nil || v.Value() != "9z" {
		t.Fatalf("c/@xml:id = %v", v)
	}
}

// Nil Warn is today's behaviour: the same document parses without complaint.
func TestParseWarnNilIsSilent(t *testing.T) {
	if _, err := ParseString(`<r><a xml:id="x"/><b xml:id="x"/><c xml:id="9z"/></r>`, ParseOptions{}); err != nil {
		t.Fatal(err)
	}
}

// A document full of xml:id errors delivers maxIDWarnings warnings and one
// summary, not one per error.
func TestParseWarnXMLIDFloodBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for range 5 * maxIDWarnings {
		b.WriteString(`<a xml:id="1"/>`)
	}
	b.WriteString("</r>")
	var got []error
	if _, err := ParseString(b.String(), ParseOptions{Warn: func(e error) { got = append(got, e) }}); err != nil {
		t.Fatal(err)
	}
	if len(got) != maxIDWarnings+1 || !strings.Contains(got[maxIDWarnings].Error(), "suppressed") {
		t.Fatalf("%d warnings, last %v", len(got), got[len(got)-1])
	}
}
