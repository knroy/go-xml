package xdm

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
)

// The entity, external-entity and XInclude limits are ParseOptions and
// XIncludeOptions fields. Each is pinned here four ways: the zero value keeps
// the old constant exactly, a raised value admits what the default refuses, a
// lowered one refuses what the default admits, and a negative value means what
// the field says (off for a budget, the default for a recursion bound, which
// also clamps at its ceiling).

// entityOf is a document whose one entity expands to n bytes (n even). The
// per-entity cap bounds an expansion, so e is made of references to f.
func entityOf(n int) string {
	return `<!DOCTYPE d [<!ENTITY f "` + strings.Repeat("a", n/2) + `"><!ENTITY e "&f;&f;">]><d>&e;</d>`
}

// parseLim parses src with a DOCTYPE allowed and the limits set by set.
func parseLim(src string, set func(*ParseOptions)) error {
	o := ParseOptions{AllowDOCTYPE: true}
	if set != nil {
		set(&o)
	}
	_, err := ParseString(src, o)
	return err
}

// wantLimit fails unless err is a resource-limit refusal.
func wantLimit(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrResourceLimit) {
		t.Errorf("%s: err = %v, want a resource-limit refusal", what, err)
	}
}

// wantOK fails on err.
func wantOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: refused: %v", what, err)
	}
}

func TestMaxEntityBytesOption(t *testing.T) {
	wantOK(t, "default, at 64 KiB", parseLim(entityOf(maxEntityBytes), nil))
	wantLimit(t, "default, over 64 KiB", parseLim(entityOf(maxEntityBytes+2), nil))
	wantOK(t, "raised", parseLim(entityOf(maxEntityBytes+2), func(o *ParseOptions) { o.MaxEntityBytes = 128 << 10 }))
	wantLimit(t, "lowered", parseLim(entityOf(2048), func(o *ParseOptions) { o.MaxEntityBytes = 1024 }))
	wantOK(t, "negative is off", parseLim(entityOf(maxEntityBytes+2), func(o *ParseOptions) { o.MaxEntityBytes = -1 }))
}

// totalOf is a document referencing a 60 KB entity n times.
func totalOf(n int) string {
	return `<!DOCTYPE d [<!ENTITY e "` + strings.Repeat("a", 60<<10) + `">]><d>` +
		strings.Repeat("&e;", n) + `</d>`
}

func TestMaxTotalEntityBytesOption(t *testing.T) {
	wantOK(t, "default, under 1 MiB", parseLim(totalOf(16), nil))
	wantLimit(t, "default, over 1 MiB", parseLim(totalOf(20), nil))
	wantOK(t, "raised", parseLim(totalOf(20), func(o *ParseOptions) { o.MaxTotalEntityBytes = 2 << 20 }))
	wantLimit(t, "lowered", parseLim(totalOf(2), func(o *ParseOptions) { o.MaxTotalEntityBytes = 100 << 10 }))
	wantOK(t, "negative is off", parseLim(totalOf(20), func(o *ParseOptions) { o.MaxTotalEntityBytes = -1 }))
}

// chainOf is a document whose entities nest n deep: e0 refers to e1, and so
// on to e(n-1), which is text.
func chainOf(n int) string {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE d [")
	for i := 0; i < n-1; i++ {
		fmt.Fprintf(&sb, `<!ENTITY e%d "&e%d;">`, i, i+1)
	}
	fmt.Fprintf(&sb, `<!ENTITY e%d "x">]><d>&e0;</d>`, n-1)
	return sb.String()
}

func TestMaxEntityDepthOption(t *testing.T) {
	wantOK(t, "default, 100 deep", parseLim(chainOf(maxEntityDepth), nil))
	wantLimit(t, "default, 102 deep", parseLim(chainOf(maxEntityDepth+2), nil))
	wantOK(t, "raised", parseLim(chainOf(maxEntityDepth+2), func(o *ParseOptions) { o.MaxEntityDepth = 200 }))
	wantLimit(t, "lowered", parseLim(chainOf(12), func(o *ParseOptions) { o.MaxEntityDepth = 10 }))
	wantLimit(t, "negative is the default", parseLim(chainOf(maxEntityDepth+2), func(o *ParseOptions) { o.MaxEntityDepth = -1 }))
}

// The ceiling is a clamp, and it is a stack bound: a chain of 1,000 levels
// parses under a 2 MB stack, so a level costs under 2 KB and the ceiling's
// 10,000 under 20 MB, against Go's 1 GB maximum. The full ceiling is not run:
// a chain's expansion is quadratic in its depth (each entity expands its
// whole tail), which is the time cost of raising MaxEntityDepth.
func TestEntityDepthCeilingStack(t *testing.T) {
	if got := (ParseOptions{MaxEntityDepth: math.MaxInt}).entityLimits().depth; got != entityDepthCeiling {
		t.Errorf("MaxEntityDepth MaxInt resolved to %d, want the ceiling %d", got, entityDepthCeiling)
	}
	defer debug.SetMaxStack(debug.SetMaxStack(2 << 20))
	set := func(o *ParseOptions) { o.MaxEntityDepth = 1000 }
	wantOK(t, "1,000 deep", parseLim(chainOf(1000), set))
	wantLimit(t, "1,002 deep", parseLim(chainOf(1002), set))
}

func TestMaxEntitiesOption(t *testing.T) {
	refs := func(n int) string {
		return `<!DOCTYPE d [<!ENTITY m "<b/>">]><d>` + strings.Repeat("&m;", n) + `</d>`
	}
	wantOK(t, "default, at 10,000", parseLim(refs(maxEntityCount), nil))
	wantLimit(t, "default, over 10,000", parseLim(refs(maxEntityCount+1), nil))
	wantOK(t, "raised", parseLim(refs(maxEntityCount+1), func(o *ParseOptions) { o.MaxEntities = 20000 }))
	wantLimit(t, "lowered", parseLim(refs(11), func(o *ParseOptions) { o.MaxEntities = 10 }))
	wantOK(t, "negative is off", parseLim(refs(maxEntityCount+1), func(o *ParseOptions) { o.MaxEntities = -1 }))
}

// The declaration count a parse used is the one Tree.UnparsedEntity reads the
// declarations back under, so the two agree on which entities exist.
func TestMaxEntitiesReachesUnparsedEntity(t *testing.T) {
	src := `<!DOCTYPE d [<!NOTATION n SYSTEM "n"><!ENTITY a "x"><!ENTITY u SYSTEM "u" NDATA n>]><d/>`
	for _, c := range []struct {
		max  int
		want bool
	}{{0, true}, {1, false}, {-1, true}} {
		tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true, MaxEntities: c.max})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := tree.UnparsedEntity("u"); ok != c.want {
			t.Errorf("MaxEntities %d: UnparsedEntity(u) = %v, want %v", c.max, ok, c.want)
		}
	}
}

// parseExternalWith is parseExternalErr with the limits set by set.
func parseExternalWith(t *testing.T, dir, name string, set func(*ParseOptions)) error {
	t.Helper()
	p := filepath.Join(dir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	o := ParseOptions{AllowDOCTYPE: true, ExternalEntities: &dirResolver{root: dir}, BaseURI: fileuri.Of(p)}
	if set != nil {
		set(&o)
	}
	_, err = ParseString(string(src), o)
	return err
}

func TestMaxExternalFetchesOption(t *testing.T) {
	files := map[string]string{}
	var decls, refs strings.Builder
	for i := 0; i < maxExternalFetches+1; i++ {
		files[fmt.Sprintf("f%d.ent", i)] = "x"
		fmt.Fprintf(&decls, "<!ENTITY e%d SYSTEM \"f%d.ent\">\n", i, i)
		fmt.Fprintf(&refs, "&e%d;", i)
	}
	files["doc.xml"] = "<!DOCTYPE r [\n" + decls.String() + "]>\n<r>" + refs.String() + "</r>"
	dir := writeFiles(t, files)
	wantLimit(t, "default, 65 fetches", parseExternalWith(t, dir, "doc.xml", nil))
	wantOK(t, "raised", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { o.MaxExternalFetches = 100 }))
	wantLimit(t, "lowered", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { o.MaxExternalFetches = 3 }))
	wantOK(t, "negative is off", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { o.MaxExternalFetches = -1 }))
}

func TestMaxExternalFetchBytesOption(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"big.ent": strings.Repeat("A", maxExternalFetchBytes+1),
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "big.ent"> ]><r>&e;</r>`,
	})
	big := func(o *ParseOptions) { o.MaxTotalEntityBytes = 2 << 20 }
	wantLimit(t, "default, 512 KiB + 1", parseExternalWith(t, dir, "doc.xml", big))
	wantOK(t, "raised", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { big(o); o.MaxExternalFetchBytes = 1 << 20 }))
	wantLimit(t, "lowered", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { o.MaxExternalFetchBytes = 1024 }))
	wantOK(t, "negative is off", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) { big(o); o.MaxExternalFetchBytes = -1 }))
	// Both byte limits off is an unbounded read, not a bound of -1 (the
	// LimitReader overflow).
	wantOK(t, "both off", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) {
		o.MaxTotalEntityBytes, o.MaxExternalFetchBytes, o.MaxEntityBytes = -1, -1, -1
	}))
}

func TestMaxExternalDepthOption(t *testing.T) {
	// n modules, each declaring and referencing the next.
	chain := func(n int) string {
		files := map[string]string{}
		for i := 0; i < n; i++ {
			files[fmt.Sprintf("p%d.ent", i)] = fmt.Sprintf(`<!ENTITY %% p%d SYSTEM "p%d.ent">%%p%d;`, i+1, i+1, i+1)
		}
		files[fmt.Sprintf("p%d.ent", n)] = `<!ENTITY final "x">`
		files["doc.xml"] = `<!DOCTYPE r [<!ENTITY % p0 SYSTEM "p0.ent">%p0;]><r>&final;</r>`
		return writeFiles(t, files)
	}
	deep := chain(maxExternalDepth + 2)
	wantOK(t, "default, within", parseExternalWith(t, chain(maxExternalDepth-2), "doc.xml", nil))
	wantLimit(t, "default, past", parseExternalWith(t, deep, "doc.xml", nil))
	wantOK(t, "raised", parseExternalWith(t, deep, "doc.xml", func(o *ParseOptions) { o.MaxExternalDepth = 20 }))
	wantLimit(t, "lowered", parseExternalWith(t, chain(4), "doc.xml", func(o *ParseOptions) { o.MaxExternalDepth = 2 }))
	wantLimit(t, "negative is the default", parseExternalWith(t, deep, "doc.xml", func(o *ParseOptions) { o.MaxExternalDepth = -1 }))
	if got := (ParseOptions{MaxExternalDepth: math.MaxInt}).entityLimits().extDepth; got != externalDepthCeiling {
		t.Errorf("MaxExternalDepth MaxInt resolved to %d, want the ceiling %d", got, externalDepthCeiling)
	}
}

// includeChain is a main document including a chain of n distinct files.
func includeChain(n int) (string, map[string]string) {
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("mem:///d/g%d.xml", i)] = fmt.Sprintf(`<g`+xiNS+`><xi:include href="g%d.xml"/></g>`, i+1)
	}
	files[fmt.Sprintf("mem:///d/g%d.xml", n)] = `<leaf/>`
	return `<root` + xiNS + `><xi:include href="g0.xml"/></root>`, files
}

// includeWith runs one XInclude pass over src with opts.
func includeWith(t *testing.T, src string, files map[string]string, opts XIncludeOptions) error {
	t.Helper()
	opts.Resolver = &mapResolver{files: files}
	_, err := ProcessXInclude(parseWithBase(t, "mem:///d/main.xml", src), opts)
	return err
}

func TestXIncludeMaxDepthOption(t *testing.T) {
	src, files := includeChain(maxIncludeDepth + 1)
	wantLimit(t, "default", includeWith(t, src, files, XIncludeOptions{}))
	wantOK(t, "raised", includeWith(t, src, files, XIncludeOptions{MaxDepth: 50}))
	wantLimit(t, "negative is the default", includeWith(t, src, files, XIncludeOptions{MaxDepth: -1}))
	short, sfiles := includeChain(5)
	wantLimit(t, "lowered", includeWith(t, short, sfiles, XIncludeOptions{MaxDepth: 3}))
	long, lfiles := includeChain(includeDepthCeiling + 1)
	wantLimit(t, "clamped at the ceiling", includeWith(t, long, lfiles, XIncludeOptions{MaxDepth: math.MaxInt, MaxFetches: -1}))
}

func TestXIncludeMaxFetchesOption(t *testing.T) {
	files := map[string]string{"mem:///d/x.xml": `<x/>`}
	src := `<root` + xiNS + `>` + strings.Repeat(`<xi:include href="x.xml"/>`, maxIncludeFetches+1) + `</root>`
	wantLimit(t, "default", includeWith(t, src, files, XIncludeOptions{}))
	wantOK(t, "raised", includeWith(t, src, files, XIncludeOptions{MaxFetches: 300}))
	wantLimit(t, "lowered", includeWith(t, src, files, XIncludeOptions{MaxFetches: 5}))
	wantOK(t, "negative is off", includeWith(t, src, files, XIncludeOptions{MaxFetches: -1}))
}

// An included document is parsed with XIncludeOptions.Parse, so the entity
// limits set there bind it: the included entity of 2 KB is refused at 1 KB.
func TestXIncludePropagatesEntityLimits(t *testing.T) {
	files := map[string]string{"mem:///d/e.xml": entityOf(2048)}
	src := `<root` + xiNS + `><xi:include href="e.xml"/></root>`
	wantOK(t, "default", includeWith(t, src, files, XIncludeOptions{Parse: ParseOptions{AllowDOCTYPE: true}}))
	wantLimit(t, "lowered in Parse", includeWith(t, src, files,
		XIncludeOptions{Parse: ParseOptions{AllowDOCTYPE: true, MaxEntityBytes: 1024}}))
}

// The largest value each field can hold is the most permissive setting, never
// a refusal (the MaxBytes overflow of limits_boundary_test.go, in these
// fields): a document using every limit at once parses with all of them at
// their type's maximum, and with all of them at one.
func TestEntityLimitsLargestValues(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"x.ent":   "xx",
		"doc.xml": `<!DOCTYPE r [<!ENTITY x SYSTEM "x.ent"><!ENTITY f "ab"><!ENTITY e "&f;&f;">]><r>&e;&x;</r>`,
	})
	wantOK(t, "all at their maximum", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) {
		o.MaxEntityDepth, o.MaxEntities, o.MaxExternalFetches, o.MaxExternalDepth = math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt
		o.MaxEntityBytes, o.MaxTotalEntityBytes, o.MaxExternalFetchBytes = math.MaxInt64, math.MaxInt64, math.MaxInt64
	}))
	wantLimit(t, "all at one", parseExternalWith(t, dir, "doc.xml", func(o *ParseOptions) {
		o.MaxEntityDepth, o.MaxEntities, o.MaxExternalFetches, o.MaxExternalDepth = 1, 1, 1, 1
		o.MaxEntityBytes, o.MaxTotalEntityBytes, o.MaxExternalFetchBytes = 1, 1, 1
	}))
}
