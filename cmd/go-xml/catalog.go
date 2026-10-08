package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/knroy/go-xml/xsd"
)

// registerCatalog declares -catalog on fs and returns its target. One
// function, so that validate, the transform and xquery describe it the same
// way.
func registerCatalog(fs *flag.FlagSet) *string {
	return fs.String("catalog", "",
		"directory holding local copies of the W3C schemas, XMLSchema.xsd "+
			"and/or xml.xsd. Every reference to them -- by their www.w3.org "+
			"URLs, a relative name, or the namespace alone -- is answered from "+
			"these files, and nothing is fetched. A DOCTYPE in these files is "+
			"permitted, since the W3C schema for schemas carries one. The "+
			"github.com/knroy/go-xml/w3cschemas module ships both under "+
			"schemas/")
}

// schemaCatalog returns a resolver that answers the W3C schemas from the files
// in dir and passes every other reference to fallback. With dir empty it
// returns fallback unchanged.
//
// A file the directory lacks is skipped rather than refused: a schema set that
// needs only xml.xsd should not have to carry the schema for schemas. A
// directory holding neither is a mistake worth saying so about.
func schemaCatalog(dir string, fallback xsd.Resolver) (xsd.Resolver, error) {
	if dir == "" {
		return fallback, nil
	}
	cat := xsd.NewCatalogResolver()
	var names []string
	found := 0
	for _, e := range xsd.W3CEntries() {
		names = append(names, e.Path)
		src, err := os.ReadFile(filepath.Join(dir, e.Path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("-catalog: %w", err)
		}
		cat.Add(e.Namespace, src, e.Aliases...)
		found++
	}
	if found == 0 {
		return nil, fmt.Errorf("-catalog %s: the directory holds none of %v", dir, names)
	}
	cat.SetFallback(fallback)
	return cat, nil
}
