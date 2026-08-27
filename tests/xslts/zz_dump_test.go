package xslts

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/knroy/go-xml/xpath"
)

// TestDumpFailures writes the failing cases to a file, for working through
// them one cluster at a time.
//
// It is a development aid rather than a check: it asserts nothing and passes
// whatever the suite reports. Set GOXSLT_DUMP to the file to write; without it
// the test does nothing, so an ordinary `go test ./...` neither needs the
// suite checked out nor writes anywhere.
//
// Set GOXSLT_DUMP_TARGET=3.0 to dump the XSLT 3.0 target's failures instead;
// the two targets fail on different cases, so working through one cluster
// means naming which target it belongs to.
//
//	GOXSLT_DUMP=/tmp/failures.txt go test ./tests/xslts/ -run TestDumpFailures
func TestDumpFailures(t *testing.T) {
	dest := os.Getenv("GOXSLT_DUMP")
	if dest == "" {
		t.Skip("set GOXSLT_DUMP to a file path to dump the failing cases")
	}

	root := os.Getenv("GOXSLT_XSLTS")
	if root == "" {
		root = "../../testdata/xslt30-test"
	}
	if _, err := os.Stat(root + "/catalog.xml"); err != nil {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test to run the suite")
	}

	r := &Runner{Root: root, Timeout: 10 * time.Second}
	if os.Getenv("GOXSLT_DUMP_TARGET") == "3.0" {
		r.Target = XSLT30
		xpath.SetBacktrackingRegex(true)
		defer xpath.SetBacktrackingRegex(false)
	}
	sum, err := r.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create dump: %v", err)
	}
	defer f.Close()
	for _, o := range sum.Failures {
		if _, err := fmt.Fprintf(f, "%s\t%s\t%s\n", o.Set, o.Name, o.Why); err != nil {
			t.Fatalf("write dump: %v", err)
		}
	}
	t.Logf("wrote %d failures to %s", len(sum.Failures), dest)
}
