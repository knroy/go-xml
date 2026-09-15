// Package uripath holds the one decision every local-file resolver has to
// make before it parses a reference as a URI: whether the reference is a
// Windows drive-letter path rather than something naming a scheme.
//
// It is a leaf package because xslt, relaxng and dtd each own a resolver that
// needs the same answer and none of the three may import another. Copying the
// rule into three files would mean three places to get a security decision
// wrong, and the copies would drift.
package uripath

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
