package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xslt"
)

// writeSecondary is the only path in this program that opens a file by name
// after checking the name, and it is a WRITE. The prefix test decides which
// names are permitted; it cannot decide what the filesystem does with them,
// so a symlink at the destination was followed straight out of the result
// directory and the bytes landed on whatever it pointed at. Every read
// resolver in the library moved to os.OpenRoot for this reason; this one had
// not.
//
// The href can also come from the SOURCE DOCUMENT through an attribute value
// template, so the name being joined is not necessarily the stylesheet
// author's -- the untrusted document steers the write.
func TestWriteSecondaryRefusesSymlinkOutOfRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	victim := filepath.Join(outside, "victim.txt")
	const keep = "SECRET-ORIGINAL"
	if err := os.WriteFile(victim, []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "second.xml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := writeSecondary([]xslt.SecondaryResult{{Href: "second.xml"}}, root)
	if err == nil {
		t.Fatal("a symlink out of the result directory was followed; the " +
			"href named a file inside the root and the write landed outside it")
	}
	// os.Root's own wording. Nothing else can produce it, so it is the
	// evidence that the refusal came from the open and not from the prefix
	// test -- which passes for this href, and would keep passing if the
	// hardening were reverted.
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("error = %v; want the refusal to come from os.Root at open "+
			"time, not from the string check", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != keep {
		t.Errorf("the file outside the root was overwritten with %q", got)
	}
}

// The same attack one level up: a symlinked DIRECTORY inside the root, so
// that creating the parent directories walks out rather than the final
// create. os.Root has no MkdirAll for exactly this reason and mkdirAllIn
// creates each component through the root.
func TestWriteSecondaryRefusesSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := writeSecondary(
		[]xslt.SecondaryResult{{Href: "link/deep/sub/pwn.xml"}}, root)
	if err == nil {
		t.Fatal("a symlinked directory was followed out of the root")
	}
	ents, rerr := os.ReadDir(outside)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(ents) != 0 {
		t.Errorf("the refused write still created %d entries outside the root",
			len(ents))
	}
}

// Vectors that need no symlink, so they RUN on an unprivileged Windows runner
// where the two tests above skip. A confinement property covered only by
// symlink tests is unverified on the platform whose path handling differs
// most, and a skipped check must never read as a passing one.
func TestWriteSecondaryRefusesUnprivilegedVectors(t *testing.T) {
	root := t.TempDir()

	for _, href := range []string{
		"../escaped.xml",
		"a/../../escaped.xml",
		"sub/./../../escaped.xml",
		"../../../../../../etc/passwd",
	} {
		t.Run(href, func(t *testing.T) {
			if err := writeSecondary(
				[]xslt.SecondaryResult{{Href: href}}, root); err == nil {
				t.Fatalf("href %q was accepted, want a refusal", href)
			}
		})
	}
}

// The other direction. A fix that refused everything would pass every test
// above and break the feature.
func TestWriteSecondaryStillWritesInsideTheRoot(t *testing.T) {
	root := t.TempDir()

	for _, href := range []string{
		"plain.xml",
		"sub/nested.xml",
		"deep/er/still.xml",
		"sub/../plain2.xml",
	} {
		t.Run(href, func(t *testing.T) {
			if err := writeSecondary(
				[]xslt.SecondaryResult{{Href: href}}, root); err != nil {
				t.Fatalf("href %q is inside the root and must be written: %v",
					href, err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(href))); err != nil {
				t.Errorf("href %q reported success but no file is there: %v",
					href, err)
			}
		})
	}
}
