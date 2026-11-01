package xpath

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// endMap builds the parsed object through xdm.MapBuilder, which writes the
// trie in place; Put allocated a path per key (2.6 KB a key at 1M keys).
func TestJSONObjectBuildAllocation(t *testing.T) {
	const keys = 20000
	var sb strings.Builder
	sb.WriteByte('{')
	for i := 0; i < keys; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":1`, i)
	}
	sb.WriteByte('}')
	text := sb.String()
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	seq, err := parseJSONText(NewContext(nil, Builtins()), text, jsonOptions{})
	runtime.ReadMemStats(&b)
	if err != nil || len(seq) != 1 {
		t.Fatalf("parse: %v %v", seq, err)
	}
	perKey := (b.TotalAlloc - a.TotalAlloc) / keys
	t.Logf("%d bytes allocated per key", perKey)
	if perKey > 1000 {
		t.Errorf("%d bytes allocated per key; want at most %d", perKey, 1000)
	}
}
