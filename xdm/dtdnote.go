package xdm

import (
	"fmt"
	"iter"

	"github.com/knroy/go-xml/v2/internal/dtdnote"
)

func init() {
	dtdnote.Notes = func(node any) iter.Seq2[any, string] {
		return func(yield func(any, string) bool) {
			t := node.(*Node).tree
			if t.source == nil {
				return
			}
			for _, n := range t.source.dtdNotes {
				if !yield(t.rec(n.at), n.msg) {
					return
				}
			}
		}
	}
}

// dtdNote is a validity error the parse saw and the tree cannot show (see
// package internal/dtdnote): at is the record it concerns.
type dtdNote struct {
	at  uint32
	msg string
}

// maxDTDNotes bounds the validity errors one parse records. dtd.Validate
// reports at most DefaultMaxErrors (100) unless told otherwise, and a
// document wrong at every element would otherwise keep a message per element.
const maxDTDNotes = 100

// note records a validity error about n for dtd.Validate, once where the same
// one recurs in a row, as at each run of white space in one element. A parse
// in bursts (xdmburst) records none: its records do not outlive the burst.
func (ps *parseState) note(t *Tree, n *Node, format string, args ...any) {
	if ps.opts.bursts != nil {
		return
	}
	s := t.ownSource()
	if len(s.dtdNotes) >= maxDTDNotes {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if k := len(s.dtdNotes); k > 0 && s.dtdNotes[k-1] == (dtdNote{n.self, msg}) {
		return
	}
	s.dtdNotes = append(s.dtdNotes, dtdNote{n.self, msg})
}
