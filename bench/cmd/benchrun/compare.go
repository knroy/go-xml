package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/xdm"
)

// Outcome is what one engine produced for one item in the correctness run.
type Outcome struct {
	Failed  bool   // non-zero exit (or, for validators, a verdict of "error")
	Verdict string // validators only: valid, invalid or error
	Output  []byte // transforms, queries and parses
	Log     string // stdout+stderr, for error codes and diagnostics
}

var errCode = regexp.MustCompile(`\b(?:XTSE|XTDE|XTTE|XTRE|XPST|XPDY|XPTY|XQST|XQDY|XQTY|FO[A-Z]{2}|SEPM|SESU|SERE|SEXQ)\d{4}\b`)

// verdict classifies a validator run: invalid if pattern matches the log,
// otherwise valid on a clean exit and error on any other.
func verdict(exitOK bool, log, invalidPattern string) string {
	if invalidPattern != "" && regexp.MustCompile(invalidPattern).MatchString(log) {
		return "invalid"
	}
	if exitOK {
		return "valid"
	}
	return "error"
}

// agree reports whether a reference outcome matches go-xml's, and if not, a
// short reason. Validators agree on the verdict; everything else agrees on
// the normalised output, or on both failing (with the same error code when
// both logs name one).
func agree(area, norm string, gx, ref Outcome) (bool, string) {
	if area == "xsd" || area == "rng" {
		if gx.Verdict == ref.Verdict {
			return true, ""
		}
		return false, fmt.Sprintf("verdict: go-xml %s, reference %s", gx.Verdict, ref.Verdict)
	}
	switch {
	case gx.Failed && ref.Failed:
		a, b := errCode.FindString(gx.Log), errCode.FindString(ref.Log)
		if a != "" && b != "" && a != b {
			return false, fmt.Sprintf("both failed, with different errors: go-xml %s, reference %s", a, b)
		}
		return true, "both raised an error"
	case gx.Failed:
		return false, "go-xml failed: " + firstLine(gx.Log)
	case ref.Failed:
		return false, "reference failed: " + firstLine(ref.Log)
	}
	a, err := normalise(norm, gx.Output)
	if err != nil {
		return false, "go-xml output: " + err.Error()
	}
	b, err := normalise(norm, ref.Output)
	if err != nil {
		return false, "reference output: " + err.Error()
	}
	if bytes.Equal(a, b) {
		return true, ""
	}
	return false, diffSummary(a, b)
}

// normalise applies a workload's agreement rule to an output.
//
//	none        byte-equal
//	whitespace  every whitespace run collapsed to one space, ends trimmed
//	text        the document's string value, whitespace-collapsed; output
//	            that is not one XML document is whitespace-collapsed as is
//	xml-c14n    parsed with xdm and written as Canonical XML 1.0, so the
//	            XML declaration, attribute order, quoting and empty-element
//	            syntax stop mattering
func normalise(rule string, out []byte) ([]byte, error) {
	switch rule {
	case "", "none":
		return out, nil
	case "whitespace":
		return collapse(out), nil
	case "text":
		if tree, err := parseOutput(out); err == nil {
			return collapse([]byte(tree.Root.StringValue())), nil
		}
		return collapse(out), nil
	case "xml-c14n":
		tree, err := parseOutput(out)
		if err != nil {
			return nil, fmt.Errorf("not well-formed XML: %w", err)
		}
		return c14n.Bytes(tree.Root, c14n.Options{Algorithm: c14n.Inclusive10})
	}
	return nil, fmt.Errorf("unknown normalise %q", rule)
}

func parseOutput(out []byte) (*xdm.Tree, error) {
	return xdm.ParseString(string(out), xdm.ParseOptions{AllowDOCTYPE: true})
}

func collapse(b []byte) []byte {
	return []byte(strings.Join(strings.Fields(string(b)), " "))
}

// diffSummary names the first differing byte and shows a little of each side.
func diffSummary(a, b []byte) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	clip := func(s []byte) string {
		lo := max(i-20, 0)
		hi := min(i+40, len(s))
		if lo > len(s) {
			lo = len(s)
		}
		return fmt.Sprintf("%q", s[lo:hi])
	}
	return fmt.Sprintf("differ at byte %d (lengths %d, %d): go-xml %s, reference %s",
		i, len(a), len(b), clip(a), clip(b))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
