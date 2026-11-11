package xslt

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
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

// guardedPackages is fixedPackages with a call cap, so a use-package cycle
// the compiler fails to refuse ends in an error rather than a stack overflow.
type guardedPackages struct {
	fixedPackages
	calls int
}

func (g *guardedPackages) ResolvePackage(name, versionMatch string) (*xdm.Node, error) {
	if g.calls++; g.calls > 50 {
		return nil, errNoSuchTestPackage
	}
	return g.fixedPackages.ResolvePackage(name, versionMatch)
}

// A package that depends on itself, directly or through another package, is
// XTSE3005 (3.5.2). Before, nothing tracked the packages on the path and the
// recursion through readUsePackage and reExportedComponents ran until a fatal
// stack overflow. A diamond -- two packages using one -- is not a cycle.
func TestUsePackageCycleIsXTSE3005(t *testing.T) {
	pkg := func(name string, uses ...string) string {
		s := `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" name="` + name +
			`" package-version="1.0" version="3.0">`
		for _, u := range uses {
			s += `<xsl:use-package name="` + u + `"/>`
		}
		return s + `</xsl:package>`
	}
	top := func(uses ...string) string {
		s := pkg("urn:top", uses...)
		return s[:len(s)-len(`</xsl:package>`)] +
			`<xsl:template name="xsl:initial-template" visibility="public"/></xsl:package>`
	}
	compile := func(src string, pkgs fixedPackages) (int, error) {
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		g := &guardedPackages{fixedPackages: pkgs}
		_, err = Compile(tree.Root, CompileOptions{PackageResolver: g})
		return g.calls, err
	}
	for name, pkgs := range map[string]fixedPackages{
		"self": {"urn:a": pkg("urn:a", "urn:a")},
		"pair": {"urn:a": pkg("urn:a", "urn:b"), "urn:b": pkg("urn:b", "urn:a")},
	} {
		calls, err := compile(top("urn:a"), pkgs)
		if err == nil || !strings.Contains(err.Error(), "XTSE3005") || calls > 50 {
			t.Errorf("%s: got %v after %d resolutions, want XTSE3005", name, err, calls)
		}
	}
	diamond := fixedPackages{
		"urn:b": pkg("urn:b", "urn:d"), "urn:c": pkg("urn:c", "urn:d"), "urn:d": pkg("urn:d"),
	}
	if _, err := compile(top("urn:b", "urn:c"), diamond); err != nil {
		t.Errorf("diamond: %v", err)
	}
}

// Whether a used package still references a private component is answered
// for every component from one scan of the package. A scan per component made
// C private functions of a 3C-node package cost C*3C, every scan allocating
// an attribute list per element: about 8 million allocations at C = 2,000.
// The answers stay the scan's: p:f0, which p:pub calls, is kept.
func TestUsedPackageReferencesAreScannedOnce(t *testing.T) {
	const c = 2000
	var b strings.Builder
	b.WriteString(`<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:base" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:function name="p:pub" visibility="public"><xsl:sequence select="p:f0()"/></xsl:function>`)
	for i := range c {
		fmt.Fprintf(&b, `<xsl:function name="p:f%d" visibility="private"><xsl:sequence select="%d"/></xsl:function>`, i, i)
	}
	b.WriteString(`</xsl:package>`)
	tree, err := xdm.ParseString(`<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:base" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:base" package-version="1.0.0"/>
		<xsl:template name="xsl:initial-template" visibility="public"><res>{p:pub()}</res></xsl:template>
	</xsl:package>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	s, err := Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": b.String()}})
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := after.Mallocs - before.Mallocs; got > 1_000_000 {
		t.Errorf("compiling the use of %d private functions made %d allocations; want under 1,000,000", c, got)
	}
	if out, err := runMain(s, ""); err != nil || !strings.Contains(out, ">0</res>") {
		t.Errorf("got %q, %v; want p:pub's call on the private p:f0 to work", out, err)
	}
}
