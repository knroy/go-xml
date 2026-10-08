//go:build !unix

package main

import "os"

// peakRSS is not measured off Unix: Windows rusage has no peak RSS field.
func peakRSS(*os.ProcessState) int64 { return 0 }
