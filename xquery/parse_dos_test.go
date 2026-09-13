package xquery

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Two crash-level faults reachable from Compile on malformed query text, both
// found by FuzzCompile. Each is an anonymous string an embedder would accept
// from a caller: no document, no schema, no privileges. In a library a hang or
// a panic on attacker-controlled input is a denial of service, so each is
// pinned here with the reduction that found it.

// Finding A: a byte that may continue a name but may not start one hung the
// clause scan forever.
//
// scanExprSingleSource's word branch tested the byte with isNameStartByte,
// which admits every byte >= 0x80 because one byte cannot say which character
// a UTF-8 sequence spells. scanNCName then decoded the whole rune and applied
// the real production. A combining mark -- a name character that is not a
// name *start* character -- passed the byte test and failed the rune test, so
// scanNCName returned "" with the cursor exactly where it found it. Nothing
// else in the loop body advanced, so the scan spun on that byte forever.
//
// U+0300 COMBINING GRAVE ACCENT is such a character, and the input below is
// the fuzzer's reduction. The compile must finish and must refuse the query;
// the timeout is what makes a regression fail this test rather than hang CI.
func TestNonStartNameCharDoesNotHangClauseScan(t *testing.T) {
	const src = "for$A in M\x17̀ 0(00000\xdf"
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		_, err := Compile(src, Options{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("%q was accepted; it is not a valid query", src)
		}
		if !strings.Contains(err.Error(), "XPST0003") {
			t.Errorf("got %v; a malformed query is a static parse error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Compile(%q) did not return; the clause scan hangs again", src)
	}
}

// A combining mark may not START a name, but it may continue one, and the fix
// must not have bought termination by rejecting it everywhere.
func TestCombiningMarkStillContinuesAName(t *testing.T) {
	for _, src := range []string{
		"let $à := 1 return $à",
		"for $à in (1, 2) return $à",
	} {
		if _, err := Compile(src, Options{}); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

// Finding B: a truncated direct constructor in a variable initialiser ran the
// cursor past the end of the source and panicked.
//
// scanDeclExpr's "<" case called skipDirConstructor and ignored its error.
// The skip had already consumed the "<" before the name it needed failed to
// parse, so the cursor sat at len(src); the fall-through "p.pos++" then put
// it one past the end, and the "p.src[start:p.pos]" closing the scan sliced
// out of range. 21 bytes of input was a panic in any embedder that compiles a
// caller-supplied query.
func TestTruncatedConstructorInDeclDoesNotPanic(t *testing.T) {
	for _, src := range []string{
		"declare variable$A:=<",
		"declare variable $A := <a",
		"declare variable $A := <a attr=",
		"declare variable $A := <!--",
		"declare variable $A := <?",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Compile(%q) panicked: %v", src, r)
				}
			}()
			_, err := Compile(src, Options{})
			if err == nil {
				t.Errorf("%q was accepted; the constructor is truncated", src)
				return
			}
			if !strings.Contains(err.Error(), "XPST0003") {
				t.Errorf("%q: got %v; want a static parse error", src, err)
			}
		}()
	}
}

// A complete constructor in an initialiser must still compile: the fix returns
// the skip's error, and returning it for a constructor that is actually well
// formed would reject valid prologs. The ";" inside an attribute and inside a
// comment are the cases that make this scan constructor-aware at all.
func TestWellFormedConstructorInDeclStillCompiles(t *testing.T) {
	for _, src := range []string{
		"declare variable $A := <a>1</a>; $A",
		"declare variable $A := <a b=\";\">t</a>; $A",
		"declare variable $A := <a><!-- ; --></a>; $A",
		"declare variable $A := <a>{ 1 }</a>; $A",
	} {
		if _, err := Compile(src, Options{}); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}
