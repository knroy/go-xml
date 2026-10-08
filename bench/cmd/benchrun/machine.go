package main

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// machineInfo describes the host for results.json. cpu and mem are best
// effort: read from sysctl on macOS and /proc on Linux, empty elsewhere.
func machineInfo() map[string]any {
	m := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cores": runtime.NumCPU()}
	switch runtime.GOOS {
	case "darwin":
		m["cpu"] = sysctl("machdep.cpu.brand_string")
		if n, err := strconv.ParseInt(sysctl("hw.memsize"), 10, 64); err == nil {
			m["mem"] = n
		}
	case "linux":
		if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			if s := regexp.MustCompile(`(?m)^model name\s*:\s*(.+)$`).FindSubmatch(b); s != nil {
				m["cpu"] = string(s[1])
			}
		}
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			if s := regexp.MustCompile(`(?m)^MemTotal:\s*(\d+) kB`).FindSubmatch(b); s != nil {
				n, _ := strconv.ParseInt(string(s[1]), 10, 64)
				m["mem"] = n * 1024
			}
		}
	}
	return m
}

func sysctl(name string) string {
	out, _ := exec.Command("sysctl", "-n", name).Output()
	return strings.TrimSpace(string(out))
}

// javaVersion is the first line of "java -version", or empty without java.
func javaVersion() string {
	out, err := exec.Command("java", "-version").CombinedOutput()
	if err != nil {
		return ""
	}
	return firstLine(string(out))
}
