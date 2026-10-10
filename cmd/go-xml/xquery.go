package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
	"github.com/knroy/go-xml/v2/xslt"
)

// runXQuery implements "go-xml xquery".
//
// It is a subcommand for the reason validate is one: a query takes one
// optional context document rather than a batch of sources, and most of the
// transform's flags -- modes, initial templates, result documents -- have no
// meaning for it.
//
// The security surface is the transform's, flag for flag: the same resolver,
// rooted at the query's own directory plus -allow-dir, answers import module,
// fn:load-xquery-module, fn:doc and (when asked) fn:unparsed-text, and the
// context document is parsed under the same DOCTYPE and external-entity gates.
func runXQuery(args []string) error {
	fs := flag.NewFlagSet("go-xml xquery", flag.ContinueOnError)
	var (
		queryPath = fs.String("q", "", "query to run (required)")
		outPath   = fs.String("o", "", "write output to this file instead of stdout")
		catalog   = registerCatalog(fs)
		allowDirs = fs.String("allow-dir", "",
			"comma-separated roots that import module, import schema, "+
				"fn:load-xquery-module, fn:doc and fn:unparsed-text may read, each covering its subdirectories to "+
				"any depth. The query's own directory is always one of them, "+
				"flag or no flag; empty adds nothing further")
		allowDoctype = fs.Bool("allow-doctype", false,
			"permit a DOCTYPE in the input document and expand the entities it "+
				"declares internally; external entities still require "+
				"-allow-external-entities")
		allowExternalEnts = fs.Bool("allow-external-entities", false,
			"let the input document read entities declared SYSTEM or PUBLIC, and "+
				"an external DTD subset, from the -allow-dir roots (this is the "+
				"XXE surface; it also requires -allow-doctype)")
		allowUnparsedText = fs.Bool("allow-unparsed-text", false,
			"let fn:unparsed-text read files from the -allow-dir roots as raw text")
		timeout  = fs.Duration("timeout", 60*time.Second, "abort the query after this long")
		maxItems = fs.Int("max-items", 0, maxItemsUsage)
		maxBytes = fs.Int64("max-bytes", 0, maxBytesUsage)
		validate = fs.String("validate", "",
			"validate the input document against the schema the query imports "+
				"with import schema, before the query runs: strict requires the "+
				"document element to be declared, lax checks it only if it is. "+
				"Validated nodes carry their schema types, so they atomise to "+
				"typed values and match schema-element() tests. Empty does not "+
				"validate")
		nowStr = fs.String("now", "",
			"fix fn:current-dateTime to this xs:dateTime, making the run reproducible")
		params = paramFlag{}
	)
	fs.Var(params, "p", "external variable, name=value (repeatable)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: go-xml xquery -q QUERY.xq [flags] [INPUT.xml]\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
INPUT.xml, when given, is the context item; without it the query has none.
With -validate it is first validated against the schema the query imports,
so a query declaring a typed context item, or comparing typed values, sees
schema types rather than untyped text.
The result is serialized with the parameters the query declares through
"declare option output:*"; with none declared the method follows from the
result, as the Serialization 3.1 specification says.

Exit status: 0 if the query ran, 1 otherwise.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *queryPath == "" {
		fs.Usage()
		return errors.New("-q is required")
	}
	if fs.NArg() > 1 {
		return errors.New("at most one input document: it becomes the context item")
	}
	if *allowExternalEnts && !*allowDoctype {
		return fmt.Errorf(
			"-allow-external-entities needs -allow-doctype: external entities are " +
				"declared in a DOCTYPE, which is refused without it")
	}

	resolver, err := xslt.NewFileResolver(readableRoots(*queryPath, *allowDirs)...)
	if err != nil {
		return err
	}
	resolver.UnparsedText = *allowUnparsedText

	schemas, err := schemaCatalog(*catalog, schemaFiles{resolver})
	if err != nil {
		return err
	}

	src, err := os.ReadFile(*queryPath)
	if err != nil {
		return err
	}
	base := fileURI(*queryPath)
	q, err := xquery.Compile(string(src), xquery.Options{
		BaseURI:            base,
		DeclarationBaseURI: base,
		ModuleResolver:     moduleFiles{resolver},
		SchemaResolver:     schemas,
	})
	if err != nil {
		return fmt.Errorf("compiling query: %w", err)
	}
	switch *validate {
	case "", "strict", "lax":
	default:
		return fmt.Errorf("-validate %q: expected strict or lax", *validate)
	}
	if *validate != "" && q.Schema() == nil {
		return fmt.Errorf("-validate needs a schema, and the query " +
			"imports none with import schema")
	}
	if *validate != "" && fs.Arg(0) == "" {
		return fmt.Errorf("-validate needs an input document to validate")
	}

	var item xdm.Item
	if in := fs.Arg(0); in != "" {
		// Streamed, as the transform's source is (see compileStylesheet).
		f, err := os.Open(in)
		if err != nil {
			return err
		}
		defer f.Close()
		abs := fileURI(in)
		popts := xdm.ParseOptions{BaseURI: abs, DocumentURI: abs, AllowDOCTYPE: *allowDoctype, MaxBytes: *maxBytes}
		if *allowExternalEnts {
			popts.ExternalEntities = resolver
		}
		tree, err := xdm.Parse(f, popts)
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
		item = tree.Root
		if *validate != "" {
			// The same assessment the transform's -validate makes: a typed
			// copy of the document becomes the context item, so the query
			// sees schema types.
			typed, err := validateSource(q.Schema(), tree.Root, *validate)
			if err != nil {
				return fmt.Errorf("%s: %w", in, err)
			}
			item = typed
		}
	}

	now := time.Now()
	if *nowStr != "" {
		if now, err = time.Parse(time.RFC3339, *nowStr); err != nil {
			return fmt.Errorf("-now %q: expected an xs:dateTime such as "+
				"2024-01-15T09:00:00Z: %w", *nowStr, err)
		}
	}

	cctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	ctx := xpath.NewContext(item, xpath.Builtins(), func(e *xpath.Env) {
		e.Now, e.HasNow = now, true
		e.Ctx = cctx
		e.MaxItems = *maxItems
		e.Docs = resolver
		// The resolver refuses every text read unless -allow-unparsed-text
		// turned it on, as in the transform.
		e.Texts = resolver
	})
	// As with -p on the transform, values arrive as xs:string.
	for k, v := range params {
		ctx.Vars[k] = xdm.One(xdm.NewString(v))
	}

	seq, err := q.Eval(ctx)
	if err != nil {
		return err
	}

	opts, err := outputSettings(q.SerializationOptions())
	if err != nil {
		return err
	}
	out := os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	if err := xslt.Serialize(out, seq, opts, nil); err != nil {
		return err
	}
	fmt.Fprintln(out)
	return nil
}

// outputSettings turns the prolog's "declare option output:*" set into the
// serializer's settings, on the defaults tests/qt3 measures the serializer
// against: the XML declaration is written unless asked away, except by the
// adaptive method, and an unstated method is chosen from the result.
func outputSettings(params map[string]string) (xslt.OutputSettings, error) {
	o := xslt.OutputSettings{Encoding: "UTF-8"}
	if strings.EqualFold(params["method"], "adaptive") {
		o.OmitXMLDecl = true
	}
	for name, val := range params {
		if err := xslt.SetSerializationParam(&o, name, val); err != nil {
			return o, err
		}
	}
	return o, nil
}

// moduleFiles answers "import module ... at" from the confined resolver: each
// location hint is tried in order, and only a file inside the roots is read.
// An import with no hint finds nothing, which the query reports as XQST0059.
type moduleFiles struct{ r *xslt.FileResolver }

func (m moduleFiles) Resolve(_ string, hints []string, base string) (io.ReadCloser, string, error) {
	var err error
	for _, h := range hints {
		var rc io.ReadCloser
		var uri string
		if rc, uri, err = m.r.ResolveEntity(h, "", base); err == nil {
			return rc, uri, nil
		}
	}
	return nil, "", err
}

// schemaFiles answers xsl:import-schema, "import schema ... at" and the
// imported schemas' own xs:include and xs:import from the same confined
// resolver, so a schema reaches no further than the stylesheet or query that
// named it. A request with no location -- an import naming a namespace alone
// -- finds no document, which both languages treat as "no schema here".
type schemaFiles struct{ r *xslt.FileResolver }

func (s schemaFiles) Resolve(_, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	return s.r.ResolveEntity(location, "", base)
}
