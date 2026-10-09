package xslt_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xslt"
)

// Global variables are evaluated on first use (see evalGlobals). These pin
// what that may and may not change.

func lazySheet(t *testing.T, body string) *xslt.Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">`+body+`</xsl:stylesheet>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	return sheet
}

func lazyRun(t *testing.T, sheet *xslt.Stylesheet, params map[string]xdm.Sequence) (*xslt.Result, error) {
	t.Helper()
	doc, err := xdm.ParseString(`<a/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{Params: params})
}

// A global nothing reads is not evaluated, so its failure is not raised.
func TestUnreferencedGlobalIsNotEvaluated(t *testing.T) {
	sheet := lazySheet(t, `<xsl:variable name="boom" select="error()"/>
	  <xsl:template match="/"><o/></xsl:template>`)
	if _, err := lazyRun(t, sheet, nil); err != nil {
		t.Fatalf("an unreferenced global was evaluated: %v", err)
	}
}

// Section 9.5: a global's failure "cannot be suppressed by use of xsl:try
// around a reference to the global variable". Evaluated on first use, the
// failure surfaces inside the try, which must let it through.
func TestGlobalFailureIsNotCaughtAroundAReference(t *testing.T) {
	sheet := lazySheet(t, `<xsl:variable name="boom" select="error(xs:QName('err:FOER0000'))"/>
	  <xsl:template match="/"><o><xsl:try><xsl:value-of select="$boom"/>
	    <xsl:catch>caught</xsl:catch></xsl:try></o></xsl:template>`)
	_, err := lazyRun(t, sheet, nil)
	if err == nil || xdm.ErrorCode(err) != "FOER0000" {
		t.Fatalf("got %v, want FOER0000 through the xsl:try", err)
	}
	// Inside the global's own initialiser a try still catches.
	sheet = lazySheet(t, `<xsl:variable name="ok"><xsl:try><xsl:sequence select="error()"/>
	    <xsl:catch>caught</xsl:catch></xsl:try></xsl:variable>
	  <xsl:template match="/"><o><xsl:value-of select="$ok"/></o></xsl:template>`)
	res, err := lazyRun(t, sheet, nil)
	if err != nil || !strings.Contains(xslt.SerializeAsXML(res), ">caught<") {
		t.Fatalf("got %v, want the initialiser's own try to catch", err)
	}
}

// A global that writes a message is still evaluated when nothing reads it.
func TestGlobalWithAMessageIsStillEvaluated(t *testing.T) {
	sheet := lazySheet(t, `<xsl:variable name="m"><xsl:message>hello</xsl:message></xsl:variable>
	  <xsl:template match="/"><o/></xsl:template>`)
	res, err := lazyRun(t, sheet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || !strings.Contains(res.Messages[0], "hello") {
		t.Fatalf("messages %q, want the global's message", res.Messages)
	}
}

// A global naming itself is XPST0008 whether or not anything reads it
// (higher-order-functions-070 never does).
func TestUnreferencedSelfReferenceIsStillReported(t *testing.T) {
	sheet := lazySheet(t, `<xsl:variable name="f" select="function($x) { $f($x) }"/>
	  <xsl:template match="/"><o/></xsl:template>`)
	if _, err := lazyRun(t, sheet, nil); xdm.ErrorCode(err) != "XPST0008" {
		t.Fatalf("got %v, want XPST0008", err)
	}
}

// Two globals defined in terms of each other are XTDE0640 when read, and a
// required parameter left unset is XTDE0050 though nothing reads it.
func TestGlobalCircularityAndRequiredParam(t *testing.T) {
	sheet := lazySheet(t, `<xsl:variable name="x" select="$y + 1"/>
	  <xsl:variable name="y" select="$x + 1"/>
	  <xsl:template match="/"><o><xsl:value-of select="$x"/></o></xsl:template>`)
	if _, err := lazyRun(t, sheet, nil); xdm.ErrorCode(err) != "XTDE0640" {
		t.Fatalf("got %v, want XTDE0640", err)
	}
	sheet = lazySheet(t, `<xsl:param name="p" required="yes"/>
	  <xsl:template match="/"><o/></xsl:template>`)
	if _, err := lazyRun(t, sheet, nil); xdm.ErrorCode(err) != "XTDE0050" {
		t.Fatalf("got %v, want XTDE0050", err)
	}
}

// One compiled stylesheet runs many transforms at once, each with its own
// parameter, and each evaluates its globals on its own. Run with -race.
func TestLazyGlobalsArePerTransform(t *testing.T) {
	sheet := lazySheet(t, `<xsl:param name="p" as="xs:integer"/>
	  <xsl:variable name="b" select="$a * 2"/>
	  <xsl:variable name="a" select="$p + 1"/>
	  <xsl:template match="/"><o><xsl:value-of select="$b"/></o></xsl:template>`)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				p := i*100 + j
				res, err := lazyRun(t, sheet, map[string]xdm.Sequence{
					"p": xdm.One(xdm.NewInteger(int64(p)))})
				if err != nil {
					t.Error(err)
					return
				}
				if got, want := xslt.SerializeAsXML(res), fmt.Sprintf(">%d<", (p+1)*2); !strings.Contains(got, want) {
					t.Errorf("p=%d: got %s, want %s", p, got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
