package xslt

import (
	"context"
	"errors"
	"testing"
	"time"
)

// pollCtx reports, on polled, each time something asks it whether it is done.
// The static phase's XPath asks at every loop boundary, so a poll after the
// compilation has started is proof that the static phase is running under it.
type pollCtx struct {
	context.Context
	polled chan struct{}
}

func (p pollCtx) Err() error {
	select {
	case p.polled <- struct{}{}:
	default:
	}
	return p.Context.Err()
}

// slowStatic is a stylesheet whose static phase loops four million times,
// polling its context at each step: long enough to be cancelled part way, and
// under the item budget, which would otherwise end it first.
const slowStatic = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:variable name="v" static="yes" select="every $i in 1 to 4000000 satisfies $i gt 0"/>
  <xsl:template name="main"><out/></xsl:template>
</xsl:stylesheet>`

// waitPolled waits for the static phase to poll p, which is how a test knows
// the compilation holding p is past the slot and inside its static phase.
func waitPolled(t *testing.T, p pollCtx) {
	t.Helper()
	select {
	case <-p.polled:
	case <-time.After(10 * time.Second):
		t.Fatal("the static phase never polled its context")
	}
}

// A compilation waiting for the slot gives up when its context does, and
// cancelling the compilation holding the slot stops its static phase and
// frees the slot.
func TestCompileContextCancelsWaitAndStaticPhase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow := pollCtx{ctx, make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		_, err := CompileContext(slow, parseSheet(t, slowStatic), CompileOptions{})
		done <- err
	}()
	waitPolled(t, slow) // CompileContext's own check, before it waits
	waitPolled(t, slow) // and then the static phase's

	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	quick := parseSheet(t, wrap(`<xsl:template match="/"><a/></xsl:template>`))
	if _, err := CompileContext(short, quick, CompileOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting compile: got %v, want context.DeadlineExceeded", err)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled compile: got %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not stop the static phase")
	}
	again, cancelAgain := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelAgain()
	if _, err := CompileContext(again, quick, CompileOptions{}); err != nil {
		t.Fatalf("the slot was not released: %v", err)
	}
}
