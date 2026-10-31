package xdm

import (
	"strings"
	"testing"
)

// The line index is built by append, which leaves its slack allocated; a
// parse must hand back an index with none (TrackPositions on a 100 MB
// document kept 0.9 MB unused).
func TestTrackPositionsLineIndexHasNoSlack(t *testing.T) {
	for _, n := range []int{1000, 5000} { // 5000 lands mid-doubling
		src := "<r>" + strings.Repeat("<e/>\n", n) + "</r>"
		opts := ParseOptions{TrackPositions: true}
		fromString, err := ParseString(src, opts)
		if err != nil {
			t.Fatal(err)
		}
		fromReader, err := Parse(strings.NewReader(src), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, tree := range []*Tree{fromString, fromReader} {
			l := tree.lineIdx()
			if len(l.starts) < n || cap(l.starts) != len(l.starts) {
				t.Errorf("n=%d: line index len %d cap %d", n, len(l.starts), cap(l.starts))
			}
		}
	}
}
