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

// untypedSortPairErrs reports whether the XPath "lt" operator raises for a
// pair in which at least one side is xs:untypedAtomic.
//
// XSLT 3.0 13.1.2 makes XTDE1030 exactly the question of whether "lt" errors,
// and F&O 3.0 B.2 answers it: an untypedAtomic operand of a value comparison
// is cast to xs:double when the other operand is numeric and to xs:string
// otherwise. So untyped against a string, a URI or a number is ordinary, and
// untyped against anything else becomes a string compared with a date, a
// duration or a boolean, which "lt" refuses. sort-080 sorts three untyped
// attributes together with an xs:date and requires the refusal.
func untypedSortPairErrs(a, b *xdm.Atomic) bool {
	if a.Type != xdm.TypeUntypedAtomic && b.Type != xdm.TypeUntypedAtomic {
		return false
	}
	other := a
	if a.Type == xdm.TypeUntypedAtomic {
		other = b
	}
	switch other.Type {
	case xdm.TypeUntypedAtomic, xdm.TypeString, xdm.TypeAnyURI:
		return false
	}
	return !other.Type.IsNumeric()
}

// missingParamCode names the error for a parameter whose implicit default --
// the empty sequence -- is not a valid instance of its declared type, so that
// section 10.1.1 treats it as required and the caller supplied nothing.
//
// XSLT 2.0 spells it XTDE0610. XSLT 3.0 dropped that code in favour of
// XTDE0700, the one it already used for a required parameter of the initial
// template, because the two conditions had become the same condition
// (W3C bug 28355). Which spelling is due is a question about the PROCESSOR's
// version, not the module's: error-0610a and error-0610c are one stylesheet
// each, run twice by the suite -- once scoped XSLT20 expecting XTDE0610 and
// once scoped XSLT30+ expecting XTDE0700.
func missingParamCode(s *Stylesheet) string {
	if s == nil || s.maxVersion == 0 || s.maxVersion >= 3.0 {
		return "XTDE0700"
	}
	return "XTDE0610"
}

// funcParamCode names the error for an argument of a stylesheet function that
// will not convert to the parameter's declared type.
//
// The XSLT 2.0 code is XTTE0790. XSLT 3.0 makes a stylesheet function a
// function item and applies the ordinary function conversion rules to its
// arguments, so the failure is XPath's own XPTY0004; the working-draft code
// did not survive into the Recommendation. As with XTDE0610 the decision
// belongs to the processor's version -- error-0790a and error-0790a3 are the
// same stylesheet scoped once each way.
func funcParamCode(s *Stylesheet) string {
	if s == nil || s.maxVersion == 0 || s.maxVersion >= 3.0 {
		return "XPTY0004"
	}
	return "XTTE0790"
}

// reservedNamespaces30 are the namespaces XSLT 3.0 adds to the reserved list
// section 3.2 gives, on top of the ones XSLT 2.0 already reserved.
//
// The map and array function namespaces did not exist in 2.0, and the error
// namespace was not reserved by it, so a 2.0 module naming one of them is not
// making the mistake XTSE0080 describes; the gate is the MODULE's version,
// since XTSE0080 is decided from the stylesheet text alone.
var reservedNamespaces30 = map[string]bool{
	xdm.NSMap:   true,
	xdm.NSArray: true,
	xdm.NSErr:   true,
}

// isReservedNamespace reports whether uri is one a stylesheet may not use in
// the name of an object it declares, for a declaration written on el.
func isReservedNamespace(el *xdm.Node, uri string) bool {
	if reservedNamespaces[uri] {
		return true
	}
	return reservedNamespaces30[uri] && moduleAtLeast30(el)
}

// checkExtensionPrefixes applies XTSE0085 and XTSE0800: an extension
// namespace may not be a reserved one.
//
// The two codes name the same mistake at two moments. XTSE0085 is the
// designation itself -- [xsl:]extension-element-prefixes naming a prefix bound
// to a reserved namespace -- and XTSE0800 is an element written in such a
// namespace and thereby claimed as an extension instruction. Where the
// stylesheet went on to write the element, the specific code is the one due,
// so the scan for it comes first: extension-functions-0105 designates the
// schema namespace AND writes <xs:special> in it, and expects XTSE0800, while
// math-3702 only designates it and expects XTSE0085.
func checkExtensionPrefixes(el *xdm.Node) error {
	// XTSE0085 is an XSLT 3.0 code: 2.0 said nothing against designating a
	// reserved namespace as an extension namespace, and function-1023 is a
	// version="2.0" module that writes extension-element-prefixes="xs" and
	// expects to reach XPST0017 at run time. So the gate is the MODULE's
	// version, the error being decided from the stylesheet text alone.
	if !moduleAtLeast30(el) {
		return nil
	}
	for _, uri := range []string{"", xdm.NSXSL} {
		if uri == "" && el.Name.URI != xdm.NSXSL {
			continue
		}
		a := el.Attr(uri, "extension-element-prefixes")
		if a == nil {
			continue
		}
		for _, p := range strings.Fields(a.Value) {
			if p == "#default" {
				p = ""
			}
			ns, ok := el.LookupPrefix(p)
			if !ok || !isReservedNamespace(el, ns) {
				continue
			}
			if name := firstElementIn(el, ns); name != "" {
				return fmt.Errorf(
					"XTSE0800: %s is an extension instruction in the "+
						"reserved namespace %q", name, ns)
			}
			return fmt.Errorf(
				"XTSE0085: extension-element-prefixes names %q, which is "+
					"bound to the reserved namespace %q", p, ns)
		}
	}
	return nil
}

// firstElementIn returns the lexical name of the first descendant of el in the
// namespace uri, or "" if there is none.
func firstElementIn(el *xdm.Node, uri string) string {
	for _, ch := range el.ChildElements() {
		if ch.Name.URI == uri {
			return ch.Name.Lexical()
		}
		if n := firstElementIn(ch, uri); n != "" {
			return n
		}
	}
	return ""
}

// checkAtomizable reports FOTY0013 for a sequence holding an item that cannot
// be atomized.
//
// Simple content is built by atomizing the sequence, and F&O 3.1 gives
// fn:data FOTY0013 for a map, an array-of-non-atomizable or a function item:
// they have no typed value. The builders join whatever they can turn into a
// string and silently drop the rest, so a map reached xsl:value-of as the
// empty string rather than as the error maps-907 requires.
func checkAtomizable(seq xdm.Sequence) error {
	for _, it := range seq {
		switch it.(type) {
		case *xdm.Node, *xdm.Atomic:
		default:
			if _, err := xdm.AtomizeChecked(xdm.Sequence{it}); err != nil {
				return err
			}
		}
	}
	return nil
}
