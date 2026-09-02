package xquery

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// TestDeclareBaseURIResolution covers the two halves of "declare base-uri"
// (§4.5), which pull in opposite directions and which earlier attempts kept
// trading against each other.
//
// A relative declaration has to be made absolute, and the only thing available
// to resolve it against is where the query text came from -- Options.
// DeclarationBaseURI. That is what the suite's K2-BaseURIProlog-4 ("abc") and
// -5 ("") require.
//
// An absolute declaration has to survive verbatim. Resolution is not the
// identity on an absolute URI: url.ResolveReference reparses and reserialises,
// which percent-encodes characters a declaration is entitled to contain. The
// suite's base-URI-12, -14, -23 and -24 declare absolute URIs holding a quote,
// a "#" and a trailing space, and assert fn:static-base-uri() returns them
// unchanged; routing those through resolution turned "examples " into
// "examples%20".
//
// The two are tested together because the bug is always a fix to one that
// costs the other.
func TestDeclareBaseURIResolution(t *testing.T) {
	const declBase = "file:///suite/prod/BaseURIDecl.xml"

	tests := []struct {
		name  string
		query string
		want  string
	}{{
		// K2-BaseURIProlog-4's shape: a relative declaration resolves against
		// the file the query was read from and comes back absolute.
		name:  "relative resolves against the declaration base",
		query: `declare base-uri "abc"; fn:string(fn:static-base-uri())`,
		want:  "file:///suite/prod/abc",
	}, {
		// K2-BaseURIProlog-5: an empty declaration is a legitimate relative
		// reference that RFC 3986 resolves to the base itself, not an absent
		// URI that blanks the base out.
		name:  "empty declaration resolves to the base itself",
		query: `declare base-uri ""; fn:string(fn:static-base-uri())`,
		want:  declBase,
	}, {
		// base-URI-23: a trailing space must not become %20.
		name: "absolute with a trailing space is verbatim",
		query: `declare base-uri "http://www.example.org/examples "; ` +
			`fn:string(fn:static-base-uri())`,
		want: "http://www.example.org/examples ",
	}, {
		// base-URI-12: an embedded quote must not become %22.
		name: "absolute with a quote is verbatim",
		query: `declare base-uri "http://www.example.com/abc"""; ` +
			`fn:string(fn:static-base-uri())`,
		want: `http://www.example.com/abc"`,
	}, {
		// base-URI-14: a "#" must not become %23. A fragment is also the one
		// thing url.ResolveReference would otherwise strip from the base.
		name: "absolute with a fragment marker is verbatim",
		query: `declare base-uri "http://www.example.com/abc##0;"; ` +
			`fn:string(fn:static-base-uri())`,
		want: "http://www.example.com/abc##0;",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Compile(tt.query, Options{DeclarationBaseURI: declBase})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if len(seq) != 1 {
				t.Fatalf("got %d items, want 1", len(seq))
			}
			if got := fmt.Sprint(seq[0]); got != tt.want {
				t.Errorf("static-base-uri() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDeclarationBaseURIDoesNotLeak is the other half of the separation, and
// the property every earlier attempt broke.
//
// A query that declares no base URI must not see DeclarationBaseURI at all:
// the value exists to resolve a declaration against, and nothing else. If it
// reached fn:static-base-uri or the elements a query constructs, it would pin
// a compile-time value the environment or the caller is entitled to supply
// later -- which is what the suite's base-URI-12/14/23/24 and K2-BaseURIFunc-30
// detect.
func TestDeclarationBaseURIDoesNotLeak(t *testing.T) {
	q, err := Compile(`fn:count(fn:static-base-uri())`,
		Options{DeclarationBaseURI: "file:///suite/prod/BaseURIDecl.xml"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(seq) != 1 {
		t.Fatalf("got %d items, want 1", len(seq))
	}
	// No base URI was supplied and none was declared, so there is none:
	// fn:static-base-uri returns the empty sequence and the count is zero.
	if got := fmt.Sprint(seq[0]); got != "0" {
		t.Errorf("count(static-base-uri()) = %q, want %q "+
			"(the declaration base leaked into the query's own base URI)",
			got, "0")
	}
}
