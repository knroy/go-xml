// Command genfunctions extracts the F&O 3.1 function proformas into the
// normalized manifest xpath/spec/function-signatures.json.
//
// The audit's finding 6 is that xpath.Function carries a name, an arity and a
// callback but no declared parameter types, which is why twelve functions each
// needed a hand fix to stop accepting an empty sequence for a parameter F&O
// declares without "?" (commit 7668773) and why nothing prevented a
// thirteenth. A manifest derived from the specification rather than typed by
// hand is what makes that class of defect structural rather than incidental.
//
// The source is the vendored Recommendation, not the network:
//
//	testdata/xslt30-test/specs/functions-and-operators-31.html
//
// "XPath and XQuery Functions and Operators 3.1", W3C Recommendation
// 21 March 2017 — the same document published at
// https://www.w3.org/TR/xpath-functions-31/. Generation is therefore offline
// and deterministic, as the plan requires: a production build never fetches a
// W3C page, and the committed JSON is the reviewable artifact.
//
// Usage:
//
//	go run ./cmd/genfunctions -spec <html> -out xpath/spec/function-signatures.json
//
// The extraction reads the <div class="proto"> blocks, which is where the
// specification puts every signature. Two layouts appear: a one-line form for
// short signatures and a <table class="proto"> whose rows are one parameter
// each. Stripping tags flattens both to the same text, so one parser covers
// them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Entry is one (name, arity) row of the manifest.
//
// Params and Result are sequence-type spellings exactly as the specification
// writes them ("xs:string?", "item()*", "node()"), because that spelling is
// already the type language the library uses: xpath/subtype.go compares
// signatures in it, and xdm.FunctionItem.Signature stores it.
type Entry struct {
	Name   string   `json:"name"`
	Arity  int      `json:"arity"`
	Params []string `json:"params"`
	Result string   `json:"result"`
}

// protoStart finds each signature block. The specification opens one per
// signature; a function with several arities has several.
var protoStart = regexp.MustCompile(`<div class="proto">`)

var tag = regexp.MustCompile(`<[^>]+>`)

// sigName matches the head of a flattened proforma: a prefixed function name
// immediately followed by its opening parenthesis. sigResult matches the
// " as R" that follows the closing one.
//
// Only the four namespaces this library registers are taken. op: proformas
// describe operators reached through syntax rather than by name, xs: ones are
// constructor functions whose signature is uniformly (xs:anyAtomicType?), and
// eg: ones are the illustrative XSLT examples in the appendices — none of the
// three is a callable F&O function, and including them would inflate the
// manifest against a library that cannot call them by these names.
//
// The parameter list is found by balancing parentheses rather than by a
// regexp: a parameter type may itself be parenthesised — array:sort's third
// parameter is "function(item()*) as xs:anyAtomicType*" — and any
// non-greedy or greedy `\(.*\)` closes at the wrong one, producing a
// three-parameter array:sort whose result type is half of a function test.
var sigName = regexp.MustCompile(`^((?:fn|math|map|array):[\w.-]+)\(`)

var sigResult = regexp.MustCompile(`^\s*as\s+(.+?)\s*$`)

// variadic names the functions whose proforma declares no fixed arity.
// fn:concat is the only one in F&O 3.1.
var variadic = map[string]bool{"fn:concat": true}

// notational reports whether a proforma is the specification explaining its
// own notation rather than defining a function.
//
// Section 1.1 introduces the signature notation with the template
//
//	fn:function-name($parameter-name as parameter-type, ...) as return-type
//
// and illustrates the "*" occurrence indicator with an invented fn:median.
// Neither is an F&O function: no catalogue entry defines them and no
// processor implements either.
//
// The template is recognised by its placeholder parameter rather than by its
// name, because fn:function-name IS a real function (16.1.2, "$func as
// function(*)") and excluding that name would drop the real signature along
// with the template. fn:median has no real counterpart, so its name is the
// only handle it offers.
func notational(name, params string) bool {
	if strings.Contains(params, "$parameter-name") {
		return true
	}
	return name == "fn:median"
}

func main() {
	specPath := flag.String("spec", "testdata/xslt30-test/specs/functions-and-operators-31.html",
		"path to the vendored F&O 3.1 Recommendation")
	out := flag.String("out", "xpath/spec/function-signatures.json", "manifest to write")
	flag.Parse()

	raw, err := os.ReadFile(*specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genfunctions:", err)
		os.Exit(1)
	}
	entries, err := extract(string(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "genfunctions:", err)
		os.Exit(1)
	}
	blob, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "genfunctions:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(blob, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genfunctions:", err)
		os.Exit(1)
	}
	fmt.Printf("genfunctions: %d (name, arity) entries -> %s\n", len(entries), *out)
}

// extract pulls every callable proforma out of the Recommendation.
//
// Duplicate (name, arity) pairs are an error rather than a silent last-wins:
// the specification states each signature once, so a duplicate means the
// parser has misread a block, and a manifest that quietly disagrees with the
// specification is worse than no manifest.
func extract(src string) ([]Entry, error) {
	seen := map[string]Entry{}
	var order []string
	for _, m := range protoStart.FindAllStringIndex(src, -1) {
		text := flatten(block(src, m[1]))
		name, list, rest, ok := splitProforma(text)
		if !ok {
			continue // an eg: example, or prose that opened no signature
		}
		res := sigResult.FindStringSubmatch(rest)
		if res == nil {
			continue
		}
		result := res[1]
		if notational(name, list) {
			continue
		}
		// fn:concat is the one variadic function in F&O. Its proforma ends in
		// a literal "..." standing for further xs:anyAtomicType? parameters,
		// so it declares no single arity and cannot be a manifest row: the
		// library registers it at every arity from 2 upwards. Dropping the
		// trailing ellipsis would record a 2-argument fn:concat and imply
		// that concat("a","b","c") has no declared types, which is worse than
		// recording nothing. Variadic functions are excluded here and are
		// exempted by name where the manifest is enforced.
		if variadic[name] {
			continue
		}
		params, err := splitParams(list)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		e := Entry{Name: name, Arity: len(params), Params: params, Result: result}
		key := fmt.Sprintf("%s#%d", e.Name, e.Arity)
		if prev, dup := seen[key]; dup {
			if !sameEntry(prev, e) {
				return nil, fmt.Errorf("conflicting proformas for %s: %v vs %v", key, prev, e)
			}
			continue
		}
		seen[key] = e
		order = append(order, key)
	}
	sort.Strings(order)
	entries := make([]Entry, 0, len(order))
	for _, k := range order {
		entries = append(entries, seen[k])
	}
	return entries, nil
}

// splitProforma breaks "fn:f($a as T, $b as U) as R" into the name, the
// parameter list "$a as T, $b as U", and the remainder " as R".
//
// The closing parenthesis is found by counting depth from the opening one, so
// a parenthesised parameter type does not end the list early. Returning ok =
// false covers both a block that names no callable function and one whose
// parentheses never balance, neither of which should become a manifest row.
func splitProforma(text string) (name, list, rest string, ok bool) {
	m := sigName.FindStringSubmatchIndex(text)
	if m == nil {
		return "", "", "", false
	}
	name = text[m[2]:m[3]]
	open := m[1] // just past the "("
	depth := 1
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return name, text[open:i], text[i+1:], true
			}
		}
	}
	return "", "", "", false
}

// block returns the text of one proto div, ending at the next one or at the
// close of the enclosing definition, whichever comes first.
//
// The specification nests no proto div inside another, so this needs no depth
// tracking; the two terminators are what actually bound a signature there.
func block(src string, from int) string {
	next := strings.Index(src[from:], `<div class="proto">`)
	end := strings.Index(src[from:], `</dd>`)
	if next != -1 && (end == -1 || next < end) {
		end = next
	}
	if end == -1 {
		return src[from:]
	}
	return src[from : from+end]
}

// flatten turns markup into the one-line proforma text. The table layout and
// the inline layout collapse to the same string, which is the whole reason
// one regexp can read both.
func flatten(s string) string {
	s = tag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// splitParams breaks a parameter list at its top-level commas and reduces each
// parameter to its declared type.
//
// The split has to respect nesting: a map or array parameter type carries a
// comma of its own — "map(xs:string, item()*)" is one parameter, not two — and
// a function test carries both parentheses and commas.
func splitParams(list string) ([]string, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return []string{}, nil
	}
	var parts []string
	depth, cur := 0, strings.Builder{}
	for _, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
		}
		cur.WriteRune(r)
	}
	if strings.TrimSpace(cur.String()) != "" {
		parts = append(parts, cur.String())
	}
	types := make([]string, 0, len(parts))
	for _, p := range parts {
		t, err := paramType(p)
		if err != nil {
			return nil, err
		}
		types = append(types, t)
	}
	return types, nil
}

// paramType reduces "$sourceString as xs:string?" to "xs:string?".
//
// Everything after the "as" is the declared sequence type. A proforma with no
// "as" is malformed for this purpose and is reported rather than guessed at.
func paramType(p string) (string, error) {
	p = strings.TrimSpace(p)
	i := strings.Index(p, " as ")
	if i < 0 {
		return "", fmt.Errorf("parameter %q declares no type", p)
	}
	return strings.TrimSpace(p[i+len(" as "):]), nil
}

func sameEntry(a, b Entry) bool {
	if a.Name != b.Name || a.Arity != b.Arity || a.Result != b.Result {
		return false
	}
	for i := range a.Params {
		if a.Params[i] != b.Params[i] {
			return false
		}
	}
	return true
}
