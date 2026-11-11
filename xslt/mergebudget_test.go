package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// optsRun compiles sheet and runs it over src with opts, returning the
// serialised result without its XML declaration.
func optsRun(t *testing.T, sheet, src string, opts TransformOptions) (string, error) {
	t.Helper()
	sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	d, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := st.Transform(context.Background(), d.Root, opts)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		return "", err
	}
	out := b.String()
	if _, rest, ok := strings.Cut(out, "?>"); ok {
		out = rest
	}
	return out, nil
}

const mergeBudgetSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="/"><n><xsl:variable name="d" select="/"/><xsl:merge>
    <xsl:merge-source ATTRS><xsl:merge-key select="0"/></xsl:merge-source>
    <xsl:merge-action><xsl:value-of select="count(current-merge-group())"/></xsl:merge-action>
  </xsl:merge></n></xsl:template>
</xsl:stylesheet>`

func mergeBudgetRun(t *testing.T, attrs, src string, opts TransformOptions) (string, error) {
	t.Helper()
	return optsRun(t, strings.Replace(mergeBudgetSheet, "ATTRS", attrs, 1), src, opts)
}

// xsl:merge retains every entry of every anchor until the groups are formed,
// but Eval gives each anchor's select a fresh item allowance: 20 anchors of
// 200 items held 4,000 under a bound of 1,000. group-by refuses the same.
func TestMergeEntriesChargedAcrossAnchors(t *testing.T) {
	opts := TransformOptions{MaxItems: 1000}
	_, err := mergeBudgetRun(t, `for-each-item="1 to 20" select="1 to 200"`, `<r/>`, opts)
	if !refused(err, "") {
		t.Fatalf("want the item refusal, got %v", err)
	}
	got, err := mergeBudgetRun(t, `for-each-item="1 to 4" select="1 to 200"`, `<r/>`, opts)
	if err != nil || got != "<n>800</n>" {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}

// A streamable="yes" source wraps its select in snapshot() on the tree too
// (15.4), and each snapshot is a copy charged to the node budget as an
// explicit fn:snapshot is: 200 copies of an 11-node document made 2,200
// nodes under a bound of 1,000.
func TestMergeImplicitSnapshotsCharged(t *testing.T) {
	src := `<r>` + strings.Repeat(`<e/>`, 9) + `</r>`
	opts := TransformOptions{MaxNodes: 1000}
	_, err := mergeBudgetRun(t, `streamable="yes" select="(1 to 200) ! $d"`, src, opts)
	if !refused(err, "result-tree nodes") {
		t.Fatalf("want the node refusal, got %v", err)
	}
	got, err := mergeBudgetRun(t, `streamable="yes" select="(1 to 20) ! $d"`, src, opts)
	if err != nil || got != "<n>20</n>" {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}
