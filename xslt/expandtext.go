package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Text value templates, XSLT 3.0 section 5.6.2.
//
// When [xsl:]expand-text is in force, a text node in a sequence constructor is
// no longer literal text: braces in it delimit XPath expressions, exactly as
// they do in an attribute value template. The parsing is therefore the AVT
// parser's job and is not repeated here — compileAVT already knows that "{{"
// is an escape and that a brace inside a string literal or an XPath comment
// does not close the expression, which is the part that is easy to get wrong.
//
// What this file adds is the decision of *whether* a given text node is a
// template, which the AVT machinery has no reason to know. The attribute is
// inherited, so the answer is found by walking up the stylesheet tree rather
// than being carried on the compiler: a value on an inner element overrides
// the one outside it, and an included module keeps its own answer.

// expandTextAt reports whether text nodes that are children of el are text
// value templates.
//
// Rule (c) of 5.6.2: the innermost ancestor-or-self element carrying the
// attribute decides, and its value must be yes. An element with no such
// ancestor is not expanding, which is what keeps every XSLT 2.0 stylesheet
// behaving as it always did — the attribute did not exist, so no ancestor can
// carry it.
func expandTextAt(el *xdm.Node) bool {
	// Text value templates are an XSLT 3.0 feature. A 2.0 stylesheet may
	// still carry the attribute — it is one of the standard attributes, so it
	// is allowed anywhere — but it selects behaviour that version of the
	// language cannot produce, and honouring it changes the meaning of a
	// stylesheet that was written before the feature existed. function-1902
	// is exactly that case: a 2.0 module whose xsl:message carries
	// expand-text="yes" and whose expected output is the run in which the
	// braces stay literal.
	if !expandTextVersion(el) {
		return false
	}
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		v, ok := expandTextAttr(cur)
		if !ok {
			continue
		}
		return v
	}
	return false
}

// expandTextAttr reads the attribute in whichever of its two spellings applies
// to this element.
//
// An XSLT element spells it "expand-text"; anything else — a literal result
// element, or a foreign element in the stylesheet — must prefix it, because on
// those an unprefixed attribute belongs to the result rather than to XSLT.
// Reading both spellings on both kinds of element would let a literal <out
// expand-text="yes"> silently change meaning while also emitting the
// attribute, and cvt-003 is written to catch exactly that confusion.
func expandTextAttr(el *xdm.Node) (bool, bool) {
	var raw string
	if el.Name.URI == xdm.NSXSL {
		if a := el.Attr("", "expand-text"); a != nil {
			raw = a.Value
		} else {
			return false, false
		}
	} else if a := el.Attr(xdm.NSXSL, "expand-text"); a != nil {
		raw = a.Value
	} else {
		return false, false
	}
	// The attribute is boolean, and 3.7 lets a boolean attribute be written
	// with leading or trailing whitespace: cvt-004 writes " yes ".
	switch strings.TrimSpace(raw) {
	case "yes", "true", "1":
		return true, true
	}
	return false, true
}

// checkExpandText validates the attribute's value wherever it appears.
//
// It is a separate pass from expandTextAt because that one is asked only about
// elements that contain text, while a bad value is an error even on an element
// whose content is empty. XTSE0020 is the code for an attribute whose value is
// not one the summary permits.
func checkExpandText(el *xdm.Node) error {
	var raw string
	if el.Name.URI == xdm.NSXSL {
		a := el.Attr("", "expand-text")
		if a == nil {
			return nil
		}
		raw = a.Value
	} else {
		a := el.Attr(xdm.NSXSL, "expand-text")
		if a == nil {
			return nil
		}
		raw = a.Value
	}
	switch strings.TrimSpace(raw) {
	case "yes", "true", "1", "no", "false", "0":
		return nil
	}
	return fmt.Errorf(
		"XTSE0020: expand-text must be yes, true, 1, no, false or 0, not %q", raw)
}

// textValueTemplateInstr emits a text node whose content is computed.
//
// It is deliberately not a valueOfInstr over the same expression. 5.6.2 says
// the result "is a (possibly zero-length) text node ... thereafter handled
// exactly as if the value had appeared explicitly as a text node", so the
// fixed parts stay fixed — whitespace in them is significant, unlike XQuery
// boundary space — and the separator attribute of a containing xsl:value-of
// has no say in how the parts are joined.
type textValueTemplateInstr struct {
	tmpl *avt
}

func (i *textValueTemplateInstr) Execute(rt *runtime, out *outputBuilder) error {
	s, err := i.tmpl.eval(rt)
	if err != nil {
		return err
	}
	out.appendText(s)
	return nil
}

// compileText compiles one text node of a sequence constructor, as either
// literal text or a text value template.
func (c *compiler) compileText(n *xdm.Node) (Instruction, error) {
	if n.Parent == nil || !expandTextAt(n.Parent) {
		return &textInstr{text: n.Value}, nil
	}
	tmpl, err := compileAVT(emptyBracesRemoved(n.Value), newNSResolver(n.Parent, ""))
	if err != nil {
		return nil, err
	}
	if tmpl.isLit {
		// No braces after all, so the doubled-brace escapes have been
		// resolved and there is nothing left to evaluate at run time.
		return &textInstr{text: tmpl.literal}, nil
	}
	return &textValueTemplateInstr{tmpl: tmpl}, nil
}

// expandTextVersion reports whether the element is in a part of the stylesheet
// that declares XSLT 3.0 or later.
//
// The version is itself inherited, and is read the same way every other
// version-sensitive decision reads it, so that a 3.0 module included from a
// 2.0 one keeps its own answer.
func expandTextVersion(el *xdm.Node) bool {
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement || !hasVersionAttr(cur) {
			continue
		}
		return versionAt(cur) >= 3.0
	}
	return false
}

// emptyBracesRemoved drops the braces that enclose nothing at all.
//
// "{}", and equally "{ }" or "{(: comment :)}", contains no expression, and
// XPath has no production for an empty one -- compiling it raises XPST0003.
// The specification does not make it an error, though: cvt-033 writes {}
// alongside a brace pair holding only a comment and expects both to
// contribute nothing, so an empty pair is a template producing the empty
// sequence. Removing it here rather than teaching the XPath parser to accept
// an empty expression keeps the concession to the one place it applies; an
// empty pair in an attribute value template is still the error it always was.
func emptyBracesRemoved(src string) string {
	if !strings.Contains(src, "{") {
		return src
	}
	var b strings.Builder
	for i := 0; i < len(src); {
		if src[i] != '{' {
			b.WriteByte(src[i])
			i++
			continue
		}
		if i+1 < len(src) && src[i+1] == '{' {
			b.WriteString("{{")
			i += 2
			continue
		}
		end, err := findAVTClose(src, i+1)
		if err != nil {
			// An unterminated brace is left alone so that compileAVT reports
			// it, with the message it has always used.
			b.WriteString(src[i:])
			break
		}
		if strings.TrimSpace(commentsStripped(src[i+1:end])) != "" {
			b.WriteString(src[i : end+1])
		}
		i = end + 1
	}
	return b.String()
}

// commentsStripped removes XPath comments, which nest.
func commentsStripped(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '(' && i+1 < len(s) && s[i+1] == ':':
			depth++
			i++
		case depth > 0 && s[i] == ':' && i+1 < len(s) && s[i+1] == ')':
			depth--
			i++
		case depth == 0:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
