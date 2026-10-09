//go:build goxml_nohttp

package xsd

import (
	"strings"
	"testing"
)

// Without HTTPResolver in the build, a refusal must not send the reader to it.
func TestRefusalsDoNotNameAMissingHTTPResolver(t *testing.T) {
	_, _, none := noResolverConfigured{}.Resolve("", "a.xsd", "")
	_, _, remote := (&FileResolver{}).Resolve("", "https://example.org/a.xsd", "")
	if none == nil || remote == nil {
		t.Fatalf("want two refusals, got %v and %v", none, remote)
	}
	if strings.Contains(none.Error(), "HTTPResolver") {
		t.Errorf("no-resolver refusal names HTTPResolver: %v", none)
	}
	if strings.Contains(remote.Error(), "see HTTPResolver") {
		t.Errorf("remote refusal points at HTTPResolver: %v", remote)
	}
}
