package main

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllowDirHelpMatchesRoots pins the -allow-dir help text against what the
// resolver is actually given.
//
// The two disagreed. readableRoots has always added the stylesheet's own
// directory unconditionally, while the help said of -allow-dir that "empty
// disables all of them" — so a caller reading -h concluded that a run with no
// flags could read nothing, and a service that saved user uploads beside the
// stylesheet exposed them to doc(). Confinement itself was never broken; the
// text simply denied a root that was there.
//
// The behaviour is the defensible half: a stylesheet that cannot read the
// modules beside it is useless. So the text was corrected, and this test keeps
// the correction honest from both directions — if the grant is ever made
// conditional, or the sentence describing it is dropped, one half fails.
func TestAllowDirHelpMatchesRoots(t *testing.T) {
	const sheet = "/srv/sheets/report.xsl"
	sheetDir := filepath.Dir(sheet)

	// The behaviour: the stylesheet's directory is a root with no flags at
	// all, and -allow-dir only ever adds to it.
	bare := readableRoots(sheet, "")
	if len(bare) != 1 || bare[0] != sheetDir {
		t.Fatalf("readableRoots(%q, \"\") = %v, want exactly [%q]",
			sheet, bare, sheetDir)
	}

	withFlag := readableRoots(sheet, "/srv/codelists,/srv/xml")
	want := []string{sheetDir, "/srv/codelists", "/srv/xml"}
	if len(withFlag) != len(want) {
		t.Fatalf("readableRoots with -allow-dir = %v, want %v", withFlag, want)
	}
	for i := range want {
		if withFlag[i] != want[i] {
			t.Fatalf("readableRoots with -allow-dir = %v, want %v",
				withFlag, want)
		}
	}

	// The text: -h must say that the stylesheet's own directory is readable
	// regardless of the flag, and must not claim an empty flag reads nothing.
	help := allowDirUsage(t)

	if strings.Contains(help, "empty disables all of them") {
		t.Error("-allow-dir help still claims \"empty disables all of them\", " +
			"but readableRoots grants the stylesheet's own directory with no " +
			"flag set")
	}
	for _, phrase := range []string{
		"stylesheet's own directory",
		"always",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("-allow-dir help does not mention %q; it must state that "+
				"the stylesheet's directory is readable without the flag.\n"+
				"help text: %s", phrase, help)
		}
	}
}

// allowDirUsage re-registers the flags on a private FlagSet and returns the
// -allow-dir usage string. run() registers on the default CommandLine set,
// which the test binary shares, so a fresh set keeps this from colliding with
// the testing package's own flags.
func allowDirUsage(t *testing.T) string {
	t.Helper()

	fs := flag.NewFlagSet("go-xml", flag.ContinueOnError)
	registerAllowDir(fs)

	f := fs.Lookup("allow-dir")
	if f == nil {
		t.Fatal("no -allow-dir flag registered")
	}
	return f.Usage
}
