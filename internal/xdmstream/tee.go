package xdmstream

import (
	"errors"
	"fmt"
)

// Tee is how several consumers read one document at once (xsl:fork, an
// xsl:map whose entries each read it: full streaming C10). Each consumer gets
// a cursor of its own over the bytes c would have parsed, which are read
// from c's reader once, a block at a time, as the consumers ask for them.
// Every cursor parses those bytes with c's options, whitespace stripping and
// Handoff,
// so each sees exactly the events, nodes, identities, errors and limits c
// would have: the consumers need nothing a single cursor does not give them.
// The cost is a parse per consumer; the memory, one block of bytes beside
// what each parse holds.
//
// The consumers take turns, one at a time, in the order k = 0..n-1 for each
// block: run(k, its cursor) runs until its parse wants bytes that have not
// been read, then the next consumer goes on. swap is called as consumer k
// stops (in false) and goes on (in true), so that state the consumers share
// can be set aside between turns (state a goroutine holds, such as a lock,
// is c's Handoff's: a turn ends as a parse waits for bytes). A consumer runs on
// a goroutine of its own, but never at the same time as another or as the
// caller: each turn starts and ends with a channel exchange.
//
// Tee returns each consumer's error. Once consumer k has failed, those after
// it are not needed (an error is reported in consumer order): their cursors
// fail with errTeeStopped at their next read. A panic in run is raised again
// in the caller, the first in consumer order. Tee may be called only before
// anything has been read from c; c is spent afterwards.
func (c *Cursor) Tee(n int, run func(k int, sub *Cursor) error, swap func(k int, in bool)) []error {
	errs := make([]error, n)
	if !c.Fresh() {
		for k := range errs {
			errs[k] = errTeeRead
		}
		return errs
	}
	c.teed = true
	t := &tee{ask: make(chan teeMsg), give: make([]chan teeBlock, n), panics: make([]any, n)}
	stopAfter := n
	waiting := make([]int, 0, n)
	turn := func(k int, b teeBlock, start bool) {
		swap(k, true)
		if start {
			t.start(k, c, run, errs)
		} else {
			t.give[k] <- b
		}
		m := <-t.ask
		swap(k, false)
		if !m.done {
			waiting = append(waiting, k)
			return
		}
		if (errs[k] != nil || t.panics[k] != nil) && k < stopAfter {
			stopAfter = k
		}
	}
	for k := range n {
		t.give[k] = make(chan teeBlock)
		turn(k, teeBlock{}, true)
	}
	buf := make([]byte, 64<<10)
	for len(waiting) > 0 {
		nr, err := c.src.Read(buf)
		if nr == 0 && err == nil {
			continue
		}
		ks := waiting
		waiting = make([]int, 0, len(ks))
		for _, k := range ks {
			b := teeBlock{buf[:nr], err}
			if k > stopAfter {
				b = teeBlock{nil, errTeeStopped}
			}
			turn(k, b, false)
		}
	}
	for _, p := range t.panics {
		if p != nil {
			panic(p)
		}
	}
	return errs
}

// Fresh reports whether c can still be teed: nothing has been read from it
// and it has no observers.
func (c *Cursor) Fresh() bool {
	return c.resumes == 0 && c.peeked == nil && c.peekErr == nil && !c.teed && len(c.obs) == 0
}

// errTeeRead is Tee's error for a cursor that has been read from, or has
// observers, which the cursors it would make could not carry.
var errTeeRead = errors.New("xdmstream: Tee of a cursor already read")

// errTeeStopped fails the cursors of the consumers after one that failed.
var errTeeStopped = errors.New("xdmstream: another reader of the document failed first")

// errTeed is the error of a cursor Tee took over.
var errTeed = errors.New("xdmstream: the cursor was handed to Tee")

type tee struct {
	ask    chan teeMsg     // a consumer's turn ended: it wants bytes, or is done
	give   []chan teeBlock // the next block, to consumer k
	panics []any
}

type teeMsg struct{ done bool }

type teeBlock struct {
	b   []byte
	err error
}

// start runs consumer k on a goroutine; it returns once k's first turn has
// begun, the caller then waiting for its end.
func (t *tee) start(k int, c *Cursor, run func(int, *Cursor) error, errs []error) {
	r := &teeReader{t: t, k: k, handoff: c.handoff}
	sub := Open(r, c.opts)
	sub.strip, sub.handoff = c.strip, c.handoff
	go func() {
		defer func() {
			if p := recover(); p != nil {
				t.panics[k] = p
				if errs[k] == nil {
					errs[k] = fmt.Errorf("xdmstream: reader %d panicked", k)
				}
			}
			sub.Close()
			t.ask <- teeMsg{done: true}
		}()
		errs[k] = run(k, sub)
	}()
}

// teeReader is consumer k's view of the bytes: the block given at its last
// turn, then a turn's end to wait for the next.
type teeReader struct {
	t   *tee
	k   int
	b   []byte
	err error
	// handoff is the cursors' Handoff: the turn ends here, inside the
	// parse, on the consumer's goroutine.
	handoff func() func()
}

func (r *teeReader) Read(p []byte) (int, error) {
	for len(r.b) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		back := func() {}
		if r.handoff != nil {
			back = r.handoff()
		}
		r.t.ask <- teeMsg{}
		blk := <-r.t.give[r.k]
		back()
		r.b, r.err = blk.b, blk.err
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
