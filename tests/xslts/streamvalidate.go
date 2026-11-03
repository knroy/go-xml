package xslts

// A principal input the catalog declares streaming="true" and
// validation="strict" (or "lax"): the tree path parses it, validates it
// against the environment's schema with the document node as the root, and
// keeps whatever annotations an invalid document gets (annotate). The stream
// validates it the same way as it reads it (TransformOptions.SourceValidation);
// what it cannot do is give an invalid document the tree's partial
// annotations, so only a document the schema accepts is streamed.

import (
	"os"
	"path/filepath"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
	"github.com/knroy/go-xml/v2/xslt"
)

// streamValidated returns the environment's schema when the principal
// source s may be streamed validated: a file, that parses, and is valid.
func (r *Runner) streamValidated(set *TestSet, env *Environment, s Source) *xsd.Schema {
	if s.File == "" || s.Content != "" {
		return nil
	}
	schema := envSchema(set, env)
	if schema == nil {
		return nil
	}
	p := filepath.Join(set.Dir, filepath.FromSlash(s.File))
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	tree, err := xdm.ParseString(string(stripBOM(data)), r.sourceParseOptions(p))
	if err != nil || schema.Validate(tree.Root, xsd.ValidateOptions{}) != nil {
		return nil
	}
	return schema
}

// principalValidation is how the case's principal source is to be validated
// on the stream: SourceStrict or SourceLax as it declares, when
// streamValidated takes it, else 0. annotate validates both the same way,
// with the document node the root; on a document the schema accepts, whose
// element is therefore declared, lax and strict assess alike.
func (r *Runner) principalValidation(set *TestSet, tc *TestCase) xslt.SourceValidation {
	env := r.environment(set, tc)
	if env == nil {
		return 0
	}
	for _, s := range env.Sources {
		if s.Role != "." || r.streamValidated(set, env, s) == nil {
			continue
		}
		switch s.Validation {
		case "strict":
			return xslt.SourceStrict
		case "lax":
			return xslt.SourceLax
		}
	}
	return 0
}
