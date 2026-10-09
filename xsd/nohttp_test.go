package xsd

import (
	"os/exec"
	"strings"
	"testing"
)

// Built with -tags goxml_nohttp, neither this package nor the CLI may link
// net/http: keeping it out is the whole point of the tag, and one untagged
// import of it would bring it back without breaking anything else.
func TestNoHTTPTagDropsNetHTTP(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	for _, pkg := range []string{".", "../cmd/go-xml"} {
		out, err := exec.Command(gobin, "list", "-deps", "-tags", "goxml_nohttp", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", pkg, err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if dep == "net/http" {
				t.Errorf("%s links net/http under goxml_nohttp", pkg)
			}
		}
	}
}
