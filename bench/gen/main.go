// Command gen writes a deterministic, well-formed XML document of roughly
// the requested size for the parse benchmarks. The shape mixes a wide
// record list with deep section nesting, attributes, escaped text, CDATA,
// comments and three namespaces. The same -seed and -size give the same bytes
// on every platform and Go version (math/rand/v2 PCG is a specified algorithm).
//
//	go run ./bench/gen -size 10000000 -o testdata/bench/parse/parse-10mb.xml
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

var words = strings.Fields(`alpha beta gamma delta epsilon zeta eta theta iota
kappa lambda invoice ledger account balance shipment order customer supplier
quantity price total tax region north south east west harbour market river
stone glass paper copper silver gold amber cobalt indigo violet`)

type gen struct {
	w     *bufio.Writer
	r     *rand.Rand
	n     int64 // bytes written
	limit int64
}

func (g *gen) put(s string) { n, _ := g.w.WriteString(s); g.n += int64(n) }

func (g *gen) text(min, max int) string {
	k := min + g.r.IntN(max-min+1)
	var b strings.Builder
	for i := 0; i < k; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[g.r.IntN(len(words))])
	}
	return b.String()
}

func (g *gen) indent(d int) { g.put("\n" + strings.Repeat("  ", d)) }

// record is the wide part: a flat, attribute-heavy element with short children.
func (g *gen) record(id, d int) {
	g.indent(d)
	g.put(fmt.Sprintf(`<rec id="r%d" status="%s" m:rev="%d">`, id,
		[]string{"open", "closed", "draft"}[g.r.IntN(3)], g.r.IntN(100)))
	g.indent(d + 1)
	g.put("<name>" + g.text(1, 4) + "</name>")
	g.indent(d + 1)
	g.put(fmt.Sprintf(`<amount currency="EUR">%d.%02d</amount>`, g.r.IntN(100000), g.r.IntN(100)))
	switch g.r.IntN(6) {
	case 0:
		g.indent(d + 1)
		g.put("<note>" + g.text(3, 12) + " &amp; " + g.text(1, 3) + " &lt;ok&gt;</note>")
	case 1:
		g.indent(d + 1)
		g.put("<x:ext><x:raw><![CDATA[" + g.text(2, 8) + " <b>&</b>]]></x:raw></x:ext>")
	case 2:
		g.indent(d + 1)
		g.put("<!-- " + g.text(2, 6) + " -->")
	}
	g.indent(d + 1)
	g.put(fmt.Sprintf(`<link ref="r%d"/>`, g.r.IntN(id+1)))
	g.indent(d)
	g.put("</rec>")
}

// section is the deep part: nested sections with mixed content paragraphs.
func (g *gen) section(depth, d int) {
	g.indent(d)
	g.put(fmt.Sprintf(`<section level="%d" xml:lang="en">`, depth))
	g.indent(d + 1)
	g.put("<title>" + g.text(2, 5) + "</title>")
	for i, n := 0, 1+g.r.IntN(3); i < n; i++ {
		g.indent(d + 1)
		g.put("<para>" + g.text(5, 20) + " <em>" + g.text(1, 3) + "</em> " + g.text(3, 10) + "</para>")
	}
	if depth < 24 && g.r.IntN(10) < 8 {
		g.section(depth+1, d+1)
	}
	g.indent(d)
	g.put("</section>")
}

func main() {
	size := flag.Int64("size", 1_000_000, "approximate output size in bytes")
	seed := flag.Uint64("seed", 20261007, "PRNG seed")
	out := flag.String("o", "", "output file (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "gen: -o is required")
		os.Exit(2)
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	g := &gen{w: bufio.NewWriterSize(f, 1<<20), r: rand.New(rand.NewPCG(*seed, 0)), limit: *size}
	g.put(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	g.put(`<doc xmlns="urn:go-xml:bench:doc" xmlns:m="urn:go-xml:bench:meta" xmlns:x="urn:go-xml:bench:ext" m:seed="` + fmt.Sprint(*seed) + `">`)
	id := 0
	for g.n < g.limit-64 {
		g.indent(1)
		g.put(fmt.Sprintf(`<group n="%d">`, id))
		for i, k := 0, 20+g.r.IntN(80); i < k; i++ {
			g.record(id, 2)
			id++
		}
		g.section(1, 2)
		g.indent(1)
		g.put("</group>")
	}
	g.put("\n</doc>\n")
	err = g.w.Flush()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}
