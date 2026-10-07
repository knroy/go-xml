package relaxng

import "testing"

// TestJoinRefAgainstADrivePath pins that a Windows drive path used as the base
// is joined as a path. url.Parse reads "C:" as a scheme, so the reference used
// to resolve to "c:///common.rnc" and FileResolver refused it as remote; that
// failed TestFileResolverReadsCompactIncludes on Windows only. The cases are
// plain strings, so this runs the same on every OS.
func TestJoinRefAgainstADrivePath(t *testing.T) {
	for _, c := range []struct{ base, ref, want string }{
		{`C:\s\main.rnc`, "common.rnc", `C:\s\common.rnc`},
		{"C:/s/main.rnc", "common.rnc", "C:/s/common.rnc"},
		{`C:\s\main.rnc`, "sub/x.rnc", `C:\s\sub/x.rnc`},
		{`C:\s\main.rnc`, "file:///d/x.rnc", "file:///d/x.rnc"},
	} {
		if got := joinRef(c.base, c.ref); got != c.want {
			t.Errorf("joinRef(%q, %q) = %q, want %q", c.base, c.ref, got, c.want)
		}
	}
}
