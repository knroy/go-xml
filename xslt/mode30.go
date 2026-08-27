package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// isStylesheetRootName reports whether local is one of the names that may be
// the outermost element of a stylesheet module.
//
// XSLT 3.0 section 3.6 adds xsl:package to the two XSLT 1.0 spellings. A
// package differs from a stylesheet only in what it exports to other packages
// and in the extra attributes it carries; as the top-level module of a
// transformation it behaves exactly like xsl:stylesheet, so everything that
// asks "is this the module element" has to accept all three names.
func isStylesheetRootName(local string) bool {
	return local == "stylesheet" || local == "transform" || local == "package"
}

// declaredModes reports whether the module requires every mode to be declared
// by an xsl:mode declaration before a template may use it.
//
// Section 6.6.1 makes the answer default to "yes" inside a package and "no"
// inside a plain xsl:stylesheet, because the rule exists to keep a package's
// modes from being extended by accident from outside — a concern a standalone
// stylesheet does not have. @declared-modes is only allowed on xsl:package,
// so a stylesheet can never turn it on.
func declaredModes(root *xdm.Node) bool {
	if !isXSL(root, "package") {
		return false
	}
	v := strings.TrimSpace(root.AttrValue("declared-modes"))
	// The element table has already rejected anything outside the yes/no
	// vocabulary, so only the two negative spellings need naming here.
	return v != "no" && v != "false" && v != "0"
}

// checkDeclaredModes reports XTSE3085 for a mode named by a template that no
// xsl:mode declaration introduces.
//
// Section 6.6.1: "if the declared-modes attribute of the xsl:package element
// has the value yes, then it is a static error if a mode is used other than
// the mode named in an xsl:mode declaration." The unnamed mode counts as
// declared only if an xsl:mode declares it: a package that declares no modes
// at all and writes an unnamed-mode template is the error the rule is for.
//
// Only xsl:template/@mode is checked. @mode on xsl:apply-templates may name a
// mode reached through a package this module uses, which this processor does
// not model, so flagging it would reject stylesheets the spec allows.
func checkDeclaredModes(root *xdm.Node) error {
	if !declaredModes(root) {
		return nil
	}
	declared := map[string]bool{}
	var scan func(*xdm.Node)
	scan = func(n *xdm.Node) {
		for _, ch := range n.ChildElements() {
			if isXSL(ch, "mode") {
				for _, m := range modeNamesOf(ch) {
					declared[m] = true
				}
			}
			scan(ch)
		}
	}
	scan(root)

	var walk func(*xdm.Node) error
	walk = func(n *xdm.Node) error {
		for _, ch := range n.ChildElements() {
			if isXSL(ch, "template") {
				for _, m := range templateModeNames(ch) {
					if !declared[m] {
						name := m
						if name == "" {
							name = "#unnamed"
						}
						return fmt.Errorf(
							"XTSE3085: mode %s is used by a template but no "+
								"xsl:mode declaration introduces it, and the "+
								"package sets declared-modes=\"yes\"", name)
					}
				}
			}
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// modeNamesOf expands the mode names an xsl:mode declaration introduces.
//
// An absent @name declares the unnamed mode; "#default" and "#unnamed" are
// two spellings of the same thing. xsl:mode accepts a list so that one
// declaration can configure several modes alike.
func modeNamesOf(el *xdm.Node) []string {
	a := el.Attr("", "name")
	if a == nil {
		return []string{""}
	}
	var out []string
	for _, tok := range strings.Fields(a.Value) {
		switch tok {
		case "#default", "#unnamed":
			out = append(out, "")
		default:
			qn, err := resolveQNameAttr(el, tok)
			if err != nil {
				// An unresolvable prefix is reported where prefixes are
				// resolved; naming it here would give the wrong code.
				continue
			}
			out = append(out, xdm.QName{URI: qn.URI, Local: qn.Local}.Clark())
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// templateModeNames expands xsl:template/@mode into Clark names. "#all" is
// excluded: it names every mode there is, so it cannot be undeclared.
func templateModeNames(el *xdm.Node) []string {
	a := el.Attr("", "mode")
	if a == nil {
		return []string{""}
	}
	var out []string
	for _, tok := range strings.Fields(a.Value) {
		switch tok {
		case "#all":
			return nil
		case "#default", "#unnamed":
			out = append(out, "")
		default:
			qn, err := resolveQNameAttr(el, tok)
			if err != nil {
				continue
			}
			out = append(out, xdm.QName{URI: qn.URI, Local: qn.Local}.Clark())
		}
	}
	return out
}

// boolAliases are the XSLT 3.0 spellings of the yes/no attribute values.
//
// Section 3.2: "the value true is a synonym for yes and false for no", and 1
// and 0 are accepted alongside them. XSLT 2.0 admits only yes and no, so the
// aliases are recognised only in a module that is written to 3.0 — accepting
// them everywhere would let a 2.0 stylesheet through that the 2.0 rules make
// XTSE0020.
var boolAliases = map[string]string{
	"true": "yes", "false": "no", "1": "yes", "0": "no",
}

// allowsBoolAliases reports whether el is inside a module that may spell its
// booleans the XSLT 3.0 way.
//
// A package is always such a module, whatever its @version says: xsl:package
// exists only in 3.0, so a stylesheet that uses one is writing 3.0 even where
// it left the version at 2.0 — which several of the conformance packages do.
func allowsBoolAliases(el *xdm.Node) bool {
	for a := el; a != nil; a = a.Parent {
		if a.Kind != xdm.KindElement {
			continue
		}
		if isXSL(a, "package") {
			return true
		}
		if hasVersionAttr(a) {
			return versionAt(a) >= 3.0
		}
	}
	return false
}

// yesAttr reads a yes/no attribute, accepting the XSLT 3.0 synonyms.
//
// The grammar check has already refused an unrecognised spelling, and refused
// true/false/1/0 outside a 3.0 module, so anything that reaches here and is
// not one of the negative spellings is affirmative.
func yesAttr(el *xdm.Node, name string) bool {
	switch strings.TrimSpace(el.AttrValue(name)) {
	case "yes", "true", "1":
		return true
	}
	return false
}
