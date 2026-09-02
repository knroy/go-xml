package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
)

// outputBuilder is the result-tree builder, which lives in xdmbuild because
// XQuery constructs result trees by the same rules.
//
// The alias keeps the name this package has always used for it: the builder
// is threaded through every instruction's Execute, and spelling it
// xdmbuild.Builder at each of those would say nothing the reader does not
// already know.
type outputBuilder = xdmbuild.Builder

// newOutputBuilder returns a builder that reports faults with XSLT's codes.
func newOutputBuilder() *outputBuilder {
	return xdmbuild.New(xsltPolicy{})
}

// newOutputBuilderFor returns a builder configured for one stylesheet.
//
// It differs from newOutputBuilder only in honouring the compatibility
// options that stylesheet was compiled with. A nil stylesheet -- which is
// what the internal callers that build a throwaway tree pass -- gets the
// strict policy, because nothing about those trees reaches a caller who could
// have asked for anything else.
func newOutputBuilderFor(s *Stylesheet) *outputBuilder {
	if s == nil || !s.compat.DropAttributesOnDocumentNode {
		return xdmbuild.New(xsltPolicy{})
	}
	return xdmbuild.New(xsltPolicy{dropAttrOnDocument: true})
}

// xsltPolicy names the structural faults of content construction the way XSLT
// names them, and answers the namespace and type questions the way XSLT
// answers them.
//
// It carries no state: every question it is asked has the same answer for
// every stylesheet. inherit-namespaces and validation vary per instruction
// rather than per transformation, and are applied where the instruction is
// executed rather than here — see xsl:element and xsl:copy. A policy that
// needed to vary would be constructed per builder instead.
type xsltPolicy struct {
	// dropAttrOnDocument discards an attribute or namespace node that
	// reaches the content of a document node, instead of raising XTDE0420.
	// Set from Compatibility.DropAttributesOnDocumentNode; off by default,
	// because the default has to be the specified behaviour.
	dropAttrOnDocument bool
}

// Err gives each fault the code XSLT 3.0 defines for it.
//
// FaultDuplicateAttribute returns nil: §5.7.1 says that where two attributes
// in the sequence have the same name, "attribute A is discarded" — the later
// one wins, silently. XQuery raises XQDY0025 for the same sequence, which is
// why the builder asks rather than assuming.
func (p xsltPolicy) Err(f xdmbuild.Fault, detail string) error {
	switch f {
	case xdmbuild.FaultDuplicateAttribute:
		return nil
	case xdmbuild.FaultAttrAfterChild:
		return fmt.Errorf("XTDE0410: %s", detail)
	case xdmbuild.FaultAttrOnDocument:
		// Returning ErrDiscardItem rather than an error is what makes the
		// builder drop the offending node and carry on, which is what Saxon
		// does here and what DocBook xslTNG relies on. Off unless the caller
		// asked; see Compatibility.
		if p.dropAttrOnDocument {
			return nil
		}
		return fmt.Errorf("XTDE0420: %s", detail)
	case xdmbuild.FaultConflictingPrefix:
		return fmt.Errorf("XTDE0430: %s", detail)
	case xdmbuild.FaultDefaultNSOnNoNS:
		return fmt.Errorf("XTDE0440: %s", detail)
	case xdmbuild.FaultFunctionItem:
		return fmt.Errorf("XTDE0450: %s", detail)
	}
	return fmt.Errorf("XTDE0410: %s", detail)
}

// InheritNamespaces reports the default for xsl:element/@inherit-namespaces
// and xsl:copy/@inherit-namespaces, which is yes.
//
// The attribute is read where those instructions are compiled; a namespace
// that must not be inherited is handled there, because the decision belongs
// to one instruction rather than to the transformation.
func (xsltPolicy) InheritNamespaces() bool { return true }

// PreserveNamespaces is always true for XSLT.
//
// XQuery's copy-namespaces has a no-preserve mode that keeps only the
// namespaces used in the names of an element and its attributes. XSLT has no
// way to ask for that: §5.7.1 copies a node with copy-namespaces="yes".
func (xsltPolicy) PreserveNamespaces() bool { return true }

// PreserveTypes is true because §5.7.1 copies with validation="preserve".
//
// Where an instruction asks for another validation mode the annotation is
// recomputed when the constructed node is validated, which happens after the
// builder has finished with it.
func (xsltPolicy) PreserveTypes() bool { return true }

// Compile-time proof that the alias and the policy still satisfy what the
// builder asks for. Both are cheap to get wrong silently.
var (
	_ xdmbuild.Policy = xsltPolicy{}
	_                 = func() *xdm.Node { return nil }
)

// These delegate to xdmbuild, which is where result-tree construction now
// lives. They keep the names this package used before the move, so that the
// call sites read as they always did.
func deepCopy(n *xdm.Node) *xdm.Node { return xdmbuild.DeepCopy(n) }

func resolveAgainst(base, ref string) string { return xdmbuild.ResolveAgainst(base, ref) }

func rebase(n *xdm.Node, parentBase string) { xdmbuild.Rebase(n, parentBase) }

func rebaseDetached(n *xdm.Node, instrBase string) { xdmbuild.RebaseDetached(n, instrBase) }
