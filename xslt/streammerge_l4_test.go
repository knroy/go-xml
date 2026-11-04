package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamMergeL4: streamed merges with last() in the action, messages
// told by the action, boolean keys, keys looked up in a global map of
// integers, and an error before anything is read (merge-043, -049, -084,
// -097s) give the tree's output, error and messages, and stream with no
// re-run unless the tree fails too.
func TestStreamMergeL4(t *testing.T) {
	dir := t.TempDir()
	for n, d := range streamMergeDocs {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const num = "xs:integer(@t)"
	two := mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'b.xml'", "log/e", num, "")
	cases := []struct {
		sources, action, decls string
		stream, rerun          bool
	}{
		{two, `<g p="{position()}" n="{last()}" k="{current-merge-key()}"/>`, "", true, false},
		{two, `<xsl:message select="current-merge-key()"/><g/>`, "", true, false},
		{two, `<xsl:message select="current-merge-key()"/><g v="{if (current-merge-key() = 7) then error() else 1}"/>`, "", true, true},
		{mergeSrc("A", "'a.xml'", "log/e", "true()", ""), `<g n="{count(current-merge-group())}"/>`, "", true, false},
		{mergeSrc("A", "'a.xml'", "log/e", "exists(n)", ""), `<g n="{count(current-merge-group())}"/>`, "", true, true}, // XTDE2220: false after true? (a3b has n)
		{mergeSrc("A", "'a.xml'", "log/e", "$m(string(@t))", ""), `<g k="{current-merge-key()}"/>`,
			`<xsl:variable name="m" as="map(*)" select="map{'1': 1, '3': 3, '7': 7}"/>`, true, false},
		// A collected source after a streamed one (merge-081/-082): document
		// order across the two, as both modes make the trees.
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + `<xsl:merge-source name="B" select="doc('b.xml')/log/e"><xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source>`,
			`<g v="{current-merge-group()/@v}" o="{count(current-merge-group()) gt 1 and current-merge-group()[1] &lt;&lt; current-merge-group()[last()]}"/>`, "", true, false},
		// The first streamed source's for-each-source fails: the tree's error, no re-run.
		{mergeSrc("A", "1 to 2", "log/e", num, ""), `<g/>`, "", true, false},
		// A message, and a key calling a stylesheet function: refused.
		{mergeSrc("A", "'a.xml'", "log/e", "f:k(@t)", ""), `<xsl:message select="1"/>`,
			`<xsl:function name="f:k" xmlns:f="urn:f"><xsl:param name="t"/><xsl:sequence select="xs:integer($t)"/></xsl:function>`, false, false},
	}
	for i, c := range cases {
		sheet := mergeSheet(c.sources, c.action)
		if c.decls != "" {
			sheet = sheet[:len(sheet)-len("</xsl:stylesheet>")] + c.decls + "</xsl:stylesheet>"
		}
		if i == len(cases)-1 {
			sheet = sheet[:len("<xsl:stylesheet")] + ` xmlns:f="urn:f"` + sheet[len("<xsl:stylesheet"):]
		}
		so, se, sm, st := msgRun(sheet, dir, true)
		to, te, tm, _ := msgRun(sheet, dir, false)
		where := fmt.Sprintf("case %d", i)
		if so != to || se != te || sm != tm {
			t.Errorf("%s:\nstream %q %q %q\ntree   %q %q %q", where, so, se, sm, to, te, tm)
		}
		if st.Guards != 0 {
			t.Errorf("%s: %d guard hits", where, st.Guards)
		}
		if ran := st.Runs[streamtrace.Merge] > 0; ran != c.stream {
			t.Errorf("%s: streamed %v, want %v", where, ran, c.stream)
		}
		if c.stream && !c.rerun && st.Reruns != 0 {
			t.Errorf("%s: %d re-runs", where, st.Reruns)
		}
	}
}

// TestStreamMergeAccums: accumulators on streamed merge sources (merge-051,
// -082): keys and actions reading them of the items' snapshots give the
// tree's output or error, streamed (a failing walk re-runs on the tree).
func TestStreamMergeAccums(t *testing.T) {
	dir := t.TempDir()
	for n, d := range streamMergeDocs {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const accs = `<xsl:accumulator name="c" as="xs:integer" initial-value="0">
	    <xsl:accumulator-rule match="e" select="$value + 1"/></xsl:accumulator>
	  <xsl:accumulator name="w" as="xs:string" initial-value="''">
	    <xsl:accumulator-rule match="e/n/text()" select="string()"/>
	    <xsl:accumulator-rule match="e" phase="end" select="$value || '.'"/></xsl:accumulator>
	  <xsl:accumulator name="p" initial-value="''">
	    <xsl:accumulator-rule match="*" select="concat(base-uri(/), ' - ', string-join(ancestor::*/name(), '/'))"/></xsl:accumulator>
	  <xsl:accumulator name="bad" as="xs:integer" initial-value="0">
	    <xsl:accumulator-rule match="e" select="xs:integer(@t) idiv (7 - xs:integer(@t))"/></xsl:accumulator>`
	cases := []struct {
		sources, action string
		rerun           bool
	}{
		{mergeSrc("A", "'a.xml'", "log/e", "accumulator-before('c')", `use-accumulators="c w"`) +
			mergeSrc("B", "'b.xml'", "log/e", "accumulator-before('c')", `use-accumulators="c"`),
			`<g k="{current-merge-key()}"><xsl:for-each select="current-merge-group()"><i a="{accumulator-after('c')}"/></xsl:for-each></g>`, false},
		{mergeSrc("A", "'a.xml'", "log/e", "xs:integer(@t)", `use-accumulators="c w"`),
			`<g><xsl:for-each select="current-merge-group()"><i w="{accumulator-after('w')}"/></xsl:for-each></g>`, false},
		// base-uri(/) in a rule (merge-065a).
		{mergeSrc("A", "('a.xml', 'b.xml')", "log/e", "xs:integer(@t)", `use-accumulators="p"`),
			`<g><xsl:for-each select="current-merge-group()"><i p="{accumulator-before('p')}"/></xsl:for-each></g>`, false},
		// Not applicable to B's documents: XTDE3362 in both.
		{mergeSrc("A", "'b.xml'", "log/e", "xs:integer(@t)", `use-accumulators="c"`),
			`<g w="{current-merge-group()[1]/accumulator-before('w')}"/>`, true},
		// A walk failing after the items read: the tree's error.
		{mergeSrc("A", "'a.xml'", "log/e", "xs:integer(@t)", `use-accumulators="bad"`),
			`<g b="{current-merge-group()[1]/accumulator-before('bad')}"/>`, true},
	}
	for i, c := range cases {
		sheet := mergeSheet(c.sources, c.action)
		sheet = sheet[:len(sheet)-len("</xsl:stylesheet>")] + accs + "</xsl:stylesheet>"
		so, se, sm, st := msgRun(sheet, dir, true)
		to, te, tm, _ := msgRun(sheet, dir, false)
		where := fmt.Sprintf("case %d", i)
		if so != to || se != te || sm != tm {
			t.Errorf("%s:\nstream %q %q %q\ntree   %q %q %q", where, so, se, sm, to, te, tm)
		}
		if st.Guards != 0 || st.Runs[streamtrace.Merge] == 0 {
			t.Errorf("%s: guards %d, merge runs %d", where, st.Guards, st.Runs[streamtrace.Merge])
		}
		if !c.rerun && st.Reruns != 0 {
			t.Errorf("%s: %d re-runs", where, st.Reruns)
		}
	}
}
