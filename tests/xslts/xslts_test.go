package xslts

import (
	"os"
	"sort"
	"testing"

	"github.com/knroy/go-xml/xpath"
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
		root = "../../testdata/xslt30-test"
	}
	if _, err := os.Stat(root + "/catalog.xml"); err != nil {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test to run the suite")
	}

	// The suite's patterns are trusted input, so the backtracking matcher is
	// enabled for the run, exactly as the QT3 harness enables it. It is off by
	// default because a pattern can come from document data and the matcher
	// has no linear-time guarantee -- a conformance run is the case where that
	// does not apply. Leaving it off failed nine backreference cases across
	// the two targets for a reason that is a deployment choice rather than a
	// conformance gap, which made the headline figure understate what the
	// engine does: the matcher for them is written, tested and shipped.
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(false)

	// The per-case deadline is 60s rather than the 10s this used to use, and
	// it is a measurement parameter rather than a limit on the engine.
	//
	// Three cases in the "catalog" set parse every non-error stylesheet in the
	// suite inside one transform -- catalog-005b and catalog-007 are the two
	// that come closest to the wall. At 10s an idle machine finished them and
	// a loaded one did not, so the reported figure moved with what else was
	// running on the box: 8605 under a parallel build, 8607 on a quiet
	// machine. That is measurement noise presented as a conformance number,
	// and it made the ratchet fire on unmodified trees in CI.
	//
	// GOXSLT_CASE_TIMEOUT overrides it for a machine slower still. Raising it
	// cannot turn a failing case into a passing one except where the only
	// thing wrong was the clock, which is precisely the situation here.
	r := &Runner{Root: root, Timeout: caseTimeout()}
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

	// Per-feature results, worst first. A single percentage hides which
	// features work: a set failing nearly everything is unimplemented, while
	// one failing a handful is edge cases, and the two want different work.
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
			total := r.pass + r.fail
			t.Logf("  %-28s %4d/%-4d %5.1f%%", r.name, r.pass, total,
				100*float64(r.pass)/float64(total))
		}
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
