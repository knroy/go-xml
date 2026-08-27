package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// The xsl:fork instruction, section 16.
//
// xsl:fork exists for streamability, not for semantics: "in a formal sense the
// effect of the instruction is simply to return the result of evaluating the
// sequence constructor", and what it changes is the streamability analysis,
// which lets each contained instruction be evaluated during a single pass of
// the input. This processor does not stream, so there is nothing left for the
// instruction to do at run time beyond concatenating its children's results —
// "the result can be determined by treating the content as a sequence
// constructor and evaluating it as such".
//
// The content model is still worth enforcing, because it is the part of the
// instruction that is not a no-op here. 16.1 writes it as
//
//	(xsl:fallback*, ((xsl:sequence, xsl:fallback*)* | (xsl:for-each-group, xsl:fallback*)))
//
// — either a single xsl:for-each-group or a run of xsl:sequence instructions,
// with xsl:fallback permitted anywhere and ignored by a 3.0 processor. The
// grammar table cannot express that alternation, so it is checked here.

// forkInstr implements xsl:fork.
//
// It holds the compiled content and nothing else. A separate instruction type
// rather than splicing the body into the parent keeps the stylesheet's own
// structure visible to anything that walks the compiled tree, and costs one
// indirection per evaluation.
type forkInstr struct {
	body []Instruction
}

func (i *forkInstr) Execute(rt *runtime, out *outputBuilder) error {
	return execSequence(i.body, rt, out)
}

// compileFork compiles an xsl:fork, checking the section 16.1 content model.
func (c *compiler) compileFork(n *xdm.Node) (Instruction, error) {
	seenSequence, seenGroup := false, false
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "fallback"):
			// Permitted anywhere in the model, and ignored: this processor
			// recognises xsl:fork, so the fallback is by definition not
			// instantiated.
		case isXSL(ch, "sequence"):
			if seenGroup {
				return nil, forkContentError(ch)
			}
			seenSequence = true
		case isXSL(ch, "for-each-group"):
			// The single xsl:for-each-group form is exclusive both ways: a
			// second one is as wrong as one mixed with an xsl:sequence.
			if seenSequence || seenGroup {
				return nil, forkContentError(ch)
			}
			seenGroup = true
		default:
			return nil, forkContentError(ch)
		}
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	return &forkInstr{body: body}, nil
}

// forkContentError reports a child that the section 16.1 content model does
// not admit at the position it appears in.
func forkContentError(ch *xdm.Node) error {
	return fmt.Errorf("XTSE0010: %s is not allowed in xsl:fork; the content "+
		"is either a single xsl:for-each-group or a sequence of xsl:sequence "+
		"instructions", ch.Name.Lexical())
}
