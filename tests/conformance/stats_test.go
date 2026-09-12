package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is two directories up from tests/conformance.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func shipped(t *testing.T) *Results {
	t.Helper()
	r, err := Load("results.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Count(repoRoot(t)); err != nil {
		t.Fatal(err)
	}
	return r
}

// The derived counts must come from the tree, not from the file. This is the
// property that makes them unforgeable, and it is worth asserting directly:
// results.json is decoded with DisallowUnknownFields, so a "value" key in a
// tree entry must be REFUSED rather than accepted and ignored. If it were
// merely ignored, someone would add one, see it have no effect, and eventually
// someone else would make it work.
func TestTreeCountCannotBeTypedIntoTheJSON(t *testing.T) {
	const withValue = `{
  "suites": [
    {"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
     "passed": 9, "disagreements": 1, "total": 10,
     "run_date": "2026-01-02", "command": "run", "cases": []}
  ],
  "tree": [
    {"id": "unit-tests", "label": "Unit tests", "kind": "func-test",
     "method": "grep", "value": 999999}
  ]
}`
	dir := t.TempDir()
	p := filepath.Join(dir, "results.json")
	if err := os.WriteFile(p, []byte(withValue), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("Load accepted a hand-typed tree count value; the whole point is that there is nowhere to type one")
	}
	if !strings.Contains(err.Error(), "value") {
		t.Errorf("the error does not name the offending field: %v", err)
	}
}

// A count that is genuinely derived changes when the tree changes. Counting a
// temporary tree with a known number of declarations proves the walker counts
// rather than reports.
func TestCountWalksTheTree(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The declarations are assembled at run time rather than written as
	// literals. A literal "func Test..." inside this file would be counted by
	// the grep pipeline the documentation quotes -- which matches anywhere on a
	// line, not only at its start -- and the fixture would then inflate the very
	// figure it exists to check. That disagreement is not hypothetical: it
	// appeared the first time this test was written with literals.
	decl := func(kw, name string) string { return "func " + kw + name + "(t *testing.T) {}\n" }
	write("a/a_test.go", "package a\n\n"+decl("Test", "One")+decl("Test", "Two")+"func helper() {}\n")
	write("a/zz_fuzz_test.go", "package a\n\nfunc "+"Fuzz"+"Thing(f *testing.F) {}\n")
	write("a/limits_boundary_test.go", "package a\n\n"+decl("Test", "LimitA")+decl("Test", "LimitB"))
	// Deep: a limits_boundary_test.go two directories down is NOT counted, so
	// the figure keeps matching the documented `./*/limits_boundary_test.go`.
	write("a/b/limits_boundary_test.go", "package b\n\n"+decl("Test", "Deep"))
	// A worktree checkout of this same repository would otherwise multiply
	// every count; testdata holds fixtures that are sometimes Go source.
	write("testdata/fixture_test.go", "package x\n\n"+decl("Test", "Fixture"))
	// Not a test file at all.
	write("a/a.go", "package a\n\n"+decl("Test", "LookingButNotOne"))
	// An indented declaration is not a declaration; the walker anchors at the
	// start of a line, and so must anything claiming to count the same thing.
	write("a/indent_test.go", "package a\n\n\t"+decl("Test", "Indented"))

	for _, tc := range []struct {
		kind string
		want int
	}{
		// TestOne, TestTwo, TestLimitA, TestLimitB, TestDeep = 5.
		{"func-test", 5},
		{"func-fuzz", 1},
		// Only a/limits_boundary_test.go: TestLimitA, TestLimitB.
		{"limits-boundary", 2},
	} {
		got, err := countKind(dir, tc.kind)
		if err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		if got != tc.want {
			t.Errorf("countKind(%q) = %d, want %d", tc.kind, got, tc.want)
		}
	}

	if _, err := countKind(dir, "invented"); err == nil {
		t.Error("countKind accepted an unknown kind; a generator that publishes 0 for a method it does not understand is worse than one that refuses to run")
	}
}

// A breakdown may not claim more cases than its suite has disagreements. "14 of
// the 34" is published as both halves, and a subset larger than the whole is
// the shape the XTSE3430 figure actually took when it read 13 in one file and
// 14 in two others.
func TestBreakdownIsCheckedAgainstItsSuite(t *testing.T) {
	tmpl := `{
  "suites": [
    {"id": "s", "component": "a", "suite": "S", "edition": "S 1.0",
     "passed": 90, "disagreements": 10, "total": 100,
     "run_date": "2026-01-02", "command": "run", "cases": []}
  ],
  "breakdowns": [{"id": "b", "label": "B", "suite": %q, "of": %d}]
}`
	load := func(suite string, of int) error {
		dir := t.TempDir()
		p := filepath.Join(dir, "results.json")
		body := strings.Replace(strings.Replace(tmpl, "%q", `"`+suite+`"`, 1), "%d", itoa(of), 1)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}
	if err := load("s", 10); err != nil {
		t.Fatalf("a subset equal to the whole must be accepted: %v", err)
	}
	err := load("s", 11)
	if err == nil {
		t.Fatal("Load accepted a breakdown of 11 against a suite with 10 disagreements")
	}
	if !strings.Contains(err.Error(), "only 10") {
		t.Errorf("the error does not say what the suite actually has: %v", err)
	}
	if err := load("nosuch", 3); err == nil {
		t.Fatal("Load accepted a breakdown against a suite that is not in the file")
	}
}

// Regenerating must settle. Every generated region is rendered from the same
// inputs twice and must be byte-identical, or CI reports a diff on every run.
func TestGenerationIsIdempotent(t *testing.T) {
	r := shipped(t)
	root := repoRoot(t)

	regions := r.Regions()
	for rel, gs := range regions {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		once, err := ReplaceAll(string(b), gs)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		twice, err := ReplaceAll(once, gs)
		if err != nil {
			t.Fatalf("%s (second pass): %v", rel, err)
		}
		if once != twice {
			t.Errorf("%s: regenerating its own output is not idempotent", rel)
		}
	}
}

// The checked-in documents must equal what the checked-in JSON and the tree
// generate. This is the assertion that fails on a hand-edit, and it is the one
// that must be provable by breaking it: change 2,149 to 2,150 in any of the
// five files and this test fails by name.
func TestEveryCheckedInRegionIsGenerated(t *testing.T) {
	r := shipped(t)
	root := repoRoot(t)
	files, err := r.ApplyAll(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no generated files; Regions() returned nothing")
	}
	for _, f := range files {
		if f.Changed {
			t.Errorf("%s is out of date with tests/conformance/results.json and the tree;\n"+
				"    run: go run tests/conformance-docs.go", f.Path)
		}
	}
}

// Every region must actually be present in the file it claims. A region whose
// markers nobody added is a check that silently guards nothing, which this
// repository has shipped before.
func TestEveryRegionHasItsMarkers(t *testing.T) {
	r := shipped(t)
	root := repoRoot(t)
	for rel, gs := range r.Regions() {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		doc := string(b)
		for _, g := range gs {
			if g.Whole {
				continue
			}
			if !strings.Contains(doc, g.Begin()) {
				t.Errorf("%s: missing %s", rel, g.Begin())
			}
			if !strings.Contains(doc, g.End()) {
				t.Errorf("%s: missing %s", rel, g.End())
			}
		}
	}
}

// A hand-edit inside a region must be detected. This is the test that proves
// the check is not decorative, so it is written to be provable in turn: it
// finds a real digit inside each real region, moves it, and asserts both that
// regeneration reverses the edit and that the edit was detectable at all.
//
// It edits INSIDE the region rather than anywhere in the file, because those
// are different claims. An edit outside a region must survive untouched --
// that is the prose contract -- and a test that conflated the two would pass
// while covering nothing.
func TestHandEditInsideARegionIsCaught(t *testing.T) {
	r := shipped(t)
	root := repoRoot(t)

	edited := 0
	for rel, gs := range r.Regions() {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		doc := string(b)
		for _, g := range gs {
			body, ok := regionBody(doc, g)
			if !ok {
				t.Errorf("%s/%s: the region is not in the file", rel, g.Name)
				continue
			}
			bad, ok := bumpFirstDigit(body)
			if !ok {
				t.Errorf("%s/%s: the region contains no digit, so it publishes no figure", rel, g.Name)
				continue
			}
			edited++
			broken := strings.Replace(doc, body, bad, 1)
			if broken == doc {
				t.Fatalf("%s/%s: could not construct the hand-edit", rel, g.Name)
			}
			next, err := ReplaceAll(broken, gs)
			if err != nil {
				t.Fatalf("%s/%s: %v", rel, g.Name, err)
			}
			if next == broken {
				t.Errorf("%s/%s: a hand-edited figure survived regeneration — the region does not cover it", rel, g.Name)
			}
			if next != doc {
				t.Errorf("%s/%s: regeneration did not restore the document exactly", rel, g.Name)
			}
		}
	}
	if edited == 0 {
		t.Fatal("no region was exercised; this test would pass over an empty mechanism")
	}
}

// An edit OUTSIDE every region must survive regeneration untouched. The
// documents are long, careful, hand-written English and generation replaces
// marked regions only; without this assertion the previous test would still
// pass if the generator rewrote whole files.
func TestProseOutsideAllRegionsSurvives(t *testing.T) {
	r := shipped(t)
	root := repoRoot(t)
	for rel, gs := range r.Regions() {
		if wholeFile(gs) {
			continue // docs/stats.md has no prose; it IS the region
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		doc := string(b)
		// A sentinel placed at the very end of the file is outside every
		// region by construction.
		marked := doc + "\nA sentence no generator wrote. 999,999 of 999,999.\n"
		next, err := ReplaceAll(marked, gs)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if next != marked {
			t.Errorf("%s: regeneration touched prose outside every marked region", rel)
		}
	}
}

// regionBody returns the text between a region's markers, markers excluded.
func regionBody(doc string, g Region) (string, bool) {
	if g.Whole {
		return doc, doc != ""
	}
	i := strings.Index(doc, g.Begin())
	if i < 0 {
		return "", false
	}
	j := strings.Index(doc, g.End())
	if j < i {
		return "", false
	}
	return doc[i+len(g.Begin()) : j], true
}

// bumpFirstDigit moves one published digit, skipping the generated-by header
// comment (which carries no figure) so the edit lands on a real number.
func bumpFirstDigit(body string) (string, bool) {
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c < '0' || c > '9' {
			continue
		}
		// Not a digit inside a section reference such as §19.8 or a file name.
		d := byte('1')
		if c == '1' {
			d = '2'
		}
		return body[:i] + string(d) + body[i+1:], true
	}
	return "", false
}

// docs/stats.md is generated end to end and must name, for every figure, where
// it came from. A page of numbers with no provenance is the thing being
// replaced, not a fix for it.
func TestStatsPageCarriesProvenance(t *testing.T) {
	r := shipped(t)
	out := r.renderStats()
	for _, want := range []string{
		"# Statistics",
		"**This file is generated.**",
		"## Counted from the source tree",
		"## Measured by a suite run",
		"## Real-world corpora",
		"## Where each figure is published",
		"## What is still prose",
		"go run tests/conformance-docs.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("docs/stats.md is missing %q", want)
		}
	}
	// Every tree figure names its counting command, and every suite row its
	// run date and command.
	for _, tc := range r.Tree {
		if !strings.Contains(out, "`"+cell(tc.Method)+"`") {
			t.Errorf("tree count %q does not publish its method", tc.ID)
		}
	}
	for _, s := range r.Suites {
		if !strings.Contains(out, "`"+cell(s.Command)+"`") {
			t.Errorf("suite %q does not publish its command", s.ID)
		}
		if !strings.Contains(out, s.RunDate) {
			t.Errorf("suite %q does not publish its run date", s.ID)
		}
	}
	// The corpora are separate from the total by construction. The page must
	// say so, and the total must be the suite sum alone.
	if !strings.Contains(out, "kept out of the total above") {
		t.Error("docs/stats.md does not state that the corpora are excluded from the total")
	}
	if strings.Contains(out, "| **Total** | | | | | **681** |") {
		t.Error("the corpora reached the total")
	}
}

// The corpora exclusion is structural and must stay so. 577 and 225 are real
// measurements, but they are not disagreements with a specification, and a
// total that swallowed them would be a conformance claim against tests nobody
// wrote to a spec.
//
// The shipped corpora both pass 100%, so asserting Total() == 104 against the
// shipped file proves NOTHING about the exclusion: a generator that summed the
// corpora would print 104 too. The first sabotage of this test found exactly
// that, so the load-bearing assertion is made against a fixture whose corpus
// carries disagreements of its own -- the only arrangement in which summing
// them is visible.
func TestCorporaStayOutOfTheTotal(t *testing.T) {
	// twoSuites (in gaps_test.go) has suites totalling 8 and a corpus with 10.
	// If the corpus reached the total it would read 18.
	r := loadString(t, twoSuites)
	if got := r.Total(); got != 8 {
		t.Fatalf("Total() = %d, want 8 — the corpus's 10 disagreements reached the total", got)
	}
	out := r.renderStats()
	if strings.Contains(out, "**18**") {
		t.Error("docs/stats.md's total includes the corpus")
	}
	// The corpus must still be PUBLISHED -- excluding it from the total is not
	// the same as hiding it, and a page that dropped it would be under-
	// reporting rather than correctly scoping.
	if !strings.Contains(out, "| Corpus |") {
		t.Errorf("the corpus row is missing from docs/stats.md entirely:\n%s", out)
	}
	if !strings.Contains(out, "kept out of the total above") {
		t.Error("docs/stats.md does not state that the corpora are excluded from the total")
	}

	// And the shipped file keeps the two apart as separate lists, which is
	// what makes the exclusion impossible to undo by an arithmetic change.
	sr := shipped(t)
	if got := sr.Total(); got != 98 {
		t.Fatalf("shipped Total() = %d, want 98", got)
	}
	for _, c := range sr.Corpora {
		for _, su := range sr.Suites {
			if su.ID == c.ID {
				t.Errorf("%q is in both Suites and Corpora", c.ID)
			}
		}
	}
	if len(sr.Corpora) != 2 {
		t.Errorf("results.json has %d corpora, want 2 (DocBook xslTNG and XSpec)", len(sr.Corpora))
	}
}

// A CRLF document must survive every region, not only the conformance summary.
// On Windows with core.autocrlf=true a generator emitting LF would report a
// diff on every run for ever, and there is no .gitattributes to prevent it.
// CLAUDE.md rule 3 asks for this to be tested rather than merely handled.
func TestEveryRegionPreservesCRLF(t *testing.T) {
	r := shipped(t)
	for rel, gs := range r.Regions() {
		for _, g := range gs {
			var doc string
			if g.Whole {
				doc = "anything\r\n"
			} else {
				doc = "Hand-written prose.\r\n" + g.Begin() + "\r\nstale\r\n" + g.End() + "\r\nMore prose.\r\n"
			}
			got, err := ReplaceAll(doc, []Region{g})
			if err != nil {
				t.Fatalf("%s/%s: %v", rel, g.Name, err)
			}
			if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
				t.Errorf("%s/%s: a bare LF survived into a CRLF document", rel, g.Name)
			}
			if !g.Whole {
				if !strings.HasPrefix(got, "Hand-written prose.\r\n") || !strings.HasSuffix(got, "More prose.\r\n") {
					t.Errorf("%s/%s: the surrounding prose did not survive:\n%q", rel, g.Name, got)
				}
			}
			// And it must settle on its own output.
			again, err := ReplaceAll(got, []Region{g})
			if err != nil {
				t.Fatalf("%s/%s (second pass): %v", rel, g.Name, err)
			}
			if again != got {
				t.Errorf("%s/%s: regenerating a CRLF document is not idempotent", rel, g.Name)
			}
		}
	}
}

// An LF document stays LF. The symmetric half: a generator that upgraded every
// file to CRLF because it once saw one would be just as bad.
func TestRegionsPreserveLF(t *testing.T) {
	r := shipped(t)
	for rel, gs := range r.Regions() {
		for _, g := range gs {
			if g.Whole {
				continue
			}
			doc := "Prose.\n" + g.Begin() + "\nstale\n" + g.End() + "\nProse.\n"
			got, err := ReplaceAll(doc, []Region{g})
			if err != nil {
				t.Fatalf("%s/%s: %v", rel, g.Name, err)
			}
			if strings.Contains(got, "\r") {
				t.Errorf("%s/%s: a CR appeared in an LF document", rel, g.Name)
			}
		}
	}
}

// A region whose markers are missing, crossed or half-present must fail loudly.
// Silently skipping it would mean a document quietly stops being guarded.
func TestReplaceRegionRequiresBothMarkers(t *testing.T) {
	r := shipped(t)
	g := r.Regions()["README.md"][0]
	for _, doc := range []string{
		"no markers at all\n",
		g.Begin() + "\nonly the opening\n",
		g.End() + "\nonly the closing\n",
		g.End() + "\n" + g.Begin() + "\n",
	} {
		if _, err := ReplaceRegion(doc, g); err == nil {
			t.Errorf("ReplaceRegion accepted %q", doc)
		}
	}
}

// Looking up a figure that does not exist must panic rather than print zero. A
// template quietly publishing "0 func Test declarations" is the failure mode
// this whole mechanism exists to make impossible.
func TestUnknownLookupPanics(t *testing.T) {
	r := shipped(t)
	for name, fn := range map[string]func(){
		"TreeCount": func() { r.TreeCount("no-such-count") },
		"Suite":     func() { r.Suite("no-such-suite") },
		"Breakdown": func() { r.Breakdown("no-such-breakdown") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s did not panic on an unknown id", name)
				}
			}()
			fn()
		}()
	}
}

// The shipped breakdown is the figure that was wrong in three files at once.
func TestShippedXTSE3430Breakdown(t *testing.T) {
	r := shipped(t)
	b := r.Breakdown("xtse3430")
	if b.Of != 8 {
		t.Errorf("xtse3430 = %d of the XSLT 3.0 failures, want 8", b.Of)
	}
	s := r.Suite(b.Suite)
	if s.Disagreements != 28 {
		t.Errorf("xslt-3.0 has %d disagreements, want 28", s.Disagreements)
	}
	// The two published halves must account for the whole, once the declared
	// overlap is discounted: 21 enumerated + 8 in the block, sharing
	// su-ascent-903, is exactly 28. This shipped so wrong -- 21 + 14 = 35 --
	// because each half was only ever checked against the total.
	if b.Overlap != 1 {
		t.Errorf("xtse3430 declares overlap %d, want 1 (su-ascent-903)", b.Overlap)
	}
	if n := len(s.Cases) + b.Of - b.Overlap; n != s.Disagreements {
		t.Errorf("%d enumerated + %d in the block - %d overlap = %d, want the suite's %d",
			len(s.Cases), b.Of, b.Overlap, n, s.Disagreements)
	}
	// su-ascent-902 is the overlap, so it must actually be enumerated; and
	// merge-097sf must not be, because it is skipped for streaming-fallback
	// rather than failing.
	var haveAscent, haveSF bool
	for _, c := range s.Cases {
		switch c.ID {
		case "su-ascent-903":
			haveAscent = true
		case "merge-097sf":
			haveSF = true
		}
	}
	if !haveAscent {
		t.Error("su-ascent-903 is declared as the overlap but is not enumerated")
	}
	if haveSF {
		t.Error("merge-097sf is enumerated as a disagreement; it is skipped for streaming-fallback")
	}
}

// The sum check the file lacked. An enumeration and a breakdown are both
// published as parts of one total, so a reader adds them; if they can sum past
// the total, the document lies and nothing says so. Undeclared overlap must
// fail, declared overlap must pass.
func TestEnumeratedCasesAndBreakdownCannotExceedTheTotal(t *testing.T) {
	body := func(overlap string) string {
		return `{
  "suites": [
    {"id": "s", "component": "a", "suite": "S", "edition": "S 1.0",
     "passed": 90, "disagreements": 10, "total": 100,
     "run_date": "2026-01-02", "command": "run",
     "cases": [
       {"id": "c1", "verdict": "fixture"},
       {"id": "c2", "verdict": "fixture"},
       {"id": "c3", "verdict": "fixture"}
     ]}
  ],
  "breakdowns": [{"id": "b", "label": "B", "suite": "s", "of": 8` + overlap + `}]
}`
	}
	load := func(overlap string) error {
		p := filepath.Join(t.TempDir(), "results.json")
		if err := os.WriteFile(p, []byte(body(overlap)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}
	// 3 enumerated + 8 in the block = 11 against 10 disagreements.
	err := load("")
	if err == nil {
		t.Fatal("Load accepted 3 enumerated cases plus a breakdown of 8 against 10 disagreements")
	}
	if !strings.Contains(err.Error(), "only 10") {
		t.Errorf("the error does not say what the suite actually has: %v", err)
	}
	// Declaring the one shared case makes the arithmetic close at exactly 10.
	if err := load(`, "overlap": 1`); err != nil {
		t.Fatalf("a declared overlap of 1 closes the sum at 10 and must be accepted: %v", err)
	}
	// An overlap larger than the breakdown itself is not a description of
	// anything, and must not be a way to silence the check.
	if err := load(`, "overlap": 9`); err == nil {
		t.Fatal("Load accepted an overlap of 9 against a breakdown of 8")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// A counting command is a shell pipeline. An unescaped pipe inside a Markdown
// table cell ends the cell -- backticks do not protect it -- so the published
// method would silently shred the table it appears in, and the reader would
// get a truncated command that returns a different number. Escaping is checked
// here because the damage is invisible in the source and only shows in a
// renderer.
func TestTableCellsEscapePipes(t *testing.T) {
	if got := cell("a | b"); got != `a \| b` {
		t.Errorf("cell(%q) = %q, want %q", "a | b", got, `a \| b`)
	}
	r := shipped(t)
	out := r.renderStats()
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		// Count unescaped pipes: a row must have exactly one more than its
		// header declares, and any command's own pipes must be escaped.
		body := line
		for i := 1; i < len(body); i++ {
			if body[i] == '|' && body[i-1] == '\\' {
				continue
			}
			if body[i] != '|' {
				continue
			}
			// An unescaped pipe must be a column separator: surrounded by
			// spaces or at the line's end.
			if body[i-1] != ' ' && body[i-1] != '\\' {
				t.Errorf("unescaped pipe inside a cell:\n  %s", line)
				break
			}
		}
	}
	// And every shipped method, which all contain pipes, must survive intact.
	for _, tc := range r.Tree {
		if strings.Contains(tc.Method, "|") && !strings.Contains(out, cell(tc.Method)) {
			t.Errorf("method for %q is not published with its pipes escaped", tc.ID)
		}
	}
}

// The suite revisions recorded beside the figures must be the ones the tree
// actually holds.
//
// A figure in results.json is a measurement, and a measurement whose
// conditions are unrecorded is a number someone later reads as current. The
// suites are separate checkouts that CI clones at --depth 1 from their
// default branch, so a suite update can move a count with no change to this
// repository -- and the ratchet would then fail on a commit that changed
// nothing, with no way to tell that from a real regression.
//
// A stale recorded revision would be worse than none, because it would be
// believed. This checks each one against the checkout, and skips rather than
// fails where the suite is absent: a developer without the corpora should not
// be told their tree is inconsistent.
func TestRecordedSuiteRevisionsMatchTheCheckouts(t *testing.T) {
	root := repoRoot(t)
	res, err := Load(filepath.Join(root, "tests", "conformance", "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SuiteRevisions) == 0 {
		t.Skip("no suite revisions recorded")
	}
	checked := 0
	for name, want := range res.SuiteRevisions {
		dir := filepath.Join(root, "testdata", name)
		if _, err := os.Stat(dir); err != nil {
			continue // corpus not present in this checkout
		}
		// The same containment test suiterev in tests/check.sh applies: a
		// directory that is not its own checkout answers with THIS
		// repository's HEAD, which would silently compare a suite revision
		// against a source commit.
		top, err := exec.Command("git", "-C", dir, "rev-parse",
			"--show-toplevel").Output()
		if err != nil {
			continue
		}
		realTop, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
		realDir, err2 := filepath.EvalSymlinks(dir)
		if err1 != nil || err2 != nil || realTop != realDir {
			t.Errorf("results.json records a revision for testdata/%s, which "+
				"is not its own git checkout; the value recorded is this "+
				"repository's HEAD and means nothing about the suite", name)
			continue
		}
		got, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			continue
		}
		checked++
		if g := strings.TrimSpace(string(got)); g != want {
			t.Errorf("testdata/%s is at %s but results.json says the figures "+
				"were measured against %s.\n    Re-run tests/check.sh and "+
				"regenerate, or restore the suite to the recorded revision. "+
				"A count that moved because the SUITE moved is not a "+
				"regression in this repository.", name, g[:12], want[:12])
		}
	}
	if checked == 0 {
		t.Skip("no recorded suite is present as its own checkout here")
	}
}
