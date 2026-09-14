package xslt

import (
	"sort"
	"strings"
	"testing"
)

// The element table is a hand transcription of the XSLT 3.0 Recommendation's
// element syntax summaries, and in a handful of places it deliberately does
// not match them: it refuses seven Last Call working draft spellings by name
// rather than merely omitting them, accepts one draft element, widens one
// enumeration, and carries one flag the braces do not justify. Each of those
// decisions is justified in docs/element-table-policy.md, where every
// divergence carries the suite case, change-log entry or passage of prose
// that argues for it.
//
// This test keeps the two in step. It holds the enumerated set of divergent
// (element, attribute) pairs and checks it against the table, so a new draft
// attribute, a widened enumeration or a cleared flag fails the build until
// the document declares it. It does not parse the specification: deriving the
// Recommendation side means stripping four megabytes of HTML, which is a
// research job and not a test. What it pins is the *shape* of each declared
// divergence, which is enough to notice one arriving or leaving.
//
// The nine pairs below are the attribute-level divergences the document
// declares: seven withdrawn draft spellings, one avt flag the braces do not
// justify, and one widened enumeration. A tenth appearing here without a row
// in the document is the finding the document exists to prevent.

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

	// Both were previously accepted and are now refused. No suite
	// stylesheet writes either: for-each-stream is mentioned only inside XML
	// comments (Bug29804 renamed it for-each-source), and param/@export was
	// tolerated solely because the attribute sweep outran the structural
	// error in iterate-024 -- which checkIteratePlacement now reports first.
	{element: "merge-source", attr: "for-each-stream", removed30: true},
	{element: "param", attr: "export", removed30: true},

	// An enumeration wider than the summary: xsl:expose confers "hidden",
	// which is the element's principal use, so the prose beats the summary.
	{element: "expose", attr: "visibility", values: []string{
		"public", "private", "final", "abstract", "hidden"}},

	// One avt flag the braces do not justify, and it is inert: xsl:copy-of's
	// @validation is refused by validate.go before the flag is consulted.
	// xsl:output's @parameter-document and @json-node-output-method were
	// listed here too, until they were given the types the summary states --
	// a uri flag and a four-token enumeration with eqnameOK. Clearing avt is
	// what gave those checks a reader, so neither is a divergence any more.
	{element: "copy-of", attr: "validation", avt: true},
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

// TestGlobalContextItemDeadEntries pins the deletion of xsl:global-context-item's
// @streamable and @use-accumulators.
//
// Section 3.10's summary gives as? and use? and no more, but the table listed
// both streaming attributes as accepted. They were never consulted:
// contextitem.go reads the declaration off the "context-item" key for both
// spellings, and that entry defines neither, so the two were refused by the
// other key while this one claimed to allow them. Deleting them removes the
// contradiction and makes the diagnostic name the element actually written.
//
// The refusal is the invariant, not the deletion, so this fails if the
// entries are restored as well as if the check is dropped.
func TestGlobalContextItemDeadEntries(t *testing.T) {
	for _, attr := range []string{"streamable", "use-accumulators"} {
		if _, ok := xsltElements["global-context-item"].attrs[attr]; ok {
			t.Errorf("xsl:global-context-item/@%s is back in the table; it "+
				"cannot be consulted, because the element is checked "+
				"against the context-item key. See "+
				"docs/element-table-policy.md.", attr)
		}
		src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    version="3.0"><xsl:global-context-item ` + attr + `="yes"/>
		  <xsl:template name="main"><out/></xsl:template></xsl:stylesheet>`
		err := compileDrift(t, src)
		if err == nil || !strings.Contains(err.Error(), "XTSE0090") {
			t.Errorf("xsl:global-context-item/@%s: want XTSE0090, got %v",
				attr, err)
		}
	}
	// The control: what §3.10 does define still compiles.
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    version="3.0"><xsl:global-context-item as="node()" use="optional"/>
	  <xsl:template name="main"><out/></xsl:template></xsl:stylesheet>`
	if err := compileDrift(t, src); err != nil {
		t.Errorf("xsl:global-context-item as/use was refused: %v", err)
	}
}

// TestStandaloneStaysNarrowAtTwoPointZero pins @standalone on both elements
// that carry it, and the asymmetry between them.
//
// REC appendix J.1 types it xsl:yes-or-no-or-omit, whose enumeration is seven
// values: "yes", "no", "omit", with true/false and 1/0 as synonyms of the
// first two. The table lists three, and that is deliberate -- the version
// gate belongs in checkAttrValue and never in the enumeration.
//
// Widening the enumeration to all seven was tried and reverted. It cost
// output-0282, a version="2.0" module writing standalone="true" whose whole
// purpose is to require XTSE0020: at XSLT 2.0 the synonym spellings are
// invalid values, not accepted ones. The suite decides the direction and
// decides it against the schema type read literally.
//
// The two elements therefore answer differently in a 2.0 module under a 3.0
// processor, and that is correct rather than an inconsistency:
// checkAttrValue grants xsl:result-document a blanket 3.0-processor widening
// on the strength of result-document-0304, which is scoped XSLT30+, while
// xsl:output has output-0282 pinning the refusal at 2.0.
func TestStandaloneStaysNarrowAtTwoPointZero(t *testing.T) {
	want := []string{"yes", "no", "omit"}
	for _, el := range []string{"output", "result-document"} {
		got := xsltElements[el].attrs["standalone"].values
		if !sameEnumeration(got, want) {
			t.Errorf("xsl:%s/@standalone is %v, want %v -- the 3.0 synonyms "+
				"are admitted by allowsBoolAliases, not by this list; "+
				"widening it costs output-0282", el, got, want)
		}
	}
	sheet := func(ver, body string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    version="` + ver + `">` + body + `</xsl:stylesheet>`
	}
	// output-0282 in miniature: "true" is XTSE0020 in a 2.0 module.
	err := compileDrift(t, sheet("2.0", `<xsl:output standalone="true"/>`))
	if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf(`xsl:output standalone="true" at 2.0: want XTSE0020, got %v`, err)
	}
	// And is accepted once the module is 3.0, through the version gate.
	if err := compileDrift(t, sheet("3.0", `<xsl:output standalone="true"/>`)); err != nil {
		t.Errorf(`xsl:output standalone="true" at 3.0 was refused: %v`, err)
	}
	// "omit" is in the enumeration proper, so it holds at either version.
	for _, v := range []string{"2.0", "3.0"} {
		if err := compileDrift(t, sheet(v, `<xsl:output standalone="omit"/>`)); err != nil {
			t.Errorf(`standalone="omit" at %s was refused: %v`, v, err)
		}
	}
	// An invented value is XTSE0020 at both, so the test cannot pass against
	// an enumeration that accepts everything.
	for _, v := range []string{"2.0", "3.0"} {
		err := compileDrift(t, sheet(v, `<xsl:output standalone="sometimes"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
			t.Errorf(`standalone="sometimes" at %s: want XTSE0020, got %v`, v, err)
		}
	}
}
