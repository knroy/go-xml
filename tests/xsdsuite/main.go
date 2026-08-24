package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

const xlinkNS = "http://www.w3.org/1999/xlink"

func href(n *xdm.Node) string {
	if a := n.Attr(xlinkNS, "href"); a != nil {
		return a.Value
	}
	return n.AttrValue("href")
}

// Version tokens, per common/xsts.xsd.
//
// The version attribute is a *list* of tokens naming versions, features and
// implementation-defined behaviours. On testSuite, testSet, testGroup,
// schemaTest and instanceTest the tokens are joined by OR — a processor runs
// the test if it supports any of them. On expected the connector is AND — the
// result is prescribed only for a processor supporting all of them. Treating
// the attribute as a single string to compare against "1.0" scored the
// multi-token spellings against neither run.
func supportedTokens(only11 bool) map[string]bool {
	t := map[string]bool{
		// The XDM filtering and runtime-error behaviours this
		// validator implements. No test in the suite uses these
		// tokens today, but naming them keeps the rule complete.
		"comments-and-PIs-included": true,
		"CTR-all-runtime":           true,
		// Datatypes are defined over XML 1.0 names.
		"XML-1.0":       true,
		"XML-1.0-1e-4e": true,
		"XML-1.0-5e":    true,
	}
	if only11 {
		t["1.1"] = true
		// Conditional type assignment is implemented with the full
		// XPath subset the tests use, not only the restricted one.
		//
		// These two name *mutually exclusive* processor configurations,
		// so claiming both is not generosity, it is a contradiction. A
		// CTA test states an expectation for each -- cta0022 is valid
		// under full-xpath and invalid under restricted-xpath -- and
		// the later <expected> wins, so claiming the restricted token
		// as well made the harness demand rejection of schemas this
		// processor correctly accepts. Only the token that describes
		// what is implemented may be claimed.
		t["full-xpath-in-CTA"] = true
	} else {
		t["1.0"] = true
		// 1.0 Second Edition, which is what the errata-corrected
		// behaviour in this validator follows.
		t["1.0-2e"] = true
	}
	return t
}

// appliesOR answers a version attribute whose tokens are joined by OR.
// An absent or empty attribute applies to every processor.
func appliesOR(v string, tok map[string]bool) bool {
	f := strings.Fields(v)
	if len(f) == 0 {
		return true
	}
	for _, w := range f {
		if tok[w] {
			return true
		}
	}
	return false
}

// appliesAND answers an expected element's version attribute, whose tokens are
// joined by AND.
func appliesAND(v string, tok map[string]bool) bool {
	f := strings.Fields(v)
	if len(f) == 0 {
		return true
	}
	for _, w := range f {
		if !tok[w] {
			return false
		}
	}
	return true
}

// expectedValidity picks the expectation that applies to this run's version.
//
// Several expected elements may be present, each naming the configuration it
// speaks for. The most specific applicable one wins: an expectation carrying
// tokens is preferred over a bare one, which is the fallback for processors no
// qualified expectation matched.
func expectedValidity(parent *xdm.Node, tok map[string]bool) (string, string) {
	want, status := "", ""
	qualified := false
	for _, c := range parent.ChildElements() {
		if c.Name.Local != "expected" {
			continue
		}
		ev := strings.TrimSpace(c.AttrValue("version"))
		if !appliesAND(ev, tok) {
			continue
		}
		if ev == "" && qualified {
			// A bare expectation does not override one that named
			// this configuration explicitly.
			continue
		}
		want = c.AttrValue("validity")
		if ev != "" {
			qualified = true
		}
	}
	// The status of a test — "accepted", "queried", "stable" — is recorded
	// on a <current> element beside <expected>, not on <expected> itself.
	// "queried" means the W3C has challenged the expected result, usually
	// with a bugzilla reference, so those disagreements are a ceiling
	// rather than work outstanding and must be counted separately.
	for _, c := range parent.ChildElements() {
		if c.Name.Local == "current" {
			status = c.AttrValue("status")
			if b := c.AttrValue("bugzilla"); b != "" {
				if i := strings.LastIndex(b, "="); i >= 0 {
					status += " bug" + b[i+1:]
				}
			}
		}
	}
	return want, status
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	return s
}

func main() {
	suite := os.Args[1]
	only11 := false
	for _, a := range os.Args[2:] {
		if a == "-11" {
			only11 = true
		}
	}
	version := xsd.Version10
	if only11 {
		version = xsd.Version11
	}
	tok := supportedTokens(only11)

	var sets []string
	{
		f, err := os.Open(filepath.Join(suite, "suite.xml"))
		if err != nil {
			panic(err)
		}
		tree, err := xdm.Parse(f, xdm.ParseOptions{})
		f.Close()
		if err != nil {
			panic(err)
		}
		root := tree.Root
		if root.Kind == xdm.KindDocument {
			root = root.ChildElements()[0]
		}
		for _, ref := range root.ChildElements() {
			if ref.Name.Local == "testSetRef" {
				sets = append(sets, filepath.Join(suite, href(ref)))
			}
		}
	}

	var sOK, sBad, iOK, iBad int

	for _, set := range sets {
		f, err := os.Open(set)
		if err != nil {
			continue
		}
		tree, err := xdm.Parse(f, xdm.ParseOptions{})
		f.Close()
		if err != nil {
			continue
		}
		root := tree.Root
		if root.Kind == xdm.KindDocument {
			els := root.ChildElements()
			if len(els) == 0 {
				continue
			}
			root = els[0]
		}
		setName := root.AttrValue("name")
		if !appliesOR(root.AttrValue("version"), tok) {
			continue
		}

		for _, g := range root.ChildElements() {
			if g.Name.Local != "testGroup" {
				continue
			}
			if !appliesOR(g.AttrValue("version"), tok) {
				continue
			}
			gname := g.AttrValue("name")

			var schemaPaths []string
			schemaValid, sStatus, sTestName := false, "", ""
			haveSchemaTest := false
			for _, st := range g.ChildElements() {
				if st.Name.Local != "schemaTest" {
					continue
				}
				if !appliesOR(st.AttrValue("version"), tok) {
					continue
				}
				haveSchemaTest = true
				sTestName = st.AttrValue("name")
				for _, c := range st.ChildElements() {
					if c.Name.Local == "schemaDocument" {
						schemaPaths = append(schemaPaths, filepath.Join(filepath.Dir(set), href(c)))
					}
				}
				w, s := expectedValidity(st, tok)
				schemaValid = w == "valid"
				sStatus = s
			}
			if len(schemaPaths) == 0 {
				continue
			}

			sch, loadErr := xsd.LoadFiles(schemaPaths,
				xsd.Options{Resolver: &xsd.FileResolver{}, Version: version})

			// Score the schema test itself.
			if haveSchemaTest {
				gotValid := loadErr == nil
				if gotValid == schemaValid {
					sOK++
				} else {
					sBad++
					kind := "SFALSEACCEPT"
					detail := ""
					if !gotValid {
						kind = "SFALSEREJECT"
						detail = firstLine(loadErr.Error())
					}
					fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						kind, setName, gname, sTestName, sStatus,
						filepath.Base(schemaPaths[0]), detail)
				}
			}
			if loadErr != nil {
				continue
			}

			for _, it := range g.ChildElements() {
				if it.Name.Local != "instanceTest" {
					continue
				}
				if !appliesOR(it.AttrValue("version"), tok) {
					continue
				}
				docPath := ""
				for _, c := range it.ChildElements() {
					if c.Name.Local == "instanceDocument" {
						docPath = filepath.Join(filepath.Dir(set), href(c))
					}
				}
				want, status := expectedValidity(it, tok)
				if docPath == "" || want == "" {
					continue
				}
				df, err := os.Open(docPath)
				if err != nil {
					continue
				}
				// The base URI is what fn:base-uri() reports, and
				// the suite tests it: cta0021 asks whether the
				// element's base URI ends in the instance's file
				// name. Parsing from a reader without it left
				// every node's base URI empty.
				dt, err := xdm.Parse(df, xdm.ParseOptions{BaseURI: docPath})
				df.Close()
				if err != nil {
					continue
				}
				// The suite expects a conforming processor to
				// follow xsi:schemaLocation: several groups
				// declare the element a strict wildcard must
				// find in a document only the instance names.
				// This validator ignores the hint by default,
				// because honouring it lets a document choose
				// its own schema, so the driver opts in — which
				// is what a processor being measured for
				// conformance is doing.
				use := sch
				if ext, err := sch.WithInstanceLocations(dt.Root,
					xsd.InstanceLocationPolicy{
						AllowNamespace:   func(string) bool { return true },
						AllowNoNamespace: true,
						Resolver:         &xsd.FileResolver{},
					}, xsd.Options{Resolver: &xsd.FileResolver{}}); err == nil && ext != nil {
					use = ext
				}
				got := use.Validate(dt.Root, xsd.ValidateOptions{MaxErrors: 1})
				valid := got == nil
				if valid == (want == "valid") {
					iOK++
					continue
				}
				iBad++
				kind := "IFALSEACCEPT"
				detail := ""
				if !valid {
					kind = "IFALSEREJECT"
					detail = firstLine(got.Error())
				}
				fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					kind, setName, gname, it.AttrValue("name"), status,
					filepath.Base(docPath), detail)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "SCHEMA  agree %d  disagree %d  (%.2f%%)\n", sOK, sBad, 100*float64(sOK)/float64(sOK+sBad))
	fmt.Fprintf(os.Stderr, "INSTANCE agree %d  disagree %d  (%.2f%%)\n", iOK, iBad, 100*float64(iOK)/float64(iOK+iBad))
	fmt.Fprintf(os.Stderr, "TOTAL   agree %d  disagree %d  (%.2f%%)\n", sOK+iOK, sBad+iBad, 100*float64(sOK+iOK)/float64(sOK+iOK+sBad+iBad))
}
