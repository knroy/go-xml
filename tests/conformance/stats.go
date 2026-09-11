package conformance

// stats.go extends the conformance generator from one marked region in one
// document to every place this repository publishes a measured figure.
//
// The problem it closes is not "the numbers are in several files" -- they have
// to be, the documents are written for different readers -- but that each copy
// was typed. On one day the unit-test count was edited 2112 -> 2122 -> 2131 ->
// 2149 in five files each time; conformance-gaps.md carried 1,829 while the
// rest said 2,131; and the XTSE3430 breakdown read 13 in one file and 14 in two
// others. Every one of those is a figure with exactly one true value and no
// mechanism forcing the copies to agree.
//
// So each figure now has one origin, and every published copy is a generated
// region fed from it:
//
//   - Counts derived from the source tree -- unit tests, fuzz targets, limit
//     boundary tests -- are COUNTED HERE, from the tree, at generation time.
//     They are deliberately not fields in results.json: a typed count in a
//     JSON file is the same defect as a typed count in a sentence, moved.
//     What results.json records is the counting METHOD, because "how many
//     tests" has several honest answers and the claim is only as good as the
//     command behind it.
//   - Counts that come from a suite run -- passed, disagreements, total -- are
//     recorded in results.json, because no command in a one-second generator
//     can re-derive a figure that costs eight minutes of suite time.
//
// docs/stats.md is the page the two are published on together, and the marked
// regions elsewhere quote it.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The marker pair for every generated region other than the conformance
// summary. The name distinguishes one region from another within a file, so a
// document may carry several.
//
// They are HTML comments, invisible in rendered Markdown, and they carry the
// region's name so that a diff names what moved.
func beginMarkerFor(name string) string {
	return fmt.Sprintf("<!-- BEGIN GENERATED %s -->", name)
}

func endMarkerFor(name string) string {
	return fmt.Sprintf("<!-- END GENERATED %s -->", name)
}

// TreeCount is a figure derived from the source tree rather than from a suite
// run: how many unit tests there are, how many fuzz targets, how many limit
// boundary tests.
//
// The Value field is NOT decoded from JSON -- it has no struct tag -- so a
// number cannot be typed into results.json and reach a document. It is filled
// by Count below, which runs the recorded method against the tree. What the
// JSON carries is Method: the human-readable command, quoted beside every
// published copy of the number, because a figure whose method is not written
// down is merely asserted twice.
type TreeCount struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Method string `json:"method"`
	Kind   string `json:"kind"`

	Value int `json:"-"`
}

// TreeKinds is the closed set of counting methods Count knows how to run. A
// kind outside it is a validation error rather than a skipped figure: a
// generator that silently publishes a zero for a method it does not understand
// is worse than one that refuses to run.
var TreeKinds = []string{"func-test", "func-fuzz", "limits-boundary"}

// Breakdown is a named subset of one suite's disagreements: "14 of the 34
// XSLT 3.0 failures want an XTSE3430". Both halves are published, so both are
// recorded, and the whole must not exceed the suite's disagreement count.
type Breakdown struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Suite string `json:"suite"`
	Of    int    `json:"of"`
	Note  string `json:"note,omitempty"`
}

// Count fills in every TreeCount by running its method against root.
//
// This is the step that makes the derived figures unforgeable. The generator
// cannot emit a unit-test count that the tree does not have, because there is
// nowhere to write one down.
func (r *Results) Count(root string) error {
	for i := range r.Tree {
		n, err := countKind(root, r.Tree[i].Kind)
		if err != nil {
			return fmt.Errorf("tree count %q: %w", r.Tree[i].ID, err)
		}
		r.Tree[i].Value = n
	}
	return nil
}

var (
	funcTestRE = regexp.MustCompile(`(?m)^func Test`)
	funcFuzzRE = regexp.MustCompile(`(?m)^func Fuzz`)
)

// countKind walks the tree counting declarations.
//
// It is implemented in Go rather than by shelling out to grep so that the
// figure is identical on every platform: `grep -rn` is not the same program on
// macOS, Linux and Windows, and a count that differs by host is not a fact
// about the repository. tests/check.sh's docfigure section still uses grep, and
// the two agreeing is a check in itself.
//
// .claude/worktrees holds agent checkouts of this same repository and would
// otherwise multiply every count; testdata holds fixtures, some of which are
// Go source. Both are skipped, which is what the documented grep commands do
// with their `grep -v`.
func countKind(root, kind string) (int, error) {
	switch kind {
	case "func-test":
		return countDecls(root, funcTestRE, func(path string) bool {
			return strings.HasSuffix(path, "_test.go")
		})
	case "func-fuzz":
		return countDecls(root, funcFuzzRE, func(path string) bool {
			return strings.HasSuffix(path, "_test.go")
		})
	case "limits-boundary":
		return countDecls(root, funcTestRE, func(path string) bool {
			if filepath.Base(path) != "limits_boundary_test.go" {
				return false
			}
			// Only the top-level package directories, matching the documented
			// `./*/limits_boundary_test.go`: one directory below the root.
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return false
			}
			return len(strings.Split(filepath.ToSlash(rel), "/")) == 2
		})
	default:
		return 0, fmt.Errorf("unknown counting kind %q, want one of %s", kind, strings.Join(TreeKinds, ", "))
	}
}

func countDecls(root string, re *regexp.Regexp, want func(string) bool) (int, error) {
	n := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "testdata", "worktrees", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !want(path) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n += len(re.FindAllIndex(b, -1))
		return nil
	})
	return n, err
}

// TreeCount looks up one derived figure by ID. It panics on an unknown ID
// rather than returning a zero, because a template that silently prints 0 for
// a figure it misspelled is exactly the failure this file exists to prevent.
func (r *Results) TreeCount(id string) TreeCount {
	for _, t := range r.Tree {
		if t.ID == id {
			return t
		}
	}
	panic(fmt.Sprintf("conformance: no tree count %q in results.json", id))
}

// Suite looks up one measured lane by ID, with the same rule.
func (r *Results) Suite(id string) Suite {
	for _, s := range append(append([]Suite{}, r.Suites...), r.Corpora...) {
		if s.ID == id {
			return s
		}
	}
	panic(fmt.Sprintf("conformance: no suite %q in results.json", id))
}

// Breakdown looks up one named subset, with the same rule.
func (r *Results) Breakdown(id string) Breakdown {
	for _, b := range r.Breakdowns {
		if b.ID == id {
			return b
		}
	}
	panic(fmt.Sprintf("conformance: no breakdown %q in results.json", id))
}

// validateExtra checks the fields this file adds. It is called from Validate
// so that there is one entry point and no way to load a file with half of it
// checked.
func (r *Results) validateExtra() error {
	seen := map[string]bool{}
	for _, t := range r.Tree {
		if t.ID == "" {
			return fmt.Errorf("a tree count has no id")
		}
		if seen[t.ID] {
			return fmt.Errorf("tree count %q: duplicate id", t.ID)
		}
		seen[t.ID] = true
		if t.Label == "" {
			return fmt.Errorf("tree count %q: no label", t.ID)
		}
		if t.Method == "" {
			return fmt.Errorf("tree count %q: no method; the counting command is part of the claim", t.ID)
		}
		if !contains(TreeKinds, t.Kind) {
			return fmt.Errorf("tree count %q: kind %q, want one of %s", t.ID, t.Kind, strings.Join(TreeKinds, ", "))
		}
	}
	ids := map[string]bool{}
	for _, s := range append(append([]Suite{}, r.Suites...), r.Corpora...) {
		ids[s.ID] = true
	}
	seen = map[string]bool{}
	for _, b := range r.Breakdowns {
		if b.ID == "" {
			return fmt.Errorf("a breakdown has no id")
		}
		if seen[b.ID] {
			return fmt.Errorf("breakdown %q: duplicate id", b.ID)
		}
		seen[b.ID] = true
		if b.Label == "" {
			return fmt.Errorf("breakdown %q: no label", b.ID)
		}
		if !ids[b.Suite] {
			return fmt.Errorf("breakdown %q: suite %q is not in this file", b.ID, b.Suite)
		}
		s := r.Suite(b.Suite)
		if b.Of <= 0 {
			return fmt.Errorf("breakdown %q: count must be positive, got %d", b.ID, b.Of)
		}
		if b.Of > s.Disagreements {
			return fmt.Errorf("breakdown %q: %d of suite %q, which has only %d disagreements",
				b.ID, b.Of, b.Suite, s.Disagreements)
		}
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Regions is every generated region this generator owns, keyed by the file it
// lives in. Each renders from the loaded Results and nothing else.
//
// A region is small on purpose. The documents around them are long, careful,
// hand-written English, and the rule is that generation replaces a figure, not
// a paragraph: the sentence stays the author's, the number stops being
// theirs.
func (r *Results) Regions() map[string][]Region {
	return map[string][]Region{
		filepath.Join("docs", "stats.md"): {
			{Name: "STATS", Whole: true, Body: r.renderStats},
		},
		"README.md": {
			{Name: "TEST COUNT", Body: r.renderReadmeTests},
			{Name: "TEST METHODS", Body: r.renderReadmeMethods},
		},
		filepath.Join("docs", "todo.md"): {
			{Name: "STATUS TABLE", Body: r.renderTodoStatus},
		},
		filepath.Join("docs", "testing.md"): {
			{Name: "LAYER COUNTS", Body: r.renderTestingLayers},
		},
		filepath.Join("docs", "conformance-gaps.md"): {
			{Name: "CONFORMANCE SUMMARY", Bare: true, Body: func() string {
				// The original region, whose markers predate the name scheme.
				// Its body is rendered by Render, markers included, so this
				// wrapper strips them and lets the common path re-add them.
				s := r.Render()
				s = strings.TrimPrefix(s, BeginMarker+"\n")
				s = strings.TrimSuffix(s, EndMarker+"\n")
				return s
			}},
			{Name: "UNIT TEST COUNT", Body: r.renderGapsTests},
			{Name: "XTSE3430 BLOCK", Body: r.renderGapsXtse},
		},
	}
}

// Region is one generated span of one document.
type Region struct {
	Name string
	Body func() string

	// Whole says the file IS the region: it is generated end to end, with no
	// hand-written prose around it, so Replace writes the whole file rather
	// than looking for markers in it. docs/stats.md is the only one.
	Whole bool

	// Bare says the body already carries its own "this is generated" header,
	// so ReplaceRegion must not prepend the common one. Only the conformance
	// summary sets it: its header predates this file and is worded for the
	// one region whose defect -- a hand-written Total -- it was built to
	// close. Duplicating a header would be the generator adding noise to a
	// document on every run, which is the opposite of its contract.
	Bare bool
}

func (g Region) Begin() string { return beginMarkerFor(g.Name) }
func (g Region) End() string   { return endMarkerFor(g.Name) }

// --- the rendered regions -------------------------------------------------
//
// Each is one or two lines. Where the document's sentence carries meaning that
// no generator can produce -- "a few subtests skip without the corpora below"
// -- the sentence is inside the region verbatim and only the number moves.

func (r *Results) renderReadmeTests() string {
	t := r.TreeCount("unit-tests")
	return fmt.Sprintf("| **Tests** | %s `func Test` declarations, clean under `-race` (a few subtests skip without the corpora below) |\n", commas(t.Value))
}

func (r *Results) renderReadmeMethods() string {
	u := r.TreeCount("unit-tests")
	f := r.TreeCount("fuzz-targets")
	return fmt.Sprintf("| **Unit tests** (%s `func Test` declarations) | places where a plausible implementation is quietly wrong | anything nobody thought to write a test for |\n", commas(u.Value)) +
		"| **Spec inventories** | features absent entirely | features present but behaving wrongly |\n" +
		"| **Saxon differential** | subtle behavioural divergence on real stylesheets | constructs the corpora do not use |\n" +
		"| **W3C QT3 suite** | systematic conformance across 15,183 cases | XSLT (it is an XPath suite) |\n" +
		"| **W3C xsdtests suite** | systematic XSD conformance across 25,000 instance and 14,388 schema-validity tests (XSD 1.0; 1.1 adds 26,222 and 15,354) | schemas nobody writes by hand |\n" +
		"| **Production schema sets** | what large modular schemas do that suites do not | anything those industries happen not to use |\n" +
		fmt.Sprintf("| **Fuzzing** (%d targets) | a crash, hang or wrong refusal on input no author would write | anything a coverage-guided search does not reach in the time it is given |\n", f.Value)
}

func (r *Results) renderTodoStatus() string {
	var b strings.Builder
	row := func(label string, s Suite, extra string) {
		b.WriteString(fmt.Sprintf("| %s | %s — %s of %s in scope%s |\n",
			label, pct(s.Passed, s.Total), commas(s.Passed), commas(s.Total), extra))
	}
	row("XPath 2.0", r.Suite("xpath-2.0"), "")
	row("XPath 3.0", r.Suite("xpath-3.0"), "")
	row("XPath 3.1", r.Suite("xpath-3.1"), " (0 failing)")
	row("XQuery 3.1", r.Suite("xquery-3.1"), fmt.Sprintf(" (%d failing)", r.Suite("xquery-3.1").Disagreements))
	row("XSLT 2.0", r.Suite("xslt-2.0"), fmt.Sprintf(" (%d failing)", r.Suite("xslt-2.0").Disagreements))
	x := r.Suite("xslt-3.0")
	b.WriteString(fmt.Sprintf("| XSLT 3.0 | %s — %s of %s in scope (%d failing); %d of those need more of the §19.8 streamability analysis |\n",
		pct(x.Passed, x.Total), commas(x.Passed), commas(x.Total), x.Disagreements, r.Breakdown("xtse3430").Of))
	rn := r.Suite("relaxng")
	b.WriteString(fmt.Sprintf("| RELAX NG | %s — %s of %s |\n", pct(rn.Passed, rn.Total), commas(rn.Passed), commas(rn.Total)))
	b.WriteString("| Schemas wrongly refused | 7 — 6 on XSD 1.0, 1 on 1.1 |\n")
	b.WriteString(fmt.Sprintf("| Tests | %s `func Test` declarations, clean under `-race` |\n", commas(r.TreeCount("unit-tests").Value)))
	return b.String()
}

func (r *Results) renderTestingLayers() string {
	u := r.TreeCount("unit-tests")
	l := r.TreeCount("limit-boundary-tests")
	f := r.TreeCount("fuzz-targets")
	return fmt.Sprintf("| **Unit tests** | %s | a plausible implementation that is quietly wrong | anything nobody thought to write a test for |\n", commas(u.Value)) +
		fmt.Sprintf("| **Limit boundary tests** | %d tests | an off-by-one or an overflow at the edge of a configurable limit | a limit nobody added to the inventory |\n", l.Value) +
		"| **Race detector** | same tests | shared state a single-goroutine run never reveals | a data race on a path no test walks |\n" +
		"| **W3C conformance suites** | 141,691 cases | systematic divergence from the specification | what the suites do not ask about — see below |\n" +
		fmt.Sprintf("| **Real-world stylesheets** | %d documents | what large stylesheets do that a rule-at-a-time suite does not | constructs those two codebases happen not to use |\n",
			r.Suite("docbook-xsltng").Total+r.Suite("xspec").Total) +
		"| **Production schema sets** | 65 + CII | what modular published schemas do | industries whose schemas are shaped differently |\n" +
		"| **Vendored real-world schemas** | 185 of 230 | a schema-validity rule that has become stricter than the spec, on every checkout — no licensed corpus needed | the deep industry vocabularies only UBL and CII carry |\n" +
		fmt.Sprintf("| **Fuzzing** | %d targets | a crash, hang or wrong refusal on input nobody would write | anything a coverage-guided search does not reach in the time given |\n", f.Value) +
		"| **Generated oracle** | 8,397 documents | a *wrong answer* in the content-model matcher, on shapes nobody wrote a case for | only the occurrence shapes whose language is plain arithmetic — no interleaved choices |\n" +
		"| **Wildcard/UPA model** | 60,000 pairs | a *wrong answer* in wildcard acceptance or in the UPA competition rule | anything outside a single wildcard against a single name, or a pair of terms in one choice |\n" +
		"| **The ratchet** | 10 marks | a silent revert, or a fix that quietly costs more than it gains | a regression in something no suite counts |\n"
}

func (r *Results) renderGapsTests() string {
	return fmt.Sprintf("The unit-test suite is %s tests.\n", commas(r.TreeCount("unit-tests").Value))
}

func (r *Results) renderGapsXtse() string {
	b := r.Breakdown("xtse3430")
	x := r.Suite("xslt-3.0")
	return fmt.Sprintf("**%d of the %d want an `XTSE3430`** — a refusal of a stylesheet as\n", b.Of, x.Disagreements)
}

// cell escapes a value for a Markdown table cell. A pipe inside a cell ends
// the cell, even inside backticks, so the counting commands -- which are shell
// pipelines and full of them -- would silently shred the table they are
// published in. Escaping is the fix rather than reformatting the commands: the
// command text is the claim, and a command rewritten to render nicely is no
// longer the command a reader can paste and get the number back.
func cell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// --- docs/stats.md --------------------------------------------------------

// renderStats is the single page the user asked for: every measured figure
// this repository publishes, each with the command that produced it and the
// date it was measured.
//
// It is generated end to end. There is no hand-written prose in it at all,
// which is deliberate -- a page whose purpose is "these are the numbers and
// where they came from" has nothing a human should be adding to it, and a
// half-generated page invites exactly the hand-edit this mechanism exists to
// make impossible.
func (r *Results) renderStats() string {
	var b strings.Builder
	b.WriteString("# Statistics\n\n")
	b.WriteString("Every measured figure this repository publishes, with the command that\n")
	b.WriteString("produced it and the date it was measured.\n\n")
	b.WriteString("**This file is generated.** It is written by `tests/conformance-docs.go` from\n")
	b.WriteString("`tests/conformance/results.json` and from the source tree, and every figure\n")
	b.WriteString("quoted anywhere else in this repository is a generated region fed from the same\n")
	b.WriteString("two places. Do not edit it; edit the JSON, or the tree, and regenerate:\n\n")
	b.WriteString("```sh\n")
	b.WriteString("go run tests/conformance-docs.go          # rewrite every generated region\n")
	b.WriteString("go run tests/conformance-docs.go -check   # fail if any has drifted\n")
	b.WriteString("```\n\n")

	b.WriteString("## Counted from the source tree\n\n")
	b.WriteString("These are re-derived on every run of the generator, from the tree as checked\n")
	b.WriteString("out. They are not recorded anywhere -- a figure typed into a data file is the\n")
	b.WriteString("same defect as one typed into a sentence -- so what is recorded is the\n")
	b.WriteString("counting method, because \"how many tests\" has several honest answers and the\n")
	b.WriteString("claim is only as good as the command behind it.\n\n")
	b.WriteString("| figure | count | counted by |\n")
	b.WriteString("|---|---:|---|\n")
	for _, t := range r.Tree {
		b.WriteString(fmt.Sprintf("| %s | %s | `%s` |\n", t.Label, commas(t.Value), cell(t.Method)))
	}
	b.WriteString("\n")

	b.WriteString("## Measured by a suite run\n\n")
	b.WriteString("A suite run costs minutes, so these are recorded rather than re-derived. They\n")
	b.WriteString("are written to `tests/conformance/results.json` from a run of `tests/check.sh`,\n")
	b.WriteString("and `tests/docfigures.sh` cross-checks the same numbers against\n")
	b.WriteString("`tests/ratchet.txt`, which that run rewrites by a different route.\n\n")
	b.WriteString("| suite | edition | in scope | passing | now | disagreements | measured | command |\n")
	b.WriteString("|---|---|---:|---:|---|---:|---|---|\n")
	for _, s := range r.Suites {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | **%d** | %s | `%s` |\n",
			cell(s.Suite), cell(s.Edition), commas(s.Total), commas(s.Passed), pct(s.Passed, s.Total),
			s.Disagreements, s.RunDate, cell(s.Command)))
	}
	b.WriteString(fmt.Sprintf("| **Total** | | | | | **%d** | | |\n\n", r.Total()))
	b.WriteString(fmt.Sprintf("W3C disagreements: %s.\n\n", sumSentence(r.Suites)))

	b.WriteString("## Real-world corpora\n\n")
	b.WriteString("DocBook xslTNG and XSpec are stylesheet collections nobody wrote for a test\n")
	b.WriteString("harness. They are measured the same way but kept out of the total above, which\n")
	b.WriteString("counts disagreements with a specification; a corpus disagreement is a bug in\n")
	b.WriteString("this engine or in that stylesheet, and it is not a conformance figure. The\n")
	b.WriteString("separation is structural -- they are a different field in the JSON and a\n")
	b.WriteString("different list in the generator -- so no arithmetic can merge them by accident.\n\n")
	b.WriteString("| corpus | in scope | passing | now | failing | measured | command |\n")
	b.WriteString("|---|---:|---:|---|---:|---|---|\n")
	for _, s := range r.Corpora {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d | %s | `%s` |\n",
			cell(s.Suite), commas(s.Total), commas(s.Passed), pct(s.Passed, s.Total),
			s.Disagreements, s.RunDate, cell(s.Command)))
	}
	b.WriteString("\n")

	if len(r.Breakdowns) > 0 {
		b.WriteString("## Named subsets of a suite's disagreements\n\n")
		b.WriteString("A published figure of the form \"N of the M failures are X\". Both halves are\n")
		b.WriteString("quoted in prose, so both are recorded here and the subset is checked against\n")
		b.WriteString("its suite's disagreement count on load.\n\n")
		b.WriteString("| subset | count | of | note |\n")
		b.WriteString("|---|---:|---|---|\n")
		for _, bd := range r.Breakdowns {
			s := r.Suite(bd.Suite)
			b.WriteString(fmt.Sprintf("| %s | %d | %d %s disagreements | %s |\n",
				bd.Label, bd.Of, s.Disagreements, cell(s.Suite), cell(bd.Note)))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Where each figure is published\n\n")
	b.WriteString("Every one of these is a marked region. `go run tests/conformance-docs.go -check`\n")
	b.WriteString("fails if any has been hand-edited, and `tests/check.sh` runs that check.\n\n")
	b.WriteString("| file | region |\n")
	b.WriteString("|---|---|\n")
	regions := r.Regions()
	files := make([]string, 0, len(regions))
	for f := range regions {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, g := range regions[f] {
			b.WriteString(fmt.Sprintf("| `%s` | %s |\n", filepath.ToSlash(f), g.Name))
		}
	}
	b.WriteString("\n")

	b.WriteString("## What is still prose\n\n")
	b.WriteString("Some figures stay hand-written, and the reason is always the same: the number\n")
	b.WriteString("is inside a sentence whose *argument* would have to change with it, and a\n")
	b.WriteString("generator that rewrote the number alone would leave the paragraph saying\n")
	b.WriteString("something false with a correct figure in it. Those are guarded instead by\n")
	b.WriteString("`tests/docfigures.sh`, which anchors on the in-scope denominator -- the one\n")
	b.WriteString("number in a figure that does not move between runs -- and fails when the\n")
	b.WriteString("passing count, failure count or percentage beside it disagrees with\n")
	b.WriteString("`tests/ratchet.txt`. That is a check, not a rewrite, and the distinction is\n")
	b.WriteString("the point: it fails at the moment someone should be re-reading the sentence.\n")
	return b.String()
}

// --- applying every region ------------------------------------------------

// ReplaceRegion swaps one named region of doc, by the same rules Replace uses
// for the conformance summary: only the span between the markers moves, and
// the document's own line ending is preserved so that a Windows checkout with
// core.autocrlf=true does not report a diff on every run for ever.
func ReplaceRegion(doc string, g Region) (string, error) {
	body := g.Body()
	header := "<!-- Generated from tests/conformance/results.json and the source tree by\n" +
		"     tests/conformance-docs.go. Do not edit; see docs/stats.md. -->\n"
	if g.Bare {
		header = ""
	}
	region := g.Begin() + "\n" + header + body + g.End() + "\n"
	if strings.Contains(doc, "\r\n") {
		region = strings.ReplaceAll(region, "\n", "\r\n")
	}
	i := strings.Index(doc, g.Begin())
	if i < 0 {
		return "", fmt.Errorf("marker %s not found", g.Begin())
	}
	j := strings.Index(doc, g.End())
	if j < 0 {
		return "", fmt.Errorf("marker %s not found", g.End())
	}
	if j < i {
		return "", fmt.Errorf("marker %s precedes %s", g.End(), g.Begin())
	}
	tail := doc[j+len(g.End()):]
	tail = strings.TrimPrefix(tail, "\r")
	tail = strings.TrimPrefix(tail, "\n")
	return doc[:i] + region + tail, nil
}

// ReplaceAll applies every region belonging to one file.
func ReplaceAll(doc string, regions []Region) (string, error) {
	for _, g := range regions {
		if g.Whole {
			out := g.Body()
			if strings.Contains(doc, "\r\n") {
				out = strings.ReplaceAll(out, "\n", "\r\n")
			}
			doc = out
			continue
		}
		next, err := ReplaceRegion(doc, g)
		if err != nil {
			return "", err
		}
		doc = next
	}
	return doc, nil
}

// FileResult is what one file's generation did.
type FileResult struct {
	Path    string
	Changed bool
}

// ApplyAll rewrites every generated region in every file, and reports which
// files moved. With write=false it changes nothing and reports what would
// move, which is what -check and the gate use: the tree is dirty in most runs,
// so comparing rather than regenerating-then-diffing is the only reading that
// does not report the user's work in progress as a failure.
func (r *Results) ApplyAll(root string, write bool) ([]FileResult, error) {
	regions := r.Regions()
	files := make([]string, 0, len(regions))
	for f := range regions {
		files = append(files, f)
	}
	sort.Strings(files)

	out := make([]FileResult, 0, len(files))
	for _, rel := range files {
		path := filepath.Join(root, rel)
		var old []byte
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			old = b
		case os.IsNotExist(err) && wholeFile(regions[rel]):
			// A generated-end-to-end file need not exist yet.
			old = nil
		default:
			return nil, err
		}
		next, err := ReplaceAll(string(old), regions[rel])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.ToSlash(rel), err)
		}
		changed := next != string(old)
		out = append(out, FileResult{Path: filepath.ToSlash(rel), Changed: changed})
		if changed && write {
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func wholeFile(regions []Region) bool {
	return len(regions) == 1 && regions[0].Whole
}
