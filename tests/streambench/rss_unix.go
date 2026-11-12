//go:build darwin || linux || freebsd || netbsd || openbsd

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS is the finished child's maximum resident set size in bytes.
// Darwin reports ru_maxrss in bytes, the other systems in kilobytes.
func peakRSS(ps *os.ProcessState) int64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss)
	}
	return int64(ru.Maxrss) * 1024
}
