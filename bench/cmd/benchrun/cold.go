package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// invocation is one engine's argv for one item, and where its output lands.
type invocation struct {
	argv    []string
	out     string // output file: the {out} argument, or stdout's destination
	stdout  bool   // true when stdout is the output
	invalid string // the engine's invalid_pattern
}

func (b *bench) invocation(engineID string, e *Engine, w *Workload, it Item) (invocation, error) {
	tmpl := e.Tasks[it.Area]
	if tmpl == nil {
		return invocation{}, fmt.Errorf("%s has no %s task", engineID, it.Area)
	}
	out := b.tmpPath(engineID + ".out")
	vars := map[string]string{
		"go-xml":      b.goxmlBin,
		"self":        b.self,
		"cache":       b.cache,
		"classpath":   b.classpath(e),
		"stylesheet":  b.abs(it.Stylesheet),
		"query":       b.abs(it.Query),
		"schema":      b.abs(it.Schema),
		"source":      b.abs(it.Source),
		"out":         out,
		"xsd_version": w.XSDVersion,
	}
	argv := expandArgv(tmpl, vars, it.Params, e.ParamFormat, w.Args[engineID])
	return invocation{
		argv:    argv,
		out:     out,
		stdout:  !slices.ContainsFunc(tmpl, func(a string) bool { return strings.Contains(a, "{out}") }),
		invalid: e.InvalidPattern,
	}, nil
}

// execOnce runs the invocation, returning wall time, peak RSS and the
// outcome. keepLog captures stdout/stderr for the correctness check; timed
// runs send them to the null device instead, so the pipe is not measured.
func execOnce(inv invocation, area string, keepLog bool) (time.Duration, int64, Outcome, error) {
	os.Remove(inv.out)
	cmd := exec.Command(inv.argv[0], inv.argv[1:]...)
	var log bytes.Buffer
	var logW io.Writer = io.Discard
	if keepLog {
		logW = &log
	}
	cmd.Stderr = logW
	cmd.Stdout = logW
	if inv.stdout {
		f, err := os.Create(inv.out)
		if err != nil {
			return 0, 0, Outcome{}, err
		}
		defer f.Close()
		cmd.Stdout = f
	}
	start := time.Now()
	err := cmd.Run()
	wall := time.Since(start)
	if cmd.ProcessState == nil {
		return 0, 0, Outcome{}, fmt.Errorf("%s: %w", inv.argv[0], err)
	}
	o := Outcome{Failed: err != nil, Log: log.String()}
	output, _ := os.ReadFile(inv.out)
	if area == "xsd" || area == "rng" {
		// A validator's report may be on stdout, which then landed in the
		// output file; the verdict reads both.
		o.Log += string(output)
		o.Verdict = verdict(err == nil, o.Log, inv.invalid)
		o.Failed = o.Verdict == "error"
	} else if !o.Failed {
		o.Output = output
	}
	return wall, peakRSS(cmd.ProcessState), o, nil
}

// cold times W warm-up and N measured processes. mayFail is set when the
// correctness check found the item to be an error in both engines.
func (b *bench) cold(inv invocation, area string, mayFail bool) (Stats, int64, int, error) {
	var ns []int64
	var rss int64
	for i := 0; i < b.warmup+b.runs; i++ {
		wall, r, o, err := execOnce(inv, area, false)
		if err != nil {
			return Stats{}, 0, 0, err
		}
		if o.Failed && area != "xsd" && area != "rng" && !mayFail {
			return Stats{}, 0, 0, fmt.Errorf("run %d failed", i)
		}
		if i >= b.warmup {
			ns = append(ns, wall.Nanoseconds())
			rss = max(rss, r)
		}
	}
	return summarise(ns), rss, len(ns), nil
}
