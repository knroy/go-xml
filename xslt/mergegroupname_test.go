package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// mergeGroupNameSheet builds an xsl:merge over two named sources whose action
// asks for the group named by group, so one stylesheet shape serves every case
// below. The input of each source is deliberately unsorted on its merge key,
// which is what makes the static half of the rule observable: a runtime report
// of XTDE3490 can only happen inside the action, and this input never reaches
// it because XTDE2220 rejects the sequence first.
func mergeGroupNameSheet(group string) string {
	return `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:merge>
	      <xsl:merge-source name="a" select="$one/r/x">
	        <xsl:merge-key select="."/>
	      </xsl:merge-source>
	      <xsl:merge-source name="b" select="$two/r/x">
	        <xsl:merge-key select="."/>
	      </xsl:merge-source>
	      <xsl:merge-action>
	        <xsl:variable name="g" select="current-merge-group(` + group + `)"/>
	        <g n="{count($g)}"/>
	      </xsl:merge-action>
	    </xsl:merge></out>
	  </xsl:template>
	  <xsl:variable name="one"><r><x>3</x><x>1</x></r></xsl:variable>
	  <xsl:variable name="two"><r><x>3</x><x>1</x></r></xsl:variable>
	</xsl:stylesheet>`
}

// TestMergeGroupUnknownNameIsStaticXTDE3490 pins the rule of XSLT 3.0 15.6.1:
// "It is a dynamic error if the $source argument of the current-merge-group
// function does not match the name attribute of any xsl:merge-source element
// for the current merge operation. The error may be reported statically if it
// can be detected statically."
//
// A string literal is detectable statically, so a name matching no source must
// be XTDE3490 -- and, because the error is allowed to be static, it must be
// reported even though nothing in this stylesheet ever evaluates the action.
// The suite's merge-077 is this case: its input is also unsorted, so before
// the check existed the transform reported XTDE2220 and the required error was
// unreachable.
func TestMergeGroupUnknownNameIsStaticXTDE3490(t *testing.T) {
	_, err := Compile(mustParse(t, mergeGroupNameSheet(`'population'`)),
		CompileOptions{})
	if err == nil {
		t.Fatal("Compile accepted current-merge-group('population'), " +
			"which names no xsl:merge-source; want XTDE3490")
	}
	if code := xdm.ErrorCode(err); code != "XTDE3490" {
		t.Fatalf("error code = %s, want XTDE3490: %v", code, err)
	}
	// The message must name the source that was asked for, because with two
	// sources declared the name is the only thing that identifies the fault.
	if !strings.Contains(err.Error(), "population") {
		t.Errorf("error does not name the unknown source: %v", err)
	}
}

// TestMergeGroupKnownNameCompiles is the other side of the same rule: the
// error is about a name that matches *no* xsl:merge-source, so a name that
// matches one must compile. Without this, a check that rejected every literal
// would pass the test above and still be wrong.
func TestMergeGroupKnownNameCompiles(t *testing.T) {
	for _, name := range []string{"a", "b"} {
		if _, err := Compile(mustParse(t, mergeGroupNameSheet(`'`+name+`'`)),
			CompileOptions{}); err != nil {
			t.Errorf("Compile rejected current-merge-group(%q), which names a "+
				"declared xsl:merge-source: %v", name, err)
		}
	}
}

// TestMergeGroupComputedNameIsNotStatic holds the line the error's own wording
// draws. "May be reported statically if it can be detected statically" gives
// no licence over a name that is not known until the action runs, so an
// expression argument must compile and leave the decision to the runtime
// check. Rejecting it here would refuse a stylesheet that is legal whenever
// the expression yields a declared name.
func TestMergeGroupComputedNameIsNotStatic(t *testing.T) {
	if _, err := Compile(mustParse(t, mergeGroupNameSheet(`concat('a', '')`)),
		CompileOptions{}); err != nil {
		t.Fatalf("Compile rejected a computed current-merge-group() argument, "+
			"whose value is not statically detectable: %v", err)
	}
}

// TestMergeGroupUnknownNameStillRaisesAtRuntime keeps the static check from
// being mistaken for the whole rule. The normative report is the dynamic one,
// so a merge whose input *is* sorted -- and whose action therefore runs --
// must still raise XTDE3490 for a name written where the static scan does not
// look. @select is scanned; an attribute value template is not.
func TestMergeGroupUnknownNameStillRaisesAtRuntime(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:merge>
	      <xsl:merge-source name="a" select="$one/r/x">
	        <xsl:merge-key select="."/>
	      </xsl:merge-source>
	      <xsl:merge-action>
	        <g n="{count(current-merge-group('population'))}"/>
	      </xsl:merge-action>
	    </xsl:merge></out>
	  </xsl:template>
	  <xsl:variable name="one"><r><x>1</x><x>2</x></r></xsl:variable>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		// Reporting it statically is also permitted, so a static XTDE3490 is
		// a correct outcome here -- any other error is not.
		if code := xdm.ErrorCode(err); code != "XTDE3490" {
			t.Fatalf("Compile error code = %s, want XTDE3490: %v", code, err)
		}
		return
	}
	_, err = sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err == nil {
		t.Fatal("the transform succeeded; current-merge-group('population') " +
			"names no xsl:merge-source and must raise XTDE3490")
	}
	if code := xdm.ErrorCode(err); code != "XTDE3490" {
		t.Fatalf("error code = %s, want XTDE3490: %v", code, err)
	}
}

// TestMergeGroupNestedMergeNamesAreItsOwn covers the scoping the walk has to
// respect. 15.6.1 binds the current merge group to the innermost containing
// xsl:merge-action, so "inner" is a legal name for the nested merge's action
// and says nothing about the outer merge's sources. A scan that descended into
// the nested xsl:merge would see a name the outer merge does not declare and
// reject a stylesheet that is correct.
func TestMergeGroupNestedMergeNamesAreItsOwn(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:merge>
	      <xsl:merge-source name="outer" select="$one/r/x">
	        <xsl:merge-key select="."/>
	      </xsl:merge-source>
	      <xsl:merge-action>
	        <xsl:merge>
	          <xsl:merge-source name="inner" select="$two/r/x">
	            <xsl:merge-key select="."/>
	          </xsl:merge-source>
	          <xsl:merge-action>
	            <xsl:variable name="g" select="current-merge-group('inner')"/>
	            <g n="{count($g)}"/>
	          </xsl:merge-action>
	        </xsl:merge>
	      </xsl:merge-action>
	    </xsl:merge></out>
	  </xsl:template>
	  <xsl:variable name="one"><r><x>1</x></r></xsl:variable>
	  <xsl:variable name="two"><r><x>1</x></r></xsl:variable>
	</xsl:stylesheet>`
	if _, err := Compile(mustParse(t, src), CompileOptions{}); err != nil {
		t.Fatalf("Compile rejected a nested xsl:merge whose action names its "+
			"own source: %v", err)
	}
}
