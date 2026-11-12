package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Every workload streams on TransformReader and writes what the tree writes:
// a small document, both modes, outputs compared by digest.
func TestWorkloadsStreamAndAgree(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the command")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "streambench")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	doc := filepath.Join(dir, "d.xml")
	if _, err := generate(60000, doc); err != nil {
		t.Fatal(err)
	}
	for _, w := range workloads {
		tree, err := runOnce(bin, "tree", w.name, doc)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := runOnce(bin, "stream", w.name, doc)
		if err != nil {
			t.Fatal(err)
		}
		if tree.out != stream.out {
			t.Errorf("%s: tree wrote %s, stream %s", w.name, tree.out, stream.out)
		}
		// 11 MB of input: the tree holds 35-60 MB, a stream about 4 MB
		// whatever the size; a stream that fell back would hold the tree's.
		if stream.heapMB*2 > tree.heapMB {
			t.Errorf("%s: stream peak heap %.1f MB against the tree's %.1f MB; did it stream?", w.name, stream.heapMB, tree.heapMB)
		}
	}
}
