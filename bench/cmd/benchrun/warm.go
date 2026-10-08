package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/relaxng"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
	"github.com/knroy/go-xml/xsd"
	"github.com/knroy/go-xml/xslt"
)

// warmResult is one compile-once group's measurements: per-item timings in
// item order, and the compile time.
type warmResult struct {
	compileNs int64
	ns        [][]int64
	errs      []string
}

type runFunc func(src []byte, uri string, params map[string]string) error

// warmGo times go-xml (or encoding/xml) in-process through the public APIs:
// compile once, then warmup untimed and iters timed passes over the items,
// each item's bytes read before timing and output serialized to io.Discard.
func warmGo(engine, area, compileInput string, items []Item, args []string,
	xsdVersion string, root string, warmup, iters int) (warmResult, error) {
	start := time.Now()
	run, err := compileGo(engine, area, compileInput, args, xsdVersion)
	if err != nil {
		return warmResult{}, err
	}
	res := warmResult{}
	if compileInput != "" {
		res.compileNs = time.Since(start).Nanoseconds()
	}
	srcs := make([][]byte, len(items))
	for i, it := range items {
		if it.Source == "" {
			continue
		}
		if srcs[i], err = os.ReadFile(filepath.Join(root, it.Source)); err != nil {
			return warmResult{}, err
		}
	}
	res.ns = make([][]int64, len(items))
	res.errs = make([]string, len(items))
	for pass := 0; pass < warmup+iters; pass++ {
		for i, it := range items {
			t := time.Now()
			err := run(srcs[i], fileURI(filepath.Join(root, it.Source)), it.Params)
			d := time.Since(t).Nanoseconds()
			if err != nil && res.errs[i] == "" {
				res.errs[i] = err.Error()
			}
			if pass >= warmup {
				res.ns[i] = append(res.ns[i], d)
			}
		}
	}
	return res, nil
}

// goArgs are the go-xml CLI flags a workload may pass that change what the
// in-process run must do; anything else is refused rather than ignored.
type goArgs struct {
	allowDir     string
	allowDoctype bool
	compatDrop   bool
}

func parseGoArgs(args []string) (goArgs, error) {
	var g goArgs
	fs := flag.NewFlagSet("go-xml args", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.allowDir, "allow-dir", "", "")
	fs.BoolVar(&g.allowDoctype, "allow-doctype", false, "")
	fs.BoolVar(&g.compatDrop, "compat-drop-attributes-on-document", false, "")
	if err := fs.Parse(args); err != nil {
		return g, fmt.Errorf("warm go-xml: unsupported args %q: %w", args, err)
	}
	if fs.NArg() > 0 {
		return g, fmt.Errorf("warm go-xml: unsupported args %q", fs.Args())
	}
	return g, nil
}

func compileGo(engine, area, input string, args []string, xsdVersion string) (runFunc, error) {
	g, err := parseGoArgs(args)
	if err != nil {
		return nil, err
	}
	popts := func(uri string) xdm.ParseOptions {
		return xdm.ParseOptions{BaseURI: uri, DocumentURI: uri, AllowDOCTYPE: g.allowDoctype}
	}
	parse := func(src []byte, uri string) (*xdm.Node, error) {
		if src == nil {
			return nil, nil
		}
		tree, err := xdm.ParseString(string(src), popts(uri))
		if err != nil {
			return nil, err
		}
		return tree.Root, nil
	}
	if engine == "encoding-xml" {
		return func(src []byte, _ string, _ map[string]string) error {
			return stdlibParse(src, io.Discard)
		}, nil
	}
	switch area {
	case "parse":
		return func(src []byte, uri string, _ map[string]string) error {
			_, err := parse(src, uri)
			return err
		}, nil
	case "c14n":
		return func(src []byte, uri string, _ map[string]string) error {
			doc, err := parse(src, uri)
			if err != nil {
				return err
			}
			return c14n.Write(io.Discard, doc, c14n.Options{Algorithm: c14n.Inclusive10})
		}, nil
	}

	roots := []string{filepath.Dir(input)}
	if g.allowDir != "" {
		roots = append(roots, strings.Split(g.allowDir, ",")...)
	}
	resolver, err := xslt.NewFileResolver(roots...)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	base := fileURI(input)
	strParams := func(p map[string]string) map[string]xdm.Sequence {
		m := map[string]xdm.Sequence{}
		for k, v := range p {
			m[k] = xdm.One(xdm.NewString(v))
		}
		return m
	}

	switch area {
	case "xslt":
		tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: base})
		if err != nil {
			return nil, err
		}
		sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
			Resolver: resolver, BaseURI: base,
			Compat: xslt.Compatibility{DropAttributesOnDocumentNode: g.compatDrop},
		})
		if err != nil {
			return nil, err
		}
		return func(src []byte, uri string, p map[string]string) error {
			doc, err := parse(src, uri)
			if err != nil {
				return err
			}
			res, err := sheet.Transform(context.Background(), doc, xslt.TransformOptions{
				Params: strParams(p), Documents: resolver, Texts: resolver,
			})
			if err != nil {
				return err
			}
			return res.Serialize(io.Discard)
		}, nil
	case "xquery":
		q, err := xquery.Compile(string(data), xquery.Options{BaseURI: base, DeclarationBaseURI: base})
		if err != nil {
			return nil, err
		}
		opts := xslt.OutputSettings{Encoding: "UTF-8"}
		for name, val := range q.SerializationOptions() {
			if err := xslt.SetSerializationParam(&opts, name, val); err != nil {
				return nil, err
			}
		}
		return func(src []byte, uri string, p map[string]string) error {
			doc, err := parse(src, uri)
			if err != nil {
				return err
			}
			var item xdm.Item
			if doc != nil {
				item = doc
			}
			ctx := xpath.NewContext(item, xpath.Builtins())
			ctx.Docs = resolver
			ctx.Texts = resolver
			for k, v := range strParams(p) {
				ctx.Vars[k] = v
			}
			seq, err := q.Eval(ctx)
			if err != nil {
				return err
			}
			return xslt.Serialize(io.Discard, seq, opts, nil)
		}, nil
	case "xsd":
		v := xsd.Version11
		if xsdVersion == "1.0" {
			v = xsd.Version10
		}
		schema, err := xsd.LoadFiles([]string{input}, xsd.Options{Version: v})
		if err != nil {
			return nil, err
		}
		return func(src []byte, uri string, _ map[string]string) error {
			o := popts(uri)
			o.TrackPositions = true // as the CLI and the Java validators do
			tree, err := xdm.ParseString(string(src), o)
			if err != nil {
				return err
			}
			schema.Validate(tree.Root, xsd.ValidateOptions{}) // a verdict, not a failure
			return nil
		}, nil
	case "rng":
		doc, err := (&relaxng.FileResolver{}).ResolveSchema(base)
		if err != nil {
			return nil, err
		}
		schema, err := relaxng.CompileWithOptions(doc, relaxng.Options{
			Resolver: &relaxng.FileResolver{Root: filepath.Dir(input)}, BaseURI: base,
		})
		if err != nil {
			return nil, err
		}
		return func(src []byte, uri string, _ map[string]string) error {
			o := popts(uri)
			o.TrackPositions = true
			tree, err := xdm.ParseString(string(src), o)
			if err != nil {
				return err
			}
			schema.Validate(tree.Root)
			return nil
		}, nil
	}
	return nil, fmt.Errorf("no warm loop for %s/%s", engine, area)
}

// warmJVM runs bench/java/Loop.java for one compile-once group.
func (b *bench) warmJVM(engineID string, e *Engine, area, compileInput string,
	items []Item, warmup, iters int) (warmResult, error) {
	list := b.tmpPath(engineID + ".items")
	var sb strings.Builder
	for _, it := range items {
		sb.WriteString(b.abs(it.Source))
		for k, v := range it.Params {
			sb.WriteString("\t" + k + "=" + v)
		}
		sb.WriteString("\n")
	}
	if err := os.WriteFile(list, []byte(sb.String()), 0o644); err != nil {
		return warmResult{}, err
	}
	argv := []string{}
	for _, o := range e.JavaOpts {
		argv = append(argv, strings.ReplaceAll(o, "{cache}", b.cache))
	}
	argv = append(argv, "-cp", b.loopDir+string(os.PathListSeparator)+b.classpath(e),
		"Loop", engineID, area, b.abs(compileInput), list, strconv.Itoa(warmup), strconv.Itoa(iters))
	cmd := exec.Command("java", argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return warmResult{}, fmt.Errorf("Loop %s: %v: %s", engineID, err, firstLine(stderr.String()))
	}
	return parseLoopOutput(stdout, stderr.Bytes(), len(items))
}

// parseLoopOutput reads Loop.java's COMPILE/RESULT/ERROR lines.
func parseLoopOutput(stdout, stderr []byte, n int) (warmResult, error) {
	res := warmResult{ns: make([][]int64, n), errs: make([]string, n)}
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 2 && f[0] == "COMPILE":
			res.compileNs, _ = strconv.ParseInt(f[1], 10, 64)
		case len(f) == 3 && f[0] == "RESULT":
			i, err1 := strconv.Atoi(f[1])
			ns, err2 := strconv.ParseInt(f[2], 10, 64)
			if err1 != nil || err2 != nil || i < 0 || i >= n {
				return res, fmt.Errorf("bad Loop line %q", sc.Text())
			}
			res.ns[i] = append(res.ns[i], ns)
		}
	}
	for _, line := range strings.Split(string(stderr), "\n") {
		f := strings.SplitN(line, " ", 3)
		if len(f) == 3 && f[0] == "ERROR" {
			if i, err := strconv.Atoi(f[1]); err == nil && i >= 0 && i < n {
				res.errs[i] = f[2]
			}
		}
	}
	return res, nil
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x on Windows
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
