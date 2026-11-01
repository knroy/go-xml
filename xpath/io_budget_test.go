package xpath

import (
	"strings"
	"testing"
)

// fn:parse-json, fn:json-doc and fn:unparsed-text-lines build one item per
// member or line, and must charge them to the item budget as they go.
func TestJSONMembersAreCharged(t *testing.T) {
	ctx := nearlyExhausted(t, 1<<20, 5)
	_, err := callFn(t, ctx, "parse-json", strSeq("[1,2,3,4,5,6,7,8,9]"))
	wantRefused(t, err, "")
	ctx = nearlyExhausted(t, 1<<20, 5)
	_, err = callFn(t, ctx, "parse-json", strSeq(`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}`))
	wantRefused(t, err, "")
	ctx = nearlyExhausted(t, 1<<20, 50)
	if _, err = callFn(t, ctx, "parse-json", strSeq(`{"a":[1,2],"b":2}`)); err != nil {
		t.Fatalf("a small document was refused: %v", err)
	}
}

func TestTextLinesAreCharged(t *testing.T) {
	text := strings.Repeat("x\n", 9)
	_, err := splitTextLines(nearlyExhausted(t, 1<<20, 5), text)
	wantRefused(t, err, "")
	got, err := splitTextLines(nearlyExhausted(t, 1<<20, 50), "a\r\nb\rc\n")
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v, %v; want 3 lines", got, err)
	}
}
