package qt3

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestQT3 runs the W3C QT3 (FOTS) suite.
//
// It skips unless GOXSLT_QT3 names a checkout, so the ordinary `go test ./...`
// is unaffected. Set GOXSLT_QT3_VERBOSE=1 to list every failure.
func TestQT3(t *testing.T) {
	root := SuiteRoot()
	if root == "" {
		t.Skip("set GOXSLT_QT3 to a checkout of github.com/w3c/qt3tests to run the suite")
	}
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	r := NewRunner(root, cat)
	var pass, fail, skip int
	failsBySet := map[string]int{}
	skipReasons := map[string]int{}
	var failures []Report

	for _, ref := range cat.TestSets {
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
