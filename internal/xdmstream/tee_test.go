package xdmstream

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// TestTee: every consumer of a Tee reads the events one cursor reads, the
// bytes read once, the consumers taking turns (swap balanced, one at a time);
// a malformed document fails each the way it fails one cursor; a failed
// consumer stops those after it, not those before; a used cursor is refused.
func TestTee(t *testing.T) {
	src := doc(40)
	all := func(c *Cursor) ([]string, error) {
		var evs []string
		for {
			ev, err := c.Next()
			if err != nil {
				return evs, err
			}
			evs = append(evs, show(ev))
			if ev.Kind == EndDocument {
				return evs, nil
			}
		}
	}
	want, werr := all(Open(strings.NewReader(src), opts))
	if werr != nil {
		t.Fatal(werr)
	}
	for _, n := range []int{1, 2, 4} {
		reads := 0 // bytes
		r := &countReads{r: iotest.HalfReader(strings.NewReader(src)), n: &reads}
		c := Open(r, opts)
		got := make([][]string, n)
		in, depth := -1, 0
		errs := c.Tee(n, func(k int, sub *Cursor) error {
			if in != k {
				return fmt.Errorf("consumer %d runs during %d's turn", k, in)
			}
			var err error
			got[k], err = all(sub)
			return err
		}, func(k int, on bool) {
			if on {
				in, depth = k, depth+1
			} else {
				in, depth = -1, depth-1
			}
			if depth != 0 && depth != 1 {
				t.Errorf("swap depth %d", depth)
			}
		})
		for k := range n {
			if errs[k] != nil {
				t.Fatalf("n=%d consumer %d: %v", n, k, errs[k])
			}
			if strings.Join(got[k], "\n") != strings.Join(want, "\n") {
				t.Fatalf("n=%d consumer %d: events differ", n, k)
			}
		}
		if _, err := c.Next(); err != errTeed {
			t.Errorf("the teed cursor: %v", err)
		}
		if reads != len(src) {
			t.Errorf("n=%d: %d bytes read of %d", n, reads, len(src))
		}
	}

	// Malformed: consumer 0 fails as one cursor does, and stops the others.
	bad := src[:len(src)-40]
	_, berr := all(Open(strings.NewReader(bad), opts))
	errs := Open(strings.NewReader(bad), opts).Tee(3, func(k int, sub *Cursor) error {
		_, err := all(sub)
		return err
	}, func(int, bool) {})
	if berr == nil || errs[0] == nil || errs[0].Error() != berr.Error() {
		t.Errorf("malformed, consumer 0: %v, want %v", errs[0], berr)
	}
	for k, err := range errs[1:] {
		if err == nil || !strings.Contains(err.Error(), errTeeStopped.Error()) {
			t.Errorf("malformed, consumer %d after 0 failed: %v", k+1, err)
		}
	}

	// Consumer 1 fails at its first event: 2 is stopped, 0 reads on.
	errs = Open(strings.NewReader(src), opts).Tee(3, func(k int, sub *Cursor) error {
		if k == 1 {
			sub.Next()
			return io.ErrUnexpectedEOF
		}
		_, err := all(sub)
		return err
	}, func(int, bool) {})
	if errs[0] != nil || errs[1] != io.ErrUnexpectedEOF || errs[2] == nil {
		t.Errorf("stop after a failure: %v", errs)
	}

	used := Open(strings.NewReader(src), opts)
	used.Next()
	if errs := used.Tee(1, func(int, *Cursor) error { return nil }, func(int, bool) {}); errs[0] != errTeeRead {
		t.Errorf("Tee of a read cursor: %v", errs[0])
	}
}

type countReads struct {
	r io.Reader
	n *int
}

func (c *countReads) Read(p []byte) (int, error) {
	m, err := c.r.Read(p)
	*c.n += m
	return m, err
}
