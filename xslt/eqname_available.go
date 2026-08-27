package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// splitEQName splits a Q{uri}local name into its two parts, reporting whether
// the string had that form at all.
//
// isEQName answers the same question without producing the pieces; this is the
// form needed where the name is about to be used rather than merely validated.
func splitEQName(s string) (uri, local string, ok bool) {
	if !strings.HasPrefix(s, "Q{") {
		return "", "", false
	}
	end := strings.IndexByte(s, '}')
	if end < 0 {
		return "", "", false
	}
	local = s[end+1:]
	if !xdm.IsNCName(local) {
		return "", "", false
	}
	return s[2:end], local, true
}

// availableName resolves the argument of fn:function-available,
// fn:type-available or fn:element-available to a namespace URI and local part,
// or reports the given error code for a name that is not a name at all.
//
// XSLT 3.0 20.1 gives these functions an argument that is "a lexical QName or
// an EQName": Q{uri}local carries its own namespace, so it needs no prefix
// resolution and answers even where nothing binds a prefix. XSLT 2.0 had no
// EQName syntax, so a processor running at 2.0 must still refuse the form —
// function-available-1011 and type-available-0150 are both scoped XSLT30+.
func availableName(
	ctx *xpath.Context, code, fn, name string,
	resolve func(string) (string, string, bool),
) (uri, local string, ok bool, err error) {
	if ctx != nil && ctx.Version.AtLeast30() {
		if u, l, isEQ := splitEQName(name); isEQ {
			return u, l, true, nil
		}
	}
	if err := checkAvailableArg(code, fn, name); err != nil {
		return "", "", false, err
	}
	u, l, found := resolve(name)
	return u, l, found, nil
}

// checkIterateOrder applies the ordering half of xsl:iterate's content model.
//
// Section 8.4 writes it as (xsl:param*, xsl:on-completion?,
// sequence-constructor): the parameters come first, the completion action
// after them, and both before any instruction. The kids map says only which
// children are allowed, so xsl:param written after an instruction was
// accepted -- iterate901err.xsl puts an xsl:variable ahead of the xsl:param
// precisely to check that it is not.
func checkIterateOrder(el *xdm.Node, model string) error {
	const (
		rankParam = iota
		rankOnCompletion
		rankConstructor
	)
	seen := rankParam
	for _, ch := range el.Children {
		r := rankConstructor
		switch ch.Kind {
		case xdm.KindText:
			if xdm.IsXMLWhitespace(ch.Value) {
				continue
			}
		case xdm.KindElement:
			if ch.Name.URI == xdm.NSXSL {
				switch ch.Name.Local {
				case "param":
					r = rankParam
				case "on-completion":
					r = rankOnCompletion
				}
			}
		default:
			continue
		}
		if r < seen {
			what := "the sequence constructor"
			if ch.Kind == xdm.KindElement {
				what = ch.Name.Lexical()
			}
			return fmt.Errorf(
				"xsl:iterate: %s is out of order, its content is %s "+
					"(XTSE0010)", what, model)
		}
		if r == rankOnCompletion && seen == rankOnCompletion {
			return fmt.Errorf(
				"xsl:iterate: at most one xsl:on-completion is allowed, its "+
					"content is %s (XTSE0010)", model)
		}
		seen = r
	}
	return nil
}
