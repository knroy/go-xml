package xslt

import (
	"context"
	"testing"
)

// TestBuildTreeAppliesToPrincipalResult pins that build-tree governs the
// principal result, not only xsl:result-document.
//
// XSLT 3.0 section 26.1 (xslt-lcwd30.xml): "The build-tree attribute controls
// whether the raw principal result or secondary result is converted to a
// final result tree." The principal result is named there explicitly, and
// section 24.1 says what the yes case means: "a document node is created ...
// The tree rooted at this document node forms the final result tree."
//
// So build-tree="no" means no document node is created. Tree() manufactured
// one unconditionally, which handed the caller the very node the stylesheet
// asked not to have built. It now reports the absence, and BuildsTree is how
// a caller tells that apart from a transform that produced nothing.
func TestBuildTreeAppliesToPrincipalResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attr  string
		build bool
	}{
		{"default is yes for xml", `method="xml"`, true},
		{"explicit yes", `method="xml" build-tree="yes"`, true},
		{"explicit no", `method="xml" build-tree="no"`, false},
		// 24.1's note and 26.1's default table: json and adaptive default to
		// no, which is why they are the methods that make the difference
		// reachable without writing the attribute at all.
		{"adaptive defaults to no", `method="adaptive"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:transform version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:output ` + tc.attr + `/>
			  <xsl:template name="main"><a/></xsl:template>
			</xsl:transform>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := res.BuildsTree(); got != tc.build {
				t.Fatalf("BuildsTree() = %v, want %v", got, tc.build)
			}
			tree := res.Tree()
			if tc.build && tree == nil {
				t.Error("build-tree is yes but Tree() returned nil")
			}
			if !tc.build && tree != nil {
				t.Error("build-tree is no but Tree() manufactured a document node")
			}
			// Serialisation is unaffected either way: the raw sequence is
			// still written, which is the whole point of build-tree="no".
			if res.String() == "" {
				t.Error("serialisation produced nothing")
			}
		})
	}
}
