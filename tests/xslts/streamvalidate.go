package xslts

// A principal input the catalog declares streaming="true" and
// validation="strict" (or "lax"): the tree path parses it, validates it
// against the environment's schema with the document node as the root, and
// keeps whatever annotations an invalid document gets (annotate). The stream
// validates it the same way as it reads it (internal/xsdstream.WithSource);
// what it cannot do is give an invalid document the tree's partial
// annotations, so only a document the schema accepts is streamed.

import (
	"os"
	"path/filepath"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
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

// principalValidated is streamValidated for the case's principal source, or
// nil when it is not one to validate.
func (r *Runner) principalValidated(set *TestSet, tc *TestCase) *xsd.Schema {
	env := r.environment(set, tc)
	if env == nil {
		return nil
	}
	for _, s := range env.Sources {
		if s.Role == "." && (s.Validation == "strict" || s.Validation == "lax") {
			return r.streamValidated(set, env, s)
		}
	}
	return nil
}
