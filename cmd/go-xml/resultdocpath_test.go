package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xslt"
)

// TestWriteSecondaryRefusesPlatformAbsoluteHref pins that an href which is
// absolute on ANY platform is refused on EVERY platform.
//
// filepath.IsAbs answers for the host, so a Windows spelling is not absolute
// on Unix: "C:/win.xml" passed the check and filepath.Join made a directory
// literally named "C:" under the result directory, while the same stylesheet
// on Windows was refused. A stylesheet that writes somewhere on one operating
// system and errors on another is the portability bug, whichever half is
// judged correct — and the file it silently creates is not the one anybody
// asked for.
//
// The refusal is deliberately textual rather than a filesystem question: it
// must give the same answer on a host that has no C: drive as on one that
// does.
func TestWriteSecondaryRefusesPlatformAbsoluteHref(t *testing.T) {
	dir := t.TempDir()

	for _, href := range []string{
		`C:/win.xml`,         // drive letter, forward slashes
		`c:\win.xml`,         // drive letter, backslashes
		`\\server\share.xml`, // UNC
		`/unix.xml`,          // POSIX absolute
	} {
		t.Run(href, func(t *testing.T) {
			err := writeSecondary(
				[]xslt.SecondaryResult{{Href: href}}, dir)
			if err == nil {
				t.Fatalf("href %q was accepted, want a refusal", href)
			}
			if !strings.Contains(err.Error(), "absolute") {
				t.Errorf("error = %v, want it to say the href is absolute", err)
			}
			// Nothing may have been created on the way to the error.
			ents, rerr := os.ReadDir(dir)
			if rerr != nil {
				t.Fatalf("read result dir: %v", rerr)
			}
			if len(ents) != 0 {
				var names []string
				for _, e := range ents {
					names = append(names, e.Name())
				}
				t.Errorf("refused href %q still created %v", href, names)
			}
		})
	}

	// The negative half: an ordinary relative href still works, and a
	// subdirectory is still allowed. A fix that refused everything would
	// pass the arms above and be useless.
	t.Run("a relative href is still written", func(t *testing.T) {
		if err := writeSecondary(
			[]xslt.SecondaryResult{{Href: "sub/out.xml"}}, dir); err != nil {
			t.Fatalf("relative href refused: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub", "out.xml")); err != nil {
			t.Errorf("relative href was not written: %v", err)
		}
	})
}
