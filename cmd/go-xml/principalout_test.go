package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The transform command writes its principal result as it is produced
// (TransformOptions.Output), to stdout or to a temporary file renamed over
// the -o file once the transform has succeeded (issue #40). These tests drive
// the built binary and pin what that must not change: a failed run leaves
// the -o file as it was, -o may name the input, a symlink is written through,
// a new file gets the mode os.Create gave it, and a device is written in
// place. Paths are built under t.TempDir with filepath, and the cases that
// need Unix (symlinks, umask, /dev/null) skip elsewhere.

// failSheet writes some output, then fails.
const failSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode streamable="yes" on-no-match="shallow-copy"/>
  <xsl:template match="stop"><xsl:message terminate="yes">stop</xsl:message></xsl:template>
</xsl:stylesheet>`

// copySheet copies its input.
const copySheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode streamable="yes" on-no-match="shallow-copy"/>
  <xsl:output omit-xml-declaration="yes"/>
</xsl:stylesheet>`

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var tmp []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			tmp = append(tmp, e.Name())
		}
	}
	return tmp
}

func TestOutputFileUntouchedOnFailure(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "fail.xsl")
	writeFile(t, xsl, failSheet)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<a><b>kept?</b><stop/></a>`)
	dest := filepath.Join(dir, "out.xml")
	writeFile(t, dest, "previous")

	if out, err := exec.Command(bin, "-xsl", xsl, "-o", dest, in).CombinedOutput(); err == nil {
		t.Fatalf("the transform succeeded:\n%s", out)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "previous" {
		t.Errorf("-o after a failure = %q, %v; want it untouched", got, err)
	}
	if tmp := tempFiles(t, dir); len(tmp) > 0 {
		t.Errorf("temporary files left behind: %v", tmp)
	}

	// A file that did not exist is not created.
	fresh := filepath.Join(dir, "fresh.xml")
	exec.Command(bin, "-xsl", xsl, "-o", fresh, in).Run()
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("a failed run created -o: %v", err)
	}
}

// -o may name the input: it is read while the result goes to a temporary
// file, which replaces it only at the end.
func TestOutputFileMayBeTheInput(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	const doc = `<a><b>x</b><b>y</b></a>`
	writeFile(t, in, doc)

	if out, err := exec.Command(bin, "-xsl", xsl, "-o", in, in).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(in); strings.TrimSpace(string(got)) != doc {
		t.Errorf("in-place output = %q, want %q", got, doc)
	}
}

func TestOutputFileThroughSymlinkAndMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and the umask are Unix")
	}
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<a/>`)

	// A symlink is written through, as os.Create wrote it, and the target
	// keeps its mode.
	target := filepath.Join(dir, "target.xml")
	writeFile(t, target, "old")
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.xml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-xsl", xsl, "-o", link, in).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if got, _ := os.ReadFile(target); strings.TrimSpace(string(got)) != "<a/>" {
		t.Errorf("the target holds %q", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o640 {
		t.Errorf("the target's mode is %v, want 0640", fi.Mode().Perm())
	}

	// A new file gets os.Create's mode, not os.CreateTemp's 0600.
	fresh := filepath.Join(dir, "fresh.xml")
	if out, err := exec.Command(bin, "-xsl", xsl, "-o", fresh, in).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	probe := filepath.Join(dir, "probe")
	f, err := os.Create(probe)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	want, _ := os.Stat(probe)
	if got, _ := os.Stat(fresh); got.Mode().Perm() != want.Mode().Perm() {
		t.Errorf("a new -o file is %v, want %v as os.Create gives", got.Mode().Perm(), want.Mode().Perm())
	}
}

// A device cannot be renamed over, so it is written in place.
func TestOutputFileDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/null is Unix")
	}
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<a/>`)
	if out, err := exec.Command(bin, "-xsl", xsl, "-o", os.DevNull, in).CombinedOutput(); err != nil {
		t.Errorf("-o %s: %v\n%s", os.DevNull, err, out)
	}
}

// -keep-going over stdout labels only the inputs that succeeded.
func TestMultipleLabelsOnlySuccesses(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "fail.xsl")
	writeFile(t, xsl, failSheet)
	good := filepath.Join(dir, "good.xml")
	writeFile(t, good, `<a/>`)
	bad := filepath.Join(dir, "bad.xml")
	writeFile(t, bad, `<a><stop/></a>`)

	out, _ := exec.Command(bin, "-xsl", xsl, "-keep-going", good, bad).Output()
	if !strings.Contains(string(out), "<!-- "+good+" -->") {
		t.Errorf("the good input is not labelled:\n%s", out)
	}
	if strings.Contains(string(out), "<!-- "+bad+" -->") {
		t.Errorf("the failed input is labelled:\n%s", out)
	}
}

func TestMaxNodesBoundsTheInput(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<a><b/><b/><b/><b/></a>`)

	for _, sheet := range []string{copySheet, treeSheet} {
		writeFile(t, xsl, sheet)
		out, err := exec.Command(bin, "-xsl", xsl, "-max-nodes", "3", in).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "exceeds 3 nodes") {
			t.Errorf("-max-nodes 3 over 6 nodes: %v\n%s", err, out)
		}
		if out, err := exec.Command(bin, "-xsl", xsl, "-max-nodes", "-1", in).CombinedOutput(); err != nil {
			t.Errorf("-max-nodes -1: %v\n%s", err, out)
		}
	}
}

// A result larger than the 2,000,000 result-tree nodes the transform may
// hold is written as it is made, so it is not refused.
func TestLargeResultIsWrittenAsItIsMade(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 30 MB file")
	}
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	const n = 2_100_000
	writeFile(t, in, "<a>"+strings.Repeat("<b>x</b>", n)+"</a>")
	dest := filepath.Join(dir, "out.xml")
	if out, err := exec.Command(bin, "-xsl", xsl, "-max-bytes", "-1", "-o", dest, in).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len("<a></a>") + n*len("<b>x</b>")); fi.Size() < want {
		t.Errorf("output is %d bytes, want at least %d", fi.Size(), want)
	}
}

// A writable file in a read-only directory cannot be renamed over, so it is
// written in place, as os.Create wrote it.
func TestOutputFileInReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix directory permissions that bind the user")
	}
	bin := buildCLI(t)
	dir := t.TempDir()
	xsl := filepath.Join(dir, "copy.xsl")
	writeFile(t, xsl, copySheet)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<a/>`)
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(ro, "out.xml")
	writeFile(t, dest, "old")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })

	if out, err := exec.Command(bin, "-xsl", xsl, "-o", dest, in).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(dest); strings.TrimSpace(string(got)) != "<a/>" {
		t.Errorf("-o in a read-only directory holds %q", got)
	}
}
