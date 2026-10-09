package xdm

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Yardsticks for the tree layout: navigation over a parsed document, and the
// cost and retained heap of parsing the benchmark corpora. They read the
// gitignored corpora under testdata/ and skip without them.
//
//	go test ./xdm -run '^$' -bench 'Nav|Corpus' -benchmem

const benchData = "../testdata/"

var navOnce struct {
	sync.Once
	tree   *Tree
	nodes  []*Node // every node, attributes included, in document order
	elems  []*Node
	el, al QName // the most frequent element and attribute names
	err    error
}

// navTree parses the 10 MB benchmark document once for every Nav benchmark.
func navTree(b *testing.B) {
	navOnce.Do(func() {
		data, err := os.ReadFile(benchData + "bench/parse/parse-10mb.xml")
		if err != nil {
			navOnce.err = err
			return
		}
		t, err := Parse(bytes.NewReader(data), ParseOptions{MaxBytes: -1})
		if err != nil {
			navOnce.err = err
			return
		}
		navOnce.tree = t
		ec, ac := map[QName]int{}, map[QName]int{}
		var walk func(n *Node)
		walk = func(n *Node) {
			navOnce.nodes = append(navOnce.nodes, n)
			navOnce.nodes = append(navOnce.nodes, attrsOf(n)...)
			if n.Kind() == KindElement {
				navOnce.elems = append(navOnce.elems, n)
				ec[QName{URI: n.Name().URI, Local: n.Name().Local}]++
				for _, a := range attrsOf(n) {
					ac[QName{URI: a.Name().URI, Local: a.Name().Local}]++
				}
			}
			for _, c := range kids(n) {
				walk(c)
			}
		}
		walk(t.Root)
		navOnce.el, navOnce.al = mostFrequent(ec), mostFrequent(ac)
	})
	if navOnce.err != nil {
		b.Skip(navOnce.err)
	}
	b.ReportAllocs()
	b.ResetTimer()
}

func mostFrequent(m map[QName]int) QName {
	var best QName
	n := -1
	for k, v := range m {
		if v > n || v == n && k.Local < best.Local {
			best, n = k, v
		}
	}
	return best
}

// navSink keeps the compiler from discarding a benchmark's result.
var navSink int

func BenchmarkNavDescendantWalk(b *testing.B) {
	navTree(b)
	for i := 0; i < b.N; i++ {
		n := 0
		var f func(x *Node)
		f = func(x *Node) {
			for _, c := range kids(x) {
				n += int(c.Kind())
				f(c)
			}
		}
		f(navOnce.tree.Root)
		navSink += n
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(navOnce.nodes)), "ns/node")
}

func BenchmarkNavChildNameTest(b *testing.B) {
	navTree(b)
	el := navOnce.el
	for i := 0; i < b.N; i++ {
		n := 0
		for _, e := range navOnce.elems {
			for _, c := range kids(e) {
				if c.Kind() == KindElement && c.Name().Local == el.Local && c.Name().URI == el.URI {
					n++
				}
			}
		}
		navSink += n
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(navOnce.elems)), "ns/elem")
}

func BenchmarkNavAttrLookup(b *testing.B) {
	navTree(b)
	al := navOnce.al
	for i := 0; i < b.N; i++ {
		n := 0
		for _, e := range navOnce.elems {
			if a := e.Attr(al.URI, al.Local); a != nil {
				n += len(a.Value())
			}
		}
		navSink += n
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(navOnce.elems)), "ns/elem")
}

// One op is one comparison of a random pair of nodes of the same document.
func BenchmarkNavDocOrderCompare(b *testing.B) {
	navTree(b)
	nodes := navOnce.nodes
	rng := rand.New(rand.NewSource(1))
	pairs := make([][2]*Node, 1<<16)
	for i := range pairs {
		pairs[i] = [2]*Node{nodes[rng.Intn(len(nodes))], nodes[rng.Intn(len(nodes))]}
	}
	b.ResetTimer()
	n := 0
	for i := 0; i < b.N; i++ {
		p := pairs[i&(len(pairs)-1)]
		n += p[0].Compare(p[1])
	}
	navSink += n
}

func BenchmarkNavStringValue(b *testing.B) {
	navTree(b)
	for i := 0; i < b.N; i++ {
		n := 0
		for _, e := range navOnce.elems {
			n += len(e.StringValue())
		}
		navSink += n
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(navOnce.elems)), "ns/elem")
}

// benchCorpora names the parse corpora: a glob each, relative to testdata/.
var benchCorpora = []struct{ name, glob string }{
	{"parse-10mb", "bench/parse/parse-10mb.xml"},
	{"xmark-0.1", "bench/xmark/auction-0.1.xml"},
	{"invoices", "bench/*/invoices/*.xml"},
	{"docbook", "xsltng/src/test/resources/xml/*.xml"},
	{"stylesheets", "xsltng/src/main/xslt/*/*.xsl"},
}

// loadCorpus reads the documents of a corpus that parse with default options.
func loadCorpus(b *testing.B, glob string) (docs [][]byte, size int) {
	paths, _ := filepath.Glob(benchData + glob)
	sort.Strings(paths)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if _, err := Parse(bytes.NewReader(data), ParseOptions{MaxBytes: -1}); err != nil {
			continue
		}
		docs = append(docs, data)
		size += len(data)
	}
	if len(docs) == 0 {
		b.Skip("no documents match testdata/" + glob)
	}
	return docs, size
}

// BenchmarkCorpusParse parses each corpus whole per op. retained-B/B is the
// heap still live after a GC with one parse of the corpus kept, per input
// byte, which is what a tree costs to hold rather than to build.
func BenchmarkCorpusParse(b *testing.B) {
	for _, c := range benchCorpora {
		b.Run(c.name, func(b *testing.B) {
			docs, size := loadCorpus(b, c.glob)
			parseAll := func() []*Tree {
				trees := make([]*Tree, 0, len(docs))
				for _, d := range docs {
					t, err := Parse(bytes.NewReader(d), ParseOptions{MaxBytes: -1})
					if err != nil {
						b.Fatal(err)
					}
					trees = append(trees, t)
				}
				return trees
			}
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&m0)
			kept := parseAll()
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&m1)
			runtime.KeepAlive(kept)
			kept = nil
			retained := float64(int64(m1.HeapAlloc) - int64(m0.HeapAlloc))

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				parseAll()
			}
			b.ReportMetric(retained, "retained-B")
			b.ReportMetric(retained/float64(size), "retained-B/B")
		})
	}
}

// BenchmarkCorpusShape logs, per corpus, what its trees contain: nodes by
// kind, how often each optional field is set, and the distribution of
// children and attributes per element. It is the input for choosing node
// record widths, not a timing; run it with -benchtime 1x.
func BenchmarkCorpusShape(b *testing.B) {
	for _, c := range benchCorpora {
		b.Run(c.name, func(b *testing.B) {
			docs, _ := loadCorpus(b, c.glob)
			var kinds [KindNamespace + 1]int
			fields := map[string]int{}
			var kidCounts, attrs []int
			var walk func(n *Node)
			walk = func(n *Node) {
				kinds[n.Kind()]++
				set := func(name string, ok bool) {
					if ok {
						fields[name]++
					}
				}
				set("Name.Prefix", n.Name().Prefix != "")
				set("Name.URI", n.Name().URI != "")
				set("Value", n.Value() != "")
				set("Namespaces", len(nsOf(n)) > 0)
				set("BaseURI", n.BaseURI() != "")
				set("BaseURI!=parent", n.Parent() != nil && n.BaseURI() != n.Parent().BaseURI())
				set("DocumentURI", n.DocumentURI() != "")
				set("TypeAnnotation", n.TypeAnnotation() != "")
				kinds[KindNamespace] += len(nsOf(n))
				if n.Kind() == KindElement {
					kidCounts = append(kidCounts, len(kids(n)))
					attrs = append(attrs, len(attrsOf(n)))
				}
				for _, a := range attrsOf(n) {
					walk(a)
				}
				for _, ch := range kids(n) {
					walk(ch)
				}
			}
			for i := 0; i < b.N; i++ {
				kinds, fields, kidCounts, attrs = [KindNamespace + 1]int{}, map[string]int{}, nil, nil
				for _, d := range docs {
					t, _ := Parse(bytes.NewReader(d), ParseOptions{MaxBytes: -1})
					walk(t.Root)
				}
			}
			total := 0
			var sb strings.Builder
			for k, n := range kinds {
				total += n
				sb.WriteString(" " + NodeKind(k).String() + "=" + strconv.Itoa(n))
			}
			b.Logf("%d docs, %d nodes:%s", len(docs), total, sb.String())
			names := make([]string, 0, len(fields))
			for f := range fields {
				names = append(names, f)
			}
			sort.Strings(names)
			for _, f := range names {
				b.Logf("  %-16s set on %5.1f%% of nodes", f, 100*float64(fields[f])/float64(total))
			}
			b.Logf("  children/element %s; attributes/element %s", pctiles(kidCounts), pctiles(attrs))
		})
	}
}

func pctiles(v []int) string {
	if len(v) == 0 {
		return "-"
	}
	sort.Ints(v)
	at := func(p float64) string { return strconv.Itoa(v[int(p*float64(len(v)-1))]) }
	return "p50=" + at(.5) + " p90=" + at(.9) + " p99=" + at(.99) + " max=" + at(1)
}
