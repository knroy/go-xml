package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// xsl:import-schema (XSLT 2.0 §3.14).
//
// The declaration makes a schema's type names available to the stylesheet, so
// that "as='my:invoiceType'" and "instance of my:invoiceType" mean something.
// It does *not* by itself cause any document to be validated: that happens when
// an instruction asks for it, or when the caller validates before transforming.
//
// What this supports, stated plainly because the gap matters: type *names*
// become known and can be asserted against a node's annotation. What it does
// not do is change how a node atomises — an element annotated as xs:integer
// still atomises as untyped, because the typed value would have to be carried
// on the node and xdm.Node holds a type name rather than a typed value. A
// stylesheet that relies on schema-aware *arithmetic* will therefore behave as
// it does without a schema; one that relies on type assertions will not.

// compileImportSchema handles an <xsl:import-schema> declaration.
func (c *compiler) compileImportSchema(el *xdm.Node) error {
	ns := el.AttrValue("namespace")
	location := el.AttrValue("schema-location")

	// An inline <xs:schema> child is an alternative to schema-location.
	var inline *xdm.Node
	for _, child := range el.ChildElements() {
		if child.IsElement(xsd.NSSchema, "schema") {
			inline = child
			break
		}
	}

	if location == "" && inline == nil {
		// Neither a location nor an inline schema: the declaration says
		// only that the namespace is expected to be available. That is
		// legal — a processor may already know the schema — and there
		// is nothing to load.
		if ns == "" {
			return fmt.Errorf(
				"xsl:import-schema needs a namespace, a schema-location, " +
					"or an inline xs:schema")
		}
		return nil
	}

	if c.sheet.schema == nil {
		c.sheet.schema = xsd.NewSchema()
	}

	opts := xsd.Options{Resolver: c.opts.SchemaResolver}
	if opts.Resolver == nil {
		// Without a resolver the stylesheet cannot name a location, for
		// the same reason xsl:include cannot: following one means
		// fetching whatever the stylesheet names.
		if inline == nil {
			return fmt.Errorf(
				"xsl:import-schema names schema-location %q but no "+
					"SchemaResolver is configured", location)
		}
	}

	var loaded *xsd.Schema
	var err error
	switch {
	case inline != nil:
		loaded, err = xsd.Load(inline, c.opts.BaseURI, opts)
	default:
		rc, resolved, rerr := opts.Resolver.Resolve(ns, location, c.opts.BaseURI)
		if rerr != nil {
			return fmt.Errorf("xsl:import-schema %q: %w", location, rerr)
		}
		if rc == nil {
			return fmt.Errorf("xsl:import-schema %q resolved to nothing", location)
		}
		defer rc.Close()
		tree, perr := xdm.Parse(rc, xdm.ParseOptions{})
		if perr != nil {
			return fmt.Errorf("xsl:import-schema %q: %w", location, perr)
		}
		loaded, err = xsd.Load(tree.Root, resolved, opts)
	}
	if err != nil {
		return fmt.Errorf("xsl:import-schema: %w", err)
	}

	mergeSchema(c.sheet.schema, loaded)
	return nil
}

// mergeSchema folds one schema's global components into another.
//
// Several xsl:import-schema declarations may name different namespaces, and the
// spec treats the result as one schema. A name already present is left alone
// rather than overwritten: the first declaration wins, which matches how
// import precedence works everywhere else in a stylesheet.
func mergeSchema(dst, src *xsd.Schema) {
	for name, t := range src.Types {
		if _, ok := dst.Types[name]; !ok {
			dst.Types[name] = t
		}
	}
	for name, d := range src.Elements {
		if _, ok := dst.Elements[name]; !ok {
			dst.Elements[name] = d
		}
	}
	for name, d := range src.Attributes {
		if _, ok := dst.Attributes[name]; !ok {
			dst.Attributes[name] = d
		}
	}
}

// Schema returns the schema assembled from the stylesheet's xsl:import-schema
// declarations, or nil when it has none.
//
// It is exposed so that a caller can validate a source document against the
// same schema the stylesheet declares, rather than having to load it twice and
// risk the two disagreeing.
func (s *Stylesheet) Schema() *xsd.Schema { return s.schema }
