// Command benchrun runs the go-xml benchmark workloads against the reference
// engines and writes bench/results.json. Run it from the repository root,
// after bench/fetch-engines.sh; see bench/README.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// bench holds what every run shares.
type bench struct {
	root, cache, tmp, loopDir string
	goxmlBin, self            string
	engines                   map[string]*Engine
	unavailable               map[string]string // engine id -> why
	loopErr                   error             // why Loop.java could not be built
	warmup, runs, warmIters   int
}

func main() {
	if len(os.Args) == 5 && os.Args[1] == "-helper" {
		if err := runHelper(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, "benchrun helper:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchrun:", err)
		os.Exit(1)
	}
}

func run() error {
	var workloads, engineFilter listFlag
	flag.Var(&workloads, "workload", "workload id to run (repeatable; default or \"all\": every workload)")
	flag.Var(&engineFilter, "engine", "engine id to time (repeatable; default: every engine the workload lists)")
	mode := flag.String("mode", "both", "cold, warm or both")
	runs := flag.Int("runs", 10, "cold mode: timed processes per item")
	warmup := flag.Int("warmup", 2, "cold mode: untimed processes before timing")
	warmIters := flag.Int("warm-iters", 30, "warm mode: timed passes per item, after an untimed warm-up of a fifth as many")
	out := flag.String("out", filepath.Join("bench", "results.json"), "results file")
	checkOnly := flag.Bool("check-only", false, "run the correctness check only, no timing")
	flag.Parse()
	if *mode != "cold" && *mode != "warm" && *mode != "both" {
		return fmt.Errorf("-mode %q: want cold, warm or both", *mode)
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "bench", "engines.json")); err != nil {
		return fmt.Errorf("run from the repository root: %w", err)
	}
	b := &bench{
		root: root, cache: filepath.Join(root, "bench", ".cache"),
		warmup: *warmup, runs: *runs, warmIters: *warmIters,
		unavailable: map[string]string{},
	}
	b.tmp = filepath.Join(b.cache, "tmp")
	b.loopDir = filepath.Join(b.cache, "loop")
	if b.engines, err = loadEngines(filepath.Join(root, "bench", "engines.json")); err != nil {
		return err
	}
	wls, err := loadWorkloads(filepath.Join(root, "bench", "workloads"), root, workloads)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(b.tmp, 0o755); err != nil {
		return err
	}
	if b.self, err = os.Executable(); err != nil {
		return err
	}
	b.goxmlBin = filepath.Join(b.cache, "go-xml")
	if runtime.GOOS == "windows" {
		b.goxmlBin += ".exe"
	}
	if msg, err := exec.Command("go", "build", "-o", b.goxmlBin, "./cmd/go-xml").CombinedOutput(); err != nil {
		return fmt.Errorf("building go-xml: %v\n%s", err, msg)
	}
	versions := b.checkEngines()
	if *mode != "cold" && !*checkOnly {
		b.loopErr = b.buildLoop()
	}

	rep := report{
		RunDate: time.Now().UTC().Format(time.RFC3339),
		Machine: machineInfo(),
		Go:      runtime.Version(),
		Java:    javaVersion(),
		Engines: versions,
	}
	timed := func(id string) bool { return len(engineFilter) == 0 || slices.Contains(engineFilter, id) }
	for _, w := range wls {
		fmt.Fprintf(os.Stderr, "workload %s: %d item(s)\n", w.ID, len(w.Items))
		rows, err := b.runWorkload(w, timed, *mode, *checkOnly)
		if err != nil {
			return fmt.Errorf("workload %s: %w", w.ID, err)
		}
		rep.Results = append(rep.Results, rows...)
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d rows)\n", *out, len(rep.Results))
	return nil
}

// checkEngines records which engines can run and their versions.
func (b *bench) checkEngines() map[string]string {
	versions := map[string]string{}
	_, javaErr := exec.LookPath("java")
	for id, e := range b.engines {
		versions[id] = e.Version
		for _, f := range e.Classpath {
			if _, err := os.Stat(filepath.Join(b.cache, filepath.FromSlash(f))); err != nil {
				b.unavailable[id] = "not fetched: run bench/fetch-engines.sh"
			}
		}
		if len(e.Classpath) > 0 && javaErr != nil {
			b.unavailable[id] = "java not found"
		}
		if len(e.VersionArgv) > 0 {
			out, err := exec.Command(e.VersionArgv[0], e.VersionArgv[1:]...).CombinedOutput()
			if err != nil {
				b.unavailable[id] = fmt.Sprintf("%s: %v", e.VersionArgv[0], err)
				continue
			}
			versions[id] = firstLine(string(out))
		}
	}
	return versions
}

// buildLoop compiles Loop.java against every fetched JVM engine's jars.
func (b *bench) buildLoop() error {
	var cp []string
	for id, e := range b.engines {
		if e.Loop && len(e.Classpath) > 0 && b.unavailable[id] == "" {
			cp = append(cp, b.classpath(e))
		}
	}
	if len(cp) == 0 {
		return fmt.Errorf("no JVM engine fetched")
	}
	cmd := exec.Command("javac", "-d", b.loopDir, "-cp", strings.Join(cp, string(os.PathListSeparator)),
		filepath.Join(b.root, "bench", "java", "Loop.java"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("javac Loop.java: %v: %s", err, firstLine(string(msg)))
	}
	return nil
}

func (b *bench) classpath(e *Engine) string {
	var p []string
	for _, f := range e.Classpath {
		p = append(p, filepath.Join(b.cache, filepath.FromSlash(f)))
	}
	return strings.Join(p, string(os.PathListSeparator))
}

func (b *bench) abs(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(b.root, filepath.FromSlash(p))
}

func (b *bench) tmpPath(name string) string { return filepath.Join(b.tmp, name) }

// report is results.json.
type report struct {
	RunDate string            `json:"run_date"`
	Machine map[string]any    `json:"machine"`
	Go      string            `json:"go"`
	Java    string            `json:"java"`
	Engines map[string]string `json:"engines"`
	Results []row             `json:"results"`
}

type row struct {
	Workload     string  `json:"workload"`
	Item         string  `json:"item"`
	Engine       string  `json:"engine"`
	Mode         string  `json:"mode"`
	Agree        *bool   `json:"agree"`
	Runs         int     `json:"runs,omitempty"`
	MedianNs     int64   `json:"median_ns,omitempty"`
	P25Ns        int64   `json:"p25_ns,omitempty"`
	P75Ns        int64   `json:"p75_ns,omitempty"`
	MinNs        int64   `json:"min_ns,omitempty"`
	ItemsPerSec  float64 `json:"items_per_s,omitempty"`
	PeakRSSBytes int64   `json:"peak_rss_bytes,omitempty"`
	CompileNs    int64   `json:"compile_ns,omitempty"`
	Note         string  `json:"note,omitempty"`
}

// check is one engine's agreement with go-xml on one item.
type check struct {
	agree  bool
	note   string
	failed bool // the item is an expected error for this engine
	inv    invocation
}

func (b *bench) runWorkload(w *Workload, timed func(string) bool, mode string, checkOnly bool) ([]row, error) {
	var rows []row
	checks := make([]map[string]check, len(w.Items))
	emit := func(it Item, id, m string, agree bool, note string) *row {
		a := agree
		rows = append(rows, row{Workload: w.ID, Item: it.Name, Engine: id, Mode: m, Agree: &a, Note: note})
		return &rows[len(rows)-1]
	}
	skip := func(it Item, id, m, note string) {
		rows = append(rows, row{Workload: w.ID, Item: it.Name, Engine: id, Mode: m, Note: note})
	}

	for i, it := range w.Items {
		cs, err := b.checkItem(w, it)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.Name, err)
		}
		checks[i] = cs
		for _, id := range w.Engines {
			c, ok := cs[id]
			if !ok || !timed(id) {
				continue
			}
			if reason := b.unavailable[id]; reason != "" {
				skip(it, id, "check", "skipped: "+reason)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s %s: agree=%v %s\n", it.Name, id, c.agree, c.note)
			if checkOnly {
				emit(it, id, "check", c.agree, c.note)
				continue
			}
			if !c.agree || mode == "warm" || !slices.Contains(w.Modes, "cold") {
				if !c.agree {
					emit(it, id, "cold", false, c.note)
				}
				continue
			}
			st, rss, n, err := b.cold(c.inv, it.Area, c.failed)
			if err != nil {
				emit(it, id, "cold", true, "timing failed: "+err.Error())
				continue
			}
			r := emit(it, id, "cold", true, c.note)
			r.Runs, r.MedianNs, r.P25Ns, r.P75Ns, r.MinNs, r.PeakRSSBytes = n, st.Median, st.P25, st.P75, st.Min, rss
			if e := b.engines[id]; len(e.Tasks[it.Area]) > 0 && e.Tasks[it.Area][0] == "{self}" {
				r.Note = strings.TrimSpace(r.Note + " cold process is benchrun -helper")
			}
		}
	}
	if checkOnly || mode == "cold" || !slices.Contains(w.Modes, "warm") {
		return rows, nil
	}

	// Warm: per engine, group the items it agreed on by what is compiled once.
	for _, id := range w.Engines {
		if !timed(id) || b.unavailable[id] != "" {
			continue
		}
		e := b.engines[id]
		type key struct{ area, input string }
		groups := map[key][]int{}
		var order []key
		for i, it := range w.Items {
			c, ok := checks[i][id]
			if !ok {
				continue
			}
			if !c.agree {
				emit(it, id, "warm", false, c.note)
				continue
			}
			if !e.Loop {
				emit(it, id, "warm", true, "cold only: no in-process harness for this engine")
				continue
			}
			k := key{it.Area, it.compileInput()}
			if groups[k] == nil {
				order = append(order, k)
			}
			groups[k] = append(groups[k], i)
		}
		for _, k := range order {
			idx := groups[k]
			items := make([]Item, len(idx))
			for j, i := range idx {
				items[j] = w.Items[i]
			}
			wu := max(b.warmIters/5, 1)
			var res warmResult
			var err error
			if len(e.Classpath) > 0 {
				if b.loopErr != nil {
					err = b.loopErr
				} else {
					res, err = b.warmJVM(id, e, k.area, k.input, items, wu, b.warmIters)
				}
			} else {
				res, err = warmGo(id, k.area, b.abs(k.input), items, w.Args[id], w.XSDVersion, b.root, wu, b.warmIters)
			}
			for j, it := range items {
				if err != nil {
					emit(it, id, "warm", true, "warm run failed: "+err.Error())
					continue
				}
				st := summarise(res.ns[j])
				r := emit(it, id, "warm", true, checks[idx[j]][id].note)
				r.Runs, r.MedianNs, r.P25Ns, r.P75Ns, r.MinNs = len(res.ns[j]), st.Median, st.P25, st.P75, st.Min
				r.CompileNs = res.compileNs
				if st.Median > 0 {
					r.ItemsPerSec = 1e9 / float64(st.Median)
				}
				var notes []string
				if r.Note != "" {
					notes = append(notes, r.Note)
				}
				if id == "basex" {
					notes = append(notes, "BaseX recompiles the query per evaluation; each item's time includes it")
				}
				if res.errs[j] != "" && !checks[idx[j]][id].failed {
					notes = append(notes, "warm run raised: "+firstLine(res.errs[j]))
				}
				r.Note = strings.Join(notes, "; ")
			}
		}
	}
	return rows, nil
}

// checkItem runs every engine once on the item and compares each reference
// with go-xml. go-xml agrees when any reference does, or when none ran.
func (b *bench) checkItem(w *Workload, it Item) (map[string]check, error) {
	gxInv, err := b.invocation("go-xml", b.engines["go-xml"], w, it)
	if err != nil {
		return nil, err
	}
	_, _, gx, err := execOnce(gxInv, it.Area, true)
	if err != nil {
		return nil, err
	}
	gxOut := gx // the next exec reuses tmp names per engine, not per item
	cs := map[string]check{}
	anyRef, anyAgree := false, false
	for _, id := range w.Engines {
		e := b.engines[id]
		if e == nil {
			return nil, fmt.Errorf("unknown engine %q", id)
		}
		if id == "go-xml" || e.Tasks[it.Area] == nil {
			continue
		}
		if b.unavailable[id] != "" {
			cs[id] = check{}
			continue
		}
		inv, err := b.invocation(id, e, w, it)
		if err != nil {
			return nil, err
		}
		_, _, o, err := execOnce(inv, it.Area, true)
		if err != nil {
			return nil, err
		}
		ok, note := agree(it.Area, w.Normalise, gxOut, o)
		anyRef = true
		anyAgree = anyAgree || ok
		cs[id] = check{agree: ok, note: note, failed: o.Failed, inv: inv}
	}
	gc := check{agree: anyAgree || !anyRef, failed: gxOut.Failed, inv: gxInv}
	switch {
	case !anyRef:
		gc.note = "no reference engine ran; output unchecked"
	case !anyAgree:
		gc.note = "no reference engine agreed"
	case gxOut.Failed:
		gc.note = "both raised an error"
	}
	cs["go-xml"] = gc
	return cs, nil
}
