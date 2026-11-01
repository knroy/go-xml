package c14n

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// ParsePrefixList scanned its result for every token: 1 MiB of distinct
// prefixes took 12 s of CPU. A map makes it linear.
func TestParsePrefixListLinear(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 1<<20; i++ {
		b.WriteString("p" + strconv.Itoa(i) + " ")
	}
	start := time.Now()
	got := ParsePrefixList(b.String())
	if d := time.Since(start); d > 2*time.Second || len(got) < 100000 {
		t.Errorf("1 MiB of prefixes: %d distinct in %v, want linear time", len(got), d)
	}
}
