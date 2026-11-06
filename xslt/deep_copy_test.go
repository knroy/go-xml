package xslt

import (
	"context"
	"io"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// deepDoc is d nested elements around a leaf; flatDoc is d sibling elements
// under one: the same nodes, one deep and one shallow.
func deepDoc(d int) string { return strings.Repeat("<a>", d) + "x" + strings.Repeat("</a>", d) }
func flatDoc(d int) string { return "<r>" + strings.Repeat("<a>x</a>", d-1) + "</r>" }

// streamedRun is outRun through TransformReader, failing unless the run was
// streamed with no fallback.
func streamedRun(t *testing.T, sheet *Stylesheet, doc string, opts TransformOptions) string {
	t.Helper()
	before := streamtrace.Snapshot()
	out, err := outRun(sheet, doc, true, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c := streamtrace.Snapshot().Sub(before); c.Streamed() == 0 || c.Fallbacks != 0 {
		t.Errorf("not streamed: %+v", c)
	}
	return out
}

// TestDeepCopyOfIsLinear: copy-of of a deep document did work per node in
// proportion to its depth, in both modes -- the tree's copy asked each node
// for its base URI, a walk up to the document, and the stream's asked each
// element for its in-scope namespaces, a walk up its ancestors -- so 200 000
// levels did not finish in two minutes. Each now passes its parent's down.
// The deep copy must cost about what a flat one of as many nodes does
// (a quadratic copy is thousands of times slower at this depth), and agree
// across the modes.
func TestDeepCopyOfIsLinear(t *testing.T) {
	const d = 100000
	sheet := outSheet(t, `<xsl:mode streamable="yes"/>`, `<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>`)
	var opts TransformOptions
	opts.SourceParseOptions.MaxDepth = 2 * d
	opts.MaxNodes, opts.MaxBytes = -1, -1
	run := func(doc string, main bool) (string, time.Duration) {
		t0 := time.Now()
		if main {
			return streamedRun(t, sheet, doc, opts), time.Since(t0)
		}
		out, err := outRun(sheet, doc, main, false, opts)
		if err != nil {
			t.Fatal(err)
		}
		return out, time.Since(t0)
	}
	var outs [2]string
	for i, main := range []bool{false, true} {
		_, flat := run(flatDoc(d), main)
		var deep time.Duration
		outs[i], deep = run(deepDoc(d), main)
		if deep > 20*flat && deep > 2*time.Second {
			t.Errorf("main %v: copy-of at depth %d took %v, a flat one of as many nodes %v", main, d, deep, flat)
		}
	}
	if outs[0] != outs[1] {
		t.Errorf("streamed copy differs from the tree's (%d vs %d bytes)", len(outs[1]), len(outs[0]))
	}
}

// TestDeepCopyOfScopes: the streamed copy works each element's in-scope
// namespaces out from its parent's and its own declarations; redeclared,
// undeclared and shadowed bindings at every level must come out as the
// tree's copy writes them.
func TestDeepCopyOfScopes(t *testing.T) {
	doc := `<a xmlns="urn:d" xmlns:p="urn:p"><b xmlns:q="urn:q"><p:c xmlns:p="urn:p2"><d xmlns=""><e xmlns:q="urn:q" xmlns:r="urn:r"><f/></e></d></p:c><g xmlns="urn:d"/></b><h/></a>`
	for _, body := range []string{
		`<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>`,
		`<xsl:template match="/"><xsl:copy-of select="*/*"/></xsl:template>`,
		`<xsl:template match="*:c"><xsl:copy-of select="."/></xsl:template>`,
	} {
		sheet := outSheet(t, `<xsl:mode streamable="yes" on-no-match="shallow-skip"/>`, body)
		want, err := outRun(sheet, doc, false, false, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := streamedRun(t, sheet, doc, TransformOptions{}); got != want {
			t.Errorf("%s:\n got %s\nwant %s", body, got, want)
		}
	}
}

// TestDeepStreamHoldsNoMoreThanTree: a streamed run kept, per open element,
// besides the parse's record, a mark, a gen and two ordinals in the parse,
// and an entry of the select's walk (112 bytes for count(//a)): a document a
// million deep held 2.7 times the tree. Those are runs now, one for a whole
// stretch of evenly nested elements. What the streamed run allocates must
// stay within a tenth of what the tree's parse and run allocate: both are
// then the parse's records, give or take what the process has cached (it was
// five times the tree's for string(.), three for count(//a)).
func TestDeepStreamHoldsNoMoreThanTree(t *testing.T) {
	const d = 200000
	var opts TransformOptions
	opts.SourceParseOptions.MaxDepth = 2 * d
	opts.MaxNodes, opts.MaxBytes = -1, -1
	doc := deepDoc(d)
	for _, body := range []string{
		`<xsl:template match="/"><xsl:value-of select="string(.)"/></xsl:template>`,
		`<xsl:template match="/"><xsl:value-of select="count(//a)"/></xsl:template>`,
	} {
		sheet := outSheet(t, `<xsl:mode streamable="yes"/>`, body)
		alloc := func(main bool) (string, uint64) {
			var m goruntime.MemStats
			goruntime.ReadMemStats(&m)
			before := m.TotalAlloc
			var out string
			if main {
				out = streamedRun(t, sheet, doc, opts)
			} else {
				var err error
				if out, err = outRun(sheet, doc, false, false, opts); err != nil {
					t.Fatal(err)
				}
			}
			goruntime.ReadMemStats(&m)
			return out, m.TotalAlloc - before
		}
		want, tree := alloc(false)
		got, stream := alloc(true)
		t.Logf("%s: stream %d KB, tree %d KB", body, stream>>10, tree>>10)
		if got != want {
			t.Errorf("%s: streamed %q, tree %q", body, got, want)
		}
		if stream > tree+tree/10 {
			t.Errorf("%s: the streamed run allocated %d KB at depth %d, the tree's %d KB", body, stream>>10, d, tree>>10)
		}
	}
}

// deepNSDoc is d nested elements, the outermost declaring a default
// namespace and two prefixes when ns; flatNSDoc is as many nodes, shallow.
func deepNSDoc(d int, ns bool) string {
	if !ns {
		return deepDoc(d)
	}
	return `<a xmlns="urn:d" xmlns:p="urn:p" xmlns:q="urn:q">` + strings.Repeat("<a>", d-1) + "x" + strings.Repeat("</a>", d)
}

func flatNSDoc(d int, ns bool) string {
	if !ns {
		return flatDoc(d)
	}
	return `<r xmlns="urn:d" xmlns:p="urn:p" xmlns:q="urn:q">` + strings.Repeat("<a>x</a>", d-1) + "</r>"
}

// TestDeepShallowCopyIsLinear: xsl:copy and the shallow-copy built-in rule
// asked each copied element for its in-scope namespaces, a walk up its
// ancestors, and asked of each binding whether the result parent supplies
// it, a walk up the result whose every step could walk up again for the
// default namespace: an identity transform 50 000 deep took 7 s, and 5 000
// deep under a default namespace 99 s. Each element now reads its parent's
// answers (nsscope.go). A deep copy must cost about what a flat one of as
// many nodes does, in both modes.
func TestDeepShallowCopyIsLinear(t *testing.T) {
	sheets := map[string]string{
		"built-in": `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`,
		"xsl:copy": `<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>`,
	}
	for name, decls := range sheets {
		sheet := outSheet(t, decls, ``)
		for _, c := range []struct {
			d  int
			ns bool
		}{{50000, false}, {3000, true}} {
			var opts TransformOptions
			opts.MaxDepth = 2 * c.d
			opts.SourceParseOptions.MaxDepth = 2 * c.d
			opts.MaxNodes, opts.MaxBytes = -1, -1
			run := func(doc string, main bool) (string, time.Duration) {
				t0 := time.Now()
				out, err := outRun(sheet, doc, main, false, opts)
				if err != nil {
					t.Fatalf("%s main %v: %v", name, main, err)
				}
				return out, time.Since(t0)
			}
			var outs [2]string
			for i, main := range []bool{false, true} {
				_, flat := run(flatNSDoc(c.d, c.ns), main)
				var deep time.Duration
				outs[i], deep = run(deepNSDoc(c.d, c.ns), main)
				if deep > 20*flat && deep > time.Second {
					t.Errorf("%s main %v ns %v: a copy %d deep took %v, a flat one of as many nodes %v", name, main, c.ns, c.d, deep, flat)
				}
			}
			if outs[0] != outs[1] {
				t.Errorf("%s ns %v: the streamed copy differs from the tree's", name, c.ns)
			}
		}
	}
}

// TestShallowCopyNamespaceRows: what the copies' namespaces are, which the
// frames of nsscope.go must leave as they were: redeclared prefixes, the
// default namespace undeclared and redeclared, copy-namespaces="no",
// inherit-namespaces="no", and literal result elements rebinding a prefix
// between the copies. The rows were taken before the frames were added;
// they must hold with a frame for every element as with none. The copy of
// an element in no namespace (xmlns="" in the source) had the default
// namespace it inherited on its namespace axis, in both modes; it has none
// now (XDM 3.1 §6.2; XSLT 3.0 §5.7.3), as Saxon-HE 12.10 and 10.6 answer.
func TestShallowCopyNamespaceRows(t *testing.T) {
	defer func(d int) { nsFrameDepth = d }(nsFrameDepth)
	for _, nsFrameDepth = range []int{0, 1000} {
		shallowCopyNamespaceRows(t)
	}
}

func shallowCopyNamespaceRows(t *testing.T) {
	const doc = `<a xmlns="urn:d" xmlns:p="urn:p"><b xmlns:p="urn:p2"><p:c><d xmlns=""><e xmlns="urn:e"><p:f xmlns:q="urn:q"><g/></p:f></e></d></p:c><h/></b><p:i/></a>`
	const decl = `<?xml version="1.0" encoding="UTF-8"?>`
	rows := []struct{ body, want string }{
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, "<a xmlns=\"urn:d\" xmlns:p=\"urn:p\"><b xmlns:p=\"urn:p2\"><p:c><d xmlns=\"\"><e xmlns=\"urn:e\"><p:f xmlns:q=\"urn:q\"><g/></p:f></e></d></p:c><h/></b><p:i/></a>"},
		{`<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>`, "<a xmlns=\"urn:d\" xmlns:p=\"urn:p\"><b xmlns:p=\"urn:p2\"><p:c><d xmlns=\"\"><e xmlns=\"urn:e\"><p:f xmlns:q=\"urn:q\"><g/></p:f></e></d></p:c><h/></b><p:i/></a>"},
		{`<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:copy copy-namespaces="no"><xsl:apply-templates/></xsl:copy></xsl:template>`, "<a xmlns=\"urn:d\"><b><p:c xmlns:p=\"urn:p2\"><d xmlns=\"\"><e xmlns=\"urn:e\"><p:f><g/></p:f></e></d></p:c><h/></b><p:i xmlns:p=\"urn:p\"/></a>"},
		{`<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:copy inherit-namespaces="no"><xsl:apply-templates/></xsl:copy></xsl:template>`, "<a xmlns=\"urn:d\" xmlns:p=\"urn:p\"><b xmlns:p=\"urn:p2\"><p:c><d xmlns=\"\"><e xmlns=\"urn:e\"><p:f xmlns:q=\"urn:q\"><g/></p:f></e></d></p:c><h/></b><p:i/></a>"},
		{`<xsl:template match="*"><xsl:copy><w xmlns:p="urn:w"><xsl:apply-templates/></w></xsl:copy></xsl:template>`, "<a xmlns=\"urn:d\" xmlns:p=\"urn:p\"><w xmlns:p=\"urn:w\" xmlns=\"\"><b xmlns=\"urn:d\" xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\" xmlns=\"\"><p:c xmlns=\"urn:d\" xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\" xmlns=\"\"><d xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\"><e xmlns=\"urn:e\" xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\" xmlns=\"\"><p:f xmlns=\"urn:e\" xmlns:p=\"urn:p2\" xmlns:q=\"urn:q\"><w xmlns:p=\"urn:w\" xmlns=\"\"><g xmlns=\"urn:e\" xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\" xmlns=\"\"/></g></w></p:f></w></e></w></d></w></p:c><h xmlns=\"urn:d\" xmlns:p=\"urn:p2\"><w xmlns:p=\"urn:w\" xmlns=\"\"/></h></w></b><p:i xmlns=\"urn:d\" xmlns:p=\"urn:p\"><w xmlns:p=\"urn:w\" xmlns=\"\"/></p:i></w></a>"},
		{`<xsl:template match="*"><xsl:copy copy-namespaces="no"><p:w xmlns:p="urn:w"><xsl:apply-templates/></p:w></xsl:copy></xsl:template>`, "<a xmlns=\"urn:d\"><p:w xmlns:p=\"urn:w\"><b><p:w><p:c xmlns:p=\"urn:p2\"><p:w xmlns:p=\"urn:w\"><d xmlns=\"\"><p:w><e xmlns=\"urn:e\"><p:w><p:f xmlns:p=\"urn:p2\"><p:w xmlns:p=\"urn:w\"><g><p:w/></g></p:w></p:f></p:w></e></p:w></d></p:w></p:c><h><p:w/></h></p:w></b><p:i xmlns:p=\"urn:p\"><p:w xmlns:p=\"urn:w\"/></p:i></p:w></a>"},
	}
	for _, r := range rows {
		sheet := outSheet(t, ``, r.body)
		for _, main := range []bool{false, true} {
			got, err := outRun(sheet, doc, main, false, TransformOptions{})
			if err != nil || got != decl+r.want {
				t.Errorf("%s (main %v, frames from %d):\n got %s %v\nwant %s", r.body, main, nsFrameDepth, got, err, decl+r.want)
			}
		}
	}
	// The namespace axis of the copies, in a temporary tree.
	axis := []struct{ attr, want string }{
		{``, "a:=urn:d,p=urn:p,;b:=urn:d,p=urn:p2,;p:c:=urn:d,p=urn:p2,;d:p=urn:p2,;e:=urn:e,p=urn:p2,;p:f:=urn:e,p=urn:p2,q=urn:q,;g:=urn:e,p=urn:p2,q=urn:q,;h:=urn:d,p=urn:p2,;p:i:=urn:d,p=urn:p,;"},
		{` copy-namespaces="no"`, "a:=urn:d,;b:=urn:d,;p:c:=urn:d,p=urn:p2,;d:p=urn:p2,;e:=urn:e,p=urn:p2,;p:f:=urn:e,p=urn:p2,;g:=urn:e,p=urn:p2,;h:=urn:d,;p:i:=urn:d,p=urn:p,;"},
		{` inherit-namespaces="no"`, "a:=urn:d,p=urn:p,;b:=urn:d,p=urn:p2,;p:c:=urn:d,p=urn:p2,;d:p=urn:p2,;e:=urn:e,p=urn:p2,;p:f:=urn:e,p=urn:p2,q=urn:q,;g:=urn:e,p=urn:p2,q=urn:q,;h:=urn:d,p=urn:p2,;p:i:=urn:d,p=urn:p,;"},
	}
	for _, r := range axis {
		sheet := outSheet(t, `<xsl:output method="text"/><xsl:mode streamable="yes"/><xsl:mode name="c" streamable="yes"/>`, `<xsl:template match="*" mode="c"><xsl:copy`+r.attr+`><xsl:apply-templates mode="c"/></xsl:copy></xsl:template>
<xsl:template match="/">`+nsAxisOf(`<xsl:apply-templates mode="c"/>`)+`</xsl:template>`)
		nsAxisRow(t, sheet, doc, "axis"+r.attr, r.want)
	}
	// A literal result element in no namespace, made in a copy whose own
	// default comes from its fixup, still to run: the default it will
	// inherit is undeclared (Saxon-HE 12.10 and 10.6 give these rows).
	sheet := outSheet(t, `<xsl:output method="text"/><xsl:mode streamable="yes"/>`, `<xsl:template match="/">`+
		nsAxisOf(`<xsl:for-each select="*"><xsl:copy copy-namespaces="no"><w/></xsl:copy></xsl:for-each>`)+`|<xsl:value-of select="serialize($v)"/></xsl:template>`)
	nsAxisRow(t, sheet, `<a xmlns="urn:d"><d xmlns=""><g/></d></a>`, "pending fixup", `a:=urn:d,;w:;|<a xmlns="urn:d"><w xmlns=""/></a>`)
}

// nsAxisOf is a template body that builds the variable v from body and lists
// each element in it with its in-scope namespaces.
func nsAxisOf(body string) string {
	return `<xsl:variable name="v">` + body + `</xsl:variable><xsl:for-each select="$v//*"><xsl:variable name="e" select="."/><xsl:value-of select="name()"/>:<xsl:for-each select="sort(in-scope-prefixes(.)[. ne 'xml'])"><xsl:value-of select=".||'='||namespace-uri-for-prefix(., $e)"/>,</xsl:for-each>;</xsl:for-each>`
}

// nsAxisRow runs sheet over doc in both modes, the stream's streamed.
func nsAxisRow(t *testing.T, sheet *Stylesheet, doc, name, want string) {
	t.Helper()
	if got, err := outRun(sheet, doc, false, false, TransformOptions{}); err != nil || got != want {
		t.Errorf("%s (frames from %d):\n got %s %v\nwant %s", name, nsFrameDepth, got, err, want)
	}
	if got := streamedRun(t, sheet, doc, TransformOptions{}); got != want {
		t.Errorf("%s streamed (frames from %d):\n got %s\nwant %s", name, nsFrameDepth, got, want)
	}
}

// deepReader reads deepDoc(d) without holding it, and calls mid once the
// parse has asked for the first end tag: every element is then open.
type deepReader struct {
	d, i int
	mid  func()
	buf  []byte
}

func (r *deepReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		switch {
		case r.i < r.d:
			n := min(r.d-r.i, 4096)
			r.buf, r.i = []byte(strings.Repeat("<a>", n)), r.i+n
		case r.i == r.d:
			r.buf, r.i = []byte("x"), r.i+1
		case r.i < 2*r.d+1:
			if r.i == r.d+1 && r.mid != nil {
				r.mid()
			}
			n := min(2*r.d+1-r.i, 4096)
			r.buf, r.i = []byte(strings.Repeat("</a>", n)), r.i+n
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func liveHeap() uint64 {
	var m goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestDeepCopyOfHoldsNoMoreThanTree: a streamed copy-of kept, per open
// element, the builder of its copy (one per level, each linked to its
// parent's) and a view of it (a copy of the element in the view batch,
// with its path, gen and record count): a million deep held 361 MB against
// the tree's 81. The copy is now written through one builder, detached from
// its parent's (xdmsink.Detach, Ascend), off the parse's own nodes, and the
// tree's open path is runs (ordStack). What the stream holds with every
// element open must stay within a fiftieth of what the tree holds once its
// copy is made (the run's fixed costs, the cursor's buffers among them).
func TestDeepCopyOfHoldsNoMoreThanTree(t *testing.T) {
	const d = 200000
	sheet := outSheet(t, `<xsl:mode streamable="yes"/>`, `<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>`)
	var opts TransformOptions
	opts.SourceParseOptions.MaxDepth = 2 * d
	opts.MaxNodes, opts.MaxBytes = -1, -1
	base := liveHeap()
	src, err := xdm.Parse(&deepReader{d: d}, opts.SourceParseOptions)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), src.Root, opts)
	if err != nil {
		t.Fatal(err)
	}
	tree := liveHeap() - base
	want := res.String()
	goruntime.KeepAlive(src)
	src, res = nil, nil
	base = liveHeap()
	var stream uint64
	res, err = sheet.TransformReader(context.Background(), &deepReader{d: d, mid: func() { stream = liveHeap() - base }}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.String() != want {
		t.Fatal("the streamed copy differs from the tree's")
	}
	t.Logf("stream %d KB with every element open, tree %d KB", stream>>10, tree>>10)
	if stream > tree+tree/50 {
		t.Errorf("the streamed copy-of held %d KB at depth %d, the tree %d KB", stream>>10, d, tree>>10)
	}
}

// heapEnv is an environment resolver that reads the live heap whenever
// environment-variable is called: a probe a stylesheet can run at the
// point it wants measured.
type heapEnv struct{ base, peak uint64 }

func (e *heapEnv) LookupEnvironment(string) (string, bool) {
	e.peak = max(e.peak, liveHeap()-e.base)
	return "", false
}
func (e *heapEnv) EnvironmentNames() []string { return nil }

// TestDeepIdentityHoldsNoMoreThanTree: the streamed identity kept, per open
// element, the filter it read the element's children with (a run's cache,
// one per depth); the cursor copies the caller's filter now. Measured by a
// rule for the leaf that reads the heap, with every element open, the
// stream must hold no more than the tree does there, within a fiftieth,
// but for the leaf's view: a node a rule is given may be kept, so it comes
// with copies of its ancestors, a record (40 bytes) each.
func TestDeepIdentityHoldsNoMoreThanTree(t *testing.T) {
	const d = 100000
	sheet := outSheet(t, `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`,
		`<xsl:template match="text()"><xsl:value-of select="environment-variable('m')"/><xsl:value-of select="."/></xsl:template>`)
	run := func(main bool) (string, uint64) {
		env := &heapEnv{}
		var opts TransformOptions
		opts.MaxDepth = 2 * d
		opts.SourceParseOptions.MaxDepth = 2 * d
		opts.MaxNodes, opts.MaxBytes = -1, -1
		opts.Environment = env
		env.base = liveHeap()
		var res *Result
		var err error
		if main {
			res, err = sheet.TransformReader(context.Background(), &deepReader{d: d}, opts)
		} else {
			var src *xdm.Tree
			if src, err = xdm.Parse(&deepReader{d: d}, opts.SourceParseOptions); err == nil {
				res, err = sheet.Transform(context.Background(), src.Root, opts)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		return res.String(), env.peak
	}
	want, tree := run(false)
	got, stream := run(true)
	t.Logf("at the leaf: stream %d KB, tree %d KB", stream>>10, tree>>10)
	if got != want {
		t.Fatal("the streamed identity differs from the tree's")
	}
	if stream > tree+tree/50+40*d {
		t.Errorf("the streamed identity held %d KB at depth %d, the tree %d KB", stream>>10, d, tree>>10)
	}
}
