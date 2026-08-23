package xslts

import "strings"

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
}

// unsupportedFeatures are the ones this engine does not implement, listed so
// that the reason is recorded rather than inferred from absence.
var unsupportedFeatures = map[string]string{
	"higher_order_functions":                "XPath 3.0",
	"streaming":                             "XSLT 3.0",
	"streaming-fallback":                    "XSLT 3.0",
	"XPath_3.1":                             "XPath 3.1",
	"dynamic_evaluation":                    "not implemented; xsl:evaluate is XSLT 3.0",
	"backwards_compatibility":               "XSLT 1.0 compatibility mode is not implemented",
	"disabling_output_escaping":             "not implemented; the serializer escapes always",
	"XML_1.1":                               "the parser implements XML 1.0",
	"XSD_1.1":                               "available, but the suite's tests assume 1.1 defaults",
	"HTML4":                                 "the HTML output method targets HTML5",
	"HTML5":                                 "not implemented",
	"xsl-stylesheet-processing-instruction": "not implemented",
}

// admitsXSLT20 reports whether a spec value admits an XSLT 2.0 processor.
//
// The value is a space-separated list of tokens, each naming a version and
// optionally a "+" meaning that version or later. "XSLT10+" admits 2.0;
// "XSLT30+" does not; "XSLT10 XSLT20" admits it explicitly.
func admitsXSLT20(value string) bool {
	for _, tok := range strings.Fields(value) {
		switch tok {
		case "XSLT10+", "XSLT20", "XSLT20+":
			return true
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
func inScope(set *TestSet, tc *TestCase) (bool, string) {
	deps := tc.Dependencies
	if len(deps.Specs) == 0 && len(deps.Features) == 0 && len(deps.Others) == 0 {
		deps = set.Dependencies
	}

	// A streamability test is XSLT 3.0 by construction, whatever it declares.
	if tc.Test.PostureAndSweep != nil {
		return false, "streamability (XSLT 3.0)"
	}
	// So is a package test: xsl:package is 3.0.
	if len(tc.Test.Packages) > 0 {
		return false, "xsl:package (XSLT 3.0)"
	}
	if tc.Test.InitialFunction != nil {
		return false, "initial function (XSLT 3.0)"
	}

	// The version gate. A case with no spec dependency at any level states no
	// constraint, and runs.
	if len(deps.Specs) > 0 {
		ok := false
		for _, s := range deps.Specs {
			if admitsXSLT20(s.Value) {
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
			if supportedFeatures[f.Value] {
				return false, "needs " + f.Value + " absent"
			}
			continue
		}
		if supportedFeatures[f.Value] {
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
