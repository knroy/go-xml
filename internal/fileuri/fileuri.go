// Package fileuri spells a filesystem path as a file: URI.
//
// It lives in its own leaf package because the tests of four packages need
// the same spelling and none of them may import another's unexported helper:
// xdm's external-entity tests stand in for xslt.FileResolver, which xdm
// cannot import, and the xslt tests build stylesheet base URIs of their own.
// Each had written "file://" + path by hand, and every one of them was wrong
// on Windows in the same two ways — see Of.
//
// The production resolvers keep their own unexported copies (xslt.fileURIOf,
// dtd.fileURIOf, the CLI's fileURI), which are already correct; this package
// exists so that a caller which does not have one of those in scope stops
// reaching for string concatenation.
package fileuri

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Of renders a filesystem path as an absolute file: URI.
//
// A value that is already a file: URI is returned unchanged, so a caller that
// passes one does not get file:///file:/...
func Of(path string) string {
	if path == "" || strings.HasPrefix(path, "file:") {
		return path
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		path = abs
	}
	return FromSlashedAbs(ToSlash(path))
}

// ToSlash is filepath.ToSlash with the backslash case done unconditionally
// rather than only where the OS separator is "\".
//
// filepath.ToSlash is a no-op on darwin and Linux, so a call site that omits
// it there behaves identically whether it is present or absent: nothing any
// test on those platforms runs can tell the two apart, and that is exactly
// how the unconverted separator reached Windows CI. Converting always makes
// the conversion observable on every platform, and costs nothing on the ones
// where a backslash is a legal filename character: a path that reaches here
// is about to become a URI path, and url.URL would otherwise escape the
// backslash to "%5C" — leaving "C:%5Cdir%5Cs.xsl" as one path segment naming
// a file that does not exist, rather than a file inside a directory.
func ToSlash(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
}

// ToPath turns a file: URI back into a filesystem path, and leaves anything
// that is not one alone. It is the inverse of Of.
//
// It exists for the same reason Of does: stripping the "file://" prefix
// textually leaves "/C:/dir/s.xsl" from the three-slash form, which is not a
// path any filesystem call accepts, and it leaves a percent-escape unescaped,
// so a directory with a space in its name becomes one with "%20" in it.
func ToPath(s string) string {
	if !strings.HasPrefix(s, "file:") {
		// A bare path on Windows may still be written with backslashes, and
		// url.Parse would read one as an escape-free path character. Leaving
		// it to filepath is correct on both platforms.
		return filepath.FromSlash(s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return filepath.FromSlash(strings.TrimPrefix(s, "file://"))
	}
	p := u.Path
	// A Windows path arrives as "/C:/dir/s.xsl" — an empty authority followed
	// by a drive-letter path. The leading slash belongs to the URI, not to the
	// path, and only there: "/home/u" keeps its own.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// Dir is Of for a directory, which differs only in the trailing slash that
// makes a relative reference resolve inside it rather than beside it.
func Dir(path string) string {
	u := Of(path)
	if u != "" && !strings.HasSuffix(u, "/") {
		u += "/"
	}
	return u
}

// FromSlashedAbs spells an already-absolute, already-slash-separated path as a
// file: URI. It is separate from Of so that both platforms' spellings can be
// tested on either platform: filepath.Abs and filepath.ToSlash are precisely
// what make Of answer differently on Windows and Unix, which is why a bug in
// this spelling survives every darwin and Linux test run and fails only in
// Windows CI.
//
// Two things about the Windows spelling are easy to get wrong, and the hand
// -written "file://" + path got both.
//
// The leading slash: an absolute Windows path is C:\dir\s.xsl and has none of
// its own, so "file://" + "C:/dir/s.xsl" makes C: the *authority* rather than
// the drive. Parsed back, that yields host "C:" and a path with the drive
// letter gone, so every URI resolved against it names a file that is not
// there. RFC 8089 gives a local path an empty authority, which is the
// three-slash form, and Saxon agrees: file:///C:/dir/s.xsl.
//
// The separator: a file: URI path is written with forward slashes, so a raw
// C:\dir\s.xsl leaves backslashes in the URI. url.Parse rejects that outright
// — a backslash after the host reads as a port — so the base URI is not
// merely wrong, it does not parse.
func FromSlashedAbs(slashed string) string {
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	// url.URL does the escaping, so a directory containing a space or any
	// other character that is not URI-safe survives the round trip.
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// OnHost spells a filesystem path as a file: URI carrying the given authority.
//
// It exists for the tests that assert a foreign host is REFUSED. Of cannot
// build one: a local file's authority is empty by RFC 8089, which is the whole
// of what Of spells. Those tests were writing "file://evil.example.com" + path
// by hand, and on Windows the path has no leading slash of its own, so the
// drive fused onto the host and the URI named the host "evil.example.comC:".
// The refusal still happened, but for a host the test never wrote -- so the
// assertion on the host name failed, and, worse, a test whose subject is the
// authority check was no longer exercising the authority the vector uses.
//
// The same fusion has a nastier form when the host is empty. "file://" + a
// Windows path yields "file://C:/dir/s.xsl", whose authority is the DRIVE, and
// `file://C:\dir\s.xsl` does not parse at all -- url.Parse reads the backslash
// run after the host as a port. A resolver that rejects foreign hosts and
// non-file schemes by inspecting the parsed URL sees neither in that case,
// because there is nothing to inspect; any refusal that follows comes from a
// later check and proves something other than what the test claims.
func OnHost(host, path string) string {
	slashed := ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Host: host, Path: slashed}
	return u.String()
}
