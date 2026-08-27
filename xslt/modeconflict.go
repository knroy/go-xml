package xslt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// modeDecl is one xsl:mode declaration as XTSE0545 sees it: the import
// precedence it was made at, and the normalised value of every attribute it
// explicitly states, keyed by local name. @name is excluded, being the
// declaration's subject rather than one of its properties.
type modeDecl struct {
	prec  int
	attrs map[string]string
}

// modeDeclAttrs reads an xsl:mode's attributes into the normalised form
// XTSE0545 compares.
//
// The comparison is over *values*, not over the text that spelled them, so
// the lexical form has to be reduced to what it denotes before two
// declarations can be said to disagree. Two reductions matter here:
//
//   - @use-accumulators is an unordered set of EQNames. mode-1512 writes
//     "b a" against an included "a b" and expects no conflict, and mode-1514
//     writes "qqqq:b pppp:a" against an included "q:b p:a" where both prefix
//     pairs bind to the same two namespaces. Expanding each token and sorting
//     makes both pairs compare equal. mode-1515 is the case this must still
//     catch: the prefixes there bind to *different* namespaces in the two
//     modules, so the expanded sets differ and the conflict stands.
//   - the boolean attributes admit the 3.0 synonyms "true"/"false"/"1"/"0"
//     for "yes"/"no", which denote the same value.
//
// Everything else is compared on its space-normalised text, which is exact
// for the enumerated attributes (@on-no-match, @on-multiple-match, @typed,
// @visibility) that make up the rest of the element.
func modeDeclAttrs(el *xdm.Node) (map[string]string, error) {
	attrs := map[string]string{}
	for _, a := range el.Attrs {
		if a.Name.URI != "" || a.Name.Local == "name" {
			continue
		}
		v := strings.TrimSpace(a.Value)
		switch a.Name.Local {
		case "use-accumulators":
			toks := strings.Fields(v)
			for i, tok := range toks {
				if tok == "#all" || tok == "#default" {
					continue
				}
				qn, err := resolveQNameAttr(el, tok)
				if err != nil {
					return nil, err
				}
				toks[i] = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
			}
			sort.Strings(toks)
			v = strings.Join(toks, " ")
		case "streamable", "warning-on-no-match", "warning-on-multiple-match":
			if alias, ok := boolAliases[v]; ok {
				v = alias
			}
		}
		attrs[a.Name.Local] = v
	}
	return attrs, nil
}

// checkModeConflicts reports XTSE0545 for a mode whose declarations disagree
// about an attribute at the import precedence that wins for that attribute.
//
// The rule is per attribute, not per declaration: "a package explicitly
// specifies two conflicting values for the same attribute in different
// xsl:mode declarations having the same import precedence, unless there is
// another definition of the same attribute with higher import precedence".
// So the highest precedence at which an attribute is *stated at all* is the
// one that decides it, and only a disagreement there is an error — a lower
// tie is masked, and agreeing declarations are no ambiguity at all. Judging
// this per declaration rather than per attribute made mode-1512 and mode-1514
// errors, though both merely restate the same accumulator set the module they
// include already gave.
//
// The tie is judged after every module has been compiled rather than as each
// declaration is compiled, because an importing module's xsl:mode masks the
// imported one entirely — so a tie among declarations that are all overridden
// is invisible, and reporting it as the earlier module was compiled invented
// an error the finished package does not have.
// checkAccumulatorConflicts settles XTSE3350 the same way for the same
// reason.
func (c *compiler) checkModeConflicts() error {
	for m, decls := range c.modeTies {
		// best[attr] is the highest precedence at which attr is stated, and
		// seen[attr] the value stated there; conflict[attr] records that a
		// second, different value was stated at that same precedence.
		best := map[string]int{}
		seen := map[string]string{}
		conflict := map[string]bool{}
		for _, d := range decls {
			for attr, val := range d.attrs {
				p, ok := best[attr]
				switch {
				case !ok || d.prec > p:
					best[attr], seen[attr] = d.prec, val
					conflict[attr] = false
				case d.prec == p && seen[attr] != val:
					conflict[attr] = true
				}
			}
		}
		for _, attr := range sortedKeys(conflict) {
			if !conflict[attr] {
				continue
			}
			name := m
			if name == "" {
				name = "#unnamed"
			}
			return fmt.Errorf(
				"XTSE0545: xsl:mode/@%s is given conflicting values for mode "+
					"%s by two declarations at the same import precedence",
				attr, name)
		}
	}
	return nil
}
