package main

// maxrssBytes normalises getrusage's ru_maxrss: Darwin reports bytes, Linux
// and the BSDs report kilobytes.
func maxrssBytes(goos string, maxrss int64) int64 {
	if goos == "darwin" || goos == "ios" {
		return maxrss
	}
	return maxrss * 1024
}
