package xslts

import (
	"path/filepath"
	"testing"
)

// TestStreamedSourceInline: a streaming="true" principal input given as
// inline <content> is handed to TransformReader (its text), not parsed
// into a tree, as a file source is; one with a @select is not.
func TestStreamedSourceInline(t *testing.T) {
	dir := t.TempDir()
	set := &TestSet{Dir: dir, Path: filepath.Join(dir, "_set.xml")}
	r := &Runner{}
	for _, c := range []struct {
		src  Source
		want bool
	}{
		{Source{Role: ".", Streaming: "true", Content: "<doc/>"}, true},
		{Source{Role: ".", Streaming: "true", Content: "<doc/>", Select: "/doc"}, false},
		{Source{Role: ".", Content: "<doc/>"}, false},
	} {
		tc := &TestCase{Environments: []Environment{{Sources: []Source{c.src}}}}
		_, content, opts := r.streamedSource(set, tc)
		if got := content != ""; got != c.want {
			t.Errorf("%+v: streamed %v, want %v", c.src, got, c.want)
		}
		if c.want && (content != "<doc/>" || opts.BaseURI != fileURI(set.Path)) {
			t.Errorf("%+v: content %q base %q", c.src, content, opts.BaseURI)
		}
	}
}
