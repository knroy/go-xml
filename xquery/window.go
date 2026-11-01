package xquery

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
)

// windowVars are the five variables a window boundary condition may bind:
// the item at the boundary, its position, the item before it and the item
// after it. Each is optional and each is written the same way at both ends,
// so one shape serves the start condition and the end condition alike.
type windowVars struct {
	item    xdm.QName
	hasItem bool
	pos     xdm.QName
	hasPos  bool
	prev    xdm.QName
	hasPrev bool
	next    xdm.QName
	hasNext bool
	when    *compiledExpr
}

// bind layers this condition's variables onto a tuple for position i (1-based)
// of the binding sequence.
//
// §3.10.4 defines "previous" and "next" as the empty sequence at the ends of
// the sequence rather than as an error, which is what makes
// "end $x next $y when empty($y)" the idiom for "the last window".
func (v *windowVars) bind(t tuple, seq xdm.Sequence, i int, x *nameExt) tuple {
	// The names this condition binds are laid out once and added to the tuple
	// in one pass, sharing the extended names slice through x, rather than
	// copying both slices once per variable.
	var nm [4]xdm.QName
	var vs [4]xdm.Sequence
	k := 0
	if v.hasItem {
		nm[k], vs[k] = v.item, xdm.One(seq[i-1])
		k++
	}
	if v.hasPos {
		nm[k], vs[k] = v.pos, xdm.One(xdm.NewInteger(int64(i)))
		k++
	}
	if v.hasPrev {
		var prev xdm.Sequence
		if i >= 2 {
			prev = xdm.One(seq[i-2])
		}
		nm[k], vs[k] = v.prev, prev
		k++
	}
	if v.hasNext {
		var next xdm.Sequence
		if i < len(seq) {
			next = xdm.One(seq[i])
		}
		nm[k], vs[k] = v.next, next
		k++
	}
	for _, n := range nm[:k] {
		if slices.Contains(t.names, n) { // rebinding replaces; the slow path
			for j := 0; j < k; j++ {
				t = t.bind(nm[j], vs[j])
			}
			return t
		}
	}
	if k == 0 {
		return t
	}
	vals := make([]xdm.Sequence, len(t.vals)+k)
	copy(vals, t.vals)
	copy(vals[len(t.vals):], vs[:k])
	return tuple{names: x.extendN(t.names, nm[:k]), vals: vals}
}

// extendN is extend for several names at once.
func (x *nameExt) extendN(names, add []xdm.QName) []xdm.QName {
	if x.to == nil || len(names) != len(x.from) ||
		(len(names) > 0 && &names[0] != &x.from[0]) {
		x.from = names
		x.to = append(append(make([]xdm.QName, 0, len(names)+len(add)), names...), add...)
	}
	return x.to
}

// winExt is the names slices a windows call extends: the start condition's on
// the input tuple, and the end condition's.
type winExt struct{ start, end nameExt }

// names returns every variable this condition binds, which is what the
// distinctness rule of XQST0103 is checked over.
func (v *windowVars) names() []xdm.QName {
	var out []xdm.QName
	if v.hasItem {
		out = append(out, v.item)
	}
	if v.hasPos {
		out = append(out, v.pos)
	}
	if v.hasPrev {
		out = append(out, v.prev)
	}
	if v.hasNext {
		out = append(out, v.next)
	}
	return out
}

// windowClause is a tumbling or sliding window: §3.10.4, productions
// [51]-[58].
//
// The two differ in exactly one rule, and it is worth naming because it is the
// whole of the distinction. A *tumbling* window may not start inside another:
// after a window closes, the search for the next start resumes at the item
// after that window's end. A *sliding* window has no such rule, so a start is
// looked for at every position, and windows overlap.
//
// Everything else — how the end is found, what "only" means, which variables
// the conditions bind — is shared, which is why one type implements both.
type windowClause struct {
	name    xdm.QName
	seq     *compiledExpr
	sliding bool
	// only makes an unclosed window be discarded rather than emitted. Without
	// it, a window whose end condition never becomes true runs to the end of
	// the sequence and is emitted anyway, which §3.10.4 calls the "final"
	// window.
	only  bool
	start windowVars
	end   windowVars
	// hasEnd reports whether an end condition was written. A tumbling window
	// with none ends where the next one starts, which partitions the sequence;
	// a sliding window must have one, which the parser enforces.
	hasEnd bool

	// windowType is the source of the window variable's declared type, empty
	// when none was written. It is checked per window rather than compiled
	// into the binding expression, because it constrains each window and the
	// windows are not known until the clause runs. See checkWindowType.
	windowType string
	// typeCheck is windowType compiled as a "treat as" over a variable this
	// clause binds, built lazily on first use.
	typeCheck *compiledExpr
}

func (c *windowClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	var out []tuple
	for _, t := range in {
		seq, err := c.seq.eval(t.sub(ctx))
		if err != nil {
			return nil, err
		}
		ts, err := c.windows(t, seq, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	return out, nil
}

// windows finds the windows of one binding sequence and returns one tuple per
// window.
func (c *windowClause) windows(t tuple, seq xdm.Sequence, ctx *evalContext) ([]tuple, error) {
	var out []tuple
	var x winExt
	for s := 1; s <= len(seq); s++ {
		st := c.start.bind(t, seq, s, &x.start)
		ok, err := c.start.when.evalBool(st.sub(ctx))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		end, closed, err := c.findEnd(st, seq, s, ctx, &x)
		if err != nil {
			return nil, err
		}
		if !closed && c.only {
			// §3.10.4: "only" discards a window whose end condition never
			// became true. Later starts are still tried, because whether a
			// window closes depends on where it started — "only end $e when
			// $e eq $s + 10" over (0, 1, 2, 3, 4, 14, 13, 12, 11) closes for
			// every start but the first, whose 10 is not in the sequence.
			// Stopping at the first unclosed window would return one window
			// where the specification asks for four.
			continue
		}
		w := c.emit(st, seq, s, end, closed, &x)
		if c.typeCheck != nil {
			v, _ := w.lookup(c.name)
			if _, err := applyCheck(c.typeCheck, v, ctx); err != nil {
				return nil, err
			}
		}
		out = append(out, w)
		if !c.sliding {
			// A tumbling window's successor starts after this one ends,
			// which is what keeps them disjoint.
			s = end
		}
	}
	return out, nil
}

// findEnd returns the position the window starting at s ends at, and whether
// an end condition actually fired there.
//
// A tumbling window with no end condition ends at the item before the next
// start, so the search is for the next position that satisfies the *start*
// condition — the one case where the end is defined in terms of the start.
func (c *windowClause) findEnd(st tuple, seq xdm.Sequence, s int,
	ctx *evalContext, x *winExt) (int, bool, error) {
	if !c.hasEnd {
		for e := s + 1; e <= len(seq); e++ {
			nt := c.start.bind(st, seq, e, &x.start)
			ok, err := c.start.when.evalBool(nt.sub(ctx))
			if err != nil {
				return 0, false, err
			}
			if ok {
				return e - 1, true, nil
			}
		}
		return len(seq), false, nil
	}
	for e := s; e <= len(seq); e++ {
		et := c.end.bind(st, seq, e, &x.end)
		ok, err := c.end.when.evalBool(et.sub(ctx))
		if err != nil {
			return 0, false, err
		}
		if ok {
			return e, true, nil
		}
	}
	return len(seq), false, nil
}

// emit builds the tuple for one window: the window variable bound to its
// items, and every boundary variable bound to the value it had at the
// boundary.
func (c *windowClause) emit(st tuple, seq xdm.Sequence, s, e int,
	closed bool, x *winExt) tuple {
	t := st
	// §3.10.4 binds the end variables to the window's last item whether the
	// end condition fired there or the window simply ran out of sequence.
	// The unclosed case is not an exception: "$e" is the last item either
	// way, and "next $en" is then the empty sequence, because there is no
	// item after the end of the sequence. That is what distinguishes the two
	// — the "next" variable, not the end variable.
	//
	// An empty window has no last item, so there is nothing to bind and the
	// variables stay unbound rather than being bound to nothing.
	if (c.hasEnd || !c.sliding) && e >= s {
		t = c.end.bind(t, seq, e, &x.end)
	}
	return t.bind(c.name, append(xdm.Sequence(nil), seq[s-1:e]...))
}
