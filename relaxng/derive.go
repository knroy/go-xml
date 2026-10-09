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
	// A choice between a pattern and itself is that pattern. Without this the
	// derivative of a oneOrMore nested in a oneOrMore carries two copies of
	// the same continuation per item, so the pattern doubles with every
	// child: DocBook's xref.001, valid, hit the 100,000-node bound on that
	// alone. Equality is structural and conservative (see patEq), which
	// catches the copies the derivative makes; hash-consing would make it a
	// pointer comparison.
	if patEq(a, b) || inChoice(a, b) {
		return a
	}
	return choicePat{a, b}
}

// inChoice reports whether b is already one of a's alternatives. A chain of
// choices is built leftwards, so the alternatives are the Right of each link
// and the Left of the last.
func inChoice(a, b pattern) bool {
	for {
		c, ok := unstatic(a).(choicePat)
		if !ok {
			return patEq(a, b)
		}
		if patEq(c.Right, b) {
			return true
		}
		a = c.Left
	}
}

// patEq reports whether two patterns are structurally equal. It may answer
// false for equal patterns but never true for different ones: values and
// data, whose fields hold maps and slices, always compare unequal, and a
// refPat is equal only to itself.
func patEq(a, b pattern) bool {
	// A static wrapper (memo.go) is compared as the subtree it holds. The
	// same wrapper on both sides is that subtree twice, whose answer was
	// worked out once when the wrapper was made.
	if x, ok := a.(*refPat); ok && x.static != nil && a == b {
		return x.static.selfEq
	}
	a, b = unstatic(a), unstatic(b)
	switch x := a.(type) {
	case choicePat:
		y, ok := b.(choicePat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case groupPat:
		y, ok := b.(groupPat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case interleavePat:
		y, ok := b.(interleavePat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case afterPat:
		y, ok := b.(afterPat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case oneOrMorePat:
		y, ok := b.(oneOrMorePat)
		return ok && patEq(x.Pattern, y.Pattern)
	case listPat:
		y, ok := b.(listPat)
		return ok && patEq(x.Pattern, y.Pattern)
	case elementPat:
		y, ok := b.(elementPat)
		return ok && x.Name == y.Name && patEq(x.Pattern, y.Pattern)
	case attributePat:
		y, ok := b.(attributePat)
		return ok && x.Name == y.Name && patEq(x.Pattern, y.Pattern)
	case valuePat, dataPat:
		return false
	}
	// notAllowedPat, emptyPat, textPat and *refPat are comparable.
	return a == b
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
	if r, ok := p.(*refPat); ok {
		if d, ok := r.open.Load(name); ok {
			return d.(pattern)
		}
		d := startTagOpenDeriv(expand(r), name)
		// ponytail: per-definition cap, not a global LRU; enough for any
		// real vocabulary, and past it the derivative is simply recomputed.
		if r.openN.Add(1) <= maxOpenMemo {
			r.open.Store(name, d)
		}
		return d
	}
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

// maxOpenMemo bounds refPat.open per definition.
const maxOpenMemo = 1024

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

type attr struct {
	name  xdm.QName
	value string
}

func attDeriv(p pattern, a attr, ctx nsContext) pattern {
	// A definition known to hold no attribute pattern (learnt by
	// startTagCloseDerivCh, which stops at the same element boundary)
	// derives to notAllowed for every attribute: every leaf does.
	if r, ok := p.(*refPat); ok {
		if r.attrFree.Load() {
			return notAllowedPat{}
		}
		if s := r.static; s != nil {
			if b, ok := s.att.Load(a.name); ok && b.(*patBox).p != nil {
				return b.(*patBox).p
			} else if ok {
				return attDeriv(r.cached, a, ctx)
			}
			d := attDeriv(r.cached, a, ctx)
			if s.attN.Add(1) <= maxOpenMemo {
				b := &patBox{}
				if anyValueFor(r.cached, a.name, nil) {
					b.p = d
				}
				s.att.Store(a.name, b)
			}
			return d
		}
	}
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

// anyValueFor reports whether every attribute pattern in p that admits name,
// up to an element boundary, takes any value (its content is text), so that
// attDeriv of p for that name does not depend on the value or its context.
// A reference not yet resolved, or one being visited, answers false.
func anyValueFor(p pattern, name xdm.QName, visiting map[*refPat]bool) bool {
	switch t := p.(type) {
	case attributePat:
		if !t.Name.contains(name) {
			return true
		}
		_, ok := t.Pattern.(textPat)
		return ok
	case choicePat:
		return anyValueFor(t.Left, name, visiting) && anyValueFor(t.Right, name, visiting)
	case groupPat:
		return anyValueFor(t.Left, name, visiting) && anyValueFor(t.Right, name, visiting)
	case interleavePat:
		return anyValueFor(t.Left, name, visiting) && anyValueFor(t.Right, name, visiting)
	case oneOrMorePat:
		return anyValueFor(t.Pattern, name, visiting)
	case *refPat:
		if t.attrFree.Load() {
			return true
		}
		if !t.done || t.err != nil || visiting[t] {
			return false
		}
		if visiting == nil {
			visiting = map[*refPat]bool{}
		}
		visiting[t] = true
		ok := anyValueFor(t.cached, name, visiting)
		delete(visiting, t)
		return ok
	}
	// Elements, values, data, lists, text, empty and notAllowed hold no
	// attribute pattern before an element boundary.
	return true
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
	if r, ok := p.(*refPat); ok && r.static != nil && r.static.dataFree {
		if b := r.static.text.Load(); b != nil {
			return b.p
		}
		d := textDeriv(r.cached, s, ctx)
		r.static.text.Store(&patBox{p: d})
		return d
	}
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
	q, _ := startTagCloseDerivCh(p)
	return q
}

// startTagCloseDerivCh is startTagCloseDeriv reporting whether anything
// changed. A subtree holding no attributePat comes back as it went in, so it
// is not rebuilt -- and its choices not deduplicated again by choice() --
// once per element when there is nothing to discard.
func startTagCloseDerivCh(p pattern) (pattern, bool) {
	if r, ok := p.(*refPat); ok {
		if r.attrFree.Load() {
			return p, false
		}
		if r.static != nil {
			if b := r.static.close.Load(); b != nil {
				return b.p, b.ch
			}
			q, ch := startTagCloseDerivCh(r.cached)
			if !ch {
				q = p
			}
			r.static.close.Store(&patBox{p: q, ch: ch})
			return q, ch
		}
		q, ch := startTagCloseDerivCh(expand(r))
		if !ch {
			r.attrFree.Store(true)
			return p, false
		}
		return q, true
	}
	switch t := expand(p).(type) {
	case afterPat:
		l, ch := startTagCloseDerivCh(t.Left)
		if !ch {
			return p, false
		}
		return after(l, t.Right), true
	case choicePat:
		l, chl := startTagCloseDerivCh(t.Left)
		r, chr := startTagCloseDerivCh(t.Right)
		if !chl && !chr {
			return p, false
		}
		return choice(l, r), true
	case groupPat:
		l, chl := startTagCloseDerivCh(t.Left)
		r, chr := startTagCloseDerivCh(t.Right)
		if !chl && !chr {
			return p, false
		}
		return group(l, r), true
	case interleavePat:
		l, chl := startTagCloseDerivCh(t.Left)
		r, chr := startTagCloseDerivCh(t.Right)
		if !chl && !chr {
			return p, false
		}
		return interleave(l, r), true
	case oneOrMorePat:
		q, ch := startTagCloseDerivCh(t.Pattern)
		if !ch {
			return p, false
		}
		return oneOrMore(q), true
	case attributePat:
		return notAllowedPat{}, true
	}
	return p, false
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
	case *refPat:
		if t.static != nil {
			return int(t.static.size)
		}
	}
	// An elementPat's and attributePat's content is not descended into: it is
	// the schema's own structure, which is fixed, and only the derivative's
	// accumulation is what grows with the document.
	return n
}
