package xslts

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestDumpFailures(t *testing.T) {
	root := "../../testdata/xslt30-test"
	r := &Runner{Root: root, Timeout: 10 * time.Second}
	sum, err := r.Run()
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create("/private/tmp/claude-501/-Users-roy-Desktop-Hash-go-xml-validator/459f7818-f853-42e2-9b12-62fa295bd029/scratchpad/failures.txt")
	defer f.Close()
	for _, o := range sum.Failures {
		fmt.Fprintf(f, "%s\t%s\t%s\n", o.Set, o.Name, o.Why)
	}
}
