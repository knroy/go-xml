package xslt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The entity-expansion budget must span every module ONE COMPILATION resolves,
// not restart for each of them.
//
// parseUncached built a fresh xdm.ParseOptions per file with no budget in it,
// so every module reached by xsl:import or xsl:include got the full 1 MB
// ceiling to itself. Sixty modules each expanding 700,000 bytes -- every one of
// them comfortably under the ceiling -- therefore expanded 42,000,000 bytes in
// total from 234 KB of source and allocated 173 MB, accepted. This is the same
// defect as XInclude's and fn:parse-xml's, one boundary further out; see
// docs/security.md.
//
// AllowDOCTYPE is off by default and the CLI does not turn it on, so this is
// hardening for a library caller that opts in rather than a default-config
// hole.

// entityModule is a stylesheet module whose one entity expands to
// entLen*refs bytes. On its own it stays under the ceiling; the point is what
// happens when many of them are imported together.
func entityModule(i, entLen, refs int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(`<!DOCTYPE xsl:stylesheet [<!ENTITY e "` +
		strings.Repeat("A", entLen) + `">]>` + "\n")
	sb.WriteString(`<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + "\n")
	fmt.Fprintf(&sb, `  <xsl:variable name="v%d">`, i)
	for j := 0; j < refs; j++ {
		sb.WriteString("&e;")
	}
	sb.WriteString("</xsl:variable>\n</xsl:stylesheet>\n")
	return sb.String()
}

// importingStylesheet writes n modules and a main stylesheet importing them
// all, and returns the resolver and the main module's parsed root.
func importingStylesheet(
	t *testing.T, n, entLen, refs int,
) (*FileResolver, *xdm.Node) {

	t.Helper()
	dir := t.TempDir()
	var imports strings.Builder
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("m%d.xsl", i)
		body := entityModule(i, entLen, refs)
		if err := os.WriteFile(
			filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&imports, "  <xsl:import href=%q/>\n", name)
	}
	main := `<?xml version="1.0"?>` + "\n" +
		`<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + "\n" +
		imports.String() +
		`  <xsl:template match="/"><out/></xsl:template>` + "\n" +
		`</xsl:stylesheet>` + "\n"
	mainPath := filepath.Join(dir, "main.xsl")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	res.AllowDOCTYPE = true

	uri := fileURIOf(mainPath)
	tree, err := xdm.ParseString(main, xdm.ParseOptions{
		BaseURI: uri, DocumentURI: uri,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, tree.Root
}

// Sixty imported modules, each under the ceiling on its own, must not expand
// past the ceiling in total.
func TestEntityBudgetSpansImportedModules(t *testing.T) {
	// 700 x 1000 = 700,000 bytes apiece, so two modules already cross the
	// 1 MB bound and sixty cross it forty-fold.
	res, root := importingStylesheet(t, 60, 700, 1000)

	_, err := Compile(root, CompileOptions{Resolver: res})
	if err == nil {
		t.Fatal("60 modules expanding 700,000 bytes each -- 42,000,000 bytes " +
			"in total -- were accepted; the budget restarts per module")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("refused, but not as a resource limit: %v", err)
	}
}

// CONTROL: a stylesheet whose modules expand a legitimate amount in total must
// still compile. A bound that refused this would break real stylesheets, which
// routinely import many modules -- so this is what stops the fix above from
// being "refuse everything".
func TestEntityBudgetAcceptsLegitimateModuleGraph(t *testing.T) {
	// 60 modules x 1000 bytes = 60,000 bytes expanded in total, well inside
	// the 1 MB ceiling, spread over the same number of modules as the case
	// above.
	res, root := importingStylesheet(t, 60, 100, 10)

	sheet, err := Compile(root, CompileOptions{Resolver: res})
	if err != nil {
		t.Fatalf("60 modules expanding 60,000 bytes in total were refused: %v", err)
	}
	src, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sheet.Transform(
		context.Background(), src.Root,
		TransformOptions{Documents: res}); err != nil {
		t.Fatalf("transforming after a legitimate module graph: %v", err)
	}
}

// A FileResolver is documented as shareable across transforms, and it caches
// parsed trees -- so an allowance held on the RESOLVER would be spent by
// unrelated runs and would eventually refuse everything. The allowance is
// scoped to one compilation, which this pins: the same resolver compiling the
// same modules again must succeed, because the second compilation mints its
// own.
func TestEntityBudgetDoesNotLeakBetweenCompilations(t *testing.T) {
	// Just over half the ceiling per compilation, so two compilations sharing
	// one allowance would cross it and two separate ones cannot.
	res, root := importingStylesheet(t, 1, 600, 1000)

	for i := 0; i < 4; i++ {
		if _, err := Compile(root, CompileOptions{Resolver: res}); err != nil {
			t.Fatalf("compilation %d through a shared resolver was refused: %v",
				i+1, err)
		}
	}
}
