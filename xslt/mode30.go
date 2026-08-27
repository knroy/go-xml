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
// A mode accepted from a used package counts as declared, and 3.5.4.1 says so
// in as many words: "A mode is declared if either of the following conditions
// is true: the package contains an xsl:mode declaration for that mode; the
// mode is a public or final mode accepted from a used package."
//
// Which modes those are cannot be read off this module's tree. A manifest may
// accept a mode without naming it -- an xsl:use-package with no xsl:accept at
// all takes every public component the used package offers -- so answering
// needs the used package loaded, which happens much later than this check.
// use-package-170 writes exactly that: its xsl:use-package names no
// components, and the mode "normal" it applies templates in is declared two
// packages down.
//
// So a module that uses a package is not judged on the modes it merely
// references. What can still be judged is what the module DECLARES: a
// template rule naming a mode is a rule the module itself contributes, and
// 6.6.1's rule about it holds whatever the manifest brings in. Where the
// module uses no package at all there is nothing a manifest could supply and
// both halves are checked.
func checkDeclaredModes(root *xdm.Node) error {
	if !declaredModes(root) {
		return nil
	}
	usesPackage := false
	for _, el := range root.ChildElements() {
		if isXSL(el, "use-package") {
			usesPackage = true
			break
		}
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

	// A @default-mode on the package element names a mode "implicitly", which
	// is the case 3.6.4.1 calls out by name: the error covers a mode name the
	// package contains "either explicitly or implicitly (for example, by
	// virtue of a relevant default-mode attribute)". A package whose default
	// mode is one no xsl:mode declares has named an undeclared mode however
	// few templates it goes on to write.
	//
	// It is judged even for a package with no templates at all, because that
	// is the whole point of package-914d and -914e: an empty package whose
	// default-mode names nothing. The catalog's description says why the
	// answer must be the static error rather than the dynamic one the
	// invocation would otherwise reach -- "static errors come before dynamic
	// errors, XTDE0044 is not raised, but XTSE3085 is".
	//
	// package-914b is the contrast that fixes the boundary: its
	// default-mode="#unnamed" is declared by a bare <xsl:mode/>, so it is not
	// this error and goes on to the XTDE0044 it expects.
	//
	// A package that uses another is exempt on the same grounds as the walk
	// below: a mode this package's default-mode names may be one it accepts
	// from a used package, and which those are is not knowable here.
	if !usesPackage {
		if a := root.Attr("", "default-mode"); a != nil {
			tok := strings.TrimSpace(a.Value)
			name := ""
			switch tok {
			case "", "#default", "#unnamed":
			default:
				if qn, err := resolveQNameAttr(root, tok); err == nil {
					name = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
				}
			}
			if name != "" && !declared[name] {
				return fmt.Errorf(
					"XTSE3085: the package's default-mode names the mode %s, "+
						"but no xsl:mode declaration introduces it and the "+
						"package sets declared-modes=\"yes\"", tok)
			}
		}
	}

	var walk func(*xdm.Node) error
	walk = func(n *xdm.Node) error {
		for _, ch := range n.ChildElements() {
			// A template rule inside xsl:override adds a rule to a mode of
			// the USED package, which 3.5.4 admits precisely because that
			// mode is public or abstract there. It is not a reference to a
			// mode of this package, so this package's xsl:mode declarations
			// have nothing to say about it: use-package-170's override adds
			// a rule to the mode "normal" that its used package declares
			// public two levels down.
			if isXSL(ch, "override") {
				continue
			}
			if isXSL(ch, "template") ||
				(isXSL(ch, "apply-templates") && !usesPackage) {
				for _, m := range templateModeNames(ch) {
					if !declared[m] {
						name := m
						if name == "" {
							name = "#unnamed"
						}
						return fmt.Errorf(
							"XTSE3085: mode %s is used but no "+
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
	// Only a template *rule* is in a mode. Section 6.1: "an xsl:template
	// element that has no match attribute must have no mode attribute", so a
	// named template belongs to no mode at all and cannot be a reference to
	// the unnamed one. xsl:apply-templates has no @match and is exempt: it
	// uses a mode rather than belonging to one.
	//
	// Reading a named template as using the unnamed mode made every package
	// whose only template is xsl:initial-template a static error, which is
	// most of the package-version set: those stylesheets declare no mode,
	// use no mode, and were still told the unnamed mode was undeclared.
	if el.Attr("", "match") == nil && !isXSL(el, "apply-templates") {
		return nil
	}
	a := el.Attr("", "mode")
	if a == nil {
		// An absent @mode names the default mode, which [xsl:]default-mode
		// may have moved off the unnamed one.
		dm, err := defaultModeAt(el)
		if err != nil {
			return nil
		}
		return []string{dm}
	}
	var out []string
	for _, tok := range strings.Fields(a.Value) {
		switch tok {
		case "#all", "#current":
			// Neither is a name: "#all" covers every mode there is, and
			// "#current" names whatever mode the enclosing rule was selected
			// in, which is by construction already in use.
			return nil
		case "#unnamed":
			out = append(out, "")
		case "#default":
			dm, err := defaultModeAt(el)
			if err != nil {
				return nil
			}
			out = append(out, dm)
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

// defaultModeAt returns the mode that "#default" names at el.
//
// XSLT 3.0 section 6.6.2 makes [xsl:]default-mode a standard attribute: it may
// be written on any XSLT element or, in the xsl: namespace, on a literal
// result element, and it applies to that element and everything within. Where
// none is in scope the default mode is the unnamed one, which is why the
// pseudo-mode used to be compiled straight to "".
//
// It is resolved statically, at the element the mode token is written on,
// exactly as the default collation and the default element namespace are. The
// Clark name is returned, or "" for the unnamed mode.
func defaultModeAt(el *xdm.Node) (string, error) {
	for a := el; a != nil; a = a.Parent {
		if a.Kind != xdm.KindElement {
			continue
		}
		v := ""
		if a.Name.URI == xdm.NSXSL {
			if at := a.Attr("", "default-mode"); at != nil {
				v = at.Value
			}
		}
		if v == "" {
			// On a literal result element the attribute must be in the XSLT
			// namespace to be the stylesheet's rather than the output's.
			if at := a.Attr(xdm.NSXSL, "default-mode"); at != nil {
				v = at.Value
			}
		}
		if v == "" {
			continue
		}
		tok := strings.TrimSpace(v)
		if tok == "#unnamed" || tok == "#default" {
			return "", nil
		}
		qn, err := resolveQNameAttr(a, tok)
		if err != nil {
			return "", err
		}
		return xdm.QName{URI: qn.URI, Local: qn.Local}.Clark(), nil
	}
	return "", nil
}
