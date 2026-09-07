package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		// Datatypes are defined over XML 1.0 names. The two edition
		// tokens name *mutually exclusive* processor configurations,
		// exactly as full-xpath-in-CTA and its restricted twin do
		// below, so claiming both is not generosity but a
		// contradiction. This validator follows Fifth Edition names,
		// so that is the only edition token it may claim.
		"XML-1.0":    true,
		"XML-1.0-5e": true,
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
//
// The validity attribute has three values, not two. Besides "valid" and
// "invalid" the suite uses "indeterminate", which prescribes no result at all:
// the annotation on schZ012_a reads "The WG decided the spec. is underspecified
// in this area, so implementations may reasonably differ", and particlesZ026
// records that the TSTF concluded the schema's validity was
// implementation-determined. Callers must test for that value explicitly.
// Reading the attribute as (w == "valid") turns "no answer is prescribed" into
// "must be rejected", which scores a defensible acceptance as a false accept.
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

// notQName and notNamespace are the two wildcard attributes XSD 1.1 Part 1
// §3.10.1 adds; neither exists in the 1.0 schema for schema documents, so a
// document using either cannot be a valid 1.0 schema at all.
var only11Wildcard = regexp.MustCompile(`\bnot(QName|Namespace)\s*=`)

// usesOnly11Syntax reports whether any of these schema documents is written in
// syntax that exists only in XSD 1.1.
//
// This is a fallback for a gap in the suite's own metadata, not a second
// scoping mechanism. The normative one is the version attribute, whose tokens
// common/xsts.xsd defines as "the versions and features for which the test is
// applicable"; appliesOR already implements it, and 463 of the suite's
// 1.1-feature groups carry it. A handful in ibmMeta do not, and for most of
// them that is harmless — a 1.0 processor still reaches the prescribed answer.
// It is not harmless where the suite expects the schema to be *valid*: a 1.0
// processor must reject a document it cannot even parse, so the group scores as
// a false reject against a conformant processor.
//
// The suite's XSD1_1TestCategories.xml documentationReference is not usable as
// the signal. common/xsts.xsd documents documentationReference as merely "a
// link to documentation relevant to a test", and empirically it does not
// discriminate: 32 groups lacking a version attribute carry one, and 28 of them
// already agree under 1.0. Excluding on it would discard 28 correct results to
// rescue 4, shrinking the denominator and inflating the score. The syntax of
// the schema document itself is the fact that actually decides the question.
func usesOnly11Syntax(paths []string) bool {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if only11Wildcard.Match(b) {
			return true
		}
	}
	return false
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
	// Tests expecting "indeterminate" leave both the numerator and the
	// denominator: the suite prescribes no result, so neither outcome is
	// agreement or disagreement, and scoring one would be scoring a coin
	// toss. They are counted here so the number stays visible in the
	// summary rather than silently vanishing from the totals.
	var sSkip, iSkip int
	// Cases out of scope for the version under test leave the numerator and
	// the denominator for the same reason, and are counted here so that
	// excluding them cannot quietly inflate the percentage.
	var sOOS, iOOS int
	// Instances that could not be opened or parsed. Not a pass and not a
	// fail, but it must never be silently dropped: an unscored case is
	// indistinguishable from one that was never there.
	var iUnread int

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
			sIndet := false
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
				sIndet = w == "indeterminate"
				sStatus = s
			}
			if len(schemaPaths) == 0 {
				continue
			}

			// A group the suite says produces a *valid* schema, out of
			// syntax that exists only in 1.1, prescribes no result for a
			// 1.0 processor: it cannot parse the document, so rejecting
			// it is conformant rather than wrong. Scoring it as a false
			// reject measured the 1.1 feature set against the 1.0 lane.
			// The group's instance tests go with it, because they can
			// only be validated against the schema that was not built.
			if !only11 && schemaValid && !sIndet && usesOnly11Syntax(schemaPaths) {
				if haveSchemaTest {
					sOOS++
				}
				for _, it := range g.ChildElements() {
					if it.Name.Local == "instanceTest" &&
						appliesOR(it.AttrValue("version"), tok) {
						iOOS++
					}
				}
				continue
			}

			// AllowDOCTYPE is off by default because a schema document
			// arriving from an untrusted source should not be able to
			// pull in external entities. A conformance corpus on disk is
			// not that, and two of its schema documents need it: the
			// IRI/URI type library (wgData/iri/TypeLibrary-URI-RFC3986.xsd
			// and TypeLibrary-IRI-RFC3987.xsd) builds its RFC 3986/3987
			// patterns out of an internal DTD subset, declaring one entity
			// per ABNF non-terminal so the regexes can be assembled
			// bottom-up instead of written out by hand. Refusing the
			// DOCTYPE made both documents unparseable, so iri-001 — whose
			// ElementDeclarations.xsd imports the library through
			// TypeLibrary-IRI-URI-driver.xsd — was scored SFALSEREJECT
			// against a schema the engine accepts once it can read it.
			// External entities stay off: the library needs only the
			// internal subset.
			sch, loadErr := xsd.LoadFiles(schemaPaths,
				xsd.Options{Resolver: &xsd.FileResolver{}, Version: version,
					ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})

			// Score the schema test itself.
			if haveSchemaTest {
				gotValid := loadErr == nil
				switch {
				case sIndet:
					// The schema is still loaded, because
					// the group's instance tests need it,
					// but neither outcome is scored.
					sSkip++
				case gotValid == schemaValid:
					sOK++
				default:
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
				if want == "indeterminate" {
					// An instance carries the value in its
					// own right, independent of its schema:
					// schA2.i and schA5.i are indeterminate
					// under schemas the suite calls valid.
					// No result being prescribed, the
					// document is not validated at all.
					iSkip++
					continue
				}
				df, err := os.Open(docPath)
				if err != nil {
					// An unreadable instance is not a result. It
					// left both the numerator and the denominator
					// unlogged, which is an invisible hole in a
					// ratchet-guarded number rather than a score.
					iUnread++
					fmt.Printf("IUNREAD\t%s\t%s\t%s\t%s\t%s\t%s\n",
						setName, gname, it.AttrValue("name"), status,
						filepath.Base(docPath), firstLine(err.Error()))
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
					// Likewise for a document the parser cannot
					// read. Some of these are deliberate: the
					// suite includes instances that are not
					// well-formed XML, for which no validity is
					// prescribed. Either way it is accounted for
					// and named, not dropped.
					iUnread++
					fmt.Printf("IUNREAD\t%s\t%s\t%s\t%s\t%s\t%s\n",
						setName, gname, it.AttrValue("name"), status,
						filepath.Base(docPath), firstLine(err.Error()))
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
	// Every category outside agree/disagree is printed, so that the size of
	// what is not being scored stays as visible as the percentage itself.
	fmt.Fprintf(os.Stderr, "SCHEMA  agree %d  disagree %d  indeterminate %d  out-of-scope %d  (%.2f%%)\n", sOK, sBad, sSkip, sOOS, 100*float64(sOK)/float64(sOK+sBad))
	fmt.Fprintf(os.Stderr, "INSTANCE agree %d  disagree %d  indeterminate %d  out-of-scope %d  unreadable %d  (%.2f%%)\n", iOK, iBad, iSkip, iOOS, iUnread, 100*float64(iOK)/float64(iOK+iBad))
	fmt.Fprintf(os.Stderr, "TOTAL   agree %d  disagree %d  indeterminate %d  out-of-scope %d  unreadable %d  (%.2f%%)\n", sOK+iOK, sBad+iBad, sSkip+iSkip, sOOS+iOOS, iUnread, 100*float64(sOK+iOK)/float64(sOK+iOK+sBad+iBad))
}
