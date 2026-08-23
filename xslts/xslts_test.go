package xslts

import (
	"os"
	"sort"
	"testing"
	"time"
)

// The W3C XSLT suite, filtered to what an XSLT 2.0 processor should pass.
//
// Set GOXSLT_XSLTS to a checkout to run it; without one these skip, so the
// ordinary `go test ./...` stays fast and needs no download:
//
//	git clone --depth 1 https://github.com/w3c/xslt30-test.git
//	GOXSLT_XSLTS=xslt30-test go test ./xslts/ -v
//
// GOXSLT_XSLTS_VERBOSE=1 lists the failures rather than only counting them.
func TestXSLTSuite(t *testing.T) {
	root := os.Getenv("GOXSLT_XSLTS")
	if root == "" {
		root = "../testdata/xslt30-test"
	}
	if _, err := os.Stat(root + "/catalog.xml"); err != nil {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test to run the suite")
	}

	r := &Runner{Root: root, Timeout: 10 * time.Second}
	sum, err := r.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	inScope := sum.Passed + sum.Failed
	if inScope == 0 {
		t.Fatal("no tests ran, which means the filter or the harness is wrong")
	}
	t.Logf("XSLT suite: %d cases, %d in scope, %d skipped",
		sum.Total, inScope, sum.Skipped)
	t.Logf("in-scope: %d passed, %d failed (%.2f%%)",
		sum.Passed, sum.Failed, 100*float64(sum.Passed)/float64(inScope))

	// The scope of a run is part of its result: a percentage over an
	// unexplained subset says nothing. The largest exclusions are printed so
	// that a change in what is skipped is as visible as a change in what
	// fails.
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

	if os.Getenv("GOXSLT_XSLTS_VERBOSE") != "" {
		for i, f := range sum.Failures {
			if i >= 20000 {
				t.Logf("  ... and %d more", len(sum.Failures)-i)
				break
			}
			t.Logf("  FAIL %s/%s: %s", f.Set, f.Name, f.Why)
		}
	}
}
