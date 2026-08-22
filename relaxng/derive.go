package relaxng

import "github.com/knroy/go-xml/xdm"

// The derivative algorithm.
//
// Validation asks one question repeatedly: given a pattern and the next item
// of input, what pattern must the *rest* of the input match? That is the
// derivative. When the input runs out, the document is valid exactly when the
// remaining pattern is nullable.
//
// The constructors below are not plain struct literals. Each one simplifies as
// it builds — a choice with a NotAllowed branch is the other branch, a group
// with an Empty branch is the other branch — and that is what keeps the
// pattern from growing without bound as the derivative is taken over a long
// document. Without it the algorithm is correct and unusable.

func choice(a, b Pattern) Pattern {
	if _, ok := a.(NotAllowed); ok {
		return b
	}
	if _, ok := b.(NotAllowed); ok {
		return a
	}
	return Choice{a, b}
}

func group(a, b Pattern) Pattern {
	if _, ok := a.(NotAllowed); ok {
		return NotAllowed{}
	}
	if _, ok := b.(NotAllowed); ok {
		return NotAllowed{}
	}
	if _, ok := a.(Empty); ok {
		return b
	}
	if _, ok := b.(Empty); ok {
		return a
	}
	return Group{a, b}
}

func interleave(a, b Pattern) Pattern {
	if _, ok := a.(NotAllowed); ok {
		return NotAllowed{}
	}
	if _, ok := b.(NotAllowed); ok {
		return NotAllowed{}
	}
	if _, ok := a.(Empty); ok {
		return b
	}
	if _, ok := b.(Empty); ok {
		return a
	}
	return Interleave{a, b}
}

func after(a, b Pattern) Pattern {
	if _, ok := a.(NotAllowed); ok {
		return NotAllowed{}
	}
	if _, ok := b.(NotAllowed); ok {
		return NotAllowed{}
	}
	return After{a, b}
}

func oneOrMore(p Pattern) Pattern {
	if _, ok := p.(NotAllowed); ok {
		return NotAllowed{}
	}
	return OneOrMore{p}
}

// startTagOpenDeriv is the derivative with respect to an element's start tag.
//
// It descends into every branch that could admit the name, replacing the
// matching Element with an After: the element's own content pattern, followed
// by whatever must come once that element closes. That pairing is what lets
// one recursion handle arbitrary nesting.
func startTagOpenDeriv(p Pattern, name xdm.QName) Pattern {
	switch t := expand(p).(type) {
	case Choice:
		return choice(startTagOpenDeriv(t.Left, name), startTagOpenDeriv(t.Right, name))
	case Element:
		if !t.Name.contains(name) {
			return NotAllowed{}
		}
		return after(t.Pattern, Empty{})
	case Interleave:
		return choice(
			applyAfter(func(x Pattern) Pattern { return interleave(x, t.Right) },
				startTagOpenDeriv(t.Left, name)),
			applyAfter(func(x Pattern) Pattern { return interleave(t.Left, x) },
				startTagOpenDeriv(t.Right, name)))
	case OneOrMore:
		return applyAfter(
			func(x Pattern) Pattern {
				return group(x, choice(oneOrMore(t.Pattern), Empty{}))
			},
			startTagOpenDeriv(t.Pattern, name))
	case Group:
		d := applyAfter(func(x Pattern) Pattern { return group(x, t.Right) },
			startTagOpenDeriv(t.Left, name))
		if t.Left.nullable() {
			return choice(d, startTagOpenDeriv(t.Right, name))
		}
		return d
	case After:
		return applyAfter(func(x Pattern) Pattern { return after(x, t.Right) },
			startTagOpenDeriv(t.Left, name))
	}
	return NotAllowed{}
}

// expand resolves a Ref to the pattern it stands for.
//
// Every function that examines a pattern calls this first, so that a lazily
// compiled definition behaves exactly like the pattern it names. Expanding
// here rather than at compile time is what lets a definition refer to itself:
// the expansion happens once per level of nesting the document actually has,
// instead of unboundedly while compiling.
func expand(p Pattern) Pattern {
	for {
		r, ok := p.(*Ref)
		if !ok {
			return p
		}
		q, err := r.get()
		if err != nil {
			return NotAllowed{}
		}
		p = q
	}
}

// applyAfter rewrites the continuation of every After inside p.
//
// The derivative of a compound pattern has to remember what follows the
// element being opened, and that "what follows" lives in the right half of an
// After. Rewriting it in place is what threads the context through without a
// separate stack.
func applyAfter(f func(Pattern) Pattern, p Pattern) Pattern {
	switch t := expand(p).(type) {
	case After:
		return after(t.Left, f(t.Right))
	case Choice:
		return choice(applyAfter(f, t.Left), applyAfter(f, t.Right))
	case NotAllowed:
		return NotAllowed{}
	}
	return NotAllowed{}
}

// attsDeriv is the derivative with respect to an element's attributes.
func attsDeriv(p Pattern, attrs []attr) Pattern {
	for _, a := range attrs {
		p = attDeriv(p, a)
	}
	return p
}

type attr struct {
	name  xdm.QName
	value string
}

func attDeriv(p Pattern, a attr) Pattern {
	switch t := expand(p).(type) {
	case After:
		return after(attDeriv(t.Left, a), t.Right)
	case Choice:
		return choice(attDeriv(t.Left, a), attDeriv(t.Right, a))
	case Group:
		return choice(
			group(attDeriv(t.Left, a), t.Right),
			group(t.Left, attDeriv(t.Right, a)))
	case Interleave:
		return choice(
			interleave(attDeriv(t.Left, a), t.Right),
			interleave(t.Left, attDeriv(t.Right, a)))
	case OneOrMore:
		return group(attDeriv(t.Pattern, a),
			choice(oneOrMore(t.Pattern), Empty{}))
	case Attribute:
		if !t.Name.contains(a.name) || !valueMatch(t.Pattern, a.value) {
			return NotAllowed{}
		}
		return Empty{}
	}
	return NotAllowed{}
}

// valueMatch reports whether a string satisfies a pattern.
//
// An attribute value and a text node are both just a string, so the same
// question is asked of both. The empty string is special: it matches a
// nullable pattern, which is how <empty/> admits an absent value.
func valueMatch(p Pattern, s string) bool {
	if p.nullable() && whitespaceOnly(s) {
		return true
	}
	return !isNotAllowed(textDeriv(p, s))
}

func isNotAllowed(p Pattern) bool {
	_, ok := p.(NotAllowed)
	return ok
}

// textDeriv is the derivative with respect to a string of character data.
func textDeriv(p Pattern, s string) Pattern {
	switch t := expand(p).(type) {
	case Choice:
		return choice(textDeriv(t.Left, s), textDeriv(t.Right, s))
	case Interleave:
		return choice(
			interleave(textDeriv(t.Left, s), t.Right),
			interleave(t.Left, textDeriv(t.Right, s)))
	case Group:
		d := group(textDeriv(t.Left, s), t.Right)
		if t.Left.nullable() {
			return choice(d, textDeriv(t.Right, s))
		}
		return d
	case After:
		return after(textDeriv(t.Left, s), t.Right)
	case OneOrMore:
		return group(textDeriv(t.Pattern, s),
			choice(oneOrMore(t.Pattern), Empty{}))
	case Text:
		// Text consumes any amount of character data and remains itself,
		// which is what makes it match a run of text nodes.
		return t
	case Value:
		if t.Type.equal(t.Value, s) {
			return Empty{}
		}
		return NotAllowed{}
	case Data:
		if err := t.Type.check(s, t.Params); err != nil {
			return NotAllowed{}
		}
		if t.Except != nil && valueMatch(t.Except, s) {
			return NotAllowed{}
		}
		return Empty{}
	case List:
		return listDeriv(t.Pattern, splitTokens(s))
	}
	return NotAllowed{}
}

// listDeriv applies a pattern to the tokens of a whitespace-separated list.
func listDeriv(p Pattern, tokens []string) Pattern {
	for _, tok := range tokens {
		p = textDeriv(p, tok)
	}
	if p.nullable() {
		return Empty{}
	}
	return NotAllowed{}
}

// startTagCloseDeriv discards the attribute patterns that were never matched.
//
// An attribute is optional in the sense that the pattern may offer one the
// document did not carry; reaching the end of the start tag with such a
// pattern still live means it went unused, and an unused Attribute cannot be
// satisfied later.
func startTagCloseDeriv(p Pattern) Pattern {
	switch t := expand(p).(type) {
	case After:
		return after(startTagCloseDeriv(t.Left), t.Right)
	case Choice:
		return choice(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case Group:
		return group(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case Interleave:
		return interleave(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case OneOrMore:
		return oneOrMore(startTagCloseDeriv(t.Pattern))
	case Attribute:
		return NotAllowed{}
	}
	return p
}

// endTagDeriv is the derivative with respect to an element's end tag.
//
// The element's content is complete, so what remains is the continuation
// stored in the After — but only if the content pattern is nullable, meaning
// everything it required was supplied.
func endTagDeriv(p Pattern) Pattern {
	switch t := expand(p).(type) {
	case Choice:
		return choice(endTagDeriv(t.Left), endTagDeriv(t.Right))
	case After:
		if t.Left.nullable() {
			return t.Right
		}
		return NotAllowed{}
	}
	return NotAllowed{}
}

func whitespaceOnly(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

func splitTokens(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
