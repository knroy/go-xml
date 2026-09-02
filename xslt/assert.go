package xslt

import (
	"github.com/knroy/go-xml/xpath"
)

// xsl:assert, XSLT 3.0 section 22.2.
//
// "The xsl:assert instruction is used to assert that the value of a particular
// expression is true; if the value of the expression is false, and assertions
// are enabled, then a dynamic error occurs."
//
// The whole of the failure behaviour is borrowed rather than invented: "If the
// assertion fails, then the effect of the instruction is governed by the rules
// for evaluation of an xsl:message instruction with the same select attribute,
// error-code attribute, and contained sequence constructor, and with the value
// terminate='yes'. However, the default error code if the error-code attribute
// is omitted is XTMM9001 rather than XTMM9000."
//
// So this instruction holds a messageInstr and delegates the failure to it.
// Reimplementing the message rules here would mean a second copy of the
// @select-versus-content precedence, of the @error-code AVT resolution and of
// its QName fallback -- and the second copy would drift from the first.

// assertInstr implements xsl:assert.
type assertInstr struct {
	// test is @test, the expression whose effective boolean value decides
	// whether the assertion holds. The element syntax summary writes it
	// without a question mark, so it is required and is never nil here.
	test *xpath.Compiled
	// msg carries @select, @error-code and the contained sequence constructor,
	// which 22.2 defines by reference to xsl:message. Its terminate field is
	// nil and its assert field is set: xsl:assert has no @terminate, because
	// a failing assertion always terminates.
	msg *messageInstr
}

func (i *assertInstr) Execute(rt *runtime, out *outputBuilder) error {
	// The second of the two ways 22.2 gives for disabling assertions: "An
	// implementation should provide an external mechanism to disable assertion
	// checking for the stylesheet as a whole (either statically or
	// dynamically). The detail of such mechanisms is implementation-defined."
	// TransformOptions.DisableAssertions is this engine's mechanism, and it is
	// honoured before @test is evaluated -- a disabled assertion must not be
	// able to fail, and should not cost anything either.
	//
	// (The first way is use-when, which needs nothing here: a use-when that is
	// false removes the element from the stylesheet before compilation, so a
	// disabled assertion never becomes an assertInstr at all.)
	//
	// The note closing the section asks implementations *not* to optimise
	// assertions away, which is the reverse requirement, and is why nothing
	// else in this engine skips them.
	if rt.opts.DisableAssertions {
		return nil
	}
	// 22.2 rule 1: "The expression in the test attribute is evaluated. If the
	// effective boolean value of the result is true, the assertion succeeds,
	// and no further action is taken. If the effective boolean value is false,
	// or if a dynamic error occurs during evaluation of the expression, then
	// the assertion fails."
	//
	// The error clause is the surprising half and is deliberate: an assertion
	// whose own test raised has not been shown to hold, so it fails like any
	// other failing assertion, and what surfaces is the assertion's error code
	// rather than the test expression's. Propagating the raw XPath error would
	// hand the author a code saying nothing about the assertion that was
	// written to catch the problem.
	ok, err := i.test.EvalBool(rt.ctx)
	if err != nil {
		ok = false
	}
	if ok {
		return nil
	}
	// "the effect of the instruction is governed by the rules for evaluation
	// of an xsl:message instruction ... and with the value terminate='yes'."
	// So the message text and value are built and recorded exactly as
	// xsl:message would build and record them, and then it terminates under
	// XTMM9001 or under whatever @error-code named. messageInstr.Execute does
	// all of that when its assert field is set.
	//
	// "The result of the xsl:assert instruction is an empty sequence" -- which
	// it is: nothing is written to out on either path.
	return i.msg.Execute(rt, out)
}
