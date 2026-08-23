package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// nsAlias is the result-namespace a stylesheet namespace is rewritten to.
type nsAlias struct {
	uri    string
	prefix string
}

// compileNamespaceAlias compiles xsl:namespace-alias.
//
// The instruction exists for one specific problem: a stylesheet that generates
// another stylesheet cannot write <xsl:template> literally, because that would
// be an instruction rather than output. So it writes the element in a
// placeholder namespace and declares that namespace to be an alias for the
// XSLT one, and the serialiser rewrites it on the way out.
func (c *compiler) compileNamespaceAlias(el *xdm.Node, precedence int) error {
	stylePrefix := el.AttrValue("stylesheet-prefix")
	resultPrefix := el.AttrValue("result-prefix")
	if stylePrefix == "" || resultPrefix == "" {
		return fmt.Errorf(
			"xsl:namespace-alias requires stylesheet-prefix and result-prefix")
	}

	resolve := func(p string) (string, error) {
		// "#default" names the default namespace rather than a prefix.
		if p == "#default" {
			uri, _ := el.LookupPrefix("")
			return uri, nil
		}
		uri, ok := el.LookupPrefix(p)
		if !ok {
			return "", fmt.Errorf("xsl:namespace-alias: unbound prefix %q", p)
		}
		return uri, nil
	}

	from, err := resolve(stylePrefix)
	if err != nil {
		return err
	}
	to, err := resolve(resultPrefix)
	if err != nil {
		return err
	}

	outPrefix := resultPrefix
	if outPrefix == "#default" {
		outPrefix = ""
	}
	// XTSE0810: "it is a static error if there is more than one such
	// declaration with the same literal namespace URI and the same import
	// precedence and different values for the target namespace URI, unless
	// there is also an xsl:namespace-alias declaration with the same literal
	// namespace URI and a higher import precedence."
	//
	// The whole sentence matters: two declarations that agree are not an
	// error, and a tie broken by a *higher* precedence declaration elsewhere
	// is not one either. The second escape is why the record is kept rather
	// than the check being made against the alias map alone — the map holds
	// only the winner, so it cannot say whether a loser conflicted.
	if prev, ok := c.aliasDecls[from]; ok {
		switch {
		case prev.precedence > precedence, precedence > prev.precedence:
			// A higher precedence declaration settles the question.
		case prev.uri != to:
			return fmt.Errorf(
				"XTSE0810: two xsl:namespace-alias declarations for %q at the "+
					"same import precedence name different target namespaces "+
					"(%q and %q)", from, prev.uri, to)
		}
	}
	if c.aliasDecls == nil {
		c.aliasDecls = map[string]aliasDecl{}
	}
	if prev, ok := c.aliasDecls[from]; !ok || precedence >= prev.precedence {
		c.aliasDecls[from] = aliasDecl{uri: to, precedence: precedence}
		c.sheet.namespaceAliases[from] = nsAlias{uri: to, prefix: outPrefix}
	}
	return nil
}

// aliasDecl records one xsl:namespace-alias for the XTSE0810 check, which
// needs the import precedence the winning alias map does not keep.
type aliasDecl struct {
	uri        string
	precedence int
}

// aliasFor rewrites a name through the declared namespace aliases, returning
// the name unchanged when no alias applies.
func (s *Stylesheet) aliasFor(n xdm.QName) xdm.QName {
	a, ok := s.namespaceAliases[n.URI]
	if !ok {
		return n
	}
	return xdm.QName{Prefix: a.prefix, URI: a.uri, Local: n.Local}
}

// compileCharacterMap compiles xsl:character-map.
//
// A character map replaces individual characters in the serialised output with
// arbitrary strings. It is applied at serialisation time and deliberately
// bypasses escaping, which is what makes it the supported way to emit a
// literal entity reference such as "&nbsp;" into HTML.
func (c *compiler) compileCharacterMap(el *xdm.Node, precedence int) error {
	name := el.AttrValue("name")
	if name == "" {
		return fmt.Errorf("xsl:character-map requires a name attribute")
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}

	m := map[rune]string{}

	// A map may include others. The names are recorded and resolved after
	// every module has compiled, because xsl:character-map is a top-level
	// declaration and may name one written below it — or in a module imported
	// afterwards. Resolving here reported XTSE1590 for a map that existed.
	var includes []string
	for _, u := range strings.Fields(el.AttrValue("use-character-maps")) {
		uq, err := resolveQNameAttr(el, u)
		if err != nil {
			return err
		}
		includes = append(includes, uq.Clark())
	}
	if len(includes) > 0 {
		c.charMapIncludes = append(c.charMapIncludes,
			charMapInclusion{name: qn.Clark(), includes: includes})
	}

	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "output-character") {
			return fmt.Errorf(
				"xsl:character-map may only contain xsl:output-character, found %s",
				ch.Name.Lexical())
		}
		charAttr := ch.AttrValue("character")
		runes := []rune(charAttr)
		if len(runes) != 1 {
			return fmt.Errorf(
				"xsl:output-character/@character must be a single character, got %q", charAttr)
		}
		m[runes[0]] = ch.AttrValue("string")
	}

	// XTSE1580: "it is a static error if the stylesheet contains two or more
	// character maps with the same name and the same import precedence,
	// unless it also contains another character map with the same name and
	// higher import precedence." Unlike XTSE0810 there is no escape for two
	// declarations that happen to agree — a character map is a set of
	// mappings rather than a single value, so "the same" has no meaning here.
	if prev, ok := c.charMapPrecedence[qn.Clark()]; ok && prev == precedence {
		return fmt.Errorf(
			"XTSE1580: two xsl:character-map declarations are named %s at the "+
				"same import precedence", qn.Lexical())
	}
	if c.charMapPrecedence == nil {
		c.charMapPrecedence = map[string]int{}
	}
	if prev, ok := c.charMapPrecedence[qn.Clark()]; ok && prev > precedence {
		// A map already declared at a higher precedence wins, and this one
		// is discarded rather than overwriting it.
		return nil
	}
	c.charMapPrecedence[qn.Clark()] = precedence
	c.sheet.characterMaps[qn.Clark()] = m
	return nil
}

// resolveOutputCharacterMaps flattens the maps named by xsl:output into the
// single table the serialiser consults.
//
// Done once after compilation rather than per serialisation, and after every
// module has been compiled so that a map declared in an imported stylesheet is
// visible to an xsl:output in the importing one.
func (s *Stylesheet) resolveOutputCharacterMaps(names []xdm.QName) error {
	merged, err := s.flattenCharacterMaps(names)
	if err != nil {
		return err
	}
	if merged != nil {
		s.activeCharMap = merged
	}
	return nil
}

// flattenCharacterMaps merges the named maps into the single table the
// serialiser consults.
//
// xsl:result-document takes @use-character-maps of its own, so this is shared
// rather than left inside the xsl:output path: a secondary document that
// names a character map must get the same substitutions the principal one
// would, and resolving the name needs the stylesheet the caller no longer has.
func (s *Stylesheet) flattenCharacterMaps(names []xdm.QName) (map[rune]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	merged := map[rune]string{}
	for _, n := range names {
		m, ok := s.characterMaps[n.Clark()]
		if !ok {
			return nil, fmt.Errorf("XTSE1590: no xsl:character-map named %q", n.Lexical())
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged, nil
}

// charMapInclusion records one xsl:character-map's use-character-maps, to be
// resolved once every module has compiled.
type charMapInclusion struct {
	name     string
	includes []string
}

// resolveCharacterMapIncludes folds included maps into the maps that include
// them.
//
// The including map's own entries win, which is why they are applied over the
// included ones rather than under. Inclusion is resolved transitively, with a
// bound: a map that includes itself, directly or through a cycle, would
// otherwise not terminate.
func (c *compiler) resolveCharacterMapIncludes() error {
	for _, inc := range c.charMapIncludes {
		own := c.sheet.characterMaps[inc.name]
		merged := map[rune]string{}
		if err := c.mergeCharMaps(merged, inc.includes, 0); err != nil {
			return err
		}
		for k, v := range own {
			merged[k] = v
		}
		c.sheet.characterMaps[inc.name] = merged
	}
	return nil
}

// maxCharMapDepth bounds transitive inclusion.
const maxCharMapDepth = 32

func (c *compiler) mergeCharMaps(dst map[rune]string, names []string, depth int) error {
	if depth > maxCharMapDepth {
		return fmt.Errorf(
			"XTSE1600: xsl:character-map inclusion nests more than %d deep, "+
				"which means it is circular", maxCharMapDepth)
	}
	for _, n := range names {
		m, ok := c.sheet.characterMaps[n]
		if !ok {
			return fmt.Errorf("XTSE1590: no xsl:character-map named %q", n)
		}
		for _, inc := range c.charMapIncludes {
			if inc.name != n {
				continue
			}
			if err := c.mergeCharMaps(dst, inc.includes, depth+1); err != nil {
				return err
			}
		}
		for k, v := range m {
			dst[k] = v
		}
	}
	return nil
}
