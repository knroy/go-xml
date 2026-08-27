package xslt

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The XSLT 3.0 changes to xsl:message, section 23.1.
//
// 3.0 adds @error-code, widens @terminate to the four boolean lexical forms,
// and makes a failure while building the message content something the
// transformation survives rather than something it dies of.

// compileMaxVersion is the MaxVersion of the compilation in progress, held as
// package state for the duration of Compile under the same lock as
// compileSchema and overrideXPathVersion; see their declarations.
//
// The static grammar check is a free function walking the tree, with no route
// to the compiler's options, and threading them through every level of it to
// answer one question about one attribute would be a large change for a small
// one. Zero means uncapped, and so 3.0.
var compileMaxVersion float64

// processorAtLeast30 reports whether the compilation in progress is acting as
// an XSLT 3.0 processor.
func processorAtLeast30() bool {
	return compileMaxVersion == 0 || compileMaxVersion >= 3.0
}

// messageTerminate reads the effective value of xsl:message/@terminate.
//
// XSLT 2.0 gives the attribute a closed set of "yes" and "no", so anything
// else is XTDE0030. 3.0 declares it as an AVT yielding xs:boolean, which
// brings in the other two lexical forms of the type — "true"/"false" and
// "1"/"0" — and message-0007 and message-0102 pin both spellings.
func messageTerminate(v string, xslt30 bool) (terminate bool, ok bool) {
	switch strings.TrimSpace(v) {
	case "yes":
		return true, true
	case "no":
		return false, true
	case "true", "1":
		return true, xslt30
	case "false", "0":
		return false, xslt30
	}
	return false, false
}

// terminateError builds the error a terminating xsl:message raises.
//
// XTMM9000 is the code unless @error-code named another. The message text
// becomes the description and the constructed content becomes the error
// value, so an enclosing xsl:catch can report all three — which is what
// message-0501 does, reading $err:code, $err:description and $err:value from a
// message it terminated on.
func terminateError(code xdm.QName, text string, value xdm.Sequence) error {
	name := code
	if name.Local == "" {
		name = xdm.QName{Prefix: "err", URI: xdm.NSErr, Local: "XTMM9000"}
	}
	// The rendered message names the code in full when it is not one of the
	// standard err: codes. A bare local name is how every spec code is
	// written and is what a reader expects, but a user-defined code in a
	// namespace of its own is only identified by the pair, so that one is
	// spelled Q{uri}local.
	shown := name.Local
	if name.URI != xdm.NSErr {
		// EQName form, which is how the catalogue writes a code that is not
		// one of the standard ones -- including Q{}UIOP9876 for a code in no
		// namespace at all.
		shown = "Q{" + name.URI + "}" + name.Local
	}
	return &xdm.Error{
		Code:     name.Local,
		CodeName: &name,
		Message:  shown + ": " + text,
		Value:    value,
	}
}

// resolveErrorCode evaluates @error-code and resolves it to a QName.
//
// The attribute is an AVT — message-0009 builds the local name from a
// parameter — so the name cannot be resolved at compile time and the
// namespace context of the xsl:message element is carried here to resolve the
// prefix against.
func (i *messageInstr) resolveErrorCode(rt *runtime) (xdm.QName, error) {
	if i.errorCode == nil {
		return xdm.QName{}, nil
	}
	lex, err := i.errorCode.eval(rt)
	if err != nil {
		return xdm.QName{}, err
	}
	if strings.TrimSpace(lex) == "" {
		return xdm.QName{}, nil
	}
	q, err := resolveQNameAttr(i.errorCodeNS, lex)
	if err != nil || !xdm.IsNCName(q.Local) {
		// A value that is not a usable QName is not an error of its own: the
		// message still terminates, under the default code. message-0406
		// builds "23CODE" from an AVT and requires XTMM9000, which is what an
		// empty name here produces.
		return xdm.QName{}, nil
	}
	return q, nil
}
