package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Every workload writes what the tree writes, and streams: its peak heap
// does not grow with the input. A stream that fell back to the tree, or kept
// something per record, would peak about four times higher on four times
// the records. The peak is not compared with the tree's: it counts garbage
// not yet swept, which on a slow two-core runner reached half the tree's
// (34.5 against 68.3 MB on Windows) without the stream holding anything.
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
	small, large := filepath.Join(dir, "small.xml"), filepath.Join(dir, "large.xml")
	if _, err := generate(60000, small); err != nil { // 11 MB
		t.Fatal(err)
	}
	if _, err := generate(240000, large); err != nil { // 45 MB
		t.Fatal(err)
	}
	for _, w := range workloads {
		tree, err := runOnce(bin, "tree", w.name, small)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := runOnce(bin, "stream", w.name, small)
		if err != nil {
			t.Fatal(err)
		}
		if tree.out != stream.out {
			t.Errorf("%s: tree wrote %s, stream %s", w.name, tree.out, stream.out)
		}
		big, err := runOnce(bin, "stream", w.name, large)
		if err != nil {
			t.Fatal(err)
		}
		if big.heapMB > 2*stream.heapMB {
			t.Errorf("%s: streamed peak heap %.1f MB at 4x the records, %.1f MB at 1x; did it stream?", w.name, big.heapMB, stream.heapMB)
		}
	}
}
