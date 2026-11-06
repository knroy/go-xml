package uripath

import (
	"os"
	"path/filepath"
)

// EvalExisting is filepath.EvalSymlinks for a path that may not exist yet:
// it resolves the deepest ancestor of abs that exists and appends the rest
// unchanged. abs must be absolute and clean (filepath.Abs output).
//
// A resolver compares a path against a root it has resolved, so both sides
// have to be spelled alike. filepath.EvalSymlinks fails outright on a missing
// file, and a missing file inside the root, left unresolved, then shares no
// prefix with the root wherever a symlink is above both -- on macOS /var is a
// link to /private/var -- so it was refused as outside the permitted
// directories rather than reported as not found.
//
// Appending the tail cannot widen containment: none of its components exist,
// so none is a link, and a clean path has no "..". A component that exists
// but does not resolve (a dangling link) stops the walk, and abs is returned
// as given, which is what the caller compared before.
func EvalExisting(abs string) string {
	tail := ""
	for q := abs; ; {
		if _, err := os.Lstat(q); err == nil {
			r, err := filepath.EvalSymlinks(q)
			if err != nil {
				return abs
			}
			return filepath.Join(r, tail)
		}
		parent := filepath.Dir(q)
		if parent == q {
			return abs
		}
		tail = filepath.Join(filepath.Base(q), tail)
		q = parent
	}
}
