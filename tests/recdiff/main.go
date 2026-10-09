// Command recdiff records and compares what two checkouts produce, case by
// case: the zero-difference differential for changes that must not move
// observable behaviour (docs/testing.md, "Output differential").
//
//	recdiff ingest  <dir> <suite> <srcdir>  # every file under srcdir, key = relative path
//	recdiff c14n    <dir> <file>...         # parse each file, record its Canonical XML (with comments)
//	recdiff compare [-allow rules] [-show n] <dirA> <dirB>
//
// A recording is the directory GOXSLT_RECORD_DIR names while the suites run
// (internal/record): <suite>.tsv manifests of key, sha256 and length, and the
// bytes under obj/. tests/record.sh makes a whole one.
//
// compare reports every case whose bytes differ. A difference is "explained"
// when it vanishes once the allow rules have deleted their matches from both
// sides; each rule line is
//
//	<suite glob> <key glob> <regexp to delete>
//
// so "xslt* * N\d+x\d+" says generate-id strings may differ anywhere in the
// XSLT suites, and "xsd11 set/group/case (?s).*" accepts one case outright. A
// case recorded on one side only is compared as the text "<missing>". The
// exit status is 1 when anything is left unexplained.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/knroy/go-xml/v2/c14n"
	"github.com/knroy/go-xml/v2/internal/record"
	"github.com/knroy/go-xml/v2/xdm"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch args := os.Args[2:]; os.Args[1] {
	case "ingest":
		if len(args) != 3 {
			usage()
		}
		err = ingest(args[0], args[1], args[2])
	case "c14n":
		if len(args) < 2 {
			usage()
		}
		err = canon(args[0], args[1:])
	case "compare":
		fs := flag.NewFlagSet("compare", flag.ExitOnError)
		allow := fs.String("allow", "", "allow-rule file")
		show := fs.Int("show", 8, "lines of diff shown per side")
		fs.Parse(args)
		if fs.NArg() != 2 {
			usage()
		}
		var bad int
		bad, err = compare(fs.Arg(0), fs.Arg(1), *allow, *show)
		if err == nil && bad > 0 {
			os.Exit(1)
		}
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "recdiff:", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: recdiff ingest <dir> <suite> <srcdir> | c14n <dir> <file>... | compare [-allow f] [-show n] <a> <b>")
	os.Exit(2)
}

func ingest(dir, suite, src string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		return record.Store(dir, suite, filepath.ToSlash(rel), data)
	})
}

func canon(dir string, files []string) error {
	for _, f := range files {
		var out strings.Builder
		in, err := os.Open(f)
		if err == nil {
			var tree *xdm.Tree
			tree, err = xdm.Parse(in, xdm.ParseOptions{MaxBytes: -1, MaxNodes: -1})
			in.Close()
			if err == nil {
				err = c14n.Write(&out, tree.Root, c14n.Options{Algorithm: c14n.Inclusive10WithComments})
			}
		}
		if err != nil {
			out.Reset()
			fmt.Fprintf(&out, "error: %v\n", err)
		}
		if err := record.Store(dir, "parse-c14n", filepath.Base(f), []byte(out.String())); err != nil {
			return err
		}
	}
	return nil
}

// recording maps suite -> key -> the hashes recorded under it, sorted, so a
// key recorded twice compares as a multiset whatever order it was written in.
type recording map[string]map[string][]string

func load(dir string) (recording, error) {
	ms, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return nil, err
	}
	rec := recording{}
	for _, m := range ms {
		f, err := os.Open(m)
		if err != nil {
			return nil, err
		}
		suite := strings.TrimSuffix(filepath.Base(m), ".tsv")
		keys := map[string][]string{}
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			fs := strings.Split(sc.Text(), "\t")
			if len(fs) != 3 {
				f.Close()
				return nil, fmt.Errorf("%s: bad line %q", m, sc.Text())
			}
			keys[fs[0]] = append(keys[fs[0]], fs[1])
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
		for _, hs := range keys {
			sort.Strings(hs)
		}
		rec[suite] = keys
	}
	return rec, nil
}

func text(dir string, hashes []string) string {
	if hashes == nil {
		return "<missing>"
	}
	var parts []string
	for _, h := range hashes {
		b, err := os.ReadFile(filepath.Join(dir, "obj", h[:2], h))
		if err != nil {
			parts = append(parts, "<unreadable: "+err.Error()+">")
			continue
		}
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n<<duplicate key>>\n")
}

type rule struct {
	line, suite, key string
	re               *regexp.Regexp
	hits             int
}

func loadRules(file string) ([]*rule, error) {
	if file == "" {
		return nil, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var rules []*rule
	for i, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln[0] == '#' {
			continue
		}
		fs := strings.Fields(ln)
		if len(fs) < 3 {
			return nil, fmt.Errorf("%s:%d: want <suite glob> <key glob> <regexp>", file, i+1)
		}
		// The regexp is the rest of the line, so it may contain spaces.
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(ln, fs[0])), fs[1]))
		re, err := regexp.Compile(rest)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", file, i+1, err)
		}
		rules = append(rules, &rule{line: ln, suite: fs[0], key: fs[1], re: re})
	}
	return rules, nil
}

func compare(dirA, dirB, allowFile string, show int) (int, error) {
	a, err := load(dirA)
	if err != nil {
		return 0, err
	}
	b, err := load(dirB)
	if err != nil {
		return 0, err
	}
	rules, err := loadRules(allowFile)
	if err != nil {
		return 0, err
	}
	var suites []string
	for s := range union(a, b) {
		suites = append(suites, s)
	}
	sort.Strings(suites)
	var totalBad int
	for _, s := range suites {
		var keys []string
		for k := range union(a[s], b[s]) {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var same, explained, bad int
		for _, k := range keys {
			ha, hb := a[s][k], b[s][k]
			if strings.Join(ha, ",") == strings.Join(hb, ",") {
				same++
				continue
			}
			ta, tb := text(dirA, ha), text(dirB, hb)
			var used []*rule
			for _, r := range rules {
				if ok, _ := path.Match(r.suite, s); !ok {
					continue
				}
				if ok, _ := path.Match(r.key, k); !ok {
					continue
				}
				na, nb := r.re.ReplaceAllString(ta, ""), r.re.ReplaceAllString(tb, "")
				if na != ta || nb != tb {
					used = append(used, r)
				}
				ta, tb = na, nb
			}
			if ta == tb {
				explained++
				for _, r := range used {
					r.hits++
				}
				continue
			}
			bad++
			fmt.Printf("DIFF %s %s\n%s", s, k, diff(ta, tb, show))
		}
		totalBad += bad
		fmt.Printf("%-16s %7d cases: %7d identical, %5d explained, %5d unexplained\n",
			s, len(keys), same, explained, bad)
	}
	for _, r := range rules {
		fmt.Printf("rule %5d cases  %s\n", r.hits, r.line)
	}
	if totalBad == 0 {
		fmt.Println("zero unexplained differences")
	} else {
		fmt.Printf("%d unexplained differences\n", totalBad)
	}
	return totalBad, nil
}

func union[V any](a, b map[string]V) map[string]bool {
	u := map[string]bool{}
	for k := range a {
		u[k] = true
	}
	for k := range b {
		u[k] = true
	}
	return u
}

// diff shows the lines between the common prefix and suffix, at most show per
// side, each cut to a window around the first byte that differs.
func diff(a, b string, show int) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	i := 0
	for i < len(la) && i < len(lb) && la[i] == lb[i] {
		i++
	}
	ja, jb := len(la), len(lb)
	for ja > i && jb > i && la[ja-1] == lb[jb-1] {
		ja--
		jb--
	}
	col := 0
	if i < ja && i < jb {
		for col < len(la[i]) && col < len(lb[i]) && la[i][col] == lb[i][col] {
			col++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  @ line %d\n", i+1)
	side := func(mark string, ls []string) {
		for n, l := range ls {
			if n == show {
				fmt.Fprintf(&sb, "  %s ... %d more lines\n", mark, len(ls)-n)
				break
			}
			fmt.Fprintf(&sb, "  %s %s\n", mark, window(l, col))
		}
	}
	side("-", la[i:ja])
	side("+", lb[i:jb])
	return sb.String()
}

func window(l string, col int) string {
	const w = 100
	lo := max(0, col-w/2)
	if lo > len(l) {
		lo = len(l)
	}
	hi := min(len(l), lo+w)
	s := l[lo:hi]
	if lo > 0 {
		s = "…" + s
	}
	if hi < len(l) {
		s += "…"
	}
	return s
}
