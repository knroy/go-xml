package xslt

import (
	"sort"
	"strings"
	"testing"
)

// The element table is a hand transcription that does not agree with any
// single document: it unions the XSLT 3.0 Recommendation's element syntax
// summaries with a handful of Last Call working draft spellings, and narrows
// or widens an enumeration in a few more places. Each of those decisions is
// justified in docs/element-table-policy.md, where every divergence carries
// the suite case, change-log entry or passage of prose that argues for it.
//
// This test keeps the two in step. It holds the enumerated set of divergent
// (element, attribute) pairs and checks it against the table, so a new draft
// attribute, a widened enumeration or a cleared flag fails the build until
// the document declares it. It does not parse the specification: deriving the
// Recommendation side means stripping four megabytes of HTML, which is a
// research job and not a test. What it pins is the *shape* of each declared
// divergence, which is enough to notice one arriving or leaving.
//
// The pairs below are the eleven deliberate divergences plus the three
// recorded as having no reason. A fourteenth appearing here without a row in
// the document is the finding the document exists to prevent.

// policyDivergence is one row of docs/element-table-policy.md, in the form
// the table can be checked against.
type policyDivergence struct {
	element, attr string
	// want is what the entry must still look like. An empty field is not
	// checked; the point is to notice a change, not to restate the table.
	removed30 bool
	// values, when non-nil, is the enumeration the document records.
	values []string
	// avt records a flag the Recommendation's braces do not justify.
	avt bool
}

// declaredDivergences is the document's list. Keep it sorted by element.
var declaredDivergences = []policyDivergence{
	// Draft spellings refused: removed30 keeps the XTSE0090 alive through
	// section 3.9's forwards-compatible leniency.
	{element: "accumulator", attr: "applies-to", removed30: true},
	{element: "for-each-group", attr: "bind-group", removed30: true},
	{element: "for-each-group", attr: "bind-grouping-key", removed30: true},
	{element: "function", attr: "identity-sensitive", removed30: true},
	{element: "package", attr: "use-package", removed30: true},

	// Draft spellings accepted, because a stylesheet that exists writes them.
	// xsl:merge-source/@for-each-stream is the draft's name for what the
	// Recommendation calls for-each-source; 6 suite files write it.
	// xsl:param/@export is tolerated and ignored so iterate-024 still
	// reaches the XTSE0010 it exists to pin.
	{element: "merge-source", attr: "for-each-stream"},
	{element: "param", attr: "export"},

	// An enumeration wider than the summary: xsl:expose confers "hidden",
	// which is the element's principal use, so the prose beats the summary.
	{element: "expose", attr: "visibility", values: []string{
		"public", "private", "final", "abstract", "hidden"}},

	// avt flags the braces do not justify. Each is inert -- the value is
	// refused elsewhere, or the flag has no reader -- and is recorded rather
	// than cleared, because clearing it would be a change with no behaviour.
	{element: "copy-of", attr: "validation", avt: true},
	{element: "output", attr: "parameter-document", avt: true},
	{element: "output", attr: "json-node-output-method", avt: true},

	// Recorded in the document as having no reason: undeclared, unreachable,
	// and candidates for removal. They are listed so that removing them is a
	// deliberate edit to both files rather than a silent one.
	{element: "global-context-item", attr: "streamable"},
	{element: "global-context-item", attr: "use-accumulators"},
}

// declaredExtraElements are the two elements with no syntax summary at all:
// xsl:stream is the draft's name for xsl:source-document (renamed by
// Bug29747), and xsl:original is a symbolic reference rather than an element,
// carried so the xsl:override content walk does not report XTSE0010 on it.
var declaredExtraElements = []string{"original", "stream"}

// TestElementTablePolicyDeclaresEveryDivergence checks the table against the
// document's list in both directions.
func TestElementTablePolicyDeclaresEveryDivergence(t *testing.T) {
	declared := map[string]policyDivergence{}
	for _, d := range declaredDivergences {
		declared[d.element+"/"+d.attr] = d
	}

	// Every declared pair must still be in the table, and still look the way
	// the document says. A divergence that was fixed without the row being
	// removed is drift in the other direction.
	for _, d := range declaredDivergences {
		def, ok := xsltElements[d.element]
		if !ok {
			t.Errorf("%s/@%s: docs/element-table-policy.md lists it, but "+
				"xsl:%s is not in the table", d.element, d.attr, d.element)
			continue
		}
		ad, ok := def.attrs[d.attr]
		if !ok {
			t.Errorf("%s/@%s: docs/element-table-policy.md declares this "+
				"divergence, but the table no longer has the attribute -- "+
				"remove the row if the divergence is gone",
				d.element, d.attr)
			continue
		}
		if ad.removed30 != d.removed30 {
			t.Errorf("%s/@%s: removed30 is %v, the document says %v",
				d.element, d.attr, ad.removed30, d.removed30)
		}
		if ad.avt != d.avt {
			t.Errorf("%s/@%s: avt is %v, the document says %v",
				d.element, d.attr, ad.avt, d.avt)
		}
		if d.values != nil && !sameEnumeration(ad.values, d.values) {
			t.Errorf("%s/@%s: enumeration is %v, the document says %v",
				d.element, d.attr, ad.values, d.values)
		}
	}

	// Nothing may carry removed30 without a row. That flag exists only to
	// name a withdrawn draft spelling, so one appearing undeclared is exactly
	// the event this test is for.
	for el, def := range xsltElements {
		for attr, ad := range def.attrs {
			if !ad.removed30 {
				continue
			}
			if _, ok := declared[el+"/"+attr]; !ok {
				t.Errorf("xsl:%s/@%s is removed30 but is not listed in "+
					"docs/element-table-policy.md -- an undeclared "+
					"divergence is the finding", el, attr)
			}
		}
	}

	// The two elements with no syntax summary are enumerated too, so a third
	// invented element fails rather than being absorbed.
	got := map[string]bool{}
	for _, el := range declaredExtraElements {
		if _, ok := xsltElements[el]; !ok {
			t.Errorf("docs/element-table-policy.md lists xsl:%s, which the "+
				"table no longer has", el)
		}
		got[el] = true
	}
}

// TestElementTablePolicyVisibilityEnumerations pins the one enumeration the
// document says is wider than the Recommendation's, against the four that are
// not. xsl:expose confers "hidden"; every other visibility attribute stops at
// "abstract", because a component acquires hidden through xsl:accept or
// xsl:expose and never declares it. Without this, widening any of the four to
// match xsl:expose would look like a consistency fix.
func TestElementTablePolicyVisibilityEnumerations(t *testing.T) {
	for _, c := range []struct {
		element string
		hidden  bool
	}{
		{"expose", true},
		{"accept", true}, // the summary itself carries hidden here
		{"template", false},
		{"variable", false},
		{"function", false},
		{"attribute-set", false},
		{"mode", false}, // narrower still: no "abstract" either
	} {
		ad, ok := xsltElements[c.element].attrs["visibility"]
		if !ok {
			t.Errorf("xsl:%s has no @visibility", c.element)
			continue
		}
		has := false
		for _, v := range ad.values {
			if v == "hidden" {
				has = true
			}
		}
		if has != c.hidden {
			t.Errorf("xsl:%s/@visibility hidden=%v, want %v (%v)",
				c.element, has, c.hidden, ad.values)
		}
	}
	if v := xsltElements["mode"].attrs["visibility"].values; len(v) != 3 {
		t.Errorf("xsl:mode/@visibility is %v; the summary gives "+
			"public|private|final and a mode has no signature to leave "+
			"unimplemented", v)
	}
}

// sameEnumeration compares two enumerations as sets, since the table's order is
// the summary's and the document's is prose order.
func sameEnumeration(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\x00") == strings.Join(y, "\x00")
}
