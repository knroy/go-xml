//go:build unix

package main

import (
	"io/fs"
	"sync"
	"syscall"
)

// newFileMode is the mode os.Create gives a new file: 0666 less the umask.
// The -o file is written to a temporary file, which os.CreateTemp makes 0600,
// so the mode is set from this once the file is complete.
//
// The umask can only be read by setting it, so it is read once, and set
// straight back.
var newFileMode = sync.OnceValue(func() fs.FileMode {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	return 0o666 &^ fs.FileMode(mask)
})
