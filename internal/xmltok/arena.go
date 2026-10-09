package xmltok

import "unsafe"

// Arena copies short byte slices into shared blocks and returns them as
// strings, so that the many small values of one parse cost one allocation
// per block rather than one each.
//
// The strings are immutable copies: a block is only ever appended to, so the
// bytes behind a string already handed out are never written again. The
// trade-off is retention: one string still reachable keeps its whole block
// alive. For a tree that lives and dies as a whole that costs nothing, and
// a value longer than arenaMax gets its own allocation, so a long value
// never pins a block nor is pinned by one.
//
// Blocks start at arenaMax and double up to arenaBlock, as xdm's node chunks
// do, so that a small document does not pin a full block it never fills: a
// parse has two arenas, and two fixed 32 KiB blocks were a fifth of the
// retained heap of an e-invoice or a stylesheet.
type Arena struct {
	buf  []byte
	size int // capacity of the last block made
}

const (
	arenaBlock = 32 << 10
	arenaMax   = 1 << 10
)

// String returns a string holding a copy of b.
func (a *Arena) String(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > arenaMax {
		return string(b)
	}
	if cap(a.buf)-len(a.buf) < len(b) {
		a.size = min(max(2*a.size, arenaMax), arenaBlock)
		a.buf = make([]byte, 0, a.size)
	}
	n := len(a.buf)
	a.buf = append(a.buf, b...)
	return unsafe.String(&a.buf[n], len(b))
}
