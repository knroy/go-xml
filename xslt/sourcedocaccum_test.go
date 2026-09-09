package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// xsl:source-document/@use-accumulators restricts which accumulators may be
// read over the document it obtains, and the restriction bites even though
// this engine never streams.
//
// 18.2.2 makes the attribute the set of accumulators "applicable" to the
// document, and XTDE3362 makes it a dynamic error to read one that is not.
// Neither rule is conditional on streaming -- the error's own wording is about
// applicability, and only its *second* sentence mentions streamed documents.
// The suite's non-stream-201 is named for exactly this point, its description
// being "Use-accumulators applies even when not streaming".
//
// The attribute was previously parsed and discarded, on the recorded reasoning
// that an engine applying no accumulators cannot be wrong about which apply.
// That reasoning had decayed: accumulators are implemented, and this very set
// is already enforced for xsl:merge-source/@use-accumulators. So a stylesheet
// naming one accumulator and reading another ran quietly to completion.
func TestSourceDocumentUseAccumulatorsRestrictsReads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.xml"),
		[]byte(`<doc><p>x</p></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Two accumulators are declared and use-accumulators names only "listed",
	// so reading "listed" yields its value and reading "unlisted" is the
	// error. Declaring both is what makes the case specific: an undeclared
	// name would be XTDE3340 and would pass for the wrong reason.
	const tmpl = `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:accumulator name="listed" as="xs:integer" initial-value="1"><xsl:accumulator-rule match="p" select="1"/></xsl:accumulator>
		<xsl:accumulator name="unlisted" as="xs:integer" initial-value="2"><xsl:accumulator-rule match="p" select="2"/></xsl:accumulator>
		<xsl:template name="xsl:initial-template">
			<xsl:source-document href="d.xml" use-accumulators="listed">
				<out><xsl:value-of select="accumulator-before('%s')"/></out>
			</xsl:source-document>
		</xsl:template>
	</xsl:transform>`

	run := func(t *testing.T, name string) (string, error) {
		t.Helper()
		path := filepath.Join(dir, "s.xsl")
		r, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		stree, err := xdm.ParseString(fmt.Sprintf(tmpl, name),
			xdm.ParseOptions{BaseURI: path})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: path})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		res, err := s.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "initial-template",
				InitialTemplateURI: xdm.NSXSL, Documents: r})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(res.String()), nil
	}

	t.Run("a listed accumulator is readable", func(t *testing.T) {
		got, err := run(t, "listed")
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if want := "<out>1</out>"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("an unlisted accumulator is XTDE3362", func(t *testing.T) {
		_, err := run(t, "unlisted")
		if err == nil {
			t.Fatal("reading an accumulator that use-accumulators omits " +
				"succeeded; want XTDE3362")
		}
		if code := xdm.ErrorCode(err); code != "XTDE3362" {
			t.Errorf("got error code %q (%v), want XTDE3362", code, err)
		}
	})

	// Without the attribute the tree is unrestricted, so both accumulators
	// remain readable. This is the half that a fix registering the set
	// unconditionally would break.
	t.Run("no attribute leaves the tree unrestricted", func(t *testing.T) {
		const open = `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
				xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs" version="3.0">
			<xsl:output omit-xml-declaration="yes"/>
			<xsl:accumulator name="listed" as="xs:integer" initial-value="1"><xsl:accumulator-rule match="p" select="1"/></xsl:accumulator>
			<xsl:accumulator name="unlisted" as="xs:integer" initial-value="2"><xsl:accumulator-rule match="p" select="2"/></xsl:accumulator>
			<xsl:template name="xsl:initial-template">
				<xsl:source-document href="d.xml">
					<out><xsl:value-of select="accumulator-before('unlisted')"/></out>
				</xsl:source-document>
			</xsl:template>
		</xsl:transform>`
		path := filepath.Join(dir, "s.xsl")
		r, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		stree, err := xdm.ParseString(open, xdm.ParseOptions{BaseURI: path})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: path})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		res, err := s.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "initial-template",
				InitialTemplateURI: xdm.NSXSL, Documents: r})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if got, want := strings.TrimSpace(res.String()), "<out>2</out>"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// A duplicate key in an XPath map constructor is XTDE3365 under XSLT, not
// XQuery's XQDY0137.
//
// The construct is one expression with two spellings of the same failure,
// because the code belongs to the host language. XSLT 3.0 section 17.4 says of
// the MapExpr that "if two or more entries have the same key then a dynamic
// error occurs [see ERR XTDE3365]", which is the same code xsl:map already
// raises for a duplicate among the maps it merges -- so in XSLT the two ways
// of writing a map agree on how they fail. XQuery 3.1 keeps XQDY0137, which
// the QT3 suite requires and which stays the default.
//
// The engine reported XQDY0137 from XSLT as well, which is what si-fork-814
// and sx-MapExpr-007 catch.
func TestMapConstructorDuplicateKeyIsXTDE3365(t *testing.T) {
	const src = `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:template name="xsl:initial-template">
			<out><xsl:value-of select="map{'a':1,'a':2}?a"/></out>
		</xsl:template>
	</xsl:transform>`
	stree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "initial-template",
			InitialTemplateURI: xdm.NSXSL})
	if err == nil {
		t.Fatal("a map constructor naming a key twice succeeded; want XTDE3365")
	}
	if code := xdm.ErrorCode(err); code != "XTDE3365" {
		t.Errorf("got error code %q (%v), want XTDE3365", code, err)
	}
}
