package xdm

import (
	"math"
	"strings"
	"testing"
)

// Every caller-settable limit in this package, exercised at the values where
// off-by-one and overflow live: zero, negative, one, exactly at the limit,
// exactly one over, and the two largest values the type can hold.
//
// The class of bug this exists to catch is the MaxBytes overflow: the reader
// was wrapped in io.LimitReader(r, maxBytes+1) so that hitting the limit could
// be told apart from a document exactly at it, and at math.MaxInt64 that
// addition overflowed to math.MinInt64, which io.LimitReader reads as "nothing
// left". The one value a caller would pick to mean "do not limit me" refused
// every document with "no root element". Nothing tested the edges, so nothing
// caught it. These tests test the edges.
//
// The at-limit / one-over pair is the load-bearing part: it pins the boundary
// in both directions, so neither loosening nor tightening the comparison can
// pass. Each refusal is checked for a message naming the limit, because a test
// that only asserts err != nil also passes when the wrong limit fires or the
// document was malformed for an unrelated reason.

// The document is four bytes, one element node, and one level deep. Every expectation below is derived from those
// three numbers rather than restated, so the test says why each value is the
// boundary.
const (
	boundaryDoc   = `<r/>`
	boundaryBytes = 4
	boundaryDepth = 1
)

func TestParseMaxBytesBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		max     int64
		wantErr string // empty means the document must be accepted
	}{
		// Deliberate: zero means DefaultMaxBytes (64 MB), documented on the
		// field and in docs/options.md. It does not mean "refuse everything".
		{"zero is the default", 0, ""},
		// Deliberate: negative means no limit, for a caller reading input it
		// produced itself. Documented on the field.
		{"negative is unlimited", -1, ""},
		{"the smallest limit refuses", 1, "document exceeds 1 bytes"},
		{"one under refuses", boundaryBytes - 1, "document exceeds 3 bytes"},
		{"exactly at the limit is accepted", boundaryBytes, ""},
		{"one over the document is accepted", boundaryBytes + 1, ""},
		// The overflow that started this. maxBytes+1 wrapped to a negative
		// io.LimitReader limit and refused everything.
		{"MaxInt64 does not overflow", math.MaxInt64, ""},
		{"MaxInt64-1 is its neighbour", math.MaxInt64 - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(boundaryDoc, ParseOptions{MaxBytes: tt.max})
			checkLimit(t, err, tt.wantErr)
		})
	}
}

func TestParseMaxNodesBoundaries(t *testing.T) {
	// Two elements, so the at-limit and one-over cases are distinguishable
	// from the one-node document above.
	const doc = `<r><a/></r>`
	const nodes = 2

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxNodes (10,000,000).
		{"zero is the default", 0, ""},
		// Deliberate: negative means no limit.
		{"negative is unlimited", -1, ""},
		{"one under refuses", nodes - 1, "document exceeds 1 nodes"},
		{"exactly at the limit is accepted", nodes, ""},
		{"one over is accepted", nodes + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(doc, ParseOptions{MaxNodes: tt.max})
			checkLimit(t, err, tt.wantErr)
		})
	}
}

func TestParseMaxDepthBoundaries(t *testing.T) {
	// Five levels of nesting, so the boundary is well clear of the trivial
	// one-element case.
	const depth = 5
	doc := strings.Repeat("<a>", depth) + strings.Repeat("</a>", depth)

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate, and the one place the "negative means unlimited" rule
		// does NOT hold in this package: xdm/parse.go tests maxDepth <= 0, so
		// zero and negative both mean DefaultMaxDepth. docs/options.md records
		// why -- a depth limit of zero would reject every document, there being
		// no such thing as a document with no elements.
		{"zero is the default", 0, ""},
		{"negative is also the default, not unlimited", -1, ""},
		{"the smallest limit refuses a nested document", 1, "nesting exceeds 1 levels"},
		{"one under refuses", depth - 1, "nesting exceeds 4 levels"},
		{"exactly at the limit is accepted", depth, ""},
		{"one over is accepted", depth + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(doc, ParseOptions{MaxDepth: tt.max})
			checkLimit(t, err, tt.wantErr)
		})
	}

	// A single-element document is exactly one level deep, so MaxDepth 1 is
	// at the limit for it and must be accepted. Without this the "smallest
	// limit refuses" case above could pass a parser that refused everything.
	if _, err := ParseString(boundaryDoc, ParseOptions{MaxDepth: boundaryDepth}); err != nil {
		t.Errorf("MaxDepth=%d on a %d-deep document: %v", boundaryDepth, boundaryDepth, err)
	}
}

// checkLimit asserts that err matches want: absent when want is empty, and
// otherwise present and naming the limit that fired.
//
// Naming matters. An error that merely exists proves the parse failed, not
// that it failed for the reason under test -- the wrong limit tripping, or a
// malformed document, would satisfy err != nil just as well.
func checkLimit(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("accepted document was refused: %v", err)
	case want != "" && err == nil:
		t.Errorf("document was accepted; want an error matching %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error %q does not name the limit; want it to contain %q", err, want)
	}
}
