// Command streamdiff is the three-way differential for streamed execution:
// it records TestXSLT30Suite on a base commit, on this checkout, and on this
// checkout with streaming switched off (GOXSLT_STREAM_OFF=1), compares the
// recordings with tests/recdiff, and summarises which cases ran on the
// streamed path.
//
//	go run ./tests/streamdiff [-floor n] [-allow file] [-output] <base-rev> <outdir>
//
// Run it from the checkout under test. The base tree is extracted with
// "git archive <base-rev> | tar -x" into <outdir>/base-src, with a testdata
// symlink to this checkout's; all three legs read the suite through the same
// resolved path, so their base URIs and error paths agree. Each leg records
// into <outdir>/<leg> (internal/record) and logs to <outdir>/<leg>.log; the
// base leg, when its <outdir>/base/DONE names the same revision, is not run
// again. The branch and off legs always are: the checkout may have changed. The legs run one after another: the suite has
// cases near its per-case deadline, and three at once on one box can time
// them out.
//
// It fails (exit 1) when base vs branch or branch vs off differ anywhere the
// allow file (default tests/streamdiff/allow.txt) does not explain, when fewer
// cases than the floor (-floor, else GOXSLT_STREAM_FLOOR, else defaultFloor)
// ran a container on the streamed path, when any guard fired, or when the off
// leg streamed anything. The summary comes from <outdir>/branch/streams/
// xslt30.tsv, which tests/xslts writes (key, runs per strategy, fallbacks,
// guard hits, re-runs, the core's sNodes per kind).
//
// With -output it also runs a fourth leg, "output", on this checkout with
// GOXSLT_OUTPUT_CHECK (tests/xslts/outputcheck.go): every case is run again
// with its principal result streamed to TransformOptions.Output, and the
// bytes and error compared with Result.Serialize's. It then fails when any
// case differs, and reports the counts over all cases and over those the
// branch leg streamed.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// defaultFloor is the streamed case count the last stage reached: 450 with
// the burst plans alone (M1.1-M1.4), 564 with the streamed core beside them
// (M1.5), 576 with the core alone (M1.6, the burst plans retired into it),
// 606 with streamable modes (M2), 629 with streamed principal input through
// TransformReader (M2b), 707 with streamed grouping and full xsl:iterate
// (M3), 877 with streamed xsl:try, function calls and attribute sets (M6),
// 922 with accumulators, fork, map, merge and streamed output (M4, M5, M7).
// A stage may only raise it.
const defaultFloor = 922

func main() {
	floor := defaultFloor
	if v := os.Getenv("GOXSLT_STREAM_FLOOR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			die(fmt.Errorf("GOXSLT_STREAM_FLOOR: %v", err))
		}
		floor = n
	}
	flag.IntVar(&floor, "floor", floor, "fewest cases that must run on the streamed path")
	allow := flag.String("allow", "", "recdiff allow-rule file (default tests/streamdiff/allow.txt)")
	outLeg := flag.Bool("output", false, "also compare TransformOptions.Output's bytes with Result.Serialize's")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tests/streamdiff [-floor n] [-allow file] [-output] <base-rev> <outdir>")
		os.Exit(2)
	}
	rev := flag.Arg(0)
	root := strings.TrimSpace(output("git", "rev-parse", "--show-toplevel"))
	td, err := filepath.EvalSymlinks(filepath.Join(root, "testdata"))
	if err != nil {
		die(err)
	}
	out, err := filepath.Abs(flag.Arg(1))
	if err != nil {
		die(err)
	}
	allowFile := filepath.Join(root, "tests", "streamdiff", "allow.txt")
	if *allow != "" {
		if allowFile, err = filepath.Abs(*allow); err != nil {
			die(err)
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		die(err)
	}

	baseSrc := filepath.Join(out, "base-src")
	leg(out, "base", rev, nil, func() {
		os.RemoveAll(baseSrc)
		if err := os.MkdirAll(baseSrc, 0o755); err != nil {
			die(err)
		}
		run(root, "sh", "-c", `git archive "$1" | tar -x -C "$2"`, "sh", rev, baseSrc)
		if err := os.Symlink(td, filepath.Join(baseSrc, "testdata")); err != nil {
			die(err)
		}
	}, baseSrc, td)
	leg(out, "branch", rev, nil, nil, root, td)
	leg(out, "off", rev, []string{"GOXSLT_STREAM_OFF=1"}, nil, root, td)
	if *outLeg {
		leg(out, "output", rev, []string{"GOXSLT_OUTPUT_CHECK=" + filepath.Join(out, "output.tsv")}, nil, root, td)
	}

	rd := filepath.Join(out, "recdiff")
	run(root, "go", "build", "-o", rd, "./tests/recdiff")
	bad := false
	for _, p := range [][2]string{{"base", "branch"}, {"branch", "off"}} {
		fmt.Printf("== %s vs %s\n", p[0], p[1])
		cmd := exec.Command(rd, "compare", "-allow", allowFile,
			filepath.Join(out, p[0]), filepath.Join(out, p[1]))
		f, err := os.Create(filepath.Join(out, p[0]+"-vs-"+p[1]+".txt"))
		if err != nil {
			die(err)
		}
		cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, f), os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("FAIL %s vs %s: %v\n", p[0], p[1], err)
			bad = true
		}
		f.Close()
	}

	br := summary(filepath.Join(out, "branch"))
	off := summary(filepath.Join(out, "off"))
	fmt.Printf("== streamed path (branch)\ncases %d (floor %d), with a fallback %d\n", br.cases, floor, br.fellCases)
	for i, n := range br.total.Runs {
		fmt.Printf("  %-9s %6d runs\n", streamtrace.Strategy(i), n)
	}
	fmt.Printf("  core sNodes by kind:")
	for i, n := range br.total.Kinds {
		fmt.Printf(" %s %d", streamtrace.Kind(i), n)
	}
	fmt.Printf("\n  fallbacks %d, re-runs %d (%d cases), guard hits %d; off leg: %d cases streamed, %d guard hits\n",
		br.total.Fallbacks, br.total.Reruns, br.rerunCases, br.total.Guards, off.cases, off.total.Guards)
	if br.cases < floor {
		fmt.Printf("FAIL %d cases on the streamed path, below the floor %d\n", br.cases, floor)
		bad = true
	}
	if br.total.Guards+off.total.Guards > 0 {
		fmt.Println("FAIL guard hits must be 0")
		bad = true
	}
	if off.total.Streamed() > 0 {
		fmt.Println("FAIL GOXSLT_STREAM_OFF=1 still streamed")
		bad = true
	}
	if *outLeg && !outputSummary(filepath.Join(out, "output.tsv"), filepath.Join(out, "branch")) {
		bad = true
	}
	if bad {
		os.Exit(1)
	}
	fmt.Println("streamdiff: ok")
}

// leg records TestXSLT30Suite from the tree at src into <out>/<name>, after
// prepare (when set); a leg with prepare (the base) is skipped when a
// finished recording for rev is already there.
func leg(out, name, rev string, env []string, prepare func(), src, td string) {
	dir := filepath.Join(out, name)
	done := filepath.Join(dir, "DONE")
	// Only the base leg is reused: the branch's code may have changed since.
	if b, err := os.ReadFile(done); err == nil && prepare != nil && strings.HasPrefix(string(b), rev+" ") {
		fmt.Printf("== %s: reusing %s\n", name, dir)
		return
	}
	if prepare != nil {
		prepare()
	}
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		die(err)
	}
	logf, err := os.Create(filepath.Join(out, name+".log"))
	if err != nil {
		die(err)
	}
	defer logf.Close()
	fmt.Printf("== %s: recording %s (log %s.log)\n", name, src, filepath.Join(out, name))
	cmd := exec.Command("go", "test", "./tests/xslts/", "-count=1", "-run", "^TestXSLT30Suite$", "-v", "-timeout", "180m")
	cmd.Dir, cmd.Stdout, cmd.Stderr = src, logf, logf
	cmd.Env = append(cleanEnv(), "GOXSLT_RECORD_DIR="+dir, "GOXSLT_XSLTS="+filepath.Join(td, "xslt30-test"))
	cmd.Env = append(cmd.Env, env...)
	status := "0"
	if err := cmd.Run(); err != nil {
		// A failing suite still recorded every case it ran; recdiff shows
		// any it did not as missing.
		status = err.Error()
		fmt.Printf("   %s: go test: %v (see the log)\n", name, err)
	}
	if err := os.WriteFile(done, []byte(rev+" "+status+"\n"), 0o644); err != nil {
		die(err)
	}
}

// cleanEnv is the environment without the variables a leg sets itself.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GOXSLT_RECORD_DIR", "GOXSLT_XSLTS", "GOXSLT_STREAM_OFF", "GOXSLT_NO_SUITES":
			continue
		}
		env = append(env, kv)
	}
	return env
}

type streams struct {
	cases, fellCases, rerunCases int
	total                        streamtrace.Counts
}

// summary totals <dir>/streams/xslt30.tsv; a leg with no file streamed nothing.
func summary(dir string) (s streams) {
	f, err := os.Open(filepath.Join(dir, "streams", "xslt30.tsv"))
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		die(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Split(sc.Text(), "\t")
		if len(fs) != 1+int(streamtrace.NumStrategies)+3+int(streamtrace.NumKinds) {
			die(fmt.Errorf("%s: bad line %q", f.Name(), sc.Text()))
		}
		var n []int64
		for _, x := range fs[1:] {
			v, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				die(fmt.Errorf("%s: %v", f.Name(), err))
			}
			n = append(n, v)
		}
		var c streamtrace.Counts
		ns := int(streamtrace.NumStrategies)
		copy(c.Runs[:], n)
		c.Fallbacks, c.Guards, c.Reruns = n[ns], n[ns+1], n[ns+2]
		copy(c.Kinds[:], n[ns+3:])
		if c.Streamed() > 0 {
			s.cases++
		}
		if c.Fallbacks > 0 {
			s.fellCases++
		}
		if c.Reruns > 0 {
			s.rerunCases++
		}
		for i := range c.Runs {
			s.total.Runs[i] += c.Runs[i]
		}
		for i := range c.Kinds {
			s.total.Kinds[i] += c.Kinds[i]
		}
		s.total.Fallbacks += c.Fallbacks
		s.total.Guards += c.Guards
		s.total.Reruns += c.Reruns
	}
	if err := sc.Err(); err != nil {
		die(err)
	}
	return s
}

// outputSummary prints the output leg's counts, over every case and over
// those the branch leg streamed, and reports whether none differed.
func outputSummary(file, branch string) bool {
	streamed := map[string]bool{}
	if f, err := os.Open(filepath.Join(branch, "streams", "xslt30.tsv")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, _, _ := strings.Cut(sc.Text(), "\t")
			streamed[k] = true
		}
		f.Close()
	}
	f, err := os.Open(file)
	if err != nil {
		fmt.Printf("FAIL output leg: %v\n", err)
		return false
	}
	defer f.Close()
	type tally struct{ cases, same, diff, errdiff, unstable, opened int }
	var all, str tally
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Split(sc.Text(), "\t")
		if len(fs) != 3 {
			die(fmt.Errorf("%s: bad line %q", file, sc.Text()))
		}
		for _, t := range []*tally{&all, &str} {
			if t == &str && !streamed[fs[0]] {
				continue
			}
			t.cases++
			switch fs[1] {
			case "same":
				t.same++
			case "diff":
				t.diff++
			case "unstable":
				t.unstable++
			default:
				t.errdiff++
			}
			if fs[2] != "0" {
				t.opened++
			}
		}
	}
	fmt.Printf("== output (TransformOptions.Output against Result.Serialize)\n")
	for _, t := range []struct {
		name string
		t    tally
	}{{"all cases", all}, {"streamed cases", str}} {
		fmt.Printf("  %-14s %5d: same %d, bytes differ %d, error differs %d, unstable %d; written before the end %d\n",
			t.name, t.t.cases, t.t.same, t.t.diff, t.t.errdiff, t.t.unstable, t.t.opened)
	}
	if all.diff+all.errdiff > 0 {
		fmt.Printf("FAIL output: %d cases differ (%s.diff)\n", all.diff+all.errdiff, file)
		return false
	}
	return true
}

func run(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		die(fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err))
	}
}

func output(name string, args ...string) string {
	b, err := exec.Command(name, args...).Output()
	if err != nil {
		die(fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err))
	}
	return string(b)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "streamdiff:", err)
	os.Exit(2)
}
