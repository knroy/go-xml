package xslt

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The element table is a hand transcription of the XSLT 3.0 Recommendation's
// element syntax summaries, and in a handful of places it deliberately does
// not match them: it refuses seven Last Call working draft spellings by name
// rather than merely omitting them, accepts one draft element, widens one
// enumeration, carries one flag the braces do not justify, and leaves two
// enumerations to the check that owns their error code. Each of those
// decisions is justified in docs/element-table-policy.md, where every
// divergence carries the suite case, change-log entry or passage of prose
// that argues for it.
//
// Two tests keep the three in step. TestElementTableMatchesRecommendation
// reads the summaries out of the vendored Recommendation
// (testdata/xslt30-test/specs/xslt-30.html) and compares every element,
// attribute, required flag, avt flag and enumeration with the table, so an
// invented name, a widened enumeration or a changed flag fails unless it is
// one of the rows below. TestElementTablePolicyDeclaresEveryDivergence checks
// the rows themselves, so a divergence that was fixed without its row being
// removed fails too.

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

	// One avt flag the braces do not justify, and it is inert: xsl:copy-of's
	// @validation is refused by validate.go before the flag is consulted.
	// xsl:output's @parameter-document and @json-node-output-method were
	// listed here too, until they were given the types the summary states --
	// a uri flag and a four-token enumeration with eqnameOK. Clearing avt is
	// what gave those checks a reader, so neither is a divergence any more.
	{element: "copy-of", attr: "validation", avt: true},

	// Two enumerations the table leaves open because another check owns
	// them, with the code the prose assigns: xsl:output/@method is XTSE1570
	// (staticerrors.go), not the generic XTSE0020 an enumeration would
	// raise first; xsl:evaluate/@schema-aware is checked as a boolean AVT by
	// compileEvaluate, XTSE0020 for a literal and XTDE0030 for a computed
	// value. An empty, non-nil values records "no enumeration".
	{element: "evaluate", attr: "schema-aware", values: []string{}},
	{element: "output", attr: "method", values: []string{}},
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
		if d.avt && !ad.avt {
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

	// The two elements with no syntax summary are enumerated too; a third
	// invented element is caught by TestElementTableMatchesRecommendation.
	for _, el := range declaredExtraElements {
		if _, ok := xsltElements[el]; !ok {
			t.Errorf("docs/element-table-policy.md lists xsl:%s, which the "+
				"table no longer has", el)
		}
	}
}

// recAttr is one attribute line of an element syntax summary.
type recAttr struct {
	required, avt bool
	// tokens are the quoted alternatives; typ is what is left once they and
	// the braces are removed ("boolean", "eqname", "expression", ...).
	tokens []string
	typ    string
}

var (
	recSummaryRE = regexp.MustCompile(`(?s)<p class="element-syntax">(.*?)</p>`)
	recTagRE     = regexp.MustCompile(`<[^>]*>`)
	recBrRE      = regexp.MustCompile(`<br\s*/?>`)
	recHeadRE    = regexp.MustCompile(`^<xsl:([\w-]+)`)
	recAttrRE    = regexp.MustCompile(`^\[?([\w-]+)\]?(\??) = (.*?)\s*/?>?$`)
	recTokenRE   = regexp.MustCompile(`"([^"]*)"`)
)

// recSummaries reads the element syntax summaries of the vendored XSLT 3.0
// Recommendation, which section 2.2 makes normative. It skips when the suite
// checkout, which carries the specification, is absent.
func recSummaries(t *testing.T) map[string]map[string]recAttr {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "testdata", "xslt30-test", "specs", "xslt-30.html"))
	if err != nil {
		t.Skipf("XSLT 3.0 Recommendation not vendored: %v", err)
	}
	out := map[string]map[string]recAttr{}
	for _, m := range recSummaryRE.FindAllSubmatch(src, -1) {
		text := recBrRE.ReplaceAllString(string(m[1]), "\n")
		text = html.UnescapeString(recTagRE.ReplaceAllString(text, ""))
		text = strings.ReplaceAll(text, " ", " ")
		name := ""
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.HasPrefix(line, "<!--") || strings.HasPrefix(line, "</"):
			case name == "":
				h := recHeadRE.FindStringSubmatch(line)
				if h == nil {
					t.Fatalf("element syntax summary with no element: %q", line)
				}
				name = h[1]
			case strings.HasPrefix(line, "|") && len(lines) > 0:
				// An enumeration too long for one line continues on the next.
				lines[len(lines)-1] += " " + line
			default:
				lines = append(lines, line)
			}
		}
		attrs := map[string]recAttr{}
		for _, line := range lines {
			a := recAttrRE.FindStringSubmatch(line)
			if a == nil {
				t.Fatalf("xsl:%s: unreadable summary line %q", name, line)
			}
			v := strings.TrimSpace(a[3])
			ra := recAttr{required: a[2] == ""}
			if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
				ra.avt = true
				v = strings.TrimSpace(v[1 : len(v)-1])
			}
			for _, tok := range recTokenRE.FindAllStringSubmatch(v, -1) {
				ra.tokens = append(ra.tokens, tok[1])
			}
			rest := recTokenRE.ReplaceAllString(v, "")
			rest = strings.Trim(strings.ReplaceAll(rest, " ", ""), "|")
			ra.typ = strings.ReplaceAll(rest, "||", "|")
			attrs[a[1]] = ra
		}
		if name == "" {
			t.Fatalf("empty element syntax summary")
		}
		out[name] = attrs
	}
	if len(out) < 70 {
		t.Fatalf("read %d element syntax summaries; the markup has changed", len(out))
	}
	return out
}

func TestElementTableMatchesRecommendation(t *testing.T) {
	rec := recSummaries(t)
	declared := map[string]policyDivergence{}
	for _, d := range declaredDivergences {
		declared[d.element+"/"+d.attr] = d
	}
	extra := map[string]bool{}
	for _, el := range declaredExtraElements {
		extra[el] = true
	}
	for el, def := range xsltElements {
		ra, ok := rec[el]
		if !ok {
			if !extra[el] {
				t.Errorf("xsl:%s is in the table but has no syntax summary", el)
			}
			continue
		}
		for attr, ad := range def.attrs {
			d, isDeclared := declared[el+"/"+attr]
			r, ok := ra[attr]
			if !ok {
				if !isDeclared || !d.removed30 {
					t.Errorf("xsl:%s/@%s is in the table but not in the summary", el, attr)
				}
				continue
			}
			if req := ad.required && !ad.optional30; req != r.required {
				t.Errorf("xsl:%s/@%s: required is %v, the summary says %v", el, attr, req, r.required)
			}
			if ad.avt != r.avt && !(isDeclared && d.avt) {
				t.Errorf("xsl:%s/@%s: avt is %v, the summary says %v", el, attr, ad.avt, r.avt)
			}
			if isDeclared && d.values != nil || standardAttributes[attr] {
				continue
			}
			if msg := recValuesDiffer(ad, r); msg != "" {
				t.Errorf("xsl:%s/@%s: %s", el, attr, msg)
			}
		}
		for attr := range ra {
			if _, ok := def.attrs[attr]; !ok && !standardAttributes[attr] {
				t.Errorf("xsl:%s/@%s is in the summary but not in the table", el, attr)
			}
		}
	}
	for el := range rec {
		// xsl:example-element is section 2.2's illustration of the notation.
		if _, ok := xsltElements[el]; !ok && el != "example-element" {
			t.Errorf("xsl:%s has a syntax summary but is not in the table", el)
		}
	}
}

// recValuesDiffer compares an attribute's enumeration with its summary type,
// returning what is wrong or "".
func recValuesDiffer(ad attrDef, r recAttr) string {
	switch r.typ {
	case "boolean":
		// The 2.0 spelling pair, or all six of 3.0's xs:boolean lexical
		// forms; allowsBoolAliases admits the other four for a 2.0 pair.
		two := append([]string{"yes", "no"}, r.tokens...)
		six := append([]string{"yes", "no", "true", "false", "1", "0"}, r.tokens...)
		if !sameEnumeration(ad.values, two) && !sameEnumeration(ad.values, six) {
			return fmt.Sprintf("enumeration %v, the summary says boolean %v", ad.values, r.tokens)
		}
	case "", "eqname":
		if len(r.tokens) == 0 {
			break
		}
		if !sameEnumeration(ad.values, r.tokens) || ad.eqnameOK != (r.typ == "eqname") {
			return fmt.Sprintf("enumeration %v (eqname %v), the summary says %v | %s",
				ad.values, ad.eqnameOK, r.tokens, r.typ)
		}
		return ""
	}
	if r.typ != "boolean" && (ad.values != nil || ad.eqnameOK) {
		return fmt.Sprintf("enumeration %v, the summary types it %s %v", ad.values, r.typ, r.tokens)
	}
	return ""
}

// TestElementTablePolicyVisibilityEnumerations pins which visibility
// enumerations carry "hidden": only xsl:accept's summary does. Every other
// one stops at "abstract" -- xsl:expose included, whose summary gives four
// values -- so widening any of them to match xsl:accept would look like a
// consistency fix and is not one.
func TestElementTablePolicyVisibilityEnumerations(t *testing.T) {
	for _, c := range []struct {
		element string
		hidden  bool
	}{
		{"expose", false},
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
// REC appendix H.1 types it xsl:yes-or-no-or-omit, whose enumeration is seven
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
