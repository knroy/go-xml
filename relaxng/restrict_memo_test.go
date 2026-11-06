package relaxng

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// restrictVerdicts runs checkRestrictions on src with followRef's memo on and
// off, and returns both results as text.
func restrictVerdicts(t *testing.T, src string) (memo, plain string) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	show := func(err error) string {
		if err == nil {
			return "ok"
		}
		return err.Error()
	}
	memo = show(checkRestrictions(rootElement(tree.Root)))
	restrictNoMemo = true
	defer func() { restrictNoMemo = false }()
	plain = show(checkRestrictions(rootElement(tree.Root)))
	return memo, plain
}

// restrictFiller is a pattern legal in every context, used as a combinator's
// second child so the combinator contributes its step (§4.12 removes one with
// a single child).
const restrictFiller = `<element name="z"><empty/></element>`

// wrapStep writes inner under the element that contributes step s to a path.
func wrapStep(s, inner string) string {
	switch s {
	case "group", "interleave":
		return "<" + s + ">" + inner + restrictFiller + "</" + s + ">"
	case "attribute":
		return `<attribute name="x">` + inner + `</attribute>`
	case "data":
		return `<data type="string"><except>` + inner + `</except></data>`
	}
	return "<" + s + ">" + inner + "</" + s + ">"
}

// leafStep is a smallest pattern whose own element is step s.
func leafStep(s string) string {
	switch s {
	case "attribute":
		return `<attribute name="y"><text/></attribute>`
	case "parentRef":
		return `<parentRef name="p"/>`
	case "list":
		return `<list><data type="string"/></list>`
	case "group", "interleave":
		return "<" + s + ">" + restrictFiller + restrictFiller + "</" + s + ">"
	case "oneOrMore":
		return `<oneOrMore>` + restrictFiller + `</oneOrMore>`
	case "data":
		return `<data type="string"/>`
	case "value":
		return `<value>v</value>`
	}
	return "<" + s + "/>"
}

// Every §7.1 prohibited path, reached through a definition that some legal
// restricted context reaches first. followRef's memo records that first walk;
// a signature that conflated the two contexts would skip the second and
// accept the schema. Each must be refused, and identically with the memo off.
func TestRestrictionMemoMatchesUnmemoisedOnProhibitedPaths(t *testing.T) {
	legal := []string{
		`<list><ref name="D"/></list>`,
		`<attribute name="a"><ref name="D"/></attribute>`,
		`<data type="string"><except><ref name="D"/></except></data>`,
		`<list><oneOrMore><ref name="D"/></oneOrMore></list>`,
		`<attribute name="a"><group><ref name="D"/>` + restrictFiller + `</group></attribute>`,
		`<data type="string"><except><choice><ref name="D"/><value>v</value></choice></except></data>`,
	}
	for _, p := range prohibited {
		last := p.steps[len(p.steps)-1]
		// The path's steps before the last, outermost first, with data
		// and except written as the one data/except wrapper.
		var outer []string
		for i := 0; i < len(p.steps)-1; i++ {
			outer = append(outer, p.steps[i])
			if p.steps[i] == "data" {
				i++ // except, which wrapStep writes with data
			}
		}
		for _, l := range legal {
			bad := `<ref name="D"/>`
			start := outer[0] == "start"
			for i := len(outer) - 1; i >= 0; i-- {
				if outer[i] != "start" {
					bad = wrapStep(outer[i], bad)
				}
			}
			var body string
			if start {
				body = `<start><choice><element name="r">` + l + `</element>` + bad + `</choice></start>`
			} else {
				body = `<start><element name="r"><choice>` + l + bad + `</choice></element></start>`
			}
			src := `<grammar xmlns="` + NS + `">` + body +
				`<define name="D">` + leafStep(last) + `</define></grammar>`
			memo, plain := restrictVerdicts(t, src)
			if memo != plain {
				t.Errorf("%s: memo says %q, unmemoised says %q\n%s",
					strings.Join(p.steps, "//"), memo, plain, src)
			}
			if plain == "ok" {
				t.Errorf("%s: the prohibited path was accepted\n%s",
					strings.Join(p.steps, "//"), src)
			}
		}
	}
}

// genPattern writes a random pattern over the constructs §7 constrains, with
// refs into defines d0..d(defs-1), recursion included.
func genPattern(r *rand.Rand, depth, defs int) string {
	if depth == 0 || r.IntN(5) == 0 {
		switch r.IntN(7) {
		case 0:
			return `<text/>`
		case 1:
			return `<empty/>`
		case 2:
			return `<data type="string"/>`
		case 3:
			return `<value>v</value>`
		case 4:
			return `<attribute><anyName/><text/></attribute>`
		case 5:
			return `<notAllowed/>`
		}
		return fmt.Sprintf(`<ref name="d%d"/>`, r.IntN(defs))
	}
	sub := func() string { return genPattern(r, depth-1, defs) }
	switch r.IntN(12) {
	case 0:
		return `<element name="e">` + sub() + `</element>`
	case 1:
		return `<attribute name="a">` + sub() + `</attribute>`
	case 2:
		return `<list>` + sub() + `</list>`
	case 3:
		return `<oneOrMore>` + sub() + `</oneOrMore>`
	case 4:
		return `<zeroOrMore>` + sub() + `</zeroOrMore>`
	case 5:
		return `<optional>` + sub() + `</optional>`
	case 6:
		return `<group>` + sub() + sub() + `</group>`
	case 7:
		return `<interleave>` + sub() + sub() + `</interleave>`
	case 8:
		return `<choice>` + sub() + sub() + `</choice>`
	case 9:
		return `<group><empty/>` + sub() + `</group>`
	}
	return `<data type="string"><except>` + sub() + `</except></data>`
}

// genContext wraps a ref to definition d in one to three random steps, so a
// definition is reached from many paths, restricted ones among them.
func genContext(r *rand.Rand, d int) string {
	p := fmt.Sprintf(`<ref name="d%d"/>`, d)
	steps := []string{"list", "attribute", "data", "oneOrMore", "zeroOrMore",
		"optional", "group", "interleave", "choice"}
	for range 1 + r.IntN(3) {
		s := steps[r.IntN(len(steps))]
		if s == "choice" {
			p = `<choice>` + p + restrictFiller + `</choice>`
			continue
		}
		p = wrapStep(s, p)
	}
	return p
}

// The memo must not change a verdict on any schema. Random grammars whose
// definitions are each reached from several contexts — data/except//X and
// oneOrMore//group//attribute among them, at every depth — are checked with
// the memo on and off, and the results must agree exactly. Definitions refer
// only forward, so recursion does not decide most of the verdicts.
func TestRestrictionMemoMatchesUnmemoisedOnRandomGrammars(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 1))
	const defs = 5
	verdicts := map[bool]int{}
	for i := 0; i < 3000; i++ {
		var sb strings.Builder
		sb.WriteString(`<grammar xmlns="` + NS + `"><start><element name="r"><choice>`)
		for range 4 {
			sb.WriteString(genContext(r, r.IntN(defs)))
		}
		sb.WriteString(`</choice></element></start>`)
		for d := 0; d < defs; d++ {
			body := genPattern(r, 2, defs)
			if d+1 < defs && r.IntN(2) == 0 {
				body = genContext(r, d+1+r.IntN(defs-d-1))
			}
			fmt.Fprintf(&sb, `<define name="d%d">%s</define>`, d, body)
		}
		sb.WriteString(`</grammar>`)
		memo, plain := restrictVerdicts(t, sb.String())
		if memo != plain {
			t.Fatalf("memo says %q, unmemoised says %q\n%s", memo, plain, sb.String())
		}
		verdicts[plain == "ok"]++
	}
	if verdicts[true] < 100 || verdicts[false] < 100 {
		t.Fatalf("the generator is lopsided (%d accepted, %d refused); the differential is not exercising both verdicts",
			verdicts[true], verdicts[false])
	}
}

// stackSig's claim, checked directly: two stacks with one signature are
// indistinguishable to everything a walk below them asks — matchProhibited at
// every element, restrictedStack, the repetition test of §7.3, and the
// signature itself — under every extension, including ones that complete a
// 3-step path across the boundary (data | except, X).
func TestStackSigIsSound(t *testing.T) {
	alpha := []string{"attribute", "oneOrMore", "zeroOrMore", "group", "interleave",
		"choice", "list", "data", "except", "start", "optional"}
	locals := append(slices.Clone(alpha), "text", "empty", "parentRef", "value", "ref")
	observe := func(stack, ext []string) string {
		full := append(slices.Clone(stack), ext...)
		var b strings.Builder
		for _, l := range locals {
			if err := matchProhibited(append(slices.Clone(full), l)); err != nil {
				b.WriteString(err.Error())
			}
			b.WriteByte(';')
		}
		fmt.Fprint(&b, restrictedStack(full),
			slices.Contains(full, "oneOrMore") || slices.Contains(full, "zeroOrMore"), stackSig(full))
		return b.String()
	}
	var stacks [][]string
	var grow func(s []string)
	grow = func(s []string) {
		stacks = append(stacks, s)
		if len(s) == 3 {
			return
		}
		for _, a := range alpha {
			grow(append(slices.Clone(s), a))
		}
	}
	grow(nil)
	rep := map[string][]string{}
	r := rand.New(rand.NewPCG(3, 9))
	for _, s := range stacks {
		sig := stackSig(s)
		first, ok := rep[sig]
		if !ok {
			rep[sig] = s
			continue
		}
		for range 50 {
			ext := make([]string, r.IntN(4))
			for i := range ext {
				ext[i] = alpha[r.IntN(len(alpha))]
			}
			if a, b := observe(first, ext), observe(s, ext); a != b {
				t.Fatalf("stacks %v and %v share signature %q but differ under extension %v:\n%s\n%s",
					first, s, sig, ext, a, b)
			}
		}
	}
	if len(rep) == len(stacks) {
		t.Fatal("no two stacks share a signature; the test compared nothing")
	}
}
