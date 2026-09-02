package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Tests for xsl:assert, XSLT 3.0 section 22.2.

// wrap30 is wrap for a stylesheet that needs XSLT 3.0 constructs. xsl:assert is
// one: the element table marks it since30, so a version="2.0" module writing it
// gets XTSE0010 rather than an assertion.
func wrap30(body string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output omit-xml-declaration="yes"/>` + body + `</xsl:stylesheet>`
}

// runAssert compiles and runs a stylesheet against <a/>, returning the result
// and the error rather than failing on one.
func runAssert(t *testing.T, sheet string, opts TransformOptions) (string, []string, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	dtree, err := xdm.ParseString(`<a/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	res, err := s.Transform(context.Background(), dtree.Root, opts)
	if err != nil {
		return "", nil, err
	}
	return res.String(), res.Messages, nil
}

// 22.2 rule 1: "If the effective boolean value of the result is true, the
// assertion succeeds, and no further action is taken." No error, no message,
// and — "The result of the xsl:assert instruction is an empty sequence" —
// nothing in the output either.
func TestAssertPasses(t *testing.T) {
	got, msgs, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="true()">never seen</xsl:assert>
		<r/>
	</xsl:template>`), TransformOptions{})
	if err != nil {
		t.Fatalf("a passing assertion raised %v", err)
	}
	if got != "<r/>" {
		t.Errorf("result = %q, want <r/>: an assertion's result is the empty sequence", got)
	}
	if len(msgs) != 0 {
		t.Errorf("messages = %v, want none: a passing assertion takes no further action", msgs)
	}
}

// "The default error code is XTMM9001; this may be overridden using the
// error-code attribute."
func TestAssertFailsWithDefaultCode(t *testing.T) {
	_, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="false()"/>
		<r/>
	</xsl:template>`), TransformOptions{})
	if err == nil {
		t.Fatal("a failing assertion produced no error")
	}
	if !strings.Contains(err.Error(), "XTMM9001") {
		t.Errorf("error = %v, want XTMM9001", err)
	}
	// The distinguishing half: xsl:message's default code must not leak in.
	if strings.Contains(err.Error(), "XTMM9000") {
		t.Errorf("error = %v, want the assert default rather than the message default", err)
	}
}

// "the effect of the instruction is governed by the rules for evaluation of an
// xsl:message instruction with the same ... error-code attribute".
func TestAssertFailsWithExplicitErrorCode(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0"` +
		` xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` +
		` xmlns:err="http://www.w3.org/2005/xqt-errors">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:template match="/">` +
		`<xsl:assert test="false()" error-code="err:XTMM9999"/>` +
		`</xsl:template></xsl:stylesheet>`
	_, _, err := runAssert(t, sheet, TransformOptions{})
	if err == nil {
		t.Fatal("a failing assertion produced no error")
	}
	if !strings.Contains(err.Error(), "XTMM9999") {
		t.Errorf("error = %v, want the code named by @error-code", err)
	}
	if strings.Contains(err.Error(), "XTMM9001") {
		t.Errorf("error = %v, want @error-code to override the default", err)
	}
}

// "governed by the rules for evaluation of an xsl:message instruction with the
// same select attribute": @select supplies the message.
func TestAssertSelectSuppliesTheMessage(t *testing.T) {
	_, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="false()" select="'from select'"/>
	</xsl:template>`), TransformOptions{})
	if err == nil {
		t.Fatal("a failing assertion produced no error")
	}
	if !strings.Contains(err.Error(), "from select") {
		t.Errorf("error = %v, want the text of @select", err)
	}
}

// "... and contained sequence constructor": with no @select, the content
// supplies the message, exactly as it does for xsl:message.
func TestAssertContentSuppliesTheMessage(t *testing.T) {
	_, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="false()">from <xsl:text>content</xsl:text></xsl:assert>
	</xsl:template>`), TransformOptions{})
	if err == nil {
		t.Fatal("a failing assertion produced no error")
	}
	if !strings.Contains(err.Error(), "from content") {
		t.Errorf("error = %v, want the text of the sequence constructor", err)
	}
}

// With neither @select nor content, the message is empty and the assertion
// still fails: 22.2 makes the message an xsl:message's, and an xsl:message with
// nothing to say still terminates when told to.
func TestAssertFailsWithNoMessage(t *testing.T) {
	_, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="1 eq 2"/>
	</xsl:template>`), TransformOptions{})
	if err == nil {
		t.Fatal("an assertion with no message text did not fail")
	}
	if !strings.Contains(err.Error(), "XTMM9001") {
		t.Errorf("error = %v, want XTMM9001", err)
	}
}

// "An implementation should provide an external mechanism to disable assertion
// checking for the stylesheet as a whole."
func TestAssertDisableSwitchSuppressesTheFailure(t *testing.T) {
	sheet := wrap30(`<xsl:template match="/">
		<xsl:assert test="false()">would have failed</xsl:assert>
		<r/>
	</xsl:template>`)

	// Enabled by default: "By default, assertions are enabled."
	if _, _, err := runAssert(t, sheet, TransformOptions{}); err == nil {
		t.Fatal("the zero TransformOptions did not leave assertions enabled")
	}

	got, msgs, err := runAssert(t, sheet, TransformOptions{DisableAssertions: true})
	if err != nil {
		t.Fatalf("DisableAssertions did not suppress the failure: %v", err)
	}
	if got != "<r/>" {
		t.Errorf("result = %q, want the transformation to run to completion", got)
	}
	// A disabled assertion is skipped whole: it does not record a message
	// either, because it is not evaluated at all.
	if len(msgs) != 0 {
		t.Errorf("messages = %v, want none from a disabled assertion", msgs)
	}
}

// 22.2 rule 1 again: "or if a dynamic error occurs during evaluation of the
// expression, then the assertion fails." The assertion's own code surfaces,
// not the test expression's.
func TestAssertTestErrorIsAFailure(t *testing.T) {
	_, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="1 div 0 eq 1"/>
	</xsl:template>`), TransformOptions{})
	if err == nil {
		t.Fatal("an assertion whose test raised did not fail")
	}
	if !strings.Contains(err.Error(), "XTMM9001") {
		t.Errorf("error = %v, want the assertion's code rather than the test's", err)
	}
}

// "As with any other dynamic error, an error caused by an assertion failing may
// be trapped using xsl:try."
func TestAssertIsCatchableByTry(t *testing.T) {
	got, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:try>
			<xsl:assert test="false()">boom</xsl:assert>
			<never/>
			<xsl:catch><caught code="{$err:code}"/></xsl:catch>
		</xsl:try>
	</xsl:template>`), TransformOptions{})
	if err != nil {
		t.Fatalf("xsl:try did not trap the assertion failure: %v", err)
	}
	if !strings.Contains(got, "XTMM9001") {
		t.Errorf("result = %q, want the caught code", got)
	}
}

// The element table marks xsl:assert since30, so a processor capped at XSLT 2.0
// must not recognise it: it is XTSE0010 there, exactly as it is for xsl:try and
// every other element 3.0 introduced. Recognising it because this engine also
// implements 3.0 would accept a stylesheet every conforming 2.0 processor
// rejects.
//
// The cap is what decides, not the module's own @version: xpathFloor raises a
// version="2.0" module to the 3.1 grammar when the processor implements 3.0,
// which is why CompileOptions.MaxVersion is set here.
func TestAssertIsNotAnXSLT20Element(t *testing.T) {
	stree, err := xdm.ParseString(wrap(`<xsl:template match="/">
		<xsl:assert test="true()"/>
	</xsl:template>`), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if _, err := Compile(stree.Root, CompileOptions{MaxVersion: 2.0}); err == nil {
		t.Error("an XSLT 2.0 processor compiled an xsl:assert")
	}
}

// @test is written without a question mark in the syntax summary, so it is
// required. An assertion missing it must be rejected rather than compiled into
// an instruction that can never fail.
func TestAssertRequiresTest(t *testing.T) {
	stree, err := xdm.ParseString(wrap30(`<xsl:template match="/">
		<xsl:assert select="'x'"/>
	</xsl:template>`), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if _, err := Compile(stree.Root, CompileOptions{}); err == nil {
		t.Error("xsl:assert compiled without a test attribute")
	}
}

// use-when is the first of the two ways 22.2 gives for disabling assertions,
// and needs nothing of its own: a false use-when removes the element before
// compilation, so the assertion never runs.
func TestAssertDisabledByUseWhen(t *testing.T) {
	got, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<xsl:assert test="false()" use-when="false()">never</xsl:assert>
		<r/>
	</xsl:template>`), TransformOptions{})
	if err != nil {
		t.Fatalf("a use-when=\"false()\" assertion still fired: %v", err)
	}
	if got != "<r/>" {
		t.Errorf("result = %q, want <r/>", got)
	}
}

// element-available('xsl:assert') answers for the instruction now that it
// exists. catalog-006 scans every stylesheet in the W3C suite and requires true
// for every XSLT element any of them writes.
func TestAssertIsElementAvailable(t *testing.T) {
	got, _, err := runAssert(t, wrap30(`<xsl:template match="/">
		<r><xsl:value-of select="element-available('xsl:assert')"/></r>
	</xsl:template>`), TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "<r>true</r>" {
		t.Errorf("element-available('xsl:assert') = %q, want true", got)
	}
}
