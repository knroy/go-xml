package xslt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// §15.4 gives xsl:merge-source/@streamable a default that is not "no": "Any
// input to a merging operation, provided it is selected by means of the
// xsl:merge-source element with a for-each-stream attribute, may be
// designated as streamable by including the attribute streamable="yes" ...
// This is also the default value when the for-each-stream attribute is
// present." (for-each-stream is the working draft's spelling of
// for-each-source.) XTSE3195 says the same thing as a constraint: "If the
// for-each-stream attribute is present, the only permitted value (and the
// default value) of the streamable attribute is yes."
//
// Neither half was implemented. compileMergeSource left src.streamed false
// unless the attribute said otherwise, and accepted streamable="no" beside
// for-each-source without complaint. Both were invisible to the W3C suite:
// every merge case that writes for-each-source also writes streamable (some
// through a _streamable shadow attribute), and merge-064, which names
// XTSE0020 for the forbidden combination, spells the value "No" -- so the
// lexical-form check answered it and the rule itself was never reached.
//
// The default matters because §15.4 makes streamable="yes" change the answer
// rather than the strategy: the select expression "is implicitly used as the
// argument of a call on the snapshot function ... whether or not streamed
// processing is actually used, and whether or not the processor supports
// streaming", and an attempt to navigate outside that snapshot "will
// typically not cause an error, but will return empty results".

// mergeDocs is a DocumentResolver over a fixed map, so the test needs no
// files on disk and no temporary directory to clean up.
type mergeDocs map[string]string

func (m mergeDocs) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		uri = uri[i+1:]
	}
	s, ok := m[uri]
	if !ok {
		return nil, fmt.Errorf("FODC0002: no document %q", uri)
	}
	return xdm.ParseString(s, xdm.ParseOptions{})
}

// mergeSnapshotSheet selects log/e from two documents and, from inside
// xsl:merge-action, steps up to the parent's preceding <meta> sibling -- a
// node a snapshot of the selected element does not carry. Under the defaulted
// streamable="yes" that step is empty; without the snapshot the real tree
// answers with the document's own marker.
func mergeSnapshotSheet(streamable string) string {
	return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
<xsl:output method="xml" omit-xml-declaration="yes"/>
<xsl:template match="/"><out><xsl:merge>
  <xsl:merge-source for-each-source="'A','B'" select="log/e"` + streamable + `>
    <xsl:merge-key select="@t"/>
  </xsl:merge-source>
  <xsl:merge-action>
    <xsl:for-each select="current-merge-group()">
      <i t="{@t}" meta="{../meta}"/>
    </xsl:for-each>
  </xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
}

func runMergeSnapshot(t *testing.T, sheet string) (string, error) {
	t.Helper()
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		return "", err
	}
	src, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	docs := mergeDocs{
		"A": `<log><meta>M-A</meta><e t="1">a1</e><e t="3">a3</e></log>`,
		"B": `<log><meta>M-B</meta><e t="2">b2</e><e t="4">b4</e></log>`,
	}
	res, err := st.Transform(context.Background(), src.Root,
		TransformOptions{Documents: docs})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatalf("serializing: %v", err)
	}
	return b.String(), nil
}

func TestMergeSourceStreamableDefaultsToYes(t *testing.T) {
	// The snapshot answer: every step out of the selected element is empty.
	const snapshotted = `<out><i t="1" meta=""/><i t="2" meta=""/>` +
		`<i t="3" meta=""/><i t="4" meta=""/></out>`

	explicit, err := runMergeSnapshot(t, mergeSnapshotSheet(` streamable="yes"`))
	if err != nil {
		t.Fatalf(`streamable="yes": %v`, err)
	}
	if explicit != snapshotted {
		t.Fatalf("streamable=\"yes\" did not snapshot the selected nodes:\n got %s\nwant %s",
			explicit, snapshotted)
	}

	// The point of the test: an absent @streamable beside for-each-source
	// must behave identically, because §15.4 makes yes its default there.
	defaulted, err := runMergeSnapshot(t, mergeSnapshotSheet(``))
	if err != nil {
		t.Fatalf("for-each-source with no @streamable: %v", err)
	}
	if defaulted != explicit {
		t.Errorf("an absent @streamable beside for-each-source did not default "+
			"to yes:\n got %s\nwant %s (the streamable=\"yes\" answer)",
			defaulted, explicit)
	}
}

func TestMergeSourceStreamableFalseIsAcceptedWithForEachSource(t *testing.T) {
	// XTSE3195's last clause says "If the for-each-stream attribute is
	// present, the only permitted value (and the default value) of the
	// streamable attribute is yes." This engine does NOT enforce it, and the
	// W3C suite is why: merge-065b and merge-066 write for-each-source beside
	// streamable="false" and expect the transform to run, and merge-067
	// expects XTDE3362 from running it. Enforcing the clause fails all three.
	//
	// merge-064 looks like the case for enforcing it, but it is satisfied
	// without: it spells the value "No", which the lexical check refuses
	// first as not a boolean.
	//
	// This is pinned as a test because enforcing the clause is a natural
	// reading of the working draft, and it has now been tried and measured
	// twice. The suite is the tiebreak.
	for _, v := range []string{"no", "false", "0"} {
		if _, err := runMergeSnapshot(t, mergeSnapshotSheet(` streamable="`+v+`"`)); err != nil {
			t.Errorf("streamable=%q beside for-each-source was refused: %v", v, err)
		}
	}

	// "No" is not a boolean, and is refused for that reason alone.
	_, err := runMergeSnapshot(t, mergeSnapshotSheet(` streamable="No"`))
	if err == nil {
		t.Error(`streamable="No" compiled; it is not a lexical boolean`)
	} else if !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf(`streamable="No": want XTSE0020, got: %v`, err)
	}

	// The constraint is keyed to for-each-source. A for-each-item source
	// does not stream, so streamable="no" states exactly that and stays
	// legal -- merge-073 and merge-082 both write it, and the suite lists
	// them as success cases.
	sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
<xsl:output method="xml" omit-xml-declaration="yes"/>
<xsl:template match="/"><out><xsl:merge>
  <xsl:merge-source for-each-item="doc('A')" select="log/e" streamable="no">
    <xsl:merge-key select="@t"/>
  </xsl:merge-source>
  <xsl:merge-action><i t="{current-merge-key()}"/></xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
	got, err := runMergeSnapshot(t, sheet)
	if err != nil {
		t.Fatalf(`streamable="no" beside for-each-item was refused: %v`, err)
	}
	if want := `<out><i t="1"/><i t="3"/></out>`; got != want {
		t.Errorf("for-each-item merge: got %s, want %s", got, want)
	}
}
