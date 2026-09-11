// Package conformance generates the conformance summary that
// docs/conformance-gaps.md publishes, from the measured counts recorded in
// tests/conformance/results.json.
//
// It exists because the document used to state its own total. It said 168
// while its own rows summed to 104, and nothing failed: a prose number is
// guarded by whoever last read the paragraph. So the total is no longer
// written anywhere a human can write it. This package adds the rows up and
// rewrites one marked region of the document; the arithmetic has exactly one
// implementation, and a stale total is unreachable rather than merely
// discouraged.
//
// Only the region between the BEGIN and END markers is generated. The rest of
// docs/conformance-gaps.md is hand-written analysis -- the per-case verdicts,
// the spec citations, the measured costs -- and is never touched here.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The markers delimiting the generated region. They are HTML comments so that
// they are invisible in rendered Markdown but survive every Markdown tool.
const (
	BeginMarker = "<!-- BEGIN GENERATED CONFORMANCE SUMMARY -->"
	EndMarker   = "<!-- END GENERATED CONFORMANCE SUMMARY -->"
)

// Verdicts is the closed vocabulary a case ID may carry. A verdict outside it
// is a validation error rather than a passthrough, because the whole value of
// the file is that the categories mean the same thing in every suite.
var Verdicts = []string{
	"implementation",
	"fixture",
	"implementation-defined",
	"optional",
	"deliberate-divergence",
	"not-run",
}

// Case is one named disagreement. The list need not be exhaustive -- XSD's 61
// are recorded set by set, and XSLT 3.0's XTSE3430 block as a block -- so the
// enumerated cases are checked against the disagreement count as an upper
// bound, never as the count itself. Deriving the total from the enumeration
// would silently under-report every suite whose cases are summarised.
type Case struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// Suite is one measured lane: a full run of one suite at one specification
// edition, with the command and date that produced it.
type Suite struct {
	ID            string `json:"id"`
	Component     string `json:"component"`
	Suite         string `json:"suite"`
	Edition       string `json:"edition"`
	Passed        int    `json:"passed"`
	Disagreements int    `json:"disagreements"`
	Total         int    `json:"total"`
	RunDate       string `json:"run_date"`
	Command       string `json:"command"`
	Cases         []Case `json:"cases"`

	// Why a suite's cases are not all enumerated above. Rendered nowhere; it
	// is here so the JSON explains itself to whoever next edits it.
	UnenumeratedNote string `json:"unenumerated_note,omitempty"`
}

// Results is the whole file. Corpora are kept separate from Suites rather than
// flagged inside one list: DocBook xslTNG and XSpec are real-world stylesheet
// collections, not W3C conformance suites, and counting them in the total
// would mean publishing a conformance figure against tests nobody wrote to a
// specification. Separate types make that impossible to do by accident.
type Results struct {
	// Decoding is strict (DisallowUnknownFields), so the JSON's own prose --
	// the note telling the next editor not to hand-edit the generated table --
	// needs a home here or the file will not load.
	Comment     string `json:"_comment,omitempty"`
	GeneratedBy string `json:"generated_by,omitempty"`

	Suites  []Suite `json:"suites"`
	Corpora []Suite `json:"corpora"`

	// Tree holds the figures that are DERIVED FROM THE SOURCE TREE rather
	// than from a suite run: how many unit tests, fuzz targets and limit
	// boundary tests there are. Their values are not in this file and cannot
	// be -- see TreeCount in stats.go -- only the counting method is.
	Tree []TreeCount `json:"tree"`

	// Breakdowns are named subsets of a suite's disagreements, such as "14 of
	// the 34 XSLT 3.0 failures want an XTSE3430". Both halves get published,
	// so both are checked: the subset may not exceed its suite's count.
	Breakdowns []Breakdown `json:"breakdowns"`
}

// Load reads and validates a results file.
func Load(path string) (*Results, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Results
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// Validate rejects a file whose arithmetic does not close.
//
// The check that matters is passed + disagreements == total, per suite, named.
// That is the invariant the document lost: rows whose parts did not add up sat
// beside a total that agreed with none of them. An unnamed failure would be
// nearly as useless as no failure, so every message carries the suite ID.
func (r *Results) Validate() error {
	if len(r.Suites) == 0 {
		return fmt.Errorf("no suites: a results file with no suite is not a measurement")
	}
	seen := map[string]bool{}
	for _, s := range append(append([]Suite{}, r.Suites...), r.Corpora...) {
		if s.ID == "" {
			return fmt.Errorf("a suite has no id")
		}
		if seen[s.ID] {
			return fmt.Errorf("suite %q: duplicate id", s.ID)
		}
		seen[s.ID] = true
		if s.Edition == "" {
			return fmt.Errorf("suite %q: no specification edition", s.ID)
		}
		if s.RunDate == "" {
			return fmt.Errorf("suite %q: no run date", s.ID)
		}
		if s.Command == "" {
			return fmt.Errorf("suite %q: no command", s.ID)
		}
		if s.Passed < 0 || s.Disagreements < 0 || s.Total <= 0 {
			return fmt.Errorf("suite %q: counts must be non-negative and the total positive (passed %d, disagreements %d, total %d)",
				s.ID, s.Passed, s.Disagreements, s.Total)
		}
		if s.Passed+s.Disagreements != s.Total {
			return fmt.Errorf("suite %q: passed %d + disagreements %d = %d, but total is %d",
				s.ID, s.Passed, s.Disagreements, s.Passed+s.Disagreements, s.Total)
		}
		if len(s.Cases) > s.Disagreements {
			return fmt.Errorf("suite %q: %d cases enumerated but only %d disagreements",
				s.ID, len(s.Cases), s.Disagreements)
		}
		ids := map[string]bool{}
		for _, c := range s.Cases {
			if c.ID == "" {
				return fmt.Errorf("suite %q: a case has no id", s.ID)
			}
			if ids[c.ID] {
				return fmt.Errorf("suite %q: duplicate case %q", s.ID, c.ID)
			}
			ids[c.ID] = true
			if !validVerdict(c.Verdict) {
				return fmt.Errorf("suite %q: case %q has verdict %q, want one of %s",
					s.ID, c.ID, c.Verdict, strings.Join(Verdicts, ", "))
			}
		}
	}
	return r.validateExtra()
}

func validVerdict(v string) bool {
	for _, w := range Verdicts {
		if v == w {
			return true
		}
	}
	return false
}

// Total is the number of W3C disagreements: the sum of the suite rows, and
// nothing else. Corpora are excluded by construction -- they are a different
// field -- so no caller can pass a total in and no caller can add a corpus to
// it. This function is the only place the number exists.
func (r *Results) Total() int {
	n := 0
	for _, s := range r.Suites {
		n += s.Disagreements
	}
	return n
}

// RunDates lists the distinct measurement dates, sorted. A summary drawn from
// runs on different days is honest about it rather than printing one date.
func (r *Results) RunDates() []string {
	set := map[string]bool{}
	for _, s := range append(append([]Suite{}, r.Suites...), r.Corpora...) {
		set[s.RunDate] = true
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// commas groups an integer for the document, which writes 11,518 rather than
// 11518 everywhere including in tests/docfigures.sh's anchors.
func commas(n int) string {
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func pct(passed, total int) string {
	return fmt.Sprintf("%.2f%%", float64(passed)*100/float64(total))
}

// Render produces the body of the generated region, markers included.
func (r *Results) Render() string {
	var b strings.Builder
	b.WriteString(BeginMarker + "\n")
	b.WriteString("<!-- Generated from tests/conformance/results.json by tests/conformance-docs.go.\n")
	b.WriteString("     Do not edit this region by hand; edit the JSON and regenerate. -->\n\n")
	b.WriteString("| Component | Suite | In scope | Passing | Now | Failing |\n")
	b.WriteString("|---|---|---:|---:|---|---:|\n")
	b.WriteString("| **xdm** | *(no external suite)* | — | — | — | — |\n")
	for _, s := range r.Suites {
		b.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s | %s | **%d** |\n",
			s.Component, s.Suite, commas(s.Total), commas(s.Passed), pct(s.Passed, s.Total), s.Disagreements))
	}
	for _, s := range r.Corpora {
		b.WriteString(fmt.Sprintf("| **%s** | %s *(real-world)* | %s | %s | %s | %d |\n",
			s.Component, s.Suite, commas(s.Total), commas(s.Passed), pct(s.Passed, s.Total), s.Disagreements))
	}
	b.WriteString(fmt.Sprintf("| | **Total** | | | | **%d** |\n", r.Total()))
	b.WriteString("\n")
	// The addition is written out so the total can be checked without
	// re-adding the table, and so the one number nobody may type by hand is
	// visibly derived rather than merely asserted.
	b.WriteString(fmt.Sprintf("W3C disagreements: %s. Measured %s.\n",
		sumSentence(r.Suites), strings.Join(r.RunDates(), ", ")))
	b.WriteString(EndMarker + "\n")
	return b.String()
}

// sumSentence writes the addition out, so a reader can check the total without
// re-adding the table. "0 + 0 + 0 + 1 + 8 + 34 + 30 + 31 + 0 = 104".
func sumSentence(suites []Suite) string {
	parts := make([]string, 0, len(suites))
	n := 0
	for _, s := range suites {
		parts = append(parts, fmt.Sprintf("%d", s.Disagreements))
		n += s.Disagreements
	}
	return strings.Join(parts, " + ") + fmt.Sprintf(" = %d", n)
}

// Apply rewrites the marked region of doc in place, leaving every other byte
// alone, and reports whether the file changed.
func Apply(docPath string, r *Results) (changed bool, err error) {
	old, err := os.ReadFile(docPath)
	if err != nil {
		return false, err
	}
	next, err := Replace(string(old), r)
	if err != nil {
		return false, fmt.Errorf("%s: %w", docPath, err)
	}
	if next == string(old) {
		return false, nil
	}
	return true, os.WriteFile(docPath, []byte(next), 0o644)
}

// Replace swaps the generated region of doc for a freshly rendered one.
//
// A CRLF document is handled rather than rewritten: on Windows a checkout with
// core.autocrlf=true gives every line a \r, and a generator that emitted LF
// into it would report a diff on every run for ever. The region is rendered in
// the line ending the document already uses, and no line outside the region is
// touched, so the file's convention survives whichever platform regenerates it.
func Replace(doc string, r *Results) (string, error) {
	region := r.Render()
	if strings.Contains(doc, "\r\n") {
		region = strings.ReplaceAll(region, "\n", "\r\n")
	}
	i := strings.Index(doc, BeginMarker)
	if i < 0 {
		return "", fmt.Errorf("marker %s not found", BeginMarker)
	}
	j := strings.Index(doc, EndMarker)
	if j < 0 {
		return "", fmt.Errorf("marker %s not found", EndMarker)
	}
	if j < i {
		return "", fmt.Errorf("marker %s precedes %s", EndMarker, BeginMarker)
	}
	// The rendered region ends in its own newline, so the document's newline
	// after the END marker is consumed rather than doubled.
	tail := doc[j+len(EndMarker):]
	tail = strings.TrimPrefix(tail, "\r")
	tail = strings.TrimPrefix(tail, "\n")
	return doc[:i] + region + tail, nil
}

// DefaultPaths resolves the two files relative to the repository root, so that
// the generator behaves the same from any working directory.
func DefaultPaths(root string) (results, doc string) {
	return filepath.Join(root, "tests", "conformance", "results.json"),
		filepath.Join(root, "docs", "conformance-gaps.md")
}
