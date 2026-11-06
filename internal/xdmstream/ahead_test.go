package xdmstream

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// An end tag right after a leaf is reported without a step of the parse of
// its own (xdmburst.EndAhead): a drain of elements holding one text each
// took a step per event.
func TestEndAheadSavesAStep(t *testing.T) {
	const n = 500
	src := "<a>" + strings.Repeat("<r>t</r>", n) + "</a>"
	for _, off := range []bool{false, true} {
		c := Open(strings.NewReader(src), opts)
		c.noAhead = off
		events := 0
		for {
			ev, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			events++
			if ev.Kind == EndDocument {
				break
			}
		}
		c.Close()
		want := events // one step per event, the last reading to the end
		if !off {
			want -= n
		}
		if c.resumes != want {
			t.Errorf("noAhead=%v: %d steps for %d events, want %d", off, c.resumes, events, want)
		}
	}
}

// TestEndAheadMatchesSteps: whatever a consumer does at a leaf before an end
// tag, and at that end tag -- Next, NextTag through any filter, Peek first,
// Observe, Flush after GroundLater, Leaf, Skip elsewhere -- it meets what a
// cursor that steps the parse to every end tag gives it.
func TestEndAheadMatchesSteps(t *testing.T) {
	docs := append([]string{doc(30),
		"<a><r>t</r><r><s>u</s>v</r><r><!--c--></r><r><?p x?></r><r/>w</a>",
		"<a>" + strings.Repeat("<r k='1'><s>t</s>\n</r>", 50) + "</a>",
	}, observeDocs...)
	filters := []*Filter{
		nil,
		{Leaves: true},
		{Leaves: true, Depth: 2},
		{Leaves: false, Depth: 1, Ends: true},
		{Leaves: true, Ends: true, Accept: func(el *xdm.Node) bool { return el.Name().Local != "s" }},
	}
	for di, src := range docs {
		for seed := range 40 {
			var logs [2][]string
			for side, off := range []bool{false, true} {
				rng := rand.New(rand.NewSource(int64(seed)))
				c := Open(strings.NewReader(src), opts)
				c.noAhead = off
				log := func(f string, a ...any) { logs[side] = append(logs[side], fmt.Sprintf(f, a...)) }
				for {
					var ev Event
					var err error
					if c.ev.Kind >= Text && c.ev.Kind <= PI && rng.Intn(3) == 0 {
						if l, lerr := c.Leaf(); lerr == nil {
							log("leaf %q %v", l.StringValue(), l.Parent().Name())
						}
					}
					if rng.Intn(4) == 0 {
						ev, err = c.Peek()
						log("peek %s %d %v", show(ev), ev.Closed, err)
					}
					if rng.Intn(8) == 0 {
						obs := len(logs[side])
						c.Observe(func(n *xdm.Node, d int, end bool, ord uint64) {
							if n != nil {
								log("obs%d %d %v %v #%d", obs, d, end, n.Name(), ord)
							}
						})
					}
					if c.ev.Kind == EndElement && rng.Intn(2) == 0 {
						log("flush %v", c.Flush())
					}
					if c.ev.Kind == StartElement && c.peeked == nil && rng.Intn(4) == 0 {
						if rng.Intn(2) == 0 {
							err = c.GroundLater(func(g *xdm.Node) error { log("later %v %q", g.Name(), g.StringValue()); return nil })
						} else {
							err = c.Skip()
						}
						log("decided %s %d %v", show(c.ev), c.ev.Closed, err)
						continue
					}
					if f := filters[rng.Intn(len(filters))]; f != nil {
						ev, err = c.NextTag(f)
					} else {
						ev, err = c.Next()
					}
					log("%s %d %v", show(ev), ev.Closed, err)
					if err != nil || ev.Kind == EndDocument {
						break
					}
				}
				c.Close()
			}
			if strings.Join(logs[0], "\n") != strings.Join(logs[1], "\n") {
				t.Logf("doc %d seed %d", di, seed)
				same(t, logs[0], logs[1])
			}
		}
	}
}
