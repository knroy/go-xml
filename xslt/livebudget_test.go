package xslt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// liveRun runs body inside a streamable xsl:source-document over n records,
// in bursts or over the tree, with the budget fields of opts.
func liveRun(t *testing.T, body string, n int, bursts bool, opts TransformOptions) (string, error) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range n {
		fmt.Fprintf(&b, `<rec k="K%07d" v="%d"><a>t</a></rec>`, i, i%97)
	}
	b.WriteString("</doc>")
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" exclude-result-prefixes="#all">` +
		`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` + body +
		`</xsl:source-document></xsl:template></xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	noBursts, traceBursts = !bursts, func(string) { ran = true }
	defer func() { noBursts, traceBursts = false, nil }()
	opts.InitialTemplate, opts.Documents = "main", docs
	res, err := sheet.Transform(context.Background(), nil, opts)
	if ran != bursts {
		t.Fatalf("bursts ran %v, want %v", ran, bursts)
	}
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

func refused(err error, what string) bool {
	return err != nil && errors.Is(err, xdm.ErrResourceLimit) &&
		strings.Contains(err.Error(), "XPDY0130") && strings.Contains(err.Error(), what)
}

// The bound lane's shapes (m10 bound, findings B and C): a stream whose state
// stays flat built more than the budget and was refused. Small bounds stand in
// for the defaults, which the same shapes crossed at a million records.
const (
	liveOC = `<xsl:on-completion><done n="{$s}"/></xsl:on-completion>`
	// V10: a two-node temporary tree per record.
	liveV10 = `<out><xsl:for-each select="doc/rec"><xsl:variable name="c"><x><xsl:value-of select="@k"/></x></xsl:variable><xsl:if test="$c eq ''">x</xsl:if></xsl:for-each></out>`
	// V1: an fn:copy-of per record, now charged.
	liveV1 = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="0"/>` + liveOC + `<xsl:variable name="c" select="copy-of(.)"/><xsl:next-iteration><xsl:with-param name="s" select="$s + count($c//*)"/></xsl:next-iteration></xsl:iterate>`
	// L2: value-of into a body variable.
	liveL2 = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="0"/>` + liveOC + `<xsl:variable name="t"><xsl:value-of select="@k,@k,@k,@k,@k,@k" separator=""/></xsl:variable><xsl:next-iteration><xsl:with-param name="s" select="$s + string-length($t)"/></xsl:next-iteration></xsl:iterate>`
	// L6: string-join per record.
	liveL6 = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="0"/>` + liveOC + `<xsl:next-iteration><xsl:with-param name="s" select="$s + string-length(string-join((@k,@k,@k,@k,@k,@k)))"/></xsl:next-iteration></xsl:iterate>`
	// G8: a carried temporary tree copied into the next, growing.
	liveG8 = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="()"/><xsl:on-completion><done n="{count($s//rec)}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s"><xsl:copy-of select="$s"/><xsl:copy-of select="."/></xsl:with-param></xsl:next-iteration></xsl:iterate>`
	// G5: a carried sequence of strings, growing while each item builds
	// one; and the same for a sequence of new elements.
	liveG5  = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="()" as="xs:string*" xmlns:xs="http://www.w3.org/2001/XMLSchema"/><xsl:on-completion><done n="{count($s)}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s" select="($s, string-join((@k, ',')))"/></xsl:next-iteration></xsl:iterate>`
	liveG5n = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="()" as="element()*"/><xsl:on-completion><done n="{count($s)}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s" as="element()*"><xsl:sequence select="$s"/><x/></xsl:with-param></xsl:next-iteration></xsl:iterate>`
	// G4: a carried string, growing.
	liveG4 = `<xsl:iterate select="doc/rec"><xsl:param name="s" select="''"/><xsl:on-completion><done n="{string-length($s)}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s" select="string-join(($s, @k), ',')"/></xsl:next-iteration></xsl:iterate>`
)

// MaxNodes: a streamed item hands back the temporary trees and copies it
// dropped, so flat state streams past the bound; over the tree nothing is
// handed back and the same bound refuses.
func TestLiveNodesStreamed(t *testing.T) {
	for _, s := range []struct{ name, body, want string }{
		{"V10", liveV10, "<out/>"},
		{"V1", liveV1, `<done n="2000"/>`},
	} {
		opts := TransformOptions{MaxNodes: 500}
		got, err := liveRun(t, s.body, 2000, true, opts)
		if err != nil || !strings.Contains(got, s.want) {
			t.Errorf("%s streamed: %q, %v", s.name, got, err)
		}
		if _, err := liveRun(t, s.body, 2000, false, opts); !refused(err, "more than 500 result-tree nodes") {
			t.Errorf("%s tree: want the node refusal, got %v", s.name, err)
		}
	}
}

// MaxBytes on the same terms, for string content.
func TestLiveBytesStreamed(t *testing.T) {
	for _, s := range []struct{ name, body string }{{"L2", liveL2}, {"L6", liveL6}} {
		opts := TransformOptions{MaxBytes: 4096}
		got, err := liveRun(t, s.body, 2000, true, opts)
		if err != nil || !strings.Contains(got, `<done n="96000"/>`) {
			t.Errorf("%s streamed: %q, %v", s.name, got, err)
		}
		if _, err := liveRun(t, s.body, 2000, false, opts); !refused(err, "more than 4096 bytes") {
			t.Errorf("%s tree: want the byte refusal, got %v", s.name, err)
		}
	}
}

// What an item carries on stays charged, so growing state is refused by the
// held bound however much is handed back.
func TestLiveGrowingRefused(t *testing.T) {
	if _, err := liveRun(t, liveG8, 2000, true,
		TransformOptions{MaxNodes: 500, MaxNodesBuilt: -1}); !refused(err, "more than 500 result-tree nodes") {
		t.Errorf("G8: want the node refusal, got %v", err)
	}
	if _, err := liveRun(t, liveG4, 2000, true,
		TransformOptions{MaxBytes: 4096, MaxBytesBuilt: -1}); !refused(err, "more than 4096 bytes") {
		t.Errorf("G4: want the byte refusal, got %v", err)
	}
	// Here no one item builds much: only keeping the carried state charged
	// refuses them.
	if _, err := liveRun(t, liveG5, 2000, true,
		TransformOptions{MaxBytes: 4096, MaxBytesBuilt: -1}); !refused(err, "more than 4096 bytes") {
		t.Errorf("G5: want the byte refusal, got %v", err)
	}
	if _, err := liveRun(t, liveG5n, 2000, true,
		TransformOptions{MaxNodes: 500, MaxNodesBuilt: -1}); !refused(err, "more than 500 result-tree nodes") {
		t.Errorf("G5 elements: want the node refusal, got %v", err)
	}
	// Under the bound, they run.
	for _, b := range []string{liveG5, liveG5n} {
		if got, err := liveRun(t, b, 400, true, TransformOptions{MaxNodes: 500, MaxBytes: 4096}); err != nil || !strings.Contains(got, `<done n="400"/>`) {
			t.Errorf("under the bound: %q, %v", got, err)
		}
	}
}

// MaxNodesBuilt: by default an item hands back at most liveNodeAllowance, so
// G8, whose work per item grows, is refused by the default bound well before
// what it holds reaches it; negative lifts that, and a positive value bounds
// the total instead.
func TestLiveNodesBuilt(t *testing.T) {
	const n = 1800 // holds 10,800 nodes at the end, builds about 9.7 million
	if _, err := liveRun(t, liveG8, n, true, TransformOptions{}); !refused(err, "more than 2000000 result-tree nodes") {
		t.Errorf("default: want the node refusal, got %v", err)
	}
	got, err := liveRun(t, liveG8, n, true, TransformOptions{MaxNodesBuilt: -1})
	if err != nil || !strings.Contains(got, fmt.Sprintf(`<done n="%d"/>`, n)) {
		t.Errorf("negative: %q, %v", got, err)
	}
	if _, err := liveRun(t, liveG8, n, true, TransformOptions{MaxNodesBuilt: 3_000_000}); !refused(err, "more than 3000000 result-tree nodes") {
		t.Errorf("positive: want the total refusal, got %v", err)
	}
	if _, err := liveRun(t, liveV10, 2000, true, TransformOptions{MaxNodes: 500, MaxNodesBuilt: 20_000_000}); err != nil {
		t.Errorf("positive above the work: %v", err)
	}
}

// MaxBytesBuilt bounds every byte built, whatever is handed back.
func TestLiveBytesBuilt(t *testing.T) {
	if _, err := liveRun(t, liveL6, 2000, true, TransformOptions{MaxBytes: 4096, MaxBytesBuilt: 50_000}); !refused(err, "more than 50000 bytes") {
		t.Errorf("want the total refusal, got %v", err)
	}
	if _, err := liveRun(t, liveL6, 2000, true, TransformOptions{MaxBytes: 4096, MaxBytesBuilt: -1}); err != nil {
		t.Errorf("negative: %v", err)
	}
}

// fn:copy-of and fn:snapshot are charged as xsl:copy-of is; they were not
// charged at all.
func TestCopyFunctionsCharged(t *testing.T) {
	for _, f := range []string{"copy-of", "snapshot"} {
		body := `<n><xsl:value-of select="count(doc/rec ! ` + f + `(.))"/></n>`
		_, err := liveRun(t, body, 200, false, TransformOptions{MaxNodes: 500})
		if !refused(err, "more than 500 result-tree nodes") {
			t.Errorf("%s: want the node refusal, got %v", f, err)
		}
	}
}

// An item that writes to its output, or forces a global variable, keeps
// charged everything it built: the output and the global's value outlive it.
func TestLiveKeepsWhatItHolds(t *testing.T) {
	opts := TransformOptions{MaxNodes: 500}
	out := `<out><xsl:for-each select="doc/rec"><r/></xsl:for-each></out>`
	if _, err := liveRun(t, out, 600, true, opts); !refused(err, "more than 500 result-tree nodes") {
		t.Errorf("output: want the node refusal, got %v", err)
	}
	// $g, 301 nodes, is built inside the first item and copied after the
	// loop: 602 nodes held.
	global := `<xsl:source-document streamable="yes" href="doc.xml"><xsl:for-each select="doc/rec">` +
		`<xsl:if test="count($g/e) lt 0">x</xsl:if></xsl:for-each></xsl:source-document><xsl:copy-of select="$g"/>`
	_, err := liveRunGlobal(t, global, `<xsl:variable name="g"><xsl:for-each select="1 to 300"><e/></xsl:for-each></xsl:variable>`, opts)
	if !refused(err, "more than 500 result-tree nodes") {
		t.Errorf("global: want the node refusal, got %v", err)
	}
}

// liveRunGlobal runs template main, body, over 10 records, with a top-level
// declaration.
func liveRunGlobal(t *testing.T, body, decl string, opts TransformOptions) (string, error) {
	t.Helper()
	dir := t.TempDir()
	doc := "<doc>" + strings.Repeat(`<rec k="K"><a>t</a></rec>`, 10) + "</doc>"
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + decl +
		`<xsl:template name="main">` + body + `</xsl:template></xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	traceBursts = func(string) { ran = true }
	defer func() { traceBursts = nil }()
	opts.InitialTemplate, opts.Documents = "main", docs
	res, err := sheet.Transform(context.Background(), nil, opts)
	if !ran {
		t.Fatal("bursts did not run")
	}
	if err != nil {
		return "", err
	}
	return res.String(), nil
}
