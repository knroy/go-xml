// Command streamdiff is the three-way differential for streamed execution:
// it records TestXSLT30Suite on a base commit, on this checkout, and on this
// checkout with streaming switched off (GOXSLT_STREAM_OFF=1), compares the
// recordings with tests/recdiff, and summarises which cases ran on the
// streamed path.
//
//	go run ./tests/streamdiff [-floor n] [-allow file] <base-rev> <outdir>
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
// cases than the floor (-floor, else GOXSLT_STREAM_FLOOR, else 450) ran a
// container on the streamed path, when any guard fired, or when the off leg
// streamed anything. The summary comes from <outdir>/branch/streams/
// xslt30.tsv, which tests/xslts writes (key, runs per strategy, fallbacks,
// guard hits).
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

func main() {
	floor := 450
	if v := os.Getenv("GOXSLT_STREAM_FLOOR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			die(fmt.Errorf("GOXSLT_STREAM_FLOOR: %v", err))
		}
		floor = n
	}
	flag.IntVar(&floor, "floor", floor, "fewest cases that must run on the streamed path")
	allow := flag.String("allow", "", "recdiff allow-rule file (default tests/streamdiff/allow.txt)")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tests/streamdiff [-floor n] [-allow file] <base-rev> <outdir>")
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
	fmt.Printf("  fallbacks %d, guard hits %d; off leg: %d cases streamed, %d guard hits\n",
		br.total.Fallbacks, br.total.Guards, off.cases, off.total.Guards)
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
	cases, fellCases int
	total            streamtrace.Counts
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
		if len(fs) != 1+int(streamtrace.NumStrategies)+2 {
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
		copy(c.Runs[:], n)
		c.Fallbacks, c.Guards = n[len(n)-2], n[len(n)-1]
		if c.Streamed() > 0 {
			s.cases++
		}
		if c.Fallbacks > 0 {
			s.fellCases++
		}
		for i := range c.Runs {
			s.total.Runs[i] += c.Runs[i]
		}
		s.total.Fallbacks += c.Fallbacks
		s.total.Guards += c.Guards
	}
	if err := sc.Err(); err != nil {
		die(err)
	}
	return s
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
