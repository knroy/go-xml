package relaxng

import (
	"net/url"
	"testing"

	"github.com/knroy/go-xml/internal/fileuri"
)

// A file: URI names the drive INSIDE the path: RFC 8089's three-slash form
// makes url.Parse report "/C:/dir/s.rng", whose leading slash belongs to the
// URI and not to the filesystem. ResolveSchema used u.Path directly, so on
// Windows every grammar inside -root was refused as outside it -- filepath.Rel
// cannot relate a volume-less "/C:/..." to a "C:\..." root.
//
// This runs on every platform because it is pure string work: url.Parse and
// fileuri.ToPath consult no filesystem. The Unix rows are the control that the
// strip applies ONLY to a drive-letter path -- "/srv/x.rng" must keep its own
// leading slash, and removing it would break the platform this test runs on.
//
// What this test CANNOT do is prove ResolveSchema calls ToPath: reverting that
// one line to u.Path leaves this green. No darwin test can close that gap,
// because url.Parse already decodes percent-escapes into u.Path, so on a Unix
// path the two are byte-identical -- the ONLY difference is the drive-letter
// leading slash, which cannot occur here. An end-to-end test was attempted on
// the theory that an escaped space would tell them apart; it would not, and it
// was removed rather than left standing as false assurance. The call site is
// verified by inspection and by Windows CI.
func TestFileURIPathDropsTheURILeadingSlashOnly(t *testing.T) {
	for _, c := range []struct {
		uri      string
		wantPath string // what u.Path gives, the pre-fix value
		wantFS   string // what the filesystem needs, slash-spelled
	}{
		{"file:///C:/Users/r/AppData/Local/Temp/T/001/top.rng",
			"/C:/Users/r/AppData/Local/Temp/T/001/top.rng",
			"C:/Users/r/AppData/Local/Temp/T/001/top.rng"},
		{"file:///D:/srv/s.rng", "/D:/srv/s.rng", "D:/srv/s.rng"},
		{"file:///srv/schemas/s.rng", "/srv/schemas/s.rng", "/srv/schemas/s.rng"},
		{"file:///tmp/a%20b/s.rng", "/tmp/a b/s.rng", "/tmp/a b/s.rng"},
	} {
		u, err := url.Parse(c.uri)
		if err != nil {
			t.Fatalf("%s: %v", c.uri, err)
		}
		if u.Path != c.wantPath {
			t.Errorf("%s: u.Path = %q, want %q", c.uri, u.Path, c.wantPath)
		}
		// ToPath returns the native spelling; compare on the slash spelling so
		// the assertion means the same thing on both platforms.
		if got := toSlashForTest(fileuri.ToPath(c.uri)); got != c.wantFS {
			t.Errorf("%s: ToPath = %q, want %q -- the URI's leading slash must "+
				"come off a drive path and stay on a rooted Unix one",
				c.uri, got, c.wantFS)
		}
	}
}

func toSlashForTest(p string) string {
	out := make([]rune, 0, len(p))
	for _, r := range p {
		if r == '\\' {
			r = '/'
		}
		out = append(out, r)
	}
	return string(out)
}
