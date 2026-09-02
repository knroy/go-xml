// Command go-xml applies an XSLT 2.0 stylesheet to XML documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xslt"
)

func main() {
	// "validate" is a subcommand; everything else keeps the original
	// invocation, so a command line that worked before still works.
	if len(os.Args) > 1 && os.Args[1] == "validate" {
		if err := runValidate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "go-xml:", err)
			os.Exit(1)
		}
		return
	}
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
			"permit a DOCTYPE in the source document and expand the entities it "+
				"declares internally; external entities still require "+
				"-allow-external-entities")
		allowExternalEnts = flag.Bool("allow-external-entities", false,
			"let the source document read entities declared SYSTEM or PUBLIC, and "+
				"an external DTD subset, from the -allow-dir roots (this is the "+
				"XXE surface; it also requires -allow-doctype)")
		allowUnparsedText = flag.Bool("allow-unparsed-text", false,
			"let fn:unparsed-text read files from the -allow-dir roots as raw text")
		xinclude = flag.Bool("xinclude", false,
			"process xi:include elements in the source document, reading the "+
				"included resources from the -allow-dir roots")
		xpathVersion = flag.String("xpath-version", "",
			"XPath version for the stylesheet's expressions: 2.0, 3.0 or 3.1. "+
				"Empty derives it from the stylesheet's own version attribute, "+
				"which is what conformance requires; setting it overrides the "+
				"stylesheet in either direction")

		backtrackRegex = flag.Bool("backtracking-regex", false,
			"evaluate regular expressions whose backreferences the default "+
				"engine cannot decide, using a backtracking matcher that has no "+
				"linear-time guarantee; bounded by a step budget, but enable it "+
				"only for patterns you trust, since a pattern taken from "+
				"document data can be made expensive on purpose")
		timeout = flag.Duration("timeout", 60*time.Second, "abort a transform after this long")
		initial = flag.String("initial-template", "",
			"start at this named template instead of applying templates to a "+
				"source document; no input document is then needed")
		mode            = flag.String("mode", "", "initial mode for apply-templates")
		showMsgs        = flag.Bool("messages", false, "print xsl:message output to stderr")
		compatDropAttrs = flag.Bool("compat-drop-attributes-on-document", false,
			"discard an attribute that reaches the content of a document node "+
				"instead of raising XTDE0420; the specified behaviour is the "+
				"error, and this matches Saxon instead, which stylesheets such "+
				"as DocBook xslTNG are written against")
		tzMin = flag.Int("timezone", 0,
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

		maxDepth = flag.Int("max-depth", 0,
			"bound template recursion; 0 uses the default, negative removes the "+
				"bound (the default guards against a stylesheet that recurses "+
				"without a base case)")

		keepGoing = flag.Bool("keep-going", false,
			"with several inputs, report failures and continue instead of stopping")
		params = paramFlag{}
	)
	flag.Var(params, "p", "stylesheet parameter, name=value (repeatable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: go-xml -xsl STYLESHEET [flags] INPUT.xml [INPUT.xml ...]\n"+
				"       go-xml -xsl STYLESHEET -initial-template NAME [flags]\n"+
				"       go-xml validate -xsd SCHEMA.xsd [flags] INPUT.xml ...\n"+
				"       go-xml validate -rng SCHEMA.rng [flags] INPUT.xml ...\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Security defaults: xsl:include, xsl:import, fn:doc and fn:document are all
disabled unless -allow-dir names the directories they may read. A DOCTYPE in
the source document is rejected unless -allow-doctype is given, and even then
only its internal declarations are expanded: reading an external entity, an
external DTD subset, or a file through fn:unparsed-text each needs its own
flag as well. Every one of those reads is confined to the -allow-dir roots,
with symlinks resolved before the check.

Exit status: 0 if every input transformed, 1 otherwise.
`)
	}
	flag.Parse()

	if *sheetPath == "" {
		flag.Usage()
		return fmt.Errorf("-xsl is required")
	}
	inputs := flag.Args()
	// A transform that starts at a named template needs no source document:
	// there is no context node to apply templates to, and the stylesheet
	// generates its own content. XSLT 2.0 section 2.3 makes the source
	// optional in exactly that case. Requiring a document anyway forced the
	// caller to invent one that would never be read.
	//
	// With no -initial-template and no input either, the stylesheet may still
	// declare an xsl:initial-template, which Transform honours as the default
	// entry point. Deciding that here would need the CLI to see inside the
	// compiled stylesheet, so the empty batch is passed through and Transform
	// reports it if there is no such template.
	sourceless := len(inputs) == 0
	if len(inputs) > 1 && *outPath != "" {
		return fmt.Errorf("-o cannot be used with more than one input")
	}
	if sourceless && *mode != "" {
		return fmt.Errorf(
			"-mode selects the mode for apply-templates over a source document, " +
				"so it needs an input document")
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
	// Both are off unless asked for, and both read only inside the roots
	// above: the resolver rejects a non-file scheme before touching the
	// filesystem and resolves symlinks before checking containment.
	resolver.ExternalEntities = *allowExternalEnts
	resolver.UnparsedText = *allowUnparsedText
	// An external entity is read while the *document* is parsed, so admitting
	// one without admitting a DOCTYPE would leave the flag with nothing to
	// act on. Saying so is better than silently ignoring it.
	if *allowExternalEnts && !*allowDoctype {
		return fmt.Errorf(
			"-allow-external-entities needs -allow-doctype: external entities are " +
				"declared in a DOCTYPE, which is refused without it")
	}

	// Process-wide rather than per-transform, because the regex functions are
	// registered once and the compiled-pattern cache is shared. The cache is
	// keyed on the setting, so this cannot serve a stale compilation.
	xpath.SetBacktrackingRegex(*backtrackRegex)

	sheet, err := compileStylesheet(*sheetPath, resolver, *xpathVersion,
		xslt.Compatibility{
			DropAttributesOnDocumentNode: *compatDropAttrs,
		})
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
		externalEnts: *allowExternalEnts,
		xinclude:     *xinclude,
		maxDepth:     *maxDepth,
	}

	// A batch is processed to the end by default only when asked: stopping at
	// the first failure is right for a one-off, but a validator run over a
	// directory should report every document rather than hide the rest behind
	// the first bad one.
	// A sourceless run is one transform with no input document, so the batch
	// loop is given a single empty name rather than being special-cased.
	if sourceless {
		inputs = []string{""}
	}
	var failed int
	for _, in := range inputs {
		err := transformOne(sheet, in, *outPath, cfg)
		if err == nil {
			continue
		}
		if !*keepGoing {
			if in == "" {
				return err
			}
			return fmt.Errorf("%s: %w", in, err)
		}
		if in == "" {
			fmt.Fprintf(os.Stderr, "go-xml: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "go-xml: %s: %v\n", in, err)
		}
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d input(s) failed", failed, len(inputs))
	}
	return nil
}

func compileStylesheet(path string, resolver *xslt.FileResolver,
	xpathVersion string, compat xslt.Compatibility) (*xslt.Stylesheet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs := fileURI(path)
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: abs})
	if err != nil {
		return nil, fmt.Errorf("parsing stylesheet: %w", err)
	}
	v, err := parseXPathVersion(xpathVersion)
	if err != nil {
		return nil, err
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
		Resolver:     resolver,
		BaseURI:      abs,
		XPathVersion: v,
		Compat:       compat,
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
	externalEnts bool
	xinclude     bool
	maxDepth     int
}

func transformOne(sheet *xslt.Stylesheet, inPath, outPath string, cfg transformCfg) error {
	// An empty path is a transform with no source document, which is how a
	// run that starts at a named template is expressed. Transform takes a nil
	// source for exactly that case, so the parse is simply skipped.
	var root *xdm.Node
	if inPath != "" {
		data, err := os.ReadFile(inPath)
		if err != nil {
			return err
		}
		abs := fileURI(inPath)
		popts := xdm.ParseOptions{
			BaseURI: abs,
			// The document URI is what fn:document-uri returns, and it is a
			// separate property from the base URI: a base URI is inherited
			// and can be overridden by xml:base, while the document URI names
			// where this document came from and nothing below it changes.
			DocumentURI:    abs,
			AllowDOCTYPE:   cfg.allowDoctype,
			TrackPositions: cfg.trackPos,
		}
		if cfg.externalEnts {
			popts.ExternalEntities = cfg.resolver
		}
		tree, err := xdm.ParseString(string(data), popts)
		if err != nil {
			return err
		}
		// XInclude is a transformation of the parsed document rather than a
		// parsing option — XInclude 1.0 section 4 defines it over a finished
		// infoset — so it runs here, between the parse and the transform, and
		// only when asked for. The resolver doing the reading is the same one
		// that gates fn:doc and xsl:include, so an inclusion is confined to
		// the -allow-dir roots on exactly the same terms.
		if cfg.xinclude {
			if err := xdm.ProcessXInclude(tree, xdm.XIncludeOptions{
				Resolver: cfg.resolver,
				// The included documents are held to the same limits as the
				// including one: an inclusion becomes part of the document,
				// so it must not be a way around a bound the document itself
				// was held to.
				Parse: popts,
			}); err != nil {
				return err
			}
		}
		root = tree.Root
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	res, err := sheet.Transform(ctx, root, xslt.TransformOptions{
		Params:           cfg.params,
		Documents:        cfg.resolver,
		InitialTemplate:  cfg.initial,
		InitialMode:      cfg.mode,
		ImplicitTimezone: cfg.timezone,
		Now:              cfg.now,
		MaxDepth:         cfg.maxDepth,
		// Texts is the same resolver, which refuses every read unless
		// -allow-unparsed-text turned it on. Passing it unconditionally keeps
		// the gate in one place rather than two.
		Texts: cfg.resolver,
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

// parseXPathVersion reads the -xpath-version flag.
//
// The empty string is nil rather than a default version, because "derive it
// from the stylesheet" is a different instruction from "use 2.0": a
// version="3.0" stylesheet must get XPath 3.1 when the flag is unset.
func parseXPathVersion(s string) (*xpath.Version, error) {
	if s == "" {
		return nil, nil
	}
	var v xpath.Version
	switch s {
	case "2.0":
		v = xpath.XPath20
	case "3.0":
		v = xpath.XPath30
	case "3.1":
		v = xpath.XPath31
	default:
		return nil, fmt.Errorf("-xpath-version %q: expected 2.0, 3.0 or 3.1", s)
	}
	return &v, nil
}

// fileURI turns a filesystem path into an absolute file: URI.
//
// A base URI is a URI, not a path. XPath's fn:resolve-uri and fn:static-base-uri
// are defined over RFC 3986 references, so handing them a bare path like
// /home/u/s.xsl makes resolve-uri(rel, static-base-uri()) raise FORG0002 —
// the path has no scheme, so it is not an absolute URI. Real stylesheets do
// exactly that to locate a file beside themselves, so the path has to be
// spelled as a URI before it ever reaches the evaluator.
//
// url.URL does the escaping, so a directory containing a space or any other
// character that is not URI-safe survives the round trip.
//
// The leading slash is added rather than left to url.URL because a Windows
// path does not have one. filepath.Abs gives C:\dir\s.xsl, which ToSlash makes
// C:/dir/s.xsl, and url.URL writes a path that does not begin with "/" without
// one -- producing file://C:/dir/s.xsl, where C: is the *authority* rather than
// the drive. Parsing that back gives host "C:" and path "/dir/s.xsl", so the
// drive letter is silently gone and every URI resolved against it names a file
// that is not there. RFC 8089 spells a local path with an empty authority, and
// Saxon agrees: file:///C:/dir/s.xsl.
func fileURI(path string) string {
	if path == "" || strings.HasPrefix(path, "file:") {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return absPathToFileURI(filepath.ToSlash(abs))
}

// absPathToFileURI spells an already-absolute, already-slash-separated path as
// a file: URI. It is separate from fileURI so that both platforms' spellings
// can be tested on either platform: filepath.Abs and ToSlash are what make
// fileURI itself answer differently on Windows and Unix.
func absPathToFileURI(slashed string) string {
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}
