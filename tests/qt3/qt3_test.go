package qt3

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// TestQT3 runs the W3C QT3 (FOTS) suite, once per language version.
//
// It skips unless GOXSLT_QT3 names a checkout, so the ordinary `go test ./...`
// is unaffected. Set GOXSLT_QT3_VERBOSE=1 to list every failure.
//
// Both versions are run because they measure different things. The 2.0 figure
// is a regression check: it is at 99.99% and must stay there while 3.0 is
// implemented, since 3.0 work that changes 2.0 behaviour is a bug rather than
// progress. The 3.0 figure is the one that moves.
func TestQT3(t *testing.T) {
	root := SuiteRoot()
	if root == "" {
		t.Skip("set GOXSLT_QT3 to a checkout of github.com/w3c/qt3tests to run the suite")
	}
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	// The suite's patterns are trusted input, so the backtracking matcher is
	// enabled for the run. It is off by default because a pattern can come
	// from document data and the matcher has no linear-time guarantee; a
	// conformance run is exactly the case where that does not apply, and
	// leaving it off failed the backreference cases for a reason that is a
	// deployment choice rather than a conformance gap.
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(false)

	for _, target := range []TargetVersion{XPath20, XPath30} {
		t.Run(target.String(), func(t *testing.T) { runSuite(t, root, cat, target) })
	}
}

func runSuite(t *testing.T, root string, cat *Catalog, target TargetVersion) {
	r := NewRunner(root, cat)
	r.Target = target
	var pass, fail, skip int
	failsBySet := map[string]int{}
	skipReasons := map[string]int{}
	var failures []Report

	// GOXSLT_QT3_SET narrows the run to test sets whose name contains the
	// value, which is how a single failure is worked on without waiting for
	// the other thirty thousand cases. The reported percentage is then over
	// that subset, so it is labelled as filtered rather than quoted as the
	// suite result.
	only := os.Getenv("GOXSLT_QT3_SET")

	for _, ref := range cat.TestSets {
		if only != "" && !strings.Contains(ref.Name, only) {
			continue
		}
		ts, err := LoadTestSet(root, ref.File)
		if err != nil {
			// A test-set that will not parse is reported, not ignored: it
			// would otherwise silently shrink the denominator.
			t.Errorf("test-set %s: %v", ref.Name, err)
			continue
		}
		for i := range ts.Cases {
			rep := r.Run(ts, &ts.Cases[i])
			switch rep.Outcome {
			case Pass:
				pass++
			case Fail:
				fail++
				failsBySet[ts.Name]++
				failures = append(failures, rep)
			case Skip:
				skip++
				skipReasons[bucket(rep.Reason)]++
			}
		}
	}

	total := pass + fail + skip
	inScope := pass + fail
	if only != "" {
		t.Logf("QT3 (filtered to test sets matching %q)", only)
	}
	t.Logf("QT3: %d cases, %d in scope, %d skipped", total, inScope, skip)
	if inScope > 0 {
		t.Logf("in-scope: %d passed, %d failed (%.2f%%)",
			pass, fail, 100*float64(pass)/float64(inScope))
	}

	t.Log("top skip reasons:")
	for _, kv := range topN(skipReasons, 10) {
		t.Logf("  %6d  %s", kv.n, kv.k)
	}
	t.Log("test sets with the most failures:")
	for _, kv := range topN(failsBySet, 15) {
		t.Logf("  %6d  %s", kv.n, kv.k)
	}

	if os.Getenv("GOXSLT_QT3_VERBOSE") != "" {
		for _, f := range failures {
			t.Logf("FAIL %s/%s: %s", f.Set, f.Case, f.Reason)
			// The expression makes the line reproducible without opening the
			// catalog. Long ones are elided rather than dropped: a truncated
			// expression still identifies the construct.
			if e := oneLine(f.Expr, 160); e != "" {
				t.Logf("     expr: %s", e)
			}
		}
	}

	// The suite is a measurement, not a gate: a hard threshold here would
	// turn every upstream test-suite update into a build break. The numbers
	// above are the result; write them down rather than asserting on them.
	if inScope == 0 {
		t.Error("no cases were in scope, which means the filter is wrong")
	}
}

func bucket(reason string) string {
	if i := strings.Index(reason, ":"); i > 0 {
		return reason[:i]
	}
	if len(reason) > 40 {
		return reason[:40]
	}
	return reason
}

type kv struct {
	k string
	n int
}

func topN(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].k < out[j].k
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

var _ = fmt.Sprint

// oneLine flattens an expression onto a single line and elides the middle when
// it is longer than max.
//
// The middle rather than the tail: an XPath expression's distinguishing part is
// as often at the end as at the start, and a trailing cut hides which of a set
// of similar cases this is.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	half := (max - 3) / 2
	return s[:half] + "..." + s[len(s)-half:]
}
