package xslts

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSIOnly(t *testing.T) {
	only := os.Getenv("GOXSLT_SI")
	if only == "" {
		t.Skip("set GOXSLT_SI")
	}
	want := map[string]bool{}
	for _, s := range strings.Split(only, ",") {
		want[s] = true
	}
	root := os.Getenv("GOXSLT_XSLTS")
	if root == "" {
		root = "../../testdata/xslt30-test"
	}
	r := &Runner{Root: root, Timeout: 10 * time.Second, Target: XSLT30}
	sum, err := r.Run()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range sum.Failures {
		if want[o.Set] {
			t.Logf("%s\t%s\t%.700s", o.Set, o.Name, o.Why)
		}
	}
	tot, totf := 0, 0
	for s := range want {
		if st := sum.BySet[s]; st != nil {
			t.Logf("SET %s passed=%d failed=%d", s, st.Passed, st.Failed)
			tot += st.Passed
			totf += st.Failed
		}
	}
	t.Logf("MINE passed=%d failed=%d  ALL-PASSED %d", tot, totf, sum.Passed)
}
