package xsd

import (
	"os/exec"
	"strings"
	"testing"
)

// Neither this package nor the CLI may link net/http: HTTPResolver lives in
// package xsdnet so that they do not, and one stray import of it would bring
// TLS and x509 back into every binary without breaking anything else.
func TestXSDAndCLIDoNotLinkNetHTTP(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	for _, pkg := range []string{".", "../cmd/go-xml"} {
		out, err := exec.Command(gobin, "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", pkg, err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if dep == "net/http" {
				t.Errorf("%s links net/http", pkg)
			}
		}
	}
}
