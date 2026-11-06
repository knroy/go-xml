package c14n

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// A cancelled context stops the XPath Filter before it evaluates the filter
// for more than a node or two, and the error says why.
func TestFromXPathFilterContextCancelled(t *testing.T) {
	doc := parse(t, `<a><b/><b/><b/><b/><b/><b/><b/><b/></a>`)
	evaluated := 0
	onFilterNode = func(*xdm.Node) { evaluated++ }
	defer func() { onFilterNode = nil }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FromXPathFilterContext(ctx, doc, "true()", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if evaluated > 1 {
		t.Fatalf("filter evaluated for %d nodes after cancellation", evaluated)
	}
	if _, err := FromXPathContext(ctx, doc, "//b", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("FromXPathContext err = %v, want context.Canceled", err)
	}
}

// Cancelled while the filter runs, the node being evaluated sees it: the
// error comes from inside that node's evaluation, not from the check
// between nodes.
func TestFromXPathFilterContextReachesEachNode(t *testing.T) {
	doc := parse(t, `<a><b/><b/><b/><b/><b/><b/><b/><b/></a>`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evaluated := 0
	onFilterNode = func(*xdm.Node) {
		if evaluated++; evaluated == 3 {
			cancel()
		}
	}
	defer func() { onFilterNode = nil }()
	_, err := FromXPathFilterContext(ctx, doc, "count(for $i in 1 to 2 return $i) gt 0", nil)
	if !errors.Is(err, context.Canceled) || strings.HasPrefix(err.Error(), "c14n:") {
		t.Fatalf("err = %v, want context.Canceled from the node's evaluation", err)
	}
	if evaluated != 3 {
		t.Fatalf("filter evaluated for %d nodes, want 3", evaluated)
	}
}

// The per-node evaluations share one item budget: a filter that stays well
// inside the budget at every node is refused once its total crosses it.
func TestFromXPathFilterSharedBudget(t *testing.T) {
	testEnv = func(e *xpath.Env) { e.MaxItems = 1000 }
	defer func() { testEnv = nil }()
	doc := parse(t, `<a>`+strings.Repeat(`<b/>`, 40)+`</a>`)
	// About 100 items per node, so no single evaluation nears 1000.
	const filter = "count(for $i in 1 to 50 return $i) gt 0"
	if _, err := FromXPathFilter(parse(t, `<a/>`), filter, nil); err != nil {
		t.Fatalf("small document: %v", err)
	}
	_, err := FromXPathFilter(doc, filter, nil)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("err = %v, want xdm.ErrResourceLimit", err)
	}
}
