//go:build !unix

package main

import "io/fs"

// newFileMode is the mode os.Create gives a new file. Off Unix there is no
// umask, and Windows keeps only the read-only bit, which 0666 leaves clear.
func newFileMode() fs.FileMode { return 0o666 }
