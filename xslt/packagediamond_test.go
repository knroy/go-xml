package xslt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// countingPackages is fixedPackages with a count of the resolutions made.
type countingPackages struct {
	fixedPackages
	calls int
}

func (p *countingPackages) ResolvePackage(name, versionMatch string) (*xdm.Node, error) {
	p.calls++
	return p.fixedPackages.ResolvePackage(name, versionMatch)
}

// layeredPackages builds levels 1..depth of packages urn:a<i> and urn:b<i>,
// each using both packages of the next level. Each declares a public function
// f:a<i> or f:b<i> naming itself and the two results of the level below, so a
// call reaches the leaves along every path.
func layeredPackages(depth int) *countingPackages {
	p := &countingPackages{fixedPackages: fixedPackages{}}
	for i := 1; i <= depth; i++ {
		for _, n := range []string{"a", "b"} {
			body, uses := fmt.Sprintf(`'%s%d'`, n, i), ""
			if i < depth {
				body = fmt.Sprintf(`concat('%s%d(', f:a%d(), f:b%d(), ')')`, n, i, i+1, i+1)
				uses = fmt.Sprintf(`<xsl:use-package name="urn:a%[1]d"><xsl:accept component="function" names="*" visibility="private"/></xsl:use-package>`+
					`<xsl:use-package name="urn:b%[1]d"><xsl:accept component="function" names="*" visibility="private"/></xsl:use-package>`, i+1)
			}
			p.fixedPackages[fmt.Sprintf("urn:%s%d", n, i)] = fmt.Sprintf(
				`<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f" `+
					`name="urn:%[1]s%[2]d" package-version="1.0" version="3.0">%[3]s`+
					`<xsl:function name="f:%[1]s%[2]d" visibility="public"><xsl:sequence select="%[4]s"/></xsl:function>`+
					`</xsl:package>`, n, i, uses, body)
		}
	}
	return p
}

const layeredTop = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f"
	name="urn:top" package-version="1.0" version="3.0">
	<xsl:use-package name="urn:a1"><xsl:accept component="function" names="*" visibility="private"/></xsl:use-package>
	<xsl:use-package name="urn:b1"><xsl:accept component="function" names="*" visibility="private"/></xsl:use-package>
	<xsl:output method="text"/>
	<xsl:template name="xsl:initial-template" visibility="public">
		<xsl:value-of select="f:a1(), f:b1()"/>
	</xsl:template>
</xsl:package>`

// A package used on several paths is compiled once per path, since each use
// may override it differently (3.5.2), so a layered graph where each level
// uses both packages of the next costs 2^depth compiles. Its read is shared,
// and the compiles are charged against maxModuleVisits: 16 levels (about
// 130,000 package compiles, which ran out of memory) is refused as a
// resource limit, resolving each package once.
func TestUsePackageDiamondIsBounded(t *testing.T) {
	tree, err := xdm.ParseString(layeredTop, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const depth = 16
	pkgs := layeredPackages(depth)
	_, err = Compile(tree.Root, CompileOptions{PackageResolver: pkgs})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("got %v after %d resolutions, want a resource limit", err, pkgs.calls)
	}
	if !strings.Contains(err.Error(), "module visits") {
		t.Errorf("error %q does not name the module-visit bound", err)
	}
	if pkgs.calls != 2*depth {
		t.Errorf("%d package resolutions, want one per package (%d)", pkgs.calls, 2*depth)
	}

	// A small diamond still compiles, and every path reaches the leaves.
	small := layeredPackages(3)
	s, err := Compile(tree.Root, CompileOptions{PackageResolver: small})
	if err != nil {
		t.Fatalf("3-level diamond: %v", err)
	}
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatal(err)
	}
	const want = "a1(a2(a3b3)b2(a3b3)) b1(a2(a3b3)b2(a3b3))"
	if got := strings.TrimSpace(res.String()); got != want {
		t.Errorf("3-level diamond: got %q, want %q", got, want)
	}
	if small.calls != 6 {
		t.Errorf("3-level diamond: %d package resolutions, want 6", small.calls)
	}
}
