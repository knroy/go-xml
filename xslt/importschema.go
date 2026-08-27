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
	inline := inlineSchemaOf(el)
	if err := checkImportSchemaInline(el, inline); err != nil {
		return err
	}

	if location == "" && inline == nil {
		// Neither a location nor an inline schema: the declaration says
		// only that the namespace is expected to be available. That is
		// legal — a processor may already know the schema — and there
		// is nothing to load.
		//
		// An absent namespace is legal too. The specification makes both
		// attributes optional and reads an absent namespace as "import the
		// components whose names are in no namespace", so <xsl:import-schema/>
		// with no attributes and no content imports the no-namespace schema,
		// which in the ordinary case has no components. Rejecting it made a
		// stylesheet that only wants the built-in types fail to compile.
		//
		// The declaration still has to leave a schema behind, though. Whether
		// a stylesheet may say validation="strict" is decided by whether it
		// imported a schema at all (XTSE1660), and an empty schema is not the
		// same thing as no schema: NewSchema carries the built-in types and
		// the built-in declarations for xml:lang, xml:space, xml:base and
		// xml:id, which is exactly what an import of the XML namespace with no
		// location asks the processor to supply (Part 1 §F.1).
		if c.sheet.schema == nil {
			c.sheet.schema = xsd.NewSchema()
		}
		// One namespace is not merely "expected to be available": the
		// specification writes the schema out. F&O 3.1 §C.2 gives the schema
		// for the XML representation of JSON, and §17.5.3 makes it the schema
		// fn:json-to-xml validates against, so a stylesheet importing
		// http://www.w3.org/2005/xpath-functions with no location is asking
		// for components this processor has rather than for none at all.
		// json-to-xml-typed-001 to -007 write exactly that import and then
		// assert "instance of element(j:map, j:mapType)", which was false for
		// want of the type name.
		if ns == xdm.NSFN {
			json, err := xsd.SchemaForJSON()
			if err != nil {
				return err
			}
			mergeSchema(c.sheet.schema, json)
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
	var schemaRoot *xdm.Node
	switch {
	case inline != nil:
		// The namespace check below does not apply to an inline schema:
		// checkImportSchemaInline already requires the namespace attribute
		// to be absent when the declaration carries an <xs:schema> child,
		// and the inline schema's own targetNamespace is then what the
		// declaration imports rather than something to agree with.
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
		schemaRoot = tree.Root
		loaded, err = xsd.Load(tree.Root, resolved, opts)
	}
	if err == nil {
		err = checkImportedNamespace(el, schemaRoot, ns)
	}
	if err != nil {
		// XTSE0220 is the code for the synthetic schema document — the
		// notional document holding every xsl:import-schema in the
		// stylesheet — failing the constraints of XML Schema Part 1, "such
		// as multiple definitions of the same name". Assembly is where
		// those are detected, so every failure here is that error; without
		// the tag the diagnosis was right but unattributable.
		return fmt.Errorf("XTSE0220: xsl:import-schema: %w", err)
	}

	mergeSchema(c.sheet.schema, loaded)
	return nil
}

// checkImportedNamespace enforces the agreement between the namespace the
// declaration names and the target namespace of the schema document it fetched.
//
// XSLT 3.0 §3.15 says the effect of xsl:import-schema is to import the schema
// components whose target namespace is the value of the namespace attribute,
// or the ones in no namespace when the attribute is absent. A schema document
// whose targetNamespace disagrees therefore cannot supply what was asked for,
// and the specification routes every failure of the notional schema document
// holding the imports to XTSE0220 — "the synthetic schema document is not a
// valid schema document according to the rules of XML Schema Part 1".
//
// The two shapes the suite writes are import-schema-200, which names a
// namespace the schema document does not have, and import-schema-201, which
// names none while the schema document declares one. Both must be errors:
// without the check the stylesheet compiled and the declaration silently
// imported components under a name the stylesheet could not have meant.
func checkImportedNamespace(el, schemaRoot *xdm.Node, ns string) error {
	if schemaRoot == nil {
		return nil
	}
	root := schemaRoot
	if root.Kind == xdm.KindDocument {
		root = nil
		for _, ch := range schemaRoot.Children {
			if ch.Kind == xdm.KindElement {
				root = ch
				break
			}
		}
	}
	if root == nil {
		return nil
	}
	declared := root.AttrValue("targetNamespace")
	if declared == ns {
		return nil
	}
	// A chameleon include is not in play here: the document named by
	// schema-location is read as a schema document in its own right, so its
	// own targetNamespace is the only one it can contribute.
	switch {
	case ns == "":
		return fmt.Errorf("XTSE0220: xsl:import-schema names no namespace "+
			"but the schema document has targetNamespace %q", declared)
	case declared == "":
		return fmt.Errorf("XTSE0220: xsl:import-schema names namespace %q "+
			"but the schema document has no targetNamespace", ns)
	default:
		return fmt.Errorf("XTSE0220: xsl:import-schema names namespace %q "+
			"but the schema document has targetNamespace %q", ns, declared)
	}
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
