package xslt

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// overrideBase and overrideTop are a used package and a package overriding
// its template "t", calling the original from the override.
const overrideBase = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	name="urn:base" package-version="1.0.0" version="3.0">
	<xsl:template name="t" visibility="public"><base/></xsl:template>
	<xsl:template name="go" visibility="public"><xsl:call-template name="t"/></xsl:template>
</xsl:package>`

const overrideTop = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	name="urn:top" package-version="1.0.0" version="3.0">
	<xsl:use-package name="urn:base" package-version="1.0.0">
		<xsl:override>
			<xsl:template name="t" visibility="public"><over><xsl:call-template name="xsl:original"/></over></xsl:template>
		</xsl:override>
	</xsl:use-package>
	<xsl:template name="xsl:initial-template" visibility="public"><out><xsl:call-template name="go"/></out></xsl:template>
</xsl:package>`

// originalNames lists the generated xsl:original names a stylesheet holds.
func originalNames(s *Stylesheet) []string {
	var out []string
	for k := range s.named {
		if strings.Contains(k, originalNS) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// A compilation numbers its packages and generated names from zero, so the
// same stylesheet compiles to the same names whatever compiled before it.
func TestCompileNamesDoNotDependOnEarlierCompiles(t *testing.T) {
	var first []string
	for i := 0; i < 3; i++ {
		s, err := Compile(parseSheet(t, overrideTop), CompileOptions{
			PackageResolver: fixedPackages{"urn:base": overrideBase}})
		if err != nil {
			t.Fatal(err)
		}
		names := originalNames(s)
		if len(names) == 0 {
			t.Fatal("no generated xsl:original name; the test no longer reaches the override")
		}
		if i == 0 {
			first = names
		} else if !reflect.DeepEqual(names, first) {
			t.Fatalf("compile %d named the original %v, compile 0 %v", i, names, first)
		}
	}
}

var _ = context.Background
var _ xdm.Sequence
