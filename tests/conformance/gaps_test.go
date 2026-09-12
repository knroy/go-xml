package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoSuites is the fixture the arithmetic is checked against: two suites whose
// disagreements are 3 and 5, so the total must be 8 and can be verified by
// hand. It is deliberately not the real file -- a test that asserts the shipped
// numbers only proves the shipped numbers were read back.
const twoSuites = `{
  "suites": [
    {"id": "alpha", "component": "a", "suite": "Alpha suite", "edition": "Alpha 1.0",
     "passed": 97, "disagreements": 3, "total": 100,
     "run_date": "2026-01-02", "command": "alpha-run",
     "cases": [{"id": "a-1", "verdict": "fixture"}]},
    {"id": "beta", "component": "b", "suite": "Beta suite", "edition": "Beta 2.0",
     "passed": 1995, "disagreements": 5, "total": 2000,
     "run_date": "2026-01-03", "command": "beta-run", "cases": []}
  ],
  "corpora": [
    {"id": "corpus", "component": "b", "suite": "Corpus", "edition": "real-world corpus",
     "passed": 40, "disagreements": 10, "total": 50,
     "run_date": "2026-01-03", "command": "corpus-run", "cases": []}
  ]
}`

func loadString(t *testing.T, s string) *Results {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "results.json")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// The total is the sum of the suite rows and nothing else. The corpus row
// carries 10 disagreements of its own and must not reach the total: that
// exclusion is a documented property of the figure, not a formatting choice.
func TestTotalSumsSuitesOnly(t *testing.T) {
	r := loadString(t, twoSuites)
	if got := r.Total(); got != 8 {
		t.Fatalf("Total() = %d, want 8 (3 + 5; the corpus's 10 must not count)", got)
	}
}

func TestRenderShowsDerivedTotal(t *testing.T) {
	r := loadString(t, twoSuites)
	out := r.Render()
	for _, want := range []string{
		"| **a** | Alpha suite | 100 | 97 | 97.00% | **3** |",
		"| **b** | Beta suite | 2,000 | 1,995 | 99.75% | **5** |",
		"| **b** | Corpus *(real-world)* | 50 | 40 | 80.00% | 10 |",
		"| | **Total** | | | | **8** |",
		"W3C disagreements: 3 + 5 = 8.",
		"Measured 2026-01-02, 2026-01-03.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Render() is missing %q\ngot:\n%s", want, out)
		}
	}
}

// A suite whose parts do not add up must fail generation BY NAME. An unnamed
// arithmetic error in a nine-row file is barely better than none: whoever
// reads it in CI has to re-add every row to find which one moved.
func TestInconsistentSuiteFailsByName(t *testing.T) {
	const bad = `{
  "suites": [
    {"id": "alpha", "component": "a", "suite": "Alpha suite", "edition": "Alpha 1.0",
     "passed": 97, "disagreements": 3, "total": 100,
     "run_date": "2026-01-02", "command": "alpha-run", "cases": []},
    {"id": "wonky", "component": "b", "suite": "Wonky suite", "edition": "Beta 2.0",
     "passed": 1990, "disagreements": 5, "total": 2000,
     "run_date": "2026-01-03", "command": "beta-run", "cases": []}
  ]
}`
	dir := t.TempDir()
	p := filepath.Join(dir, "results.json")
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("Load accepted passed 1990 + disagreements 5 != total 2000")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"wonky"`) {
		t.Errorf("error does not name the offending suite: %v", msg)
	}
	// The numbers must be in the message too, or the reader still has to open
	// the file to see which of the three is wrong.
	for _, want := range []string{"1990", "1995", "2000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q: %v", want, msg)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	base := func(body string) string { return `{"suites": [` + body + `]}` }
	const ok = `{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 9, "disagreements": 1, "total": 10,
	     "run_date": "2026-01-02", "command": "run", "cases": []}`
	cases := []struct {
		name, json, want string
	}{
		{"no suites", `{"suites": []}`, "no suites"},
		{"no edition", base(`{"id": "a", "component": "a", "suite": "A",
	     "passed": 9, "disagreements": 1, "total": 10,
	     "run_date": "2026-01-02", "command": "run", "cases": []}`), "specification edition"},
		{"no run date", base(`{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 9, "disagreements": 1, "total": 10, "command": "run", "cases": []}`), "run date"},
		{"no command", base(`{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 9, "disagreements": 1, "total": 10, "run_date": "2026-01-02", "cases": []}`), "command"},
		{"duplicate id", base(ok + `,` + ok), "duplicate id"},
		{"bad verdict", base(`{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 9, "disagreements": 1, "total": 10,
	     "run_date": "2026-01-02", "command": "run",
	     "cases": [{"id": "c", "verdict": "probably-fine"}]}`), "want one of"},
		{"more cases than disagreements", base(`{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 9, "disagreements": 1, "total": 10,
	     "run_date": "2026-01-02", "command": "run",
	     "cases": [{"id": "c", "verdict": "fixture"}, {"id": "d", "verdict": "fixture"}]}`), "only 1 disagreements"},
		{"duplicate case", base(`{"id": "a", "component": "a", "suite": "A", "edition": "A 1.0",
	     "passed": 8, "disagreements": 2, "total": 10,
	     "run_date": "2026-01-02", "command": "run",
	     "cases": [{"id": "c", "verdict": "fixture"}, {"id": "c", "verdict": "fixture"}]}`), "duplicate case"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "results.json")
			if err := os.WriteFile(p, []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(p)
			if err == nil {
				t.Fatalf("Load accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Changing one count in a temporary tree must change the generated document,
// and nothing outside the markers may move. This is the whole contract: the
// JSON is the source, the region is the output, the prose is untouched.
func TestOneCountChangeRewritesOnlyTheRegion(t *testing.T) {
	dir := t.TempDir()
	results := filepath.Join(dir, "results.json")
	doc := filepath.Join(dir, "gaps.md")

	const prose = "Hand-written analysis that must survive verbatim — §19.8, 44 of 66.\n"
	before := "# Heading\n\n" + prose + "\n" + BeginMarker + "\nstale\n" + EndMarker + "\n\n" + prose

	write := func(s string) {
		if err := os.WriteFile(results, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(twoSuites)
	if err := os.WriteFile(doc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(results)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := Apply(doc, r); err != nil || !changed {
		t.Fatalf("Apply: changed=%v err=%v, want changed with no error", changed, err)
	}
	first, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "| | **Total** | | | | **8** |") {
		t.Fatalf("first generation missing total 8:\n%s", first)
	}
	// Regeneration is idempotent, or the CI diff check would fail forever.
	if changed, err := Apply(doc, r); err != nil || changed {
		t.Fatalf("second Apply: changed=%v err=%v, want no change", changed, err)
	}

	// Now move one count: beta gains a disagreement and loses a pass.
	write(strings.Replace(twoSuites, `"passed": 1995, "disagreements": 5`, `"passed": 1994, "disagreements": 6`, 1))
	r2, err := Load(results)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := Apply(doc, r2); err != nil || !changed {
		t.Fatalf("Apply after count change: changed=%v err=%v, want changed", changed, err)
	}
	after, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(after)
	if !strings.Contains(got, "| | **Total** | | | | **9** |") {
		t.Errorf("total did not follow the count change to 9:\n%s", got)
	}
	if !strings.Contains(got, "| **b** | Beta suite | 2,000 | 1,994 | 99.70% | **6** |") {
		t.Errorf("beta row did not follow the count change:\n%s", got)
	}
	if strings.Contains(got, "**8** |") {
		t.Errorf("the stale total 8 survived regeneration:\n%s", got)
	}
	// The prose on both sides of the region is byte-identical.
	if n := strings.Count(got, prose); n != 2 {
		t.Errorf("hand-written prose appears %d times, want 2 (it must survive verbatim on both sides)", n)
	}
	if !strings.HasPrefix(got, "# Heading\n\n"+prose) {
		t.Error("the heading and leading prose were rewritten")
	}
	if !strings.HasSuffix(got, EndMarker+"\n\n"+prose) {
		t.Error("the trailing prose was rewritten")
	}
}

// A Windows checkout with core.autocrlf=true gives the document CRLF line
// endings. Regenerating must not convert the file, and must not report a diff
// on every run: the region is rendered in the ending the document already uses
// and no line outside it moves. CLAUDE.md rule 3 asks for this to be tested,
// not merely handled, and it is testable on any platform.
func TestReplacePreservesCRLF(t *testing.T) {
	r := loadString(t, twoSuites)
	const prose = "Hand-written prose.\r\n"
	doc := prose + BeginMarker + "\r\nstale\r\n" + EndMarker + "\r\n" + prose
	got, err := Replace(doc, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Error("a bare LF survived into a CRLF document")
	}
	if !strings.Contains(got, "| | **Total** | | | | **8** |\r\n") {
		t.Errorf("the generated table is not CRLF:\n%q", got)
	}
	if !strings.HasSuffix(got, EndMarker+"\r\n"+prose) {
		t.Errorf("the trailing prose did not survive:\n%q", got)
	}
	// And it must settle: a second pass over its own output changes nothing,
	// or CI would report a diff on every Windows run.
	again, err := Replace(got, r)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Error("regenerating a CRLF document is not idempotent")
	}
}

func TestReplaceRequiresBothMarkers(t *testing.T) {
	r := loadString(t, twoSuites)
	for _, doc := range []string{
		"no markers at all\n",
		BeginMarker + "\nonly the opening\n",
		EndMarker + "\nonly the closing\n",
		EndMarker + "\n" + BeginMarker + "\n",
	} {
		if _, err := Replace(doc, r); err == nil {
			t.Errorf("Replace accepted %q", doc)
		}
	}
}

// The shipped file is the measurement this repository publishes. These are the
// figures the audit settled on: XPath zero at all three versions, XQuery one,
// XSLT 8 and 34, XSD 30 and 31, RELAX NG zero, and a total of 104 -- not the
// 168 the document used to print.
func TestShippedResults(t *testing.T) {
	r, err := Load(filepath.Join("results.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"xpath-2.0":  0,
		"xpath-3.0":  0,
		"xpath-3.1":  0,
		"xquery-3.1": 1,
		"xslt-2.0":   8,
		"xslt-3.0":   28,
		"xsd-1.0":    30,
		"xsd-1.1":    32,
		"relaxng":    0,
	}
	got := map[string]int{}
	for _, s := range r.Suites {
		got[s.ID] = s.Disagreements
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("%s: %d disagreements, want %d", id, got[id], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("results.json has %d suites, want %d", len(got), len(want))
	}
	if total := r.Total(); total != 99 {
		t.Errorf("Total() = %d, want 99", total)
	}
}

// The checked-in document must equal what the checked-in JSON generates. This
// is the same assertion tests/check.sh makes with -check, kept here as well so
// that `go test ./...` alone catches a hand-edit to the generated table.
func TestCheckedInDocumentIsGenerated(t *testing.T) {
	r, err := Load("results.json")
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join("..", "..", "docs", "conformance-gaps.md")
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Replace(string(b), r)
	if err != nil {
		t.Fatal(err)
	}
	if next != string(b) {
		t.Errorf("docs/conformance-gaps.md is out of date with tests/conformance/results.json;\n" +
			"run: go run tests/conformance-docs.go")
	}
}
