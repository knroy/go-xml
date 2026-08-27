package xslts

import (
	"os"
	"sort"
	"testing"
	"time"
)

// TestXSLT30Suite is TestXSLTSuite at the XSLT 3.0 target.
//
// It is a separate test rather than a mode of the first because both must run
// on every change: the question a change has to answer is not "how much 3.0
// works" but "how much 3.0 works without costing 2.0", and only two runs
// answer it.
func TestXSLT30Suite(t *testing.T) {
	root := os.Getenv("GOXSLT_XSLTS")
	if root == "" {
		root = "../../testdata/xslt30-test"
	}
	if _, err := os.Stat(root + "/catalog.xml"); err != nil {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test to run the suite")
	}

	r := &Runner{Root: root, Timeout: 10 * time.Second, Target: XSLT30}
	sum, err := r.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	inScope := sum.Passed + sum.Failed
	if inScope == 0 {
		t.Fatal("no tests ran, which means the filter or the harness is wrong")
	}
	t.Logf("XSLT 3.0 suite: %d cases, %d in scope, %d skipped",
		sum.Total, inScope, sum.Skipped)
	t.Logf("in-scope: %d passed, %d failed (%.2f%%)",
		sum.Passed, sum.Failed, 100*float64(sum.Passed)/float64(inScope))

	type reason struct {
		why string
		n   int
	}
	var reasons []reason
	for w, n := range sum.SkipReasons {
		reasons = append(reasons, reason{w, n})
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i].n > reasons[j].n })
	for i, r := range reasons {
		if i >= 12 {
			break
		}
		t.Logf("  skipped %5d  %s", r.n, r.why)
	}

	// The failing sets, worst first: a set failing nearly everything is an
	// unimplemented feature, one failing a handful is edge cases, and the two
	// want different work.
	if os.Getenv("GOXSLT_XSLTS_BYSET") != "" {
		type row struct {
			name       string
			pass, fail int
		}
		var rows []row
		for name, st := range sum.BySet {
			if st.Failed > 0 {
				rows = append(rows, row{name, st.Passed, st.Failed})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].fail != rows[j].fail {
				return rows[i].fail > rows[j].fail
			}
			return rows[i].name < rows[j].name
		})
		for _, r := range rows {
			t.Logf("  %-40s %5d pass %5d fail", r.name, r.pass, r.fail)
		}
	}
}
