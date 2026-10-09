package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// -max-items and -max-bytes reach the evaluation and the parse, in the
// transform and in the xquery subcommand, with 0 the default and a negative
// value no bound.
func TestMaxItemsAndMaxBytesFlags(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<r>`+strings.Repeat("<b/>", 20)+`</r>`)
	q := filepath.Join(dir, "q.xq")
	writeFile(t, q, `<n>{ count((1 to 11)[. ge 0]) }</n>`)

	for _, c := range []struct {
		args []string
		want string // "" means success
	}{
		{nil, ""},
		{[]string{"-max-items", "10"}, "the 10 item limit"},
		{[]string{"-max-items", "-1"}, ""},
		{[]string{"-max-bytes", "10"}, "document exceeds 10 bytes"},
		{[]string{"-max-bytes", "-1"}, ""},
	} {
		_, err := runQuery(t, dir, append(append(c.args, "-q", q), in)...)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("xquery %v: error %v, want %q", c.args, err, c.want)
		}
	}

	bin := buildCLI(t)
	xsl := filepath.Join(dir, "t.xsl")
	writeFile(t, xsl, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="/"><n><xsl:value-of select="count((1 to 11)[. ge 0])"/></n></xsl:template>
</xsl:stylesheet>`)
	for _, c := range []struct {
		args []string
		want string // the output, or the error text
	}{
		{nil, "<n>11</n>"},
		{[]string{"-max-items", "10"}, "the 10 item limit"},
		{[]string{"-max-items", "-1"}, "<n>11</n>"},
		{[]string{"-max-bytes", "10"}, "document exceeds 10 bytes"},
		{[]string{"-max-bytes", "-1"}, "<n>11</n>"},
	} {
		out, err := exec.Command(bin, append(append([]string{"-xsl", xsl}, c.args...), in)...).CombinedOutput()
		ok := err == nil
		if !strings.Contains(string(out), c.want) || ok != strings.HasPrefix(c.want, "<n>") {
			t.Errorf("transform %v: %v\n%s\nwant %q", c.args, err, out, c.want)
		}
	}
}
