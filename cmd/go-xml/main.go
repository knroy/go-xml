// Command go-xml applies an XSLT 2.0 stylesheet to XML documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "go-xml:", err)
		os.Exit(1)
	}
}

// paramFlag collects repeated -p name=value flags.
type paramFlag map[string]string

func (p paramFlag) String() string { return "" }

func (p paramFlag) Set(v string) error {
	name, val, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("expected name=value, got %q", v)
	}
	p[name] = val
	return nil
}

func run() error {
	var (
		sheetPath = flag.String("xsl", "", "stylesheet to apply (required)")
		outPath   = flag.String("o", "", "write output to this file instead of stdout")
		allowDirs = flag.String("allow-dir", "",
			"comma-separated directories that xsl:include and document() may read; "+
				"empty disables both")
		allowDoctype = flag.Bool("allow-doctype", false,
			"permit a DOCTYPE in the source document (enables XXE and entity expansion)")
		timeout  = flag.Duration("timeout", 60*time.Second, "abort a transform after this long")
		initial  = flag.String("initial-template", "", "start at this named template")
		mode     = flag.String("mode", "", "initial mode for apply-templates")
		showMsgs = flag.Bool("messages", false, "print xsl:message output to stderr")
		tzMin    = flag.Int("timezone", 0,
			"implicit timezone in minutes east of UTC, for dates that carry none")
		nowStr = flag.String("now", "",
			"fix fn:current-dateTime to this xs:dateTime, making the run reproducible")
		trackPos = flag.Bool("track-positions", false,
			"record source line and column for each element, readable from a "+
				"stylesheet as gx:line-number() and gx:column-number() in the "+
				"namespace https://github.com/knroy/go-xml")

		resultDir = flag.String("result-dir", "",
			"write xsl:result-document outputs into this directory; without it a "+
				"stylesheet that produces secondary results is an error")

		keepGoing = flag.Bool("keep-going", false,
			"with several inputs, report failures and continue instead of stopping")
		params = paramFlag{}
	)
	flag.Var(params, "p", "stylesheet parameter, name=value (repeatable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: go-xml -xsl STYLESHEET [flags] INPUT.xml [INPUT.xml ...]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Security defaults: xsl:include, xsl:import, fn:doc and fn:document are all
disabled unless -allow-dir names the directories they may read, and a DOCTYPE
in the source document is rejected unless -allow-doctype is given.

Exit status: 0 if every input transformed, 1 otherwise.
`)
	}
	flag.Parse()

	if *sheetPath == "" {
		flag.Usage()
		return fmt.Errorf("-xsl is required")
	}
	inputs := flag.Args()
	if len(inputs) == 0 {
		return fmt.Errorf("no input documents given")
	}
	if len(inputs) > 1 && *outPath != "" {
		return fmt.Errorf("-o cannot be used with more than one input")
	}

	// The stylesheet's own directory is always readable, since a stylesheet
	// that includes a sibling module is the normal case; anything beyond that
	// must be named explicitly.
	//
	// Note what this grants: the directory is one root list shared by
	// xsl:include and by doc()/document(), so a stylesheet can also *read*
	// any file sitting beside it, not only include one. That is wider than
	// -allow-dir alone suggests, and it matters when the stylesheet lives in
	// a directory holding anything the caller would not hand over — put the
	// stylesheet somewhere of its own if that is a concern. Containment is
	// still enforced: nothing outside that directory is reachable without
	// -allow-dir, symlinks are resolved before the check, and the roots are
	// the only paths the resolver will open.
	var roots []string
	if dir := filepath.Dir(*sheetPath); dir != "" {
		roots = append(roots, dir)
	}
	if *allowDirs != "" {
		roots = append(roots, strings.Split(*allowDirs, ",")...)
	}
	resolver, err := xslt.NewFileResolver(roots...)
	if err != nil {
		return err
	}

	sheet, err := compileStylesheet(*sheetPath, resolver)
	if err != nil {
		return err
	}

	// A fixed clock makes a run reproducible, which is what a golden-file
	// comparison needs; without it fn:current-dateTime follows wall time.
	var now time.Time
	if *nowStr != "" {
		var err error
		if now, err = time.Parse(time.RFC3339, *nowStr); err != nil {
			return fmt.Errorf("-now %q: expected an xs:dateTime such as "+
				"2024-01-15T09:00:00Z: %w", *nowStr, err)
		}
	}

	// Parameters arrive as strings; the stylesheet sees them as xs:string,
	// which is what a command line can honestly express.
	tparams := map[string]xdm.Sequence{}
	for k, v := range params {
		tparams[k] = xdm.One(xdm.NewString(v))
	}

	cfg := transformCfg{
		resolver:     resolver,
		params:       tparams,
		timeout:      *timeout,
		initial:      *initial,
		mode:         *mode,
		showMessages: *showMsgs,
		allowDoctype: *allowDoctype,
		timezone:     *tzMin,
		now:          now,
		multiple:     len(inputs) > 1,
		resultDir:    *resultDir,
		trackPos:     *trackPos,
	}

	// A batch is processed to the end by default only when asked: stopping at
	// the first failure is right for a one-off, but a validator run over a
	// directory should report every document rather than hide the rest behind
	// the first bad one.
	var failed int
	for _, in := range inputs {
		err := transformOne(sheet, in, *outPath, cfg)
		if err == nil {
			continue
		}
		if !*keepGoing {
			return fmt.Errorf("%s: %w", in, err)
		}
		fmt.Fprintf(os.Stderr, "go-xml: %s: %v\n", in, err)
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d input(s) failed", failed, len(inputs))
	}
	return nil
}

func compileStylesheet(path string, resolver *xslt.FileResolver) (*xslt.Stylesheet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, _ := filepath.Abs(path)
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: abs})
	if err != nil {
		return nil, fmt.Errorf("parsing stylesheet: %w", err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
		Resolver: resolver,
		BaseURI:  abs,
	})
	if err != nil {
		return nil, fmt.Errorf("compiling stylesheet: %w", err)
	}
	return sheet, nil
}

type transformCfg struct {
	resolver     *xslt.FileResolver
	params       map[string]xdm.Sequence
	timeout      time.Duration
	initial      string
	mode         string
	showMessages bool
	allowDoctype bool
	timezone     int
	now          time.Time
	multiple     bool
	resultDir    string
	trackPos     bool
}

func transformOne(sheet *xslt.Stylesheet, inPath, outPath string, cfg transformCfg) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	abs, _ := filepath.Abs(inPath)
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{
		BaseURI:        abs,
		AllowDOCTYPE:   cfg.allowDoctype,
		TrackPositions: cfg.trackPos,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{
		Params:           cfg.params,
		Documents:        cfg.resolver,
		InitialTemplate:  cfg.initial,
		InitialMode:      cfg.mode,
		ImplicitTimezone: cfg.timezone,
		Now:              cfg.now,
	})
	if err != nil {
		return err
	}

	if cfg.showMessages {
		for _, m := range res.Messages {
			fmt.Fprintln(os.Stderr, "message:", m)
		}
	}

	if err := writeSecondary(res.Secondary, cfg.resultDir); err != nil {
		return err
	}

	out := os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	} else if cfg.multiple {
		// With several inputs going to stdout, label each result so the
		// output is not an undifferentiated run of documents.
		fmt.Fprintf(os.Stdout, "<!-- %s -->\n", inPath)
	}

	if err := res.Serialize(out); err != nil {
		return err
	}
	fmt.Fprintln(out)
	return nil
}

// writeSecondary writes the documents produced by xsl:result-document.
//
// Without -result-dir a stylesheet that produces secondary results is an
// error rather than a silent drop: the author asked for several documents, and
// printing only the principal one would look like a successful transform while
// discarding most of its output.
//
// Each href is resolved inside dir and checked for containment, because an
// href is stylesheet-controlled and "../../etc/thing" would otherwise let a
// transform write anywhere the process can reach.
func writeSecondary(results []xslt.SecondaryResult, dir string) error {
	if len(results) == 0 {
		return nil
	}
	if dir == "" {
		return fmt.Errorf(
			"the stylesheet produced %d xsl:result-document output(s); "+
				"pass -result-dir to say where they should be written",
			len(results))
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// Resolve symlinks so that containment is checked against the real
	// location, not a link that points outside it.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	for _, r := range results {
		if r.Href == "" {
			return fmt.Errorf("xsl:result-document has no href, so there is no file to write")
		}
		// An absolute href is refused rather than joined. filepath.Join would
		// silently reinterpret "/tmp/x.xml" as "<root>/tmp/x.xml", which is
		// safely contained but writes somewhere the stylesheet did not name —
		// and a caller reading the href back would look in the wrong place.
		href := filepath.FromSlash(r.Href)
		if filepath.IsAbs(href) || strings.HasPrefix(r.Href, "/") {
			return fmt.Errorf(
				"xsl:result-document href %q is absolute; it must be relative to -result-dir",
				r.Href)
		}
		dest := filepath.Join(root, href)
		clean := filepath.Clean(dest)
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return fmt.Errorf(
				"xsl:result-document href %q resolves outside %s", r.Href, root)
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return err
		}
		f, err := os.Create(clean)
		if err != nil {
			return err
		}
		err = r.Serialize(f, nil)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
