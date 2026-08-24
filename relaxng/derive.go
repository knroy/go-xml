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
// it builds — a choice with a notAllowedPat branch is the other branch, a group
// with an emptyPat branch is the other branch — and that is what keeps the
// pattern from growing without bound as the derivative is taken over a long
// document. Without it the algorithm is correct and unusable.

func choice(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return b
	}
	if _, ok := b.(notAllowedPat); ok {
		return a
	}
	return choicePat{a, b}
}

func group(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := a.(emptyPat); ok {
		return b
	}
	if _, ok := b.(emptyPat); ok {
		return a
	}
	return groupPat{a, b}
}

func interleave(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := a.(emptyPat); ok {
		return b
	}
	if _, ok := b.(emptyPat); ok {
		return a
	}
	return interleavePat{a, b}
}

func after(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	return afterPat{a, b}
}

func oneOrMore(p pattern) pattern {
	if _, ok := p.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	return oneOrMorePat{p}
}

// startTagOpenDeriv is the derivative with respect to an element's start tag.
//
// It descends into every branch that could admit the name, replacing the
// matching elementPat with an afterPat: the element's own content pattern, followed
// by whatever must come once that element closes. That pairing is what lets
// one recursion handle arbitrary nesting.
func startTagOpenDeriv(p pattern, name xdm.QName) pattern {
	switch t := expand(p).(type) {
	case choicePat:
		return choice(startTagOpenDeriv(t.Left, name), startTagOpenDeriv(t.Right, name))
	case elementPat:
		if !t.Name.contains(name) {
			return notAllowedPat{}
		}
		return after(t.Pattern, emptyPat{})
	case interleavePat:
		return choice(
			applyAfter(func(x pattern) pattern { return interleave(x, t.Right) },
				startTagOpenDeriv(t.Left, name)),
			applyAfter(func(x pattern) pattern { return interleave(t.Left, x) },
				startTagOpenDeriv(t.Right, name)))
	case oneOrMorePat:
		return applyAfter(
			func(x pattern) pattern {
				return group(x, choice(oneOrMore(t.Pattern), emptyPat{}))
			},
			startTagOpenDeriv(t.Pattern, name))
	case groupPat:
		d := applyAfter(func(x pattern) pattern { return group(x, t.Right) },
			startTagOpenDeriv(t.Left, name))
		if t.Left.nullable() {
			return choice(d, startTagOpenDeriv(t.Right, name))
		}
		return d
	case afterPat:
		return applyAfter(func(x pattern) pattern { return after(x, t.Right) },
			startTagOpenDeriv(t.Left, name))
	}
	return notAllowedPat{}
}

// expand resolves a refPat to the pattern it stands for.
//
// Every function that examines a pattern calls this first, so that a lazily
// compiled definition behaves exactly like the pattern it names. Expanding
// here rather than at compile time is what lets a definition refer to itself:
// the expansion happens once per level of nesting the document actually has,
// instead of unboundedly while compiling.
func expand(p pattern) pattern {
	for {
		r, ok := p.(*refPat)
		if !ok {
			return p
		}
		q, err := r.get()
		if err != nil {
			return notAllowedPat{}
		}
		p = q
	}
}

// applyAfter rewrites the continuation of every afterPat inside p.
//
// The derivative of a compound pattern has to remember what follows the
// element being opened, and that "what follows" lives in the right half of an
// afterPat. Rewriting it in place is what threads the context through without a
// separate stack.
func applyAfter(f func(pattern) pattern, p pattern) pattern {
	switch t := expand(p).(type) {
	case afterPat:
		return after(t.Left, f(t.Right))
	case choicePat:
		return choice(applyAfter(f, t.Left), applyAfter(f, t.Right))
	case notAllowedPat:
		return notAllowedPat{}
	}
	return notAllowedPat{}
}

// attsDeriv is the derivative with respect to an element's attributes.
func attsDeriv(p pattern, attrs []attr, ctx nsContext) pattern {
	for _, a := range attrs {
		p = attDeriv(p, a, ctx)
	}
	return p
}

type attr struct {
	name  xdm.QName
	value string
}

func attDeriv(p pattern, a attr, ctx nsContext) pattern {
	switch t := expand(p).(type) {
	case afterPat:
		return after(attDeriv(t.Left, a, ctx), t.Right)
	case choicePat:
		return choice(attDeriv(t.Left, a, ctx), attDeriv(t.Right, a, ctx))
	case groupPat:
		return choice(
			group(attDeriv(t.Left, a, ctx), t.Right),
			group(t.Left, attDeriv(t.Right, a, ctx)))
	case interleavePat:
		return choice(
			interleave(attDeriv(t.Left, a, ctx), t.Right),
			interleave(t.Left, attDeriv(t.Right, a, ctx)))
	case oneOrMorePat:
		return group(attDeriv(t.Pattern, a, ctx),
			choice(oneOrMore(t.Pattern), emptyPat{}))
	case attributePat:
		if !t.Name.contains(a.name) || !valueMatch(t.Pattern, a.value, ctx) {
			return notAllowedPat{}
		}
		return emptyPat{}
	}
	return notAllowedPat{}
}

// valueMatch reports whether a string satisfies a pattern.
//
// An attribute value and a text node are both just a string, so the same
// question is asked of both. The empty string is special: it matches a
// nullable pattern, which is how <empty/> admits an absent value.
func valueMatch(p pattern, s string, ctx nsContext) bool {
	if p.nullable() && whitespaceOnly(s) {
		return true
	}
	return !isNotAllowed(textDeriv(p, s, ctx))
}

func isNotAllowed(p pattern) bool {
	_, ok := p.(notAllowedPat)
	return ok
}

// textDeriv is the derivative with respect to a string of character data.
func textDeriv(p pattern, s string, ctx nsContext) pattern {
	switch t := expand(p).(type) {
	case choicePat:
		return choice(textDeriv(t.Left, s, ctx), textDeriv(t.Right, s, ctx))
	case interleavePat:
		return choice(
			interleave(textDeriv(t.Left, s, ctx), t.Right),
			interleave(t.Left, textDeriv(t.Right, s, ctx)))
	case groupPat:
		d := group(textDeriv(t.Left, s, ctx), t.Right)
		if t.Left.nullable() {
			return choice(d, textDeriv(t.Right, s, ctx))
		}
		return d
	case afterPat:
		return after(textDeriv(t.Left, s, ctx), t.Right)
	case oneOrMorePat:
		return group(textDeriv(t.Pattern, s, ctx),
			choice(oneOrMore(t.Pattern), emptyPat{}))
	case textPat:
		// textPat consumes any amount of character data and remains itself,
		// which is what makes it match a run of text nodes.
		return t
	case valuePat:
		// A qnamePat value is compared by what its prefix means on each side,
		// so the datatype is asked with both contexts when it has an opinion.
		if ct, ok := t.Type.(contextualType); ok {
			if ct.equalIn(t.Value, nsContext{prefixes: t.Prefixes, dflt: t.Ns},
				s, ctx) {
				return emptyPat{}
			}
			return notAllowedPat{}
		}
		if t.Type.equal(t.Value, s) {
			return emptyPat{}
		}
		return notAllowedPat{}
	case dataPat:
		if err := t.Type.check(s, t.Params); err != nil {
			return notAllowedPat{}
		}
		if t.Except != nil && valueMatch(t.Except, s, ctx) {
			return notAllowedPat{}
		}
		return emptyPat{}
	case listPat:
		return listDeriv(t.Pattern, splitTokens(s), ctx)
	}
	return notAllowedPat{}
}

// listDeriv applies a pattern to the tokens of a whitespace-separated list.
func listDeriv(p pattern, tokens []string, ctx nsContext) pattern {
	for _, tok := range tokens {
		p = textDeriv(p, tok, ctx)
	}
	if p.nullable() {
		return emptyPat{}
	}
	return notAllowedPat{}
}

// startTagCloseDeriv discards the attribute patterns that were never matched.
//
// An attribute is optional in the sense that the pattern may offer one the
// document did not carry; reaching the end of the start tag with such a
// pattern still live means it went unused, and an unused attributePat cannot be
// satisfied later.
func startTagCloseDeriv(p pattern) pattern {
	switch t := expand(p).(type) {
	case afterPat:
		return after(startTagCloseDeriv(t.Left), t.Right)
	case choicePat:
		return choice(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case groupPat:
		return group(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case interleavePat:
		return interleave(startTagCloseDeriv(t.Left), startTagCloseDeriv(t.Right))
	case oneOrMorePat:
		return oneOrMore(startTagCloseDeriv(t.Pattern))
	case attributePat:
		return notAllowedPat{}
	}
	return p
}

// endTagDeriv is the derivative with respect to an element's end tag.
//
// The element's content is complete, so what remains is the continuation
// stored in the afterPat — but only if the content pattern is nullable, meaning
// everything it required was supplied.
func endTagDeriv(p pattern) pattern {
	switch t := expand(p).(type) {
	case choicePat:
		return choice(endTagDeriv(t.Left), endTagDeriv(t.Right))
	case afterPat:
		if t.Left.nullable() {
			return t.Right
		}
		return notAllowedPat{}
	}
	return notAllowedPat{}
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

// patternSize measures a pattern's node count, stopping once it exceeds the
// limit so that measuring an already-huge pattern is not itself expensive.
//
// It exists to bound the derivative's growth. The algorithm's cost is the size
// of the pattern it is carrying, and the constructors' simplifications keep
// that bounded for ordinary schemas — but not for all of them. A oneOrMore
// nested inside a oneOrMore duplicates its operand on every child, so the
// pattern grows multiplicatively in the number of children: measured, a
// 189-byte schema and a 63-byte instance of fourteen children reached 1.2 GB
// and 1.35 seconds, growing about ninefold for every two children added.
// Nothing else bounded it — MaxDepth does not, because the document is two
// levels deep whatever its width.
//
// A structural fix is to intern patterns so that equal branches collapse, the
// way jing does. That is a redesign of this file rather than a bound, so what
// is here is the bound: the size is checked as the derivative is taken, and a
// pattern past the limit ends validation with an error that says so rather
// than with a verdict that cost a gigabyte to reach.
func patternSize(p pattern, limit int) int {
	if limit <= 0 {
		return 0
	}
	n := 1
	switch t := p.(type) {
	case choicePat:
		n += patternSize(t.Left, limit-n)
		if n <= limit {
			n += patternSize(t.Right, limit-n)
		}
	case groupPat:
		n += patternSize(t.Left, limit-n)
		if n <= limit {
			n += patternSize(t.Right, limit-n)
		}
	case interleavePat:
		n += patternSize(t.Left, limit-n)
		if n <= limit {
			n += patternSize(t.Right, limit-n)
		}
	case afterPat:
		n += patternSize(t.Left, limit-n)
		if n <= limit {
			n += patternSize(t.Right, limit-n)
		}
	case oneOrMorePat:
		n += patternSize(t.Pattern, limit-n)
	case listPat:
		n += patternSize(t.Pattern, limit-n)
	}
	// An elementPat's and attributePat's content is not descended into: it is
	// the schema's own structure, which is fixed, and only the derivative's
	// accumulation is what grows with the document.
	return n
}
