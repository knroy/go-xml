// Command streambench measures the memory and time of one XSLT transform run
// on a tree (xdm.Parse + Stylesheet.Transform) against the same transform
// streamed (Stylesheet.TransformReader), over generated documents of growing
// size. docs/benchmark.md ("Streaming against the tree") reports its figures.
//
//	go run ./tests/streambench [-records 60000,600000,3000000] [-reps 3] [-dir d]
//
// Each run is a child process of its own, so its peak RSS is its alone; the
// child also samples its live heap every millisecond. Peak RSS comes from the
// child's rusage (rss_unix.go); where the platform reports none (Windows) the
// column is empty and peak heap is the figure to read. A record is about 185
// bytes, so the default sizes are 11, 112 and 564 MB. The documents are
// written to -dir (a temporary directory when unset) and removed after,
// unless -keep. Both modes write the principal result to
// TransformOptions.Output, so neither holds a result tree; the command fails
// when the two modes' outputs differ.
//
// The parse limits are lifted (MaxBytes and MaxNodes -1) so the tree can hold
// the largest document; the tree's peak is several times the input, so the
// default sizes want about 4 GB free.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xslt"
)

const head = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`

// workloads are streamable in both modes, so each pair runs one stylesheet.
var workloads = []struct{ name, sheet string }{
	// every node copied
	{"identity", head + `<xsl:mode streamable="yes" on-no-match="shallow-copy"/></xsl:stylesheet>`},
	// one small element per record
	{"filter", head + `<xsl:mode streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="/data"><out><xsl:apply-templates/></out></xsl:template>
<xsl:template match="record"><r id="{@id}"><xsl:value-of select="amount"/></r></xsl:template>
</xsl:stylesheet>`},
	// one number for the whole document
	{"sum", head + `<xsl:mode streamable="yes"/>
<xsl:template match="/"><total><xsl:value-of select="sum(data/record/amount)"/></total></xsl:template>
</xsl:stylesheet>`},
}

var modes = []string{"tree", "stream"}

func main() {
	child := flag.String("child", "", "internal: mode,workload,file of one run")
	records := flag.String("records", "60000,600000,3000000", "comma-separated record counts")
	reps := flag.Int("reps", 3, "runs per cell; the table shows medians")
	dir := flag.String("dir", "", "where the documents are written (default: a temporary directory)")
	keep := flag.Bool("keep", false, "keep the generated documents")
	flag.Parse()
	if *child != "" {
		check(runChild(*child))
		return
	}
	check(bench(*records, *reps, *dir, *keep))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "streambench:", err)
		os.Exit(1)
	}
}

// run is one child's report.
type run struct {
	ms, heapMB, rssMB float64
	out               string // output bytes and digest
}

func bench(records string, reps int, dir string, keep bool) error {
	if dir == "" {
		d, err := os.MkdirTemp("", "streambench")
		if err != nil {
			return err
		}
		dir = d
		if !keep {
			defer os.RemoveAll(d)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Println("| Input | Workload | Tree RSS | Stream RSS | Tree heap | Stream heap | Tree time | Stream time |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|")
	for _, rs := range strings.Split(records, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(rs))
		if err != nil {
			return fmt.Errorf("-records: %v", err)
		}
		doc := filepath.Join(dir, fmt.Sprintf("records-%d.xml", n))
		size, err := generate(n, doc)
		if err != nil {
			return err
		}
		for _, w := range workloads {
			cells := map[string]run{}
			for _, m := range modes {
				var all []run
				for range reps {
					r, err := runOnce(self, m, w.name, doc)
					if err != nil {
						return err
					}
					all = append(all, r)
				}
				cells[m] = median(all)
			}
			if t, s := cells["tree"].out, cells["stream"].out; t != s {
				return fmt.Errorf("%s at %d records: tree wrote %s, stream %s", w.name, n, t, s)
			}
			t, s := cells["tree"], cells["stream"]
			fmt.Printf("| %.0f MB | %s | %s | %s | %.1f MB | %.1f MB | %.2f s | %.2f s |\n",
				float64(size)/1e6, w.name, mb(t.rssMB), mb(s.rssMB), t.heapMB, s.heapMB, t.ms/1000, s.ms/1000)
		}
		if !keep {
			os.Remove(doc)
		}
	}
	return nil
}

func mb(v float64) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%.0f MB", v)
}

// median takes each column's median on its own; out is the same in every run.
func median(rs []run) run {
	col := func(f func(run) float64) float64 {
		v := make([]float64, len(rs))
		for i, r := range rs {
			v[i] = f(r)
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	return run{
		ms:     col(func(r run) float64 { return r.ms }),
		heapMB: col(func(r run) float64 { return r.heapMB }),
		rssMB:  col(func(r run) float64 { return r.rssMB }),
		out:    rs[0].out,
	}
}

func runOnce(self, mode, work, doc string) (run, error) {
	cmd := exec.Command(self, "-child", mode+","+work+","+doc)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return run{}, fmt.Errorf("%s %s: %v", mode, work, err)
	}
	f := strings.Fields(string(b))
	if len(f) != 3 {
		return run{}, fmt.Errorf("%s %s: unexpected report %q", mode, work, b)
	}
	var r run
	r.ms, _ = strconv.ParseFloat(f[0], 64)
	r.heapMB, _ = strconv.ParseFloat(f[1], 64)
	r.out = f[2]
	r.rssMB = float64(peakRSS(cmd.ProcessState)) / (1 << 20)
	return r, nil
}

// generate writes n records and returns the document's size.
func generate(n int, path string) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	note := strings.Repeat("lorem ipsum ", 8)
	w.WriteString("<data>\n")
	for i := range n {
		fmt.Fprintf(w, "<record id=\"r%d\"><name>Item %d</name><amount>%d.%02d</amount><note>%s</note></record>\n",
			i, i, i%1000, i%100, note)
	}
	w.WriteString("</data>\n")
	if err := w.Flush(); err != nil {
		f.Close()
		return 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return 0, err
	}
	return st.Size(), f.Close()
}

// digest counts and hashes what the transform writes.
type digest struct {
	n int64
	h hash.Hash
}

func (d *digest) Write(p []byte) (int, error) {
	d.n += int64(len(p))
	return d.h.Write(p)
}

// runChild runs one transform and prints its time in ms, its peak live heap
// in MB and its output as bytes:digest.
func runChild(spec string) error {
	parts := strings.SplitN(spec, ",", 3)
	if len(parts) != 3 {
		return fmt.Errorf("-child %q", spec)
	}
	mode, work, path := parts[0], parts[1], parts[2]
	i := slices.IndexFunc(workloads, func(w struct{ name, sheet string }) bool { return w.name == work })
	if i < 0 {
		return fmt.Errorf("workload %q", work)
	}
	tree, err := xdm.ParseString(workloads[i].sheet, xdm.ParseOptions{})
	if err != nil {
		return err
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	unlimited := xdm.ParseOptions{MaxBytes: -1, MaxNodes: -1}
	out := &digest{h: sha256.New()}
	opts := xslt.TransformOptions{Output: out, SourceParseOptions: unlimited}

	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { samplePeak(stop, &peak); close(done) }()
	start := time.Now()
	var r io.Reader = bufio.NewReaderSize(f, 1<<16)
	switch mode {
	case "tree":
		doc, err := xdm.Parse(r, unlimited)
		if err != nil {
			return err
		}
		if _, err := sheet.Transform(context.Background(), doc.Root, opts); err != nil {
			return err
		}
	case "stream":
		if _, err := sheet.TransformReader(context.Background(), r, opts); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mode %q", mode)
	}
	el := time.Since(start)
	close(stop)
	<-done
	fmt.Printf("%d %.1f %d:%s\n", el.Milliseconds(), float64(peak.Load())/(1<<20), out.n, hex.EncodeToString(out.h.Sum(nil))[:16])
	return nil
}

// samplePeak records the largest live heap seen, once a millisecond.
func samplePeak(stop <-chan struct{}, peak *atomic.Uint64) {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		metrics.Read(s)
		if v := s[0].Value.Uint64(); v > peak.Load() {
			peak.Store(v)
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}
