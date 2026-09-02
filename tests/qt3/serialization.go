package qt3

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xslt"
)

// This file implements the suite's three serialization assertions:
// serialization-matches, assert-serialization and assert-serialization-error.
//
// They are the only assertions that are not about the *value* a case
// produces. A query's result is a sequence of items in the data model, and
// every other assertion interrogates it as such; these three ask what
// happens when that sequence is written out, which is a second step governed
// by the serialization parameters the query's own prolog declared. The ser/
// test sets exist almost entirely to exercise that step: six hundred cases
// across method-xml, method-html, method-xhtml, method-text, method-json and
// method-adaptive, each of which states "declare option output:method" and
// then asserts on the characters that come out.
//
// The serialiser itself is not written here. xslt.Serialize implements the
// XSLT and XQuery Serialization 3.1 specification, which is a single
// specification shared by both host languages, and reimplementing it for the
// harness would measure the harness rather than the engine.

// serialized returns the case's result written with the serialization
// parameters its prolog declared, serialising once and caching the answer.
//
// The result is cached because a case may carry several serialization
// assertions over one result — the JSON set writes half a dozen
// serialization-matches under an all-of, each probing a different part of the
// same document — and each would otherwise repeat the whole rendering.
func serialized(res *outcome) (string, error) {
	if res.haveSerial {
		return res.serialized, res.serialErr
	}
	res.haveSerial = true
	// An evaluation error is carried through rather than replaced: the
	// sequence never existed, so there is nothing to serialise, and a case
	// asserting a serialization error is entitled to be satisfied by an
	// error the evaluator raised first. SERE0022 for two map keys that
	// render alike is the case in point — an engine may discover it while
	// building the map or while writing it, and both are the same error.
	if res.err != nil {
		res.serialErr = res.err
		return "", res.serialErr
	}
	opts, err := serializationSettings(res.serialParams, res.paramDoc)
	if err != nil {
		res.serialErr = err
		return "", err
	}
	var sb strings.Builder
	if err := xslt.Serialize(&sb, res.seq, opts, opts.InlineCharMap); err != nil {
		res.serialErr = err
		return "", err
	}
	res.serialized = sb.String()
	return res.serialized, nil
}

// serializationSettings turns a prolog's "declare option output:*" set into
// the output settings the serialiser reads.
//
// The defaults are the specification's own: omit-xml-declaration is "no", so
// the declaration is written unless the query asks for it to be left out.
// That is what the method-xml cases assume -- K2-Serialization-22 declares
// only standalone="yes" and asks to see it, which it can only do in a
// declaration nobody asked for explicitly.
//
// Method is deliberately left empty when unstated, so that the serialiser
// chooses it from the result — a document whose element is <html> is written
// as HTML — which is the specification's rule and not a default that can be
// filled in ahead of the result existing.
func serializationSettings(params map[string]string, paramDoc *xdm.Node) (xslt.OutputSettings, error) {
	o := xslt.OutputSettings{Encoding: "UTF-8"}
	// The adaptive method's default is the other way round. Serialization
	// 3.1 §10 renders a node by handing it to the XML output method, so the
	// declaration is something it *can* write, but a debugger's view of a
	// sequence is not a document and the method defaults to leaving it out;
	// every method-adaptive case anchors its pattern with ^ and $ around the
	// items alone. output-0721 is the case that shows the parameter still
	// works when a query asks for it.
	if strings.EqualFold(params["method"], "adaptive") {
		o.OmitXMLDecl = true
	}
	// The parameter document is applied FIRST, so that a parameter the prolog
	// also declares overrides it.
	//
	// XQuery 3.1 §2.2.4 settles the direction: "if a serialization parameter
	// is specified both in the parameter document and in an option
	// declaration, the value in the option declaration takes precedence".
	// (§25.1's opposite rule is XSLT's, where the parameter document is named
	// by xsl:result-document and is the more specific of the two.) The case
	// that pins it is Serialization-xml-04, whose title is "explicit
	// declaration overrides parameter document": its prolog says indent "no"
	// and omit-xml-declaration "yes" where the document says the reverse, and
	// it asks for an unindented result with no declaration -- while still
	// taking cdata-section-elements from the document, which the prolog does
	// not mention.
	if paramDoc != nil {
		if err := xslt.ApplyParameterDocument(paramDoc, &o); err != nil {
			return o, err
		}
	}
	for name, val := range params {
		switch name {
		case "parameter-document":
			// The document itself is fetched by the caller, which is the
			// only place that knows where a test-set's files live, and is
			// applied above.
			continue
		case "use-character-maps":
			// A character map is spelled out as markup in a parameter
			// document; there is no syntax for one in a prolog option, whose
			// value is a string literal. No case declares it.
			continue
		default:
			if err := xslt.SetSerializationParam(&o, name, val); err != nil {
				return o, err
			}
		}
	}
	return o, nil
}

// serializationMatches implements <serialization-matches>: the serialised
// result must match a regular expression.
//
// The expression is an XPath one — the suite is written for fn:matches, with
// its @flags — and it is unanchored, so it holds when it matches anywhere in
// the output. That is why the sets that mean "the whole output and nothing
// else" write ^...$ themselves, as method-adaptive does throughout.
func serializationMatches(res *outcome, a Assertion) (bool, string) {
	out, err := serialized(res)
	if err != nil {
		return false, "serialization failed: " + err.Error()
	}
	// The suite indents its assertion text with the markup around it. A
	// pattern is only trimmed of that framing whitespace, never of anything
	// inside it, because a pattern is otherwise significant to the byte.
	pat := strings.TrimSpace(a.Value)
	re, err := xpath.CompileRegexpVersion(pat, a.Flags, xpath.XPath31)
	if err != nil {
		return false, "unusable pattern " + pat + ": " + err.Error()
	}
	if re.MatchString(out) {
		return true, ""
	}
	if e := xpath.RegexpErr(re); e != nil {
		return false, "matching " + pat + ": " + e.Error()
	}
	return false, fmt.Sprintf("serialized %q does not match %q", elide(out), pat)
}

// serializationEquals implements <assert-serialization>: the serialised
// result must equal the expected text.
//
// Comparison is on the collapsed forms as well as the exact ones, because the
// expected value carries the indentation of the test-set file around it and
// an engine is free to indent differently within what it is not told to
// preserve.
func serializationEquals(res *outcome, a Assertion) (bool, string) {
	out, err := serialized(res)
	if err != nil {
		return false, "serialization failed: " + err.Error()
	}
	want := strings.TrimSpace(a.Value)
	got := strings.TrimSpace(out)
	if got == want || collapse(got) == collapse(want) {
		return true, ""
	}
	return false, fmt.Sprintf("serialized %q, want %q", elide(got), elide(want))
}

// serializationErrorIs implements <assert-serialization-error>: writing the
// result must raise the named error.
//
// The code is compared when the engine produced one, exactly as the <error>
// assertion does: accepting any error would let a wrong-code bug pass, and
// scoring an error that carries no recognisable code as a mismatch would
// punish an engine for the shape of its message rather than its behaviour.
func serializationErrorIs(res *outcome, a Assertion) (bool, string) {
	out, err := serialized(res)
	if err == nil {
		return false, fmt.Sprintf("expected serialization error %s, serialized %q",
			a.Code, elide(out))
	}
	code := xdm.ErrorCode(err)
	if code == "" || a.Code == "" || a.Code == "*" ||
		code == a.Code || sameErrorCode(code, a.Code) {
		return true, ""
	}
	return false, fmt.Sprintf("serialization error %s, want %s", code, a.Code)
}

// collapse reduces every run of whitespace to one space, which is how two
// spellings of the same indented markup are compared.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// elide shortens a string for a failure message. A serialised document can be
// thousands of characters and the whole of it in a log line hides the rest of
// the run; the head is enough to identify what came out.
func elide(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// paramDocElement returns a parsed parameter document's
// output:serialization-parameters element.
//
// A document whose element is something else is not a parameter document, and
// nil is returned rather than an error: the option that named it is then
// ignored, which is the same outcome as the document not being there at all.
func paramDocElement(doc *xdm.Node) *xdm.Node {
	if doc == nil {
		return nil
	}
	el := doc
	if el.Kind != xdm.KindElement {
		el = nil
		for _, c := range doc.Children {
			if c.Kind == xdm.KindElement {
				el = c
				break
			}
		}
	}
	if el == nil || el.Name.URI != nsSerialization ||
		el.Name.Local != "serialization-parameters" {
		return nil
	}
	return el
}

// nsSerialization is the namespace a serialization parameter document uses.
const nsSerialization = "http://www.w3.org/2010/xslt-xquery-serialization"
