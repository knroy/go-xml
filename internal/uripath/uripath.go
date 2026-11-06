// Package uripath holds the decisions every local-file resolver has to make
// before the filesystem sees a reference: whether the reference is a Windows
// drive-letter path rather than something naming a scheme, and whether the
// path it becomes names a network share (IsForeignNetworkPath).
//
// It is a leaf package because xslt, relaxng and dtd each own a resolver that
// needs the same answer and none of the three may import another. Copying the
// rule into three files would mean three places to get a security decision
// wrong, and the copies would drift.
package uripath

import "strings"

// IsDriveLetterPath reports whether s is a Windows absolute path naming a
// drive — "C:\dir\f.rng", "C:/dir/f.rng", "C:" — rather than a URI reference
// carrying a one-letter scheme.
//
// # Why this exists
//
// url.Parse("C:/dir/f.rng") returns Scheme "c". A resolver that refuses every
// scheme but "file" therefore refuses every absolute path on Windows, before
// the code that knows how to turn a drive path into a filesystem path ever
// runs. The guard sits upstream of the knowledge.
//
// # The rule, and why it is the only safe one
//
// A drive letter and a genuine one-letter scheme are textually identical up to
// the colon, and they are identical AFTER url.Parse as well:
//
//	url.Parse("C:/Users/x")      -> Scheme "c", Host "",  Path "/Users/x"
//	url.Parse("c:///secret.rng") -> Scheme "c", Host "",  Path "/secret.rng"
//
// Nothing in the parse result separates them. So the decision has to be made
// on the raw string, and the discriminator is the two slashes:
//
//	a reference is a drive-letter path iff its first byte is an ASCII
//	letter, its second byte is ':', and what follows the colon does not
//	begin with "//".
//
// RFC 3986 section 3 is what makes that sound: "//" immediately after the
// scheme's colon is the delimiter that introduces an authority component. A
// filesystem drive path has no authority and can never produce one — "C://x"
// is not a path any Windows API accepts. So every string that could be a
// hierarchical URI with an authority is excluded, and what remains is the
// drive-path shape. Concretely:
//
//	"C:\Users\x"      -> admitted (remainder "\Users\x")
//	"C:/Users/x"      -> admitted (remainder "/Users/x")
//	"C:"              -> admitted (remainder "")
//	"C:rel\x"         -> admitted (drive-relative; a real Windows spelling)
//	"c:///secret.rng" -> REFUSED  (remainder "///..." begins "//")
//	"c://host/x"      -> REFUSED  (remainder "//host/x" begins "//")
//	"http://e/x"      -> REFUSED  (two-letter-plus scheme)
//	"/etc/passwd"     -> REFUSED  (no colon at index 1; not a drive path)
//
// The "c:///" form is the escape payload the confinement tests plant. It must
// keep being refused, because admitting it would let a reference reach the
// filesystem as "/secret.rng" — a rooted path outside any grant.
//
// # Why this is not gated on runtime.GOOS
//
// Two reasons. Admitting "c:/x" on a Unix host gives up nothing: it is not an
// absolute path there, so it is joined against the base directory and then
// meets the same root-confinement check as any relative reference. The scheme
// guard is not the confinement boundary — filepath.Rel against the root is,
// and that is unchanged on every platform.
//
// And a security branch that only executes on one OS is a security branch that
// the CI host running most of the suite never tests. Keeping the rule
// unconditional keeps it exercised everywhere, which matters more than
// narrowing a surface that the confinement check already covers. It also keeps
// this package consistent with the rest of the tree, which carries almost no
// runtime.GOOS branching outside tests.
func IsDriveLetterPath(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
		return false
	}
	// An authority after the colon means this is a hierarchical URI, not a
	// path. This is the single check that keeps "c:///secret.rng" refused.
	rest := s[2:]
	return len(rest) < 2 || rest[0] != '/' || rest[1] != '/'
}

// IsForeignNetworkPath reports whether p, a filesystem path a resolver is
// about to hand to the filesystem, names a Windows network or device path --
// `\\host\share\x`, `//host/share/x`, `\\?\UNC\host\share\x`, `\\.\pipe\x`,
// `\??\UNC\host\share\x` -- on a volume that none of roots is on.
//
// On Windows any filesystem call on a UNC path, a stat or a readlink as much
// as an open, connects to the named host over SMB and offers it the process's
// NTLM credentials. A resolver that resolves symlinks before its containment
// check therefore reaches an attacker-named host before refusing the path, so
// this check has to come first. It is decided on the string, with either
// separator, on every OS, for the reason IsDriveLetterPath gives: a branch
// that only Windows runs is one most of CI never tests. On Unix a leading "//"
// is an ordinary rooted path, so refusing it there gives up nothing a caller
// would rely on.
//
// A root may itself be a share. A path on the same `\\host\share` volume as a
// root names a host the caller already granted, so it is admitted here and
// left to the containment check. Device paths (`\\?\`, `\\.\`) and NT
// object-namespace paths (`\??\`) name no host that a root could grant and are
// always refused. Windows passes a `\??\` path to the object manager as
// written, so `\??\UNC\host\share` reaches the host exactly as a UNC path does,
// and `\??\C:\x` names a local file by a spelling no root is written in.
func IsForeignNetworkPath(p string, roots ...string) bool {
	v, ok := networkVolume(p)
	if !ok {
		return false
	}
	if v == "" {
		return true
	}
	for _, root := range roots {
		if rv, ok := networkVolume(root); ok && strings.EqualFold(rv, v) {
			return false
		}
	}
	return true
}

// networkVolume reports whether p begins with two separators, which Windows
// reads as a UNC or device path, or with `\??\`, the NT object namespace, and
// returns its `host\share` volume. The
// volume is "" when no root could grant it: a device path, or a missing host
// or share.
func networkVolume(p string) (string, bool) {
	// `\??\` begins with one separator, not two, so it is matched on its
	// own: an NT object-namespace path, which is a device path to a root.
	if len(p) >= 4 && isSep(p[0]) && p[1] == '?' && p[2] == '?' && isSep(p[3]) {
		return "", true
	}
	if len(p) < 2 || strings.Trim(p[:2], `/\`) != "" {
		return "", false
	}
	part := func(s string) (string, string) {
		if i := strings.IndexAny(s, `/\`); i >= 0 {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	host, rest := part(p[2:])
	share, _ := part(rest)
	if host == "" || host == "?" || host == "." || share == "" {
		return "", true
	}
	return host + `\` + share, true
}

func isSep(c byte) bool { return c == '/' || c == '\\' }
