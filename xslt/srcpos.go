package xslt

import (
	"errors"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// posInstr is an instruction that remembers where it was written.
//
// XSLT 3.0 section 8.3 lets an xsl:catch clause read $err:line-number and
// $err:module, the position of the instruction that raised the error it
// caught. Nothing below the XSLT engine can supply those: an error is
// constructed deep in xdm, xpath or xsd, by code that has never heard of a
// stylesheet. So the position is attached on the way *out* instead — as the
// error unwinds past the instruction that raised it, execSequence stamps the
// first position it passes.
//
// Only the innermost stamp survives, because an error already carrying a
// position is left alone. That is what makes the answer the line the error
// happened on rather than the line of the outermost instruction still on the
// stack: try-022 wants line 21, the xsl:value-of that divided by zero, not
// line 13, the xsl:try that caught it.
//
// The wrapper is only created when a position is actually known, so a
// stylesheet parsed without TrackPositions compiles to exactly the
// instructions it always did.
type posInstr struct {
	Instruction
	line   int
	module string
}

// unwrapInstr strips the position wrapper, for the few places that decide what
// to do from an instruction's concrete type. Those tests are about what the
// instruction *is* — a variable declaration, an xsl:on-empty — and a recorded
// position does not change that.
func unwrapInstr(instr Instruction) Instruction {
	if p, ok := instr.(*posInstr); ok {
		return p.Instruction
	}
	return instr
}

// withPosition wraps instr so that errors escaping it are stamped with n's
// position, when the stylesheet was parsed with positions tracked.
func withPosition(instr Instruction, n *xdm.Node) Instruction {
	if instr == nil || n == nil {
		return instr
	}
	line, _, ok := n.Position()
	if !ok {
		return instr
	}
	return &posInstr{Instruction: instr, line: line, module: n.BaseURI}
}

// stampPosition records where an error was raised, if it does not already say.
//
// An error that already carries a line was stamped closer to where it happened
// and is the better answer, so it is never overwritten.
//
// Not every error in this engine is an *xdm.Error: a good many are raised as
// fmt.Errorf("FODC0002: ...") and are recognised by the prefix, which is what
// xdm.ErrorCode reads. One of those has nowhere to record a position, so it is
// promoted to an *xdm.Error here — keeping the original as the cause, so that
// every errors.Is and every message that already matched on the text still
// does. try-018 needs exactly this: fn:doc raises its FODC0002 in the prefix
// form, and $err:line-number would otherwise be empty for it.
func stampPosition(err error, instr Instruction) error {
	p, ok := instr.(*posInstr)
	if !ok || err == nil {
		return err
	}
	var e *xdm.Error
	if errors.As(err, &e) {
		if e.Line == 0 {
			e.Line = p.line
			e.Module = p.module
		}
		return err
	}
	code := xdm.ErrorCode(err)
	if code == "" {
		// No code means one of this engine's own internal failures, which no
		// xsl:catch may see (see catchable) and which nothing reads a
		// position off. Promoting it would only disguise it as a spec error.
		return err
	}
	// The message loses the code prefix, because *xdm.Error puts the code
	// back on in Error(); leaving it would double it.
	msg := strings.TrimPrefix(err.Error(), code+": ")
	return &xdm.Error{Code: code, Message: msg, Err: err,
		Line: p.line, Module: p.module}
}
