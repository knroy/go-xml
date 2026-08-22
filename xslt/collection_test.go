package xslt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// sheetCollections resolves one fixed collection, standing in for a caller
// that knows which documents a stylesheet is allowed to see.
type sheetCollections struct{ docs []string }

func (c sheetCollections) ResolveCollection(uri, base string) (xdm.Sequence, error) {
	if uri != "lists" {
		return nil, fmt.Errorf("no collection %q", uri)
	}
	var out xdm.Sequence
	for _, s := range c.docs {
		tree, err := xdm.ParseString(s, xdm.ParseOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, tree.Root)
	}
	return out, nil
}

// runWithCollections is run() with a collection resolver installed; the
// package helpers deliberately pass empty options, which is the behaviour
// under test here.
func runWithCollections(t *testing.T, sheet, source string, r xdm.Sequence) (string, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		return "", err
	}
	dtree, err := xdm.ParseString(source, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	opts := TransformOptions{}
	if r != nil {
		opts.Collections = sheetCollections{docs: []string{
			`<item>a</item>`, `<item>b</item>`,
		}}
	}
	res, err := s.Transform(context.Background(), dtree.Root, opts)
	if err != nil {
		return "", err
	}
	out := res.String()
	if i := strings.Index(out, "?>"); i >= 0 {
		out = out[i+2:]
	}
	return strings.TrimSpace(out), nil
}

const collSheet = `<xsl:stylesheet version="2.0"
   xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
 <xsl:template match="/">
  <out><xsl:value-of select="string-join(collection('lists')/item, ',')"/></out>
 </xsl:template>
</xsl:stylesheet>`

// A stylesheet reaches a collection only when TransformOptions supplies one.
func TestTransformCollectionResolver(t *testing.T) {
	got, err := runWithCollections(t, collSheet, `<doc/>`, xdm.Sequence{nil})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if want := `<out>a,b</out>`; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// Without one, fn:collection stays refused: the default must be closed for a
// stylesheet exactly as it is for a bare XPath expression.
func TestTransformCollectionDisabledByDefault(t *testing.T) {
	_, err := runWithCollections(t, collSheet, `<doc/>`, nil)
	if err == nil {
		t.Fatal("collection() should fail with no resolver configured")
	}
	if !strings.Contains(err.Error(), "FODC0002") {
		t.Errorf("error = %v, want FODC0002", err)
	}
}
