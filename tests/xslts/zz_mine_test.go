package xslts

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Scratch filter used during development: GOXSLT_ONLY=set1,set2 runs only
// those sets and prints each failure in full.
func TestMineOnly(t *testing.T) {
	only := os.Getenv("GOXSLT_ONLY")
	if only == "" {
		t.Skip("set GOXSLT_ONLY")
	}
	want := map[string]bool{}
	for _, s := range strings.Split(only, ",") {
		want[s] = true
	}
	r := &Runner{Root: "../../testdata/xslt30-test", Timeout: 10 * time.Second}
	if os.Getenv("GOXSLT_ONLY_TARGET") == "3.0" {
		r.Target = XSLT30
	}
	sum, err := r.Run()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range sum.Failures {
		if want[o.Set] {
			n++
			t.Logf("%s\t%s\t%.900s", o.Set, o.Name, o.Why)
		}
	}
	tot := 0
	for s := range want {
		if st := sum.BySet[s]; st != nil {
			t.Logf("SET %s passed=%d failed=%d", s, st.Passed, st.Failed)
			tot += st.Passed
		}
	}
	t.Logf("MINE-PASSED %d  failures %d  ALL-PASSED %d", tot, n, sum.Passed)
}
