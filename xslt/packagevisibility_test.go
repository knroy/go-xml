package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// fixedPackages resolves xsl:use-package from a table of sources, ignoring the
// version expression: the tests here are about visibility, not matching.
type fixedPackages map[string]string

func (f fixedPackages) ResolvePackage(name, versionMatch string) (*xdm.Node, error) {
	src, ok := f[name]
	if !ok {
		return nil, errNoSuchTestPackage
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	for _, el := range tree.Root.ChildElements() {
		return el, nil
	}
	return nil, errNoSuchTestPackage
}

var errNoSuchTestPackage = &packageTestError{}

type packageTestError struct{}

func (*packageTestError) Error() string { return "no such package" }

// A private function of a used package is not in the using package's static
// context, and a call to it from there is XPST0017.
//
// XSLT 3.0 3.6.3.4 puts in a package's static context only "the components of
// the packages it uses that are visible to it", which for a function means
// public, final or abstract. The suite's use-package-003 is exactly this: the
// using package calls p:f-private and expects the static error.
//
// The case is not answerable by removing the declaration, which is how a
// withheld component was handled before. The SAME declaration has to stay
// reachable from inside the package that wrote it -- use-package-base-001's
// public p:f calls its own private p:f-private, and use-package-001 requires
// that to work -- so the answer depends on which package the call is written
// in, not on the component alone.
func TestPrivateFunctionOfUsedPackageIsNotCallable(t *testing.T) {
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:p="urn:base" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:function name="p:pub" as="xs:string" visibility="public">
			<xsl:param name="in" as="xs:string"/>
			<xsl:sequence select="p:priv($in)"/>
		</xsl:function>
		<xsl:function name="p:priv" as="xs:string" visibility="private">
			<xsl:param name="in" as="xs:string"/>
			<xsl:sequence select="concat($in, $in)"/>
		</xsl:function>
	</xsl:package>`
	pkgs := fixedPackages{"urn:base": base}

	// The used package's own call through its public wrapper still works.
	const viaPublic = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:base" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:base" package-version="1.0.0"/>
		<xsl:template name="xsl:initial-template" visibility="public">
			<res>{p:pub('x')}</res>
		</xsl:template>
	</xsl:package>`
	tree, err := xdm.ParseString(viaPublic, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{PackageResolver: pkgs})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "initial-template",
			InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, ">xx</res>") {
		t.Errorf("public wrapper: got %q, want <res>xx</res> -- the used "+
			"package must still reach its own private function", got)
	}

	// Naming the private function from the using package does not.
	const direct = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:base" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:base" package-version="1.0.0"/>
		<xsl:template name="xsl:initial-template" visibility="public">
			<res>{p:priv('x')}</res>
		</xsl:template>
	</xsl:package>`
	tree2, err := xdm.ParseString(direct, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s2, cerr := Compile(tree2.Root, CompileOptions{PackageResolver: pkgs})
	if cerr == nil {
		_, cerr = s2.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "initial-template",
				InitialTemplateURI: xdm.NSXSL})
	}
	if cerr == nil {
		t.Fatal("calling the used package's private function succeeded, " +
			"want XPST0017")
	}
	if !strings.Contains(cerr.Error(), "XPST0017") {
		t.Errorf("got %v, want XPST0017", cerr)
	}
}

// A function the manifest exposes is visible even though its declaration
// carries no visibility attribute.
//
// 3.5.2 gives a component's visibility two sources, "the value of the
// visibility declaration on the declaration itself (if present), and the rules
// given in the xsl:expose declarations of the package manifest", and the
// manifest half is the one that is easy to lose: composition consumes the
// xsl:expose elements, so a visibility read off the tree afterwards sees only
// the private default. expose-A is that package -- p:f1 has no attribute and
// is exposed with names="p:*" -- and expose-002 calls it from outside.
func TestExposedFunctionOfUsedPackageIsCallable(t *testing.T) {
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:exp" name="urn:exp" package-version="1.0.0" version="3.0">
		<xsl:expose visibility="private" component="*" names="*"/>
		<xsl:expose visibility="public" component="function" names="p:*"/>
		<xsl:function name="p:f1"><xsl:sequence select="1"/></xsl:function>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:exp" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:exp" package-version="1.0.0"/>
		<xsl:template name="xsl:initial-template" visibility="public">
			<res>{p:f1()}</res>
		</xsl:template>
	</xsl:package>`
	tree, err := xdm.ParseString(top, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root,
		CompileOptions{PackageResolver: fixedPackages{"urn:exp": base}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "initial-template",
			InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, ">1</res>") {
		t.Errorf("got %q, want <res>1</res> -- an xsl:expose makes the "+
			"function public even with no visibility attribute", got)
	}
}

// An overriding declaration does not hide the component it substitutes for
// from the package that declared it.
//
// The visibility on a declaration inside xsl:override governs that declaration
// within the overriding package; the component it supplies a body for keeps the
// visibility the used package gave it. override-f-026 overrides an abstract
// g:neighbours with visibility="private" and the library's own public
// g:transitive-closure still calls it.
func TestOverridingDeclarationDoesNotHideFromUsedPackage(t *testing.T) {
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:g="urn:lib" name="urn:lib" package-version="1.0.0" version="3.0">
		<xsl:function name="g:hook" as="xs:string" visibility="abstract">
			<xsl:param name="in" as="xs:string"/>
		</xsl:function>
		<xsl:function name="g:outer" as="xs:string" visibility="public">
			<xsl:param name="in" as="xs:string"/>
			<xsl:sequence select="g:hook($in)"/>
		</xsl:function>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:g="urn:lib" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:lib" package-version="1.0.0">
			<xsl:override>
				<xsl:function name="g:hook" as="xs:string" visibility="private">
					<xsl:param name="in" as="xs:string"/>
					<xsl:sequence select="concat($in, '!')"/>
				</xsl:function>
			</xsl:override>
		</xsl:use-package>
		<xsl:template name="xsl:initial-template" visibility="public">
			<res>{g:outer('x')}</res>
		</xsl:template>
	</xsl:package>`
	tree, err := xdm.ParseString(top, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root,
		CompileOptions{PackageResolver: fixedPackages{"urn:lib": base}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "initial-template",
			InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, ">x!</res>") {
		t.Errorf("got %q, want <res>x!</res> -- the used package's own call "+
			"must reach the overriding declaration", got)
	}
}
