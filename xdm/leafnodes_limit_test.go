package xdm

import (
	"errors"
	"strings"
	"testing"
)

// MaxNodes counts every node the parse builds: text, comments and processing
// instructions as well as elements. Only elements used to be charged, so a
// document of 2,000 PIs parsed under MaxNodes 100.
func TestParseMaxNodesCountsLeaves(t *testing.T) {
	for _, leaf := range []string{"<?a?>", "<!--c-->", "<b/>", "x<b/>"} {
		doc := "<r>" + strings.Repeat(leaf, 2000) + "</r>"
		_, err := ParseString(doc, ParseOptions{MaxNodes: 100})
		if !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "document exceeds 100 nodes") {
			t.Errorf("%q x2000 under MaxNodes 100: err = %v, want the node limit", leaf, err)
		}
	}
	// At the limit is accepted, and stripped whitespace is not a node.
	doc := "<r>t<!--c--><?p?></r>"
	if _, err := ParseString(doc, ParseOptions{MaxNodes: 4}); err != nil {
		t.Errorf("4-node document under MaxNodes 4: %v", err)
	}
	if _, err := ParseString(doc, ParseOptions{MaxNodes: 3}); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("4-node document under MaxNodes 3: err = %v, want the node limit", err)
	}
	// 51 elements and 51 whitespace runs.
	ws := "<r>" + strings.Repeat("\n <b/>", 50) + "\n</r>"
	if _, err := ParseString(ws, ParseOptions{MaxNodes: 101}); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("kept whitespace under MaxNodes 101: err = %v, want the node limit", err)
	}
	stripAll := func(QName) bool { return true }
	if _, err := ParseString(ws, ParseOptions{MaxNodes: 51, StripSpace: stripAll}); err != nil {
		t.Errorf("stripped whitespace under MaxNodes 51: %v", err)
	}
}
