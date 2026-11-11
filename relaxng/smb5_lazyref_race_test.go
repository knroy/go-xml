package relaxng

import (
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// refPat.get writes its cache unsynchronised. That is safe only because
// Compile resolves every reachable lazy reference (checkCompetition, then
// addMemoPoints) before it publishes the Schema. Concurrent validations of one Schema through recursive
// definitions, run under -race, fail if a first resolution ever moves to
// validation time.
func TestSMB5ConcurrentValidationsOfLazyRefs(t *testing.T) {
	s, err := compileWith(t, meshGrammar(6), Options{})
	if err != nil {
		t.Fatal(err)
	}
	docs := []string{
		`<e0><e1><e2><e3/></e2></e1></e0>`,
		`<e0><e5><e4><e5/></e4></e5></e0>`,
		`<e0><e3><e3><e1><e0/></e1></e3></e3></e0>`,
		`<e0><e9/></e0>`,
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src := docs[i%len(docs)]
			doc, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if err := s.Validate(doc.Root); (err == nil) != (i%len(docs) != 3) {
				t.Errorf("%s: err = %v", src, err)
			}
		}()
	}
	wg.Wait()
}
