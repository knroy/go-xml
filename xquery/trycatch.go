package xquery

import (
	"context"
	"errors"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// tryCatch is "try { Expr } catch NameTests { Expr } ...", XQuery 3.1 §3.16,
// productions [78]-[82].
type tryCatch struct {
	body    *enclosed
	catches []catchClause
}

// catchClause is one "catch" of a try expression: the name tests that select
// it, and the expression it evaluates.
type catchClause struct {
	tests []errorNameTest
	body  *enclosed
}

// errorNameTest is one alternative of a CatchErrorList [80]. Each is a
// NameTest, so it is a QName, a wildcard, "prefix:*" or "*:local".
type errorNameTest struct {
	anyURI   bool // "*:local" or "*"
	anyLocal bool // "prefix:*" or "*"
	uri      string
	local    string
}

func (t errorNameTest) matches(q xdm.QName) bool {
	if !t.anyURI && t.uri != q.URI {
		return false
	}
	if !t.anyLocal && t.local != q.Local {
		return false
	}
	return true
}

// parseTryCatch parses a try/catch expression.
//
// "try" is not a reserved word, so it only commits once a brace follows: "try"
// alone is a name and "try(1)" is a function call.
func (p *parser) parseTryCatch() (node, bool, error) {
	save := p.pos
	p.pos += len("try")
	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		p.pos = save
		return nil, false, nil
	}
	body, err := p.parseBracedExprSingle()
	if err != nil {
		return nil, true, err
	}
	tc := &tryCatch{body: body}
	for p.consumeKeyword("catch") {
		tests, err := p.parseCatchErrorList()
		if err != nil {
			return nil, true, err
		}
		cb, err := p.parseBracedExprSingle()
		if err != nil {
			return nil, true, err
		}
		tc.catches = append(tc.catches, catchClause{tests: tests, body: cb})
	}
	if len(tc.catches) == 0 {
		return nil, true, p.errorf(
			"XPST0003: a %q expression needs at least one %q clause",
			"try", "catch")
	}
	return tc, true, nil
}

// parseCatchErrorList parses [80] CatchErrorList: NameTest ("|" NameTest)*.
func (p *parser) parseCatchErrorList() ([]errorNameTest, error) {
	var out []errorNameTest
	for {
		p.skipSpaceAndComments()
		t, err := p.parseErrorNameTest()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		p.skipSpaceAndComments()
		if !p.consume("|") {
			return out, nil
		}
	}
}

// parseErrorNameTest parses one NameTest of a catch clause.
//
// The forms are the four of [46] NameTest / [47] Wildcard: a QName, "*",
// "prefix:*" and "*:local". An unprefixed QName here is *not* given the
// default element namespace — §3.16 says the name test is matched against the
// error code, and an error code is always in a namespace, so an unprefixed
// name could only match a code in no namespace.
func (p *parser) parseErrorNameTest() (errorNameTest, error) {
	if p.consume("*") {
		if !p.consume(":") {
			return errorNameTest{anyURI: true, anyLocal: true}, nil
		}
		local := p.scanNCName()
		if local == "" {
			return errorNameTest{}, p.errorf(
				"XPST0003: expected a local name after %q", "*:")
		}
		return errorNameTest{anyURI: true, local: local}, nil
	}
	// "Q{uri}local" and "Q{uri}*" name the namespace outright, which is the
	// only way to write a catch for an error in a namespace the query has
	// bound no prefix to.
	if uri, ok, err := p.parseBracedURI(); err != nil {
		return errorNameTest{}, err
	} else if ok {
		if p.consume("*") {
			return errorNameTest{uri: uri, anyLocal: true}, nil
		}
		local := p.scanNCName()
		if local == "" {
			return errorNameTest{}, p.errorf(
				"XPST0003: expected a local name or %q after %q", "*", "Q{"+uri+"}")
		}
		return errorNameTest{uri: uri, local: local}, nil
	}
	prefix := p.scanNCName()
	if prefix == "" {
		return errorNameTest{}, p.errorf(
			"XPST0003: expected a name test in a %q clause", "catch")
	}
	if !p.consume(":") {
		// An unprefixed name is in no namespace, as a name test always is.
		return errorNameTest{local: prefix}, nil
	}
	if p.consume("*") {
		uri, ok := p.sc.ResolvePrefix(prefix)
		if !ok {
			return errorNameTest{}, p.errorf(
				"XPST0081: the prefix %q is not bound to a namespace", prefix)
		}
		return errorNameTest{uri: uri, anyLocal: true}, nil
	}
	local := p.scanNCName()
	if local == "" {
		return errorNameTest{}, p.errorf(
			"XPST0003: expected a local name after %q", prefix+":")
	}
	uri, ok := p.sc.ResolvePrefix(prefix)
	if !ok {
		return errorNameTest{}, p.errorf(
			"XPST0081: the prefix %q is not bound to a namespace", prefix)
	}
	return errorNameTest{uri: uri, local: local}, nil
}

// eval runs the try body, and on a dynamic error hands the error to the first
// catch clause whose name test matches its code.
//
// The clause runs with the seven err: variables of §3.16 in scope. They are
// bound unconditionally rather than only when the clause mentions them,
// because whether it does is a property of an expression this package has
// already handed to xpath and no longer inspects; the cost is seven map
// entries on a path that only runs when something failed.
//
// An error raised *by* a catch clause is not caught by a later clause of the
// same try: §3.16 makes the clauses alternatives, not a chain.
func (n *tryCatch) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.run(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

func (n *tryCatch) sequence(ctx *evalContext) (xdm.Sequence, error) {
	return n.run(ctx)
}

func (n *tryCatch) run(ctx *evalContext) (xdm.Sequence, error) {
	seq, err := n.body.sequence(ctx)
	if err == nil {
		return seq, nil
	}
	// A cancelled or timed-out evaluation is not a dynamic error of the query
	// and must not be swallowed by "catch *": the deadline belongs to the
	// caller, not to the expression.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	code := errorCodeName(err)
	// §3.16: a try/catch catches a *dynamic* error. A static error is not
	// catchable — it is a property of the expression, not of one evaluation
	// of it, and the suite says so outright ("An undefined variable (static
	// error) is not caught"). This engine resolves variables, functions and
	// prefixes when the reference is evaluated rather than in a binding pass,
	// so an error the spec raises statically arrives here looking dynamic;
	// the code is what distinguishes the two, and it is enough. Letting it
	// through unchanged reports it as the static error it is.
	if isStaticErrorCode(code) {
		return nil, err
	}
	for _, c := range n.catches {
		for _, t := range c.tests {
			if !t.matches(code) {
				continue
			}
			return c.body.sequence(&evalContext{
				xp: bindErrorVars(ctx.xp, err, code),
				sc: ctx.sc,
			})
		}
	}
	return nil, err
}

// errorCodeName recovers the QName of an error's code.
//
// The engine reports errors as Go values whose code is either a field of
// *xdm.Error or, for older paths, the "XPTY0004: ..." prefix of the message;
// xdm.ErrorCode reads both. fn:error may name a code in a namespace of its
// own, which *xdm.Error carries separately as CodeName, and that wins when it
// is there. Everything else is in the standard error namespace.
//
// An error with no recoverable code at all is given FOER0000, the code §3.16
// assigns to an error raised with no code.
func errorCodeName(err error) xdm.QName {
	var e *xdm.Error
	if errors.As(err, &e) && e.CodeName != nil {
		return *e.CodeName
	}
	code := xdm.ErrorCode(err)
	if code == "" {
		code = "FOER0000"
	}
	return xdm.QName{Prefix: "err", URI: xdm.NSErr, Local: code}
}

// bindErrorVars binds the seven variables §3.16 puts in scope in a catch
// clause.
//
// All seven are in the http://www.w3.org/2005/xqt-errors namespace whatever
// prefix the query binds to it, so they are bound by expanded name and a query
// that spells the prefix differently still finds them. The four the engine
// cannot always supply — value, module, line-number, column-number — are the
// empty sequence when nothing recorded them, which is what the specification
// permits: it says a processor that does not record a position reports the
// empty sequence rather than a wrong one.
//
// err:additional is always empty here. It is the slot for implementation
// defined extra information and this engine defines none.
func bindErrorVars(ctx *xpath.Context, err error, code xdm.QName) *xpath.Context {
	var e *xdm.Error
	errors.As(err, &e)

	description := err.Error()
	if e != nil {
		description = e.Message
	} else if c := xdm.ErrorCode(err); c != "" {
		// Strip the code prefix that older paths carry in the message, so
		// that err:description is the prose and err:code is the code, rather
		// than both holding the code.
		description = strings.TrimPrefix(description, c+": ")
	}

	bind := func(local string, v xdm.Sequence) {
		ctx = ctx.WithVar(xdm.QName{URI: xdm.NSErr, Local: local}, v)
	}
	bind("code", xdm.One(xdm.NewQNameValue(code)))
	bind("description", xdm.One(xdm.NewString(description)))
	if e != nil {
		bind("value", e.Value)
	} else {
		bind("value", nil)
	}
	if e != nil && e.Module != "" {
		bind("module", xdm.One(xdm.NewString(e.Module)))
	} else {
		bind("module", nil)
	}
	if e != nil && e.Line > 0 {
		bind("line-number", xdm.One(xdm.NewInteger(int64(e.Line))))
	} else {
		bind("line-number", nil)
	}
	bind("column-number", nil)
	bind("additional", nil)
	return ctx
}

// isStaticErrorCode reports whether a code names a static error, which a
// try/catch must not catch.
//
// The XQuery and XPath error codes encode their kind in the prefix: XPST and
// XQST are the static errors, XPDY and XQDY the dynamic ones, and XPTY/XQTY
// the type errors, which §2.3.1 raises dynamically wherever this engine has
// no static typing feature to raise them earlier. The two static families are
// the whole of what is excluded here; every other code, including the FO*
// function errors, is dynamic and catchable.
func isStaticErrorCode(code xdm.QName) bool {
	if code.URI != xdm.NSErr {
		// fn:error may raise a code in a namespace of its own, and such a
		// code is dynamic whatever it is spelled like: only the standard
		// namespace's codes carry the spec's kind in their prefix.
		return false
	}
	return strings.HasPrefix(code.Local, "XPST") ||
		strings.HasPrefix(code.Local, "XQST")
}
