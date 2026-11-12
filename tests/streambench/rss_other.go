//go:build !(darwin || linux || freebsd || netbsd || openbsd)

package main

import "os"

// peakRSS is not reported here (Windows' rusage carries no memory figure);
// the table leaves the column empty and peak heap stands in.
func peakRSS(*os.ProcessState) int64 { return 0 }
