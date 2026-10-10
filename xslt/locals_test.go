package xslt

import (
	"context"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Locals are bound on one stack per transform (locals.go). These are the
// scoping rules the stack has to keep: a binding is visible from its
// declaration to the end of its sequence constructor, a template, function or
// global body sees none of its invoker's, and a value that outlives a scope
// keeps what it captured.
func TestLocalStackScoping(t *testing.T) {
	const head = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="f xs">
		<xsl:output method="text"/>`
	cases := []struct{ name, body, want string }{
		{"a called template does not see its caller's locals", `
			<xsl:variable name="x" select="'global'"/>
			<xsl:template match="/"><xsl:variable name="x" select="'local'"/>
				<xsl:value-of select="$x"/>|<xsl:call-template name="t"/>|<xsl:apply-templates select="r"/></xsl:template>
			<xsl:template name="t"><xsl:param name="p" select="$x"/><xsl:value-of select="$x, $p"/></xsl:template>
			<xsl:template match="r"><xsl:value-of select="$x"/></xsl:template>`,
			"local|global global|global"},
		{"a function sees neither its caller's locals nor its range variables", `
			<xsl:variable name="y" select="'global'"/>
			<xsl:function name="f:g"><xsl:param name="x"/><xsl:sequence select="$x, $y"/></xsl:function>
			<xsl:template match="/"><xsl:variable name="y" select="'local'"/>
				<xsl:value-of select="let $x := 1 return f:g(2), $y"/></xsl:template>`,
			"2 global local"},
		{"a global first read under a local of its name", `
			<xsl:variable name="a" select="$x"/>
			<xsl:variable name="x" select="'global'"/>
			<xsl:template match="/"><xsl:variable name="x" select="'local'"/><xsl:value-of select="$x, $a"/></xsl:template>`,
			"local global"},
		{"an attribute set does not see the locals where it is used", `
			<xsl:variable name="x" select="'global'"/>
			<xsl:attribute-set name="s"><xsl:attribute name="a" select="$x"/></xsl:attribute-set>
			<xsl:template match="/"><xsl:variable name="x" select="'local'"/>
				<xsl:variable name="e"><e xsl:use-attribute-sets="s" b="{$x}"/></xsl:variable>
				<xsl:value-of select="$e/e/@a, $e/e/@b"/></xsl:template>`,
			"global local"},
		{"shadowing, and the outer binding back after the inner scope", `
			<xsl:template match="/"><xsl:variable name="v" select="1"/>
				<xsl:for-each select="1 to 2"><xsl:variable name="v" select="$v + 10 * ."/><xsl:value-of select="$v"/>,</xsl:for-each>
				<xsl:if test="true()"><xsl:variable name="v" select="'if'"/><xsl:value-of select="$v"/>,</xsl:if>
				<xsl:value-of select="$v, for $v in 7 return $v, $v"/></xsl:template>`,
			"11,21,if,1 7 1"},
		{"closures capture each iteration's binding", `
			<xsl:template match="/">
				<xsl:variable name="fs" as="function(*)*"><xsl:for-each select="1 to 3">
					<xsl:variable name="i" select="."/><xsl:sequence select="function() { $i * 10 }"/></xsl:for-each></xsl:variable>
				<xsl:variable name="i" select="'later'"/>
				<xsl:value-of select="$fs ! .(), $i"/></xsl:template>`,
			"10 20 30 later"},
		{"an inline function escaping the template that made it", `
			<xsl:template name="mk"><xsl:param name="n"/><xsl:variable name="m" select="$n * 2"/>
				<xsl:sequence select="function($x) { $x + $n + $m }"/></xsl:template>
			<xsl:template match="/">
				<xsl:variable name="f" as="function(*)"><xsl:call-template name="mk"><xsl:with-param name="n" select="5"/></xsl:call-template></xsl:variable>
				<xsl:variable name="n" select="100"/><xsl:variable name="m" select="1000"/>
				<xsl:value-of select="$f(1), $n, $m"/></xsl:template>`,
			"16 100 1000"},
		{"recursion restores each level's parameters and locals", `
			<xsl:function name="f:fact" as="xs:integer"><xsl:param name="n" as="xs:integer"/>
				<xsl:variable name="m" select="$n - 1"/>
				<xsl:sequence select="if ($n le 1) then 1 else $n * f:fact($m)"/></xsl:function>
			<xsl:template name="t"><xsl:param name="n"/><xsl:variable name="before" select="$n"/>
				<xsl:value-of select="$n"/><xsl:if test="$n gt 0"><xsl:call-template name="t">
					<xsl:with-param name="n" select="$n - 1"/></xsl:call-template></xsl:if><xsl:value-of select="$before"/></xsl:template>
			<xsl:template match="/"><xsl:call-template name="t"><xsl:with-param name="n" select="3"/></xsl:call-template>
				<xsl:value-of select="concat(' ', f:fact(5))"/></xsl:template>`,
			"32100123 120"},
		{"xsl:iterate parameters, locals, next-iteration and on-completion", `
			<xsl:template match="/"><xsl:variable name="sum" select="'outer'"/>
				<xsl:iterate select="1 to 5"><xsl:param name="sum" select="0"/>
					<xsl:on-completion><xsl:value-of select="$sum"/></xsl:on-completion>
					<xsl:variable name="next" select="$sum + ."/>
					<xsl:choose><xsl:when test=". = 4"><xsl:break select="'broke at', $next"/></xsl:when>
					<xsl:otherwise><xsl:next-iteration><xsl:with-param name="sum" select="$next"/></xsl:next-iteration></xsl:otherwise></xsl:choose>
				</xsl:iterate>|<xsl:iterate select="1 to 3"><xsl:param name="p" select="0"/>
					<xsl:on-completion><xsl:value-of select="$p"/></xsl:on-completion>
					<xsl:next-iteration><xsl:with-param name="p" select="$p + ."/></xsl:next-iteration>
				</xsl:iterate>|<xsl:value-of select="$sum"/></xsl:template>`,
			"broke at 10|6|outer"},
		{"xsl:for-each-group with locals", `
			<xsl:template match="/"><xsl:for-each-group select="1 to 5" group-by=". mod 2">
				<xsl:variable name="k" select="current-grouping-key()"/>
				<xsl:value-of select="$k, sum(current-group())"/>;</xsl:for-each-group></xsl:template>`,
			"1 9;0 6;"},
		{"tunnel parameters pass a template whose local has their name", `
			<xsl:template match="/"><xsl:call-template name="b"><xsl:with-param name="t" select="'tunnel'" tunnel="yes"/></xsl:call-template></xsl:template>
			<xsl:template name="b"><xsl:variable name="t" select="'local'"/><xsl:value-of select="$t"/>|<xsl:call-template name="c"/></xsl:template>
			<xsl:template name="c"><xsl:param name="t" tunnel="yes"/><xsl:value-of select="$t"/></xsl:template>`,
			"local|tunnel"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := runErr(t, head+c.body+`</xsl:stylesheet>`, `<r/>`)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// A stylesheet function item that escapes a finished transform may be called
// from several goroutines; its parameters and locals must not share the
// transform's stack then. Run with -race.
func TestLocalsAfterTheTransformReturns(t *testing.T) {
	s := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:function name="f:add" visibility="public"><xsl:param name="a"/><xsl:param name="b"/>
			<xsl:variable name="s" select="$a + $b"/><xsl:sequence select="$s"/></xsl:function>
		<xsl:function name="f:get" visibility="public"><xsl:sequence select="function($a, $b) { f:add($a, $b) }"/></xsl:function>
	</xsl:stylesheet>`)
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialFunction: xdm.QName{URI: "urn:f", Local: "get"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := res.Nodes[0].(*xdm.FunctionItem)
	if !ok || len(res.Nodes) != 1 {
		t.Fatalf("got %v, want one function item", res.Nodes)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				v, err := fn.Invoke(xpath.NewContext(nil, xpath.Builtins()),
					[]xdm.Sequence{xdm.One(xdm.NewInteger(int64(g))), xdm.One(xdm.NewInteger(int64(i)))})
				if err != nil {
					t.Error(err)
					return
				}
				if len(v) != 1 || v[0].(*xdm.Atomic).String() != xdm.NewInteger(int64(g+i)).String() {
					t.Errorf("f:add(%d, %d) = %v", g, i, v)
					return
				}
			}
		}()
	}
	wg.Wait()
}
