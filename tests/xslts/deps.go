package xslts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Deciding which tests are in scope.
//
// This is the part of a conformance run that decides what the number means. A
// filter that is too generous reports the engine failing tests it was never
// meant to pass; one that is too strict reports a high percentage of a small
// number. Both are worse than no figure, so each exclusion below names what
// it excludes and why.

// supportedFeatures are the optional features this engine implements. A test
// depending on one not listed here is out of scope rather than failing.
var supportedFeatures = map[string]bool{
	// xsl:import-schema and schema-aware validation are implemented, and the
	// xsd package is what backs them.
	"schema_aware": true,
	// The namespace axis is implemented; XSLT 2.0 requires it where XPath 2.0
	// deprecates it.
	"namespace_axis": true,
	// Serialization is implemented for the output methods XSLT 2.0 defines.
	"serialization": true,
	// A DOCTYPE is parsed under AllowDOCTYPE, which the runner sets for the
	// suite's own documents.
	"dtd": true,
	// The XSD 1.1 built-in types are available through the xsd package.
	"built_in_derived_types": true,
	// XSLT 1.0 backwards-compatible behaviour is implemented: the XPath 2.0
	// appendix B.1 coercion rules are in force for expressions written inside
	// a version="1.0" scope. See compatModeAt in the xslt package.
	"backwards_compatibility": true,
	// xsl:evaluate compiles and runs an XPath expression built at run time.
	"dynamic_evaluation": true,
	// Function items, inline functions and the fn: higher-order library are
	// implemented: XPath 3.0 passes the QT3 suite at 100%. The feature was
	// listed unsupported long after it stopped being so, which excluded two
	// hundred cases that pass.
	"higher_order_functions": true,
}

// unsupportedFeatures are the ones this engine does not implement, listed so
// that the reason is recorded rather than inferred from absence.
var unsupportedFeatures = map[string]string{
	"streaming":                 "XSLT 3.0",
	"streaming-fallback":        "XSLT 3.0",
	"XPath_3.1":                 "XPath 3.1",
	"disabling_output_escaping": "not implemented; the serializer escapes always",
	"XML_1.1":                   "the parser implements XML 1.0",
	// XSD_1.1 is not listed: it is answered by supports, because whether the
	// processor has it is a question about the XSLT version being measured
	// rather than about the engine. See the commentary there.
	"HTML4":                                 "the HTML output method targets HTML5",
	"HTML5":                                 "not implemented",
	"xsl-stylesheet-processing-instruction": "not implemented",
}

// supports reports whether the engine offers an optional feature to a
// processor of the given version.
//
// Almost every feature is a flat property of the engine, and supportedFeatures
// answers for those. XSD_1.1 is not: it is a property of the LANGUAGE VERSION
// the processor is claiming to implement.
//
// XSLT 3.0 section 3.2 says it in as many words -- "XSLT 3.0 processors may
// optionally include types defined in XSD 1.1 ... and adds one new type:
// xs:dateTimeStamp". It is an option the 3.0 specification grants and this
// engine takes: the xsd package implements XSD 1.1, xs:dateTimeStamp is a
// built-in, and the regular expression grammar is 1.1's, admitting the
// character-class subtractions ([^a-d-b-c], [a-a-x-x]) that 1.0 makes
// FORX0002.
//
// The XSLT 2.0 Recommendation grants no such option -- it does not mention XSD
// 1.1 once -- so an XSLT 2.0 processor is an XSD 1.0 processor and the answer
// there is no. That is not a fiction about the engine: at the 2.0 target the
// engine is being measured AS an XSLT 2.0 processor, and every one of the
// suite's XSD_1.1 cases in a 2.0 scope was written for one.
//
// Claiming the feature at both targets was measured and costs more than it
// buys: the regex-syntax-xslt20 set has four satisfied="false" cases with no
// XSD 1.1 counterparts to replace them, so they leave the denominator and
// nothing arrives, taking XSLT 2.0 from 6149 to 6145. At the 3.0 target every
// such case is paired -- regex-syntax-0056/0056a, 0086/0086a, 0102/0102a,
// error-0951/0951a, type-available-0151/0151a -- so the swap is even by
// construction and the engine's actual answer decides each.
func supports(feature string, target Target) bool {
	if feature == "XSD_1.1" {
		return target == XSLT30
	}
	return supportedFeatures[feature]
}

// admits reports whether a spec value admits a processor of the target
// version.
//
// The value is a space-separated list of tokens, each naming a version and
// optionally a "+" meaning that version or later. "XSLT10+" admits 2.0;
// "XSLT30+" does not; "XSLT10 XSLT20" admits it explicitly.
func admits(value string, target Target) bool {
	for _, tok := range strings.Fields(value) {
		switch tok {
		case "XSLT10+", "XSLT20+":
			return true
		case "XSLT20":
			if target == XSLT20 {
				return true
			}
		case "XSLT30", "XSLT30+":
			if target == XSLT30 {
				return true
			}
		}
	}
	return false
}

// inScope reports whether a case should run, and why not when it should not.
//
// A case's own dependencies replace the test-set's rather than adding to
// them, which is how the suite is written: seven thousand of the fifteen
// thousand cases state none and inherit the set's. Reading only the
// case-level ones admits tests needing XSLT 3.0 and counts them as failures
// of this engine.
func inScope(set *TestSet, tc *TestCase, target Target) (bool, string) {
	// A case's dependencies override the set's *per kind*, not wholesale.
	//
	// Reading it as wholesale loses the set's version gate whenever a case
	// states any dependency of its own: regex-syntax declares XSLT30+ once
	// for the set, and its cases declare only feature dependencies, so all
	// 1,500 of them ran as if they were XSLT 2.0 tests and reported the
	// engine failing at XSLT 3.0 syntax.
	deps := tc.Dependencies
	if len(deps.Specs) == 0 {
		deps.Specs = set.Dependencies.Specs
	}
	if len(deps.Features) == 0 {
		deps.Features = set.Dependencies.Features
	}
	if len(deps.Others) == 0 {
		deps.Others = set.Dependencies.Others
	}

	// A streamability test is XSLT 3.0 by construction, whatever it declares.
	// Streaming is not implemented at either target, so the exclusion does not
	// depend on one.
	if tc.Test.PostureAndSweep != nil {
		return false, "streamability (XSLT 3.0)"
	}
	// A package test is XSLT 3.0 by construction, so it is out of scope at
	// the 2.0 target however its own metadata reads. At the 3.0 target it is
	// exactly what is under test.
	if target == XSLT20 && len(tc.Test.Packages) > 0 {
		return false, "xsl:package (XSLT 3.0)"
	}
	// A package may also be declared by the environment rather than by
	// <test>, and it makes the case just as much an XSLT 3.0 test either way:
	// the principal stylesheet still has to name it in xsl:use-package, which
	// a 2.0 processor has no element for. collection-006 is the case --
	// declared XSLT20+, environment-scoped package, and both of its
	// stylesheets written version="3.0" around xsl:package and
	// xsl:use-package -- so at the 2.0 target it failed on the xsl:package
	// its own metadata never mentioned.
	if target == XSLT20 {
		for _, env := range tc.Environments {
			if len(env.Packages) > 0 {
				return false, "xsl:package (XSLT 3.0)"
			}
		}
		for _, env := range set.Environments {
			if !envReferenced(env.Name, tc) || len(env.Packages) == 0 {
				continue
			}
			return false, "xsl:package (XSLT 3.0)"
		}
	}
	if tc.Test.InitialFunction != nil {
		return false, "initial function (XSLT 3.0)"
	}
	// A case that dereferences a document only the network can supply is out
	// of scope, whether or not it remembered to say so.
	//
	// The suite has a purpose-built dependency for this -- <available_documents
	// value="..."/> -- and the loop over deps.Others below already excludes
	// every case declaring one. unparsed-text-2003 is the case that does not
	// declare one and needs to: it asks
	// unparsed-text-available('http://www.w3.org/Consortium/mission.html') and
	// asserts true, which no offline processor can answer. Its neighbour
	// unparsed-text-2002 reads the same two URLs from the same environment and
	// DOES declare available_documents for both, so the omission on 2003 is a
	// slip in the catalog rather than a statement that the case is offline.
	//
	// Rather than name the case, the exclusion is derived from the fact that
	// makes it unrunnable. The environment both cases share declares those
	// URLs as <resource file="http://..."/> -- a resource whose @file is an
	// absolute http(s) URI rather than a path beside the test set is one the
	// harness can only obtain over the wire -- and the exclusion fires only
	// when the case's own stylesheet actually contains that URI. That keeps
	// the other forty-odd cases sharing the environment in scope: they never
	// mention the remote resources and pass offline today.
	if why := remoteResource(set, tc); why != "" {
		return false, why
	}
	// And so is a stylesheet that can only be read as XSLT 3.0, whatever the
	// catalog's <spec> says. Two cases are mislabelled XSLT20+ in the suite's
	// own metadata: character-map-026 asks for the adaptive output method,
	// and variable-4802 relies on text value templates. Neither exists in
	// XSLT 2.0, so a 2.0 processor cannot produce the asserted result and
	// counting the disagreement as a conformance failure measures the
	// catalog rather than the engine.
	// Only when the target is 2.0: at the 3.0 target these constructs are
	// exactly what is under test.
	if target == XSLT20 {
		if why := xslt30OnlyConstruct(set, tc); why != "" {
			return false, why
		}
	}

	// The version gate. A case with no spec dependency at any level states no
	// constraint, and runs.
	if len(deps.Specs) > 0 {
		ok := false
		for _, s := range deps.Specs {
			if admits(s.Value, target) {
				ok = true
				break
			}
		}
		if !ok {
			return false, "spec " + deps.Specs[0].Value
		}
	}

	for _, f := range deps.Features {
		// satisfied="false" means the test needs the feature *absent*, which
		// a processor lacking it satisfies.
		if f.Satisfied == "false" {
			if supports(f.Value, target) {
				return false, "needs " + f.Value + " absent"
			}
			continue
		}
		if supports(f.Value, target) {
			continue
		}
		if why, known := unsupportedFeatures[f.Value]; known {
			return false, f.Value + ": " + why
		}
		return false, "unknown feature " + f.Value
	}

	// Any dependency this runner does not model excludes the test. Ignoring
	// one would run a test under conditions it did not ask for, and report
	// the mismatch as a failure.
	for _, o := range deps.Others {
		switch o.XMLName.Local {
		case "spec", "feature":
			// Modelled above; encoding/xml also collects them here.
		case "unicode-version", "unicode-normalization-form",
			"default-language", "language", "languages_for_numbering",
			"combinations_for_numbering", "year_component_values",
			"formatted-date", "sweep_and_posture", "on-multiple-match",
			"xsd-version", "available_documents", "unparsed-text",
			"enviromentVariable", "environmentVariable":
			return false, "depends on " + o.XMLName.Local
		default:
			return false, "unmodelled dependency " + o.XMLName.Local
		}
	}
	return true, ""
}

// XSLT 3.0 constructs that no forwards-compatible reading of a 2.0 stylesheet
// can accommodate.
//
// Deliberately narrow. The stylesheet's own version="3.0" is *not* usable as
// the signal: 2,878 of the suite's stylesheet files carry it, and most are
// perfectly good 2.0 stylesheets that this engine runs and passes, so gating
// on it would delete thousands of passing tests from the denominator. Each
// pattern below names a construct that changes the meaning of the stylesheet
// rather than merely its declared version.
var xslt30Constructs = []struct {
	re  *regexp.Regexp
	why string
}{
	{
		// The adaptive and JSON output methods are XSLT 3.0 serialization
		// methods. Every other test using one is already out of scope
		// through a correct <spec> value; character-map-026 leaks through a
		// mislabelled one.
		regexp.MustCompile(`method\s*=\s*["'](adaptive|json)["']`),
		"adaptive/json output method (XSLT 3.0)",
	},
	{
		// fn:current-output-uri is an XSLT 3.0 function -- the name does not
		// occur anywhere in the XSLT 2.0 Recommendation -- so a 2.0 processor
		// must raise XPST0017 for it, which is what this engine does once the
		// function library is held at the module's own version.
		//
		// result-document-1006 is the only case affected: it is declared
		// XSLT20+ and its stylesheet is written version="2.0", but it opens
		// by binding a variable to current-output-uri() before it can reach
		// the nested xsl:result-document whose XTRE1495 the test is about.
		// Every other stylesheet in the suite that names the function is
		// written version="3.0" and is already out of scope through a correct
		// <spec>. Counting the XPST0017 as a failure measured the catalog
		// rather than the engine.
		regexp.MustCompile(`\bcurrent-output-uri\s*\(`),
		"fn:current-output-uri (XSLT 3.0)",
	},
}

// xslt30OnlyConstruct reports why a case's stylesheets put it outside XSLT
// 2.0, or "" if nothing does.
func xslt30OnlyConstruct(set *TestSet, tc *TestCase) string {
	for _, sr := range tc.Test.Stylesheets {
		src := sr.Content
		if sr.File != "" {
			data, err := os.ReadFile(
				filepath.Join(set.Dir, filepath.FromSlash(sr.File)))
			if err != nil {
				continue
			}
			src = string(data)
		}
		if src == "" {
			continue
		}
		for _, c := range xslt30Constructs {
			if c.re.MatchString(src) {
				return c.why
			}
		}
	}
	return ""
}

// remoteResource reports why a case needs the network, or "" if it does not.
//
// An environment <resource> whose @file is an absolute http(s) URI is a
// document the harness has no local copy of; every other resource names a file
// shipped beside the test set. A case is excluded only when one of its own
// stylesheets names such a URI, because the environments declaring them are
// shared with many cases that never read them.
//
// The comparison is on the URI as written. The suite's stylesheets and its
// environments do disagree on scheme for these documents -- the environment
// says http, unparsed-text-2002 asks for https -- so both forms of each
// declared resource are looked for.
func remoteResource(set *TestSet, tc *TestCase) string {
	var remote []string
	add := func(envs []Environment, only bool) {
		for _, env := range envs {
			if only && !envReferenced(env.Name, tc) {
				continue
			}
			for _, r := range env.Resources {
				u := r.File
				if u == "" {
					u = r.URI
				}
				switch {
				case strings.HasPrefix(u, "http://"):
					remote = append(remote, u, "https://"+u[len("http://"):])
				case strings.HasPrefix(u, "https://"):
					remote = append(remote, u, "http://"+u[len("https://"):])
				}
			}
		}
	}
	add(tc.Environments, false)
	add(set.Environments, true)
	if len(remote) == 0 {
		return ""
	}
	for _, sr := range tc.Test.Stylesheets {
		src := sr.Content
		if sr.File != "" {
			data, err := os.ReadFile(
				filepath.Join(set.Dir, filepath.FromSlash(sr.File)))
			if err != nil {
				continue
			}
			src = string(data)
		}
		for _, u := range remote {
			if strings.Contains(src, u) {
				return "depends on available_documents"
			}
		}
	}
	return ""
}

// envReferenced reports whether tc runs against the test-set environment
// called name.
//
// A case names its environment with <environment ref="..."/>, which the
// catalog resolves against the set's own declarations and then the catalog's.
// Only the set's are consulted here: the version gate this serves asks whether
// the case brings a library package with it, and a package is declared beside
// the stylesheets that use it rather than in the shared catalog.
func envReferenced(name string, tc *TestCase) bool {
	if name == "" {
		return false
	}
	for _, e := range tc.Environments {
		if e.Ref == name {
			return true
		}
	}
	return false
}
