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

// Pattern is a RELAX NG pattern.
//
// The set is closed and small, which is the language's chief virtue: eleven
// cases cover everything, and validation is one function over them.
type Pattern interface {
	// nullable reports whether the pattern matches the empty sequence. It is
	// the accept test, and it is what every derivative is finally asked.
	nullable() bool
}

// NotAllowed matches nothing. It is the failure value: a derivative that
// reaches it can never recover, which is what lets the whole computation stop
// short rather than carry a doomed branch forward.
type NotAllowed struct{}

// Empty matches only the empty sequence.
type Empty struct{}

// Text matches any sequence of text nodes, including none.
type Text struct{}

// Choice matches either branch.
type Choice struct{ Left, Right Pattern }

// Interleave matches both branches with their items in any interleaving.
//
// This is the construct that makes RELAX NG more expressive than a DTD or an
// XSD all group: the branches may be arbitrary patterns, not just elements,
// and they interleave rather than merely being unordered.
type Interleave struct{ Left, Right Pattern }

// Group matches the left branch followed by the right.
type Group struct{ Left, Right Pattern }

// OneOrMore matches its pattern one or more times.
type OneOrMore struct{ Pattern Pattern }

// Element matches one element whose name the class admits and whose content
// matches the pattern.
type Element struct {
	Name    NameClass
	Pattern Pattern
}

// Attribute matches one attribute whose name the class admits and whose value
// matches the pattern.
type Attribute struct {
	Name    NameClass
	Pattern Pattern
}

// Value matches a single string equal to the given value, compared according
// to the datatype's rules.
type Value struct {
	Type  Datatype
	Value string
	// Ns is the namespace in force where the value was written, which the
	// QName datatype needs to resolve an unprefixed name.
	Ns string
	// Prefixes are the namespace bindings in scope where the value was
	// written. A QName value is compared by what its prefix *means*, not by
	// how it is spelled, so both sides need their own bindings: the schema's
	// live here and the document's arrive with the text being matched.
	Prefixes map[string]string
}

// Data matches a string the datatype accepts, optionally excluding a pattern.
type Data struct {
	Type   Datatype
	Params []Param
	Except Pattern
}

// List matches a whitespace-separated list of tokens against a pattern.
type List struct{ Pattern Pattern }

// After is an internal pattern: it matches the first pattern, then continues
// with the second.
//
// It has no syntax — it arises only while computing a derivative, to remember
// what must follow once an element's content is complete. Keeping it a pattern
// rather than a separate stack is what makes the algorithm one recursion.
type After struct{ Left, Right Pattern }

// Ref is a definition not yet expanded.
//
// A definition may refer to itself — a <bar> whose content optionally holds
// another <bar> is the ordinary way to write a nested structure — and
// expanding that while compiling would not terminate. So a reference through
// an element boundary becomes this instead, and is expanded only when a
// derivative actually needs it, which happens once per level of nesting the
// document actually has.
type Ref struct {
	// resolve produces the pattern, and is called at most once.
	resolve func() (Pattern, error)
	// cached is the result, kept so that a definition reached many times is
	// compiled once.
	cached Pattern
	err    error
	done   bool
	// name is for error messages.
	name string
}

// get expands the reference.
func (r *Ref) get() (Pattern, error) {
	if !r.done {
		r.done = true
		r.cached, r.err = r.resolve()
		if r.cached == nil && r.err == nil {
			r.cached = NotAllowed{}
		}
	}
	return r.cached, r.err
}

// Param is one <param> on a data pattern.
type Param struct {
	Name  string
	Value string
}

func (NotAllowed) nullable() bool   { return false }
func (Empty) nullable() bool        { return true }
func (Text) nullable() bool         { return true }
func (p Choice) nullable() bool     { return p.Left.nullable() || p.Right.nullable() }
func (p Interleave) nullable() bool { return p.Left.nullable() && p.Right.nullable() }
func (p Group) nullable() bool      { return p.Left.nullable() && p.Right.nullable() }
func (p OneOrMore) nullable() bool  { return p.Pattern.nullable() }
func (Element) nullable() bool      { return false }
func (Attribute) nullable() bool    { return false }
func (Value) nullable() bool        { return false }
func (Data) nullable() bool         { return false }
func (List) nullable() bool         { return false }
func (After) nullable() bool        { return false }

// nullable expands the reference.
//
// A definition that cannot be expanded matches nothing, which is the safe
// reading: the failure is reported when the schema is compiled, and by the
// time a derivative is asking, refusing is right.
func (r *Ref) nullable() bool {
	p, err := r.get()
	if err != nil {
		return false
	}
	return p.nullable()
}

// NameClass decides which names a pattern admits.
type NameClass interface {
	contains(name xdm.QName) bool
}

// AnyName matches every name, less an exception.
type AnyName struct{ Except NameClass }

// NsName matches every name in one namespace, less an exception.
type NsName struct {
	Ns     string
	Except NameClass
}

// QName matches exactly one name.
type QName struct{ Name xdm.QName }

// NameChoice matches either class.
type NameChoice struct{ Left, Right NameClass }

func (c AnyName) contains(n xdm.QName) bool {
	return c.Except == nil || !c.Except.contains(n)
}

func (c NsName) contains(n xdm.QName) bool {
	if n.URI != c.Ns {
		return false
	}
	return c.Except == nil || !c.Except.contains(n)
}

func (c QName) contains(n xdm.QName) bool { return n == c.Name }

func (c NameChoice) contains(n xdm.QName) bool {
	return c.Left.contains(n) || c.Right.contains(n)
}
