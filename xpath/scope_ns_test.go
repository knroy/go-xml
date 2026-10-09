package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// mapNS is a resolver whose values cannot be compared with ==; ptrNS is one
// that can.
type mapNS map[string]string

func (m mapNS) ResolvePrefix(p string) (string, bool) { u, ok := m[p]; return u, ok }
func (mapNS) DefaultElementNamespace() string         { return "" }
func (mapNS) DefaultFunctionNamespace() string        { return xdm.NSFN }

type ptrNS struct{ m mapNS }

func (p *ptrNS) ResolvePrefix(s string) (string, bool) { return p.m.ResolvePrefix(s) }
func (*ptrNS) DefaultElementNamespace() string         { return "" }
func (*ptrNS) DefaultFunctionNamespace() string        { return xdm.NSFN }

// An expression evaluated from inside another expands a prefixed $calendar
// against its own statically known namespaces, not the outer expression's:
// the inner one binds c, the outer one does not.
func TestNestedEvaluationUsesItsOwnNamespaces(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outer, inner NamespaceResolver
	}{
		{"uncomparable", mapNS{}, mapNS{"c": "urn:cal", "xs": xdm.NSXS}},
		{"pointer", &ptrNS{mapNS{}}, &ptrNS{mapNS{"c": "urn:cal", "xs": xdm.NSXS}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner, err := CompileVersion(
				`format-date(xs:date('2020-01-02'), '[D]', (), 'c:x', ())`,
				tc.inner, XPath31)
			if err != nil {
				t.Fatal(err)
			}
			lib := NewLibrary(Builtins())
			lib.Add(Function{Name: xdm.QName{URI: "urn:t", Local: "inner"},
				Call: func(c *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
					return inner.Eval(c)
				}})
			outer, err := CompileVersion(`Q{urn:t}inner()`, tc.outer, XPath31)
			if err != nil {
				t.Fatal(err)
			}
			ctx := NewContext(nil, lib)
			ctx.LibraryVersion = XPath31
			for i := 0; i < 2; i++ {
				seq, err := outer.Eval(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if got := seq[0].(*xdm.Atomic).String(); !strings.HasSuffix(got, "2") ||
					!strings.Contains(got, "Calendar") {
					t.Fatalf("got %q, want the calendar fallback", got)
				}
			}
		})
	}
}
