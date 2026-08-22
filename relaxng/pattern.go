// Package relaxng validates XML documents against RELAX NG schemas.
//
// RELAX NG validates by a different model from XSD: a schema is a *pattern*,
// and validation computes the derivative of that pattern with respect to each
// item of input, accepting when what remains can match the empty sequence.
// That is why this is a separate engine rather than a use of the XSD
// automaton — there is no finite automaton to build, and interleave, which
// admits its branches in any order, is not something a Glushkov construction
// expresses.
//
// The implementation follows James Clark's derivative algorithm, which is both
// the clearest description of the language and the one the conformance suite
// was written against.
package relaxng

import "github.com/knroy/go-xml/xdm"

// pattern is a RELAX NG pattern.
//
// The set is closed and small, which is the language's chief virtue: eleven
// cases cover everything, and validation is one function over them.
type pattern interface {
	// nullable reports whether the pattern matches the empty sequence. It is
	// the accept test, and it is what every derivative is finally asked.
	nullable() bool
}

// notAllowedPat matches nothing. It is the failure value: a derivative that
// reaches it can never recover, which is what lets the whole computation stop
// short rather than carry a doomed branch forward.
type notAllowedPat struct{}

// emptyPat matches only the empty sequence.
type emptyPat struct{}

// textPat matches any sequence of text nodes, including none.
type textPat struct{}

// choicePat matches either branch.
type choicePat struct{ Left, Right pattern }

// interleavePat matches both branches with their items in any interleaving.
//
// This is the construct that makes RELAX NG more expressive than a DTD or an
// XSD all group: the branches may be arbitrary patterns, not just elements,
// and they interleave rather than merely being unordered.
type interleavePat struct{ Left, Right pattern }

// groupPat matches the left branch followed by the right.
type groupPat struct{ Left, Right pattern }

// oneOrMorePat matches its pattern one or more times.
type oneOrMorePat struct{ Pattern pattern }

// elementPat matches one element whose name the class admits and whose content
// matches the pattern.
type elementPat struct {
	Name    nameClass
	Pattern pattern
}

// attributePat matches one attribute whose name the class admits and whose value
// matches the pattern.
type attributePat struct {
	Name    nameClass
	Pattern pattern
}

// valuePat matches a single string equal to the given value, compared according
// to the datatype's rules.
type valuePat struct {
	Type  datatype
	Value string
	// Ns is the namespace in force where the value was written, which the
	// qnamePat datatype needs to resolve an unprefixed name.
	Ns string
	// Prefixes are the namespace bindings in scope where the value was
	// written. A qnamePat value is compared by what its prefix *means*, not by
	// how it is spelled, so both sides need their own bindings: the schema's
	// live here and the document's arrive with the text being matched.
	Prefixes map[string]string
}

// dataPat matches a string the datatype accepts, optionally excluding a pattern.
type dataPat struct {
	Type   datatype
	Params []param
	Except pattern
}

// listPat matches a whitespace-separated list of tokens against a pattern.
type listPat struct{ Pattern pattern }

// afterPat is an internal Pattern: it matches the first pattern, then continues
// with the second.
//
// It has no syntax — it arises only while computing a derivative, to remember
// what must follow once an element's content is complete. Keeping it a pattern
// rather than a separate stack is what makes the algorithm one recursion.
type afterPat struct{ Left, Right pattern }

// refPat is a definition not yet expanded.
//
// A definition may refer to itself — a <bar> whose content optionally holds
// another <bar> is the ordinary way to write a nested structure — and
// expanding that while compiling would not terminate. So a reference through
// an element boundary becomes this instead, and is expanded only when a
// derivative actually needs it, which happens once per level of nesting the
// document actually has.
type refPat struct {
	// resolve produces the pattern, and is called at most once.
	resolve func() (pattern, error)
	// cached is the result, kept so that a definition reached many times is
	// compiled once.
	cached pattern
	err    error
	done   bool
	// name is for error messages.
	name string
}

// get expands the reference.
func (r *refPat) get() (pattern, error) {
	if !r.done {
		r.done = true
		r.cached, r.err = r.resolve()
		if r.cached == nil && r.err == nil {
			r.cached = notAllowedPat{}
		}
	}
	return r.cached, r.err
}

// param is one <param> on a data pattern.
type param struct {
	Name  string
	Value string
}

func (notAllowedPat) nullable() bool   { return false }
func (emptyPat) nullable() bool        { return true }
func (textPat) nullable() bool         { return true }
func (p choicePat) nullable() bool     { return p.Left.nullable() || p.Right.nullable() }
func (p interleavePat) nullable() bool { return p.Left.nullable() && p.Right.nullable() }
func (p groupPat) nullable() bool      { return p.Left.nullable() && p.Right.nullable() }
func (p oneOrMorePat) nullable() bool  { return p.Pattern.nullable() }
func (elementPat) nullable() bool      { return false }
func (attributePat) nullable() bool    { return false }
func (valuePat) nullable() bool        { return false }
func (dataPat) nullable() bool         { return false }
func (listPat) nullable() bool         { return false }
func (afterPat) nullable() bool        { return false }

// nullable expands the reference.
//
// A definition that cannot be expanded matches nothing, which is the safe
// reading: the failure is reported when the schema is compiled, and by the
// time a derivative is asking, refusing is right.
func (r *refPat) nullable() bool {
	p, err := r.get()
	if err != nil {
		return false
	}
	return p.nullable()
}

// nameClass decides which names a pattern admits.
type nameClass interface {
	contains(name xdm.QName) bool
}

// anyNamePat matches every name, less an exception.
type anyNamePat struct{ Except nameClass }

// nsNamePat matches every name in one namespace, less an exception.
type nsNamePat struct {
	Ns     string
	Except nameClass
}

// qnamePat matches exactly one name.
type qnamePat struct{ Name xdm.QName }

// nameChoicePat matches either class.
type nameChoicePat struct{ Left, Right nameClass }

func (c anyNamePat) contains(n xdm.QName) bool {
	return c.Except == nil || !c.Except.contains(n)
}

func (c nsNamePat) contains(n xdm.QName) bool {
	if n.URI != c.Ns {
		return false
	}
	return c.Except == nil || !c.Except.contains(n)
}

func (c qnamePat) contains(n xdm.QName) bool { return n == c.Name }

func (c nameChoicePat) contains(n xdm.QName) bool {
	return c.Left.contains(n) || c.Right.contains(n)
}
