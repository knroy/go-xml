//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS returns the finished process's peak resident set in bytes.
func peakRSS(ps *os.ProcessState) int64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	return maxrssBytes(runtime.GOOS, int64(ru.Maxrss))
}
