package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestSM11EmptyAllowDirSegment: an empty -allow-dir segment must not become a
// root, since the resolver turns "" into the working directory.
func TestSM11EmptyAllowDirSegment(t *testing.T) {
	sheet := filepath.Join("s", "sheet.xsl")
	got := readableRoots(sheet, ",/a,, ,/b,")
	want := []string{filepath.Dir(sheet), "/a", "/b"}
	if !slices.Equal(got, want) {
		t.Fatalf("readableRoots = %q, want %q", got, want)
	}
}
