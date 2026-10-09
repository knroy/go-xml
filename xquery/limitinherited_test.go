package xquery_test

import (
	"testing"

	"github.com/knroy/go-xml/v2/xquery"
)

// limitInherited skips its work when nothing in scope at the parent declares
// a namespace, and must still undeclare a fixup binding when something does:
// on the child <a:n1/>, a is in scope (its name needs it) and b is not
// (K2-NameTest-30), while children of an undeclaring parent see only xml.
func TestConstructedChildDoesNotInheritFixupBindings(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`declare namespace a="urn:a"; declare namespace b="urn:b";
		  string-join(sort(in-scope-prefixes(<e a:n1="c" b:n1="c"><a:n1/></e>/a:n1)), ' ')`, `a xml`},
		{`declare namespace b="urn:b";
		  string-join(sort(in-scope-prefixes(<e b:n1="c"><f><g/></f></e>/f/g)), ' ')`, `xml`},
		{`string-join(sort(in-scope-prefixes(<e><f><g/></f></e>/f/g)), ' ')`, `xml`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}
