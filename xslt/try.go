package xslt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// xsl:try and xsl:catch, XSLT 3.0 section 8.3.
//
// xsl:try runs a sequence constructor (or a select expression) and, if it
// raises a recoverable dynamic error, hands control to the first xsl:catch
// whose errors attribute matches the error's code. The catch body sees the
// error through six variables in the err: namespace, and its result replaces
// whatever the failed body had produced.

// tryInstr implements xsl:try.
type tryInstr struct {
	// sel is @select, and body the sequence constructor. Exactly one is in
	// play: the grammar forbids content other than xsl:catch alongside a
	// select, so body is empty whenever sel is set.
	sel  *xpath.Compiled
	body []Instruction
	// catches are the xsl:catch clauses in document order. The first whose
	// name test matches wins, which is why order is preserved rather than
	// indexing them by code.
	catches []catchClause
	// module is the base URI of the xsl:try element, published as $err:module.
	//
	// The engine records no location on an error, so the module of the
	// instruction that caught it stands in for the module that raised it.
	// They differ only when the failure happened in an imported module, and
	// every case in the suite that inspects err:module raises the error in
	// the same file as the try.
	module string
	// noRollback records rollback-output="no".
	//
	// The default is "yes": output the failed body wrote is discarded before
	// the catch runs. This builder buffers everything, so discarding is
	// always possible and "yes" is free. "no" is a promise the *stylesheet*
	// makes that it does not need the rollback, and it licenses the
	// processor to give up instead — see the XTDE3530 check in Execute.
	noRollback bool
}

// catchClause is one xsl:catch.
type catchClause struct {
	// tests is @errors parsed into name tests. An absent attribute means "*",
	// so this is never empty.
	tests []errorNameTest
	// sel is @select, body the sequence constructor; as on xsl:try, only one
	// of the two is populated.
	sel  *xpath.Compiled
	body []Instruction
}

// errorNameTest is one token of an xsl:catch/@errors NameTest list: a QName,
// a wildcard, or a half-wildcard such as err:* or *:FOAR0001.
type errorNameTest struct {
	// anyURI and anyLocal record which halves are wildcarded.
	anyURI, anyLocal bool
	uri, local       string
}

func (t errorNameTest) matches(uri, local string) bool {
	return (t.anyURI || t.uri == uri) && (t.anyLocal || t.local == local)
}

// compileTry builds an xsl:try.
func (c *compiler) compileTry(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &tryInstr{module: n.BaseURI}
	switch v := strings.TrimSpace(n.AttrValue("rollback-output")); v {
	case "", "yes", "true", "1":
	case "no", "false", "0":
		instr.noRollback = true
	default:
		return nil, fmt.Errorf(
			"XTSE0020: xsl:try/@rollback-output must be yes or no, got %q", v)
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:try/@select: %w", err)
		}
		instr.sel = comp
	}

	// The children are the sequence constructor followed by the xsl:catch
	// clauses. They are separated here rather than by compileSequence,
	// because an xsl:catch reaching the generic instruction dispatcher would
	// be rejected as misplaced — which is right for a stray one.
	var bodyNodes []*xdm.Node
	seenCatch := false
	for _, ch := range n.Children {
		if isXSL(ch, "catch") {
			seenCatch = true
			cl, err := c.compileCatch(ch, ch)
			if err != nil {
				return nil, err
			}
			instr.catches = append(instr.catches, cl)
			continue
		}
		if seenCatch {
			// "The xsl:catch elements must come last": a real instruction
			// after the first catch is a grammar error rather than part of
			// the try body. xsl:fallback is exempt — it is permitted
			// anywhere in an instruction's content and never instantiated
			// here — and so is the whitespace between the clauses.
			if ch.Kind == xdm.KindElement && !isXSL(ch, "fallback") {
				return nil, fmt.Errorf(
					"XTSE0010: %s follows xsl:catch in xsl:try; the xsl:catch "+
						"elements must come last", ch.Name.Lexical())
			}
			continue
		}
		bodyNodes = append(bodyNodes, ch)
	}
	if len(instr.catches) == 0 {
		return nil, fmt.Errorf("XTSE0010: xsl:try requires at least one xsl:catch")
	}
	if instr.sel != nil {
		// XTSE3140: with @select the only permitted content is xsl:catch and
		// xsl:fallback, so anything the loop kept as body is misplaced.
		for _, ch := range bodyNodes {
			if ch.Kind == xdm.KindElement && !isXSL(ch, "fallback") {
				return nil, fmt.Errorf(
					"XTSE3140: xsl:try has a select attribute, so it may not " +
						"also have a sequence constructor")
			}
		}
		return instr, nil
	}
	body, err := c.compileNodes(bodyNodes, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// compileCatch builds one xsl:catch clause.
func (c *compiler) compileCatch(n *xdm.Node, nsScope *xdm.Node) (catchClause, error) {
	var cl catchClause
	if err := checkCatchSelect(n); err != nil {
		return cl, err
	}
	tests, err := parseErrorNameTests(n)
	if err != nil {
		return cl, err
	}
	cl.tests = tests

	ns := newNSResolver(nsScope, "")
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, ns)
		if err != nil {
			return cl, fmt.Errorf("in xsl:catch/@select: %w", err)
		}
		cl.sel = comp
		return cl, nil
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return cl, err
	}
	cl.body = body
	return cl, nil
}

// parseErrorNameTests reads xsl:catch/@errors, whose value is a whitespace
// separated list of NameTests. An absent attribute catches everything.
func parseErrorNameTests(n *xdm.Node) ([]errorNameTest, error) {
	raw := strings.TrimSpace(n.AttrValue("errors"))
	if raw == "" {
		return []errorNameTest{{anyURI: true, anyLocal: true}}, nil
	}
	var tests []errorNameTest
	for _, tok := range strings.Fields(raw) {
		switch {
		case tok == "*":
			tests = append(tests, errorNameTest{anyURI: true, anyLocal: true})
		case strings.HasPrefix(tok, "*:"):
			tests = append(tests, errorNameTest{anyURI: true, local: tok[2:]})
		case strings.HasSuffix(tok, ":*"):
			prefix := tok[:len(tok)-2]
			uri, ok := n.LookupPrefix(prefix)
			if !ok {
				return nil, fmt.Errorf(
					"XTSE0280: unbound namespace prefix %q in xsl:catch/@errors",
					prefix)
			}
			tests = append(tests, errorNameTest{uri: uri, anyLocal: true})
		default:
			q, err := resolveQNameAttr(n, tok)
			if err != nil {
				return nil, err
			}
			// An unprefixed name is in no namespace, and xpath-default-namespace
			// does not reach here: @errors is a NameTest list in an attribute,
			// not an XPath expression. Every code the specs define is in the
			// err: namespace, so a bare FOAR0001 catches nothing — which is
			// what try-036 requires, writing exactly that under an
			// xpath-default-namespace of the error namespace and expecting the
			// following catch-all to win.
			tests = append(tests, errorNameTest{uri: q.URI, local: q.Local})
		}
	}
	return tests, nil
}

func (i *tryInstr) Execute(rt *runtime, out *outputBuilder) error {
	// The body writes into a builder of its own so that a failure can be
	// rolled back by discarding it. Writing into out and truncating afterwards
	// would not do: out may already have an open element whose attributes the
	// body added, and there is no record of where the body's contribution
	// began.
	sub := newOutputBuilder()
	err := i.run(rt, sub)
	if err == nil {
		return appendSequence(sub.Sequence(), out)
	}

	if !catchable(err) {
		// An unwinding signal is not a failure of the body, so the body's
		// output stands rather than being rolled back. xsl:break inside an
		// xsl:try inside an xsl:iterate is the case iterate-035 writes: the
		// <pos> element the try had already produced is part of the result,
		// and only the loop stops.
		if isUnwindSignal(err) {
			// The append's own error cannot displace the one being
			// propagated: this path exists to preserve the output the body
			// had already produced, and the signal is what the caller is
			// waiting for.
			_ = appendSequence(sub.Sequence(), out)
		}
		return err
	}
	cl, ok := i.selectCatch(errorQName(err))
	if !ok {
		return err
	}
	if i.noRollback && len(sub.Sequence()) > 0 {
		// rollback-output="no" waives the guarantee that the failed body's
		// output is discarded. This engine could discard it anyway, but the
		// stylesheet has declared it does not need to be able to, and 8.3.1
		// makes XTDE3530 the response when a processor cannot recover — which
		// is what a streaming processor would do here, and what try-034
		// requires.
		return xdm.Errorf("XTDE3530",
			"xsl:try has rollback-output=\"no\" and the failed sequence "+
				"constructor had already written output, so %s cannot be "+
				"recovered from", xdm.ErrorCode(err))
	}
	// The err: variables are in scope for the catch body only.
	crt := bindErrorVars(rt, err, i.module)
	if cl.sel != nil {
		seq, err := cl.sel.Eval(crt.ctx)
		if err != nil {
			return err
		}
		return appendSequence(seq, out)
	}
	return execSequence(cl.body, crt, out)
}

// run instantiates the try body, from @select or from the constructor.
func (i *tryInstr) run(rt *runtime, out *outputBuilder) error {
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		return appendSequence(seq, out)
	}
	return execSequence(i.body, rt, out)
}

// selectCatch finds the first clause matching the error code.
func (i *tryInstr) selectCatch(name xdm.QName) (catchClause, bool) {
	for _, cl := range i.catches {
		for _, t := range cl.tests {
			if t.matches(name.URI, name.Local) {
				return cl, true
			}
		}
	}
	return catchClause{}, false
}

// catchable reports whether err is a dynamic error xsl:try may recover from.
//
// Static errors are not: they are detected before the instruction runs, so no
// try is ever in scope for one. A terminating xsl:message is, despite ending
// the transformation when nothing catches it — message-0501 wraps one in an
// xsl:try and reads the code, description and value it carries.
func catchable(err error) bool {
	code := xdm.ErrorCode(err)
	if code == "" {
		// An error with no code is one of this engine's own internal
		// failures. Treating it as catchable would let a stylesheet mask
		// bugs, so it propagates.
		return false
	}
	switch {
	case strings.HasPrefix(code, "XTSE"), strings.HasPrefix(code, "XPST"),
		strings.HasPrefix(code, "XQST"), strings.HasPrefix(code, "XTSA"):
		// Static error codes.
		return false
	}
	// XTDE3530 is raised by xsl:try itself when it declines to recover; an
	// enclosing try must not then catch it as if it were the original error.
	return code != "XTDE3530"
}

// errorQName is the code of err as the QName xsl:catch matches against.
func errorQName(err error) xdm.QName {
	var e *xdm.Error
	if errors.As(err, &e) && e.CodeName != nil {
		return *e.CodeName
	}
	return xdm.QName{Prefix: "err", URI: xdm.NSErr, Local: xdm.ErrorCode(err)}
}

// bindErrorVars binds the six err: variables section 8.3 makes available to a
// catch clause.
func bindErrorVars(rt *runtime, err error, module string) *runtime {
	var e *xdm.Error
	desc := err.Error()
	// The code is a QName. fn:error may have named one outside the standard
	// error namespace, in which case the error carries the name it was given;
	// everything the engine raises itself uses an err: code.
	name := xdm.QName{Prefix: "err", URI: xdm.NSErr, Local: xdm.ErrorCode(err)}
	if errors.As(err, &e) {
		desc = e.Message
		if e.CodeName != nil {
			name = *e.CodeName
		}
	}

	bind := func(rt *runtime, local string, val xdm.Sequence) *runtime {
		return rt.withVar(xdm.QName{URI: xdm.NSErr, Local: local}, val)
	}
	// err:code is an xs:QName, not a string: try-002 writes it into an
	// attribute and expects the lexical form "err:FOAR0001", which only a
	// QName carrying the err prefix produces.
	rt = bind(rt, "code", xdm.Sequence{xdm.NewQNameValue(name)})
	rt = bind(rt, "description", xdm.Sequence{xdm.NewString(desc)})
	rt = bind(rt, "value", errorValue(err))
	// The module and line the error was raised in are stamped on the error as
	// it unwinds past the instruction that raised it (see srcpos.go), so they
	// name the failing instruction rather than the xsl:try that caught it.
	// try-021 turns on the difference: the xsl:result-document that raised
	// XTDE1490 is four lines below the xsl:try, and the test asserts the
	// former. Only when nothing was stamped — a stylesheet parsed without
	// positions, or an error raised outside any sequence constructor — does
	// the catching instruction's own module stand in.
	if e != nil && e.Module != "" {
		module = e.Module
	}
	if module == "" {
		rt = bind(rt, "module", nil)
	} else {
		rt = bind(rt, "module", xdm.Sequence{xdm.NewString(module)})
	}
	// err:line-number and err:column-number are xs:integer?, and 8.3 allows
	// the empty sequence when the processor does not record where an error
	// was raised. A column is never recorded here: the position tracked on a
	// stylesheet element is where its start tag begins, which for a
	// multi-line instruction is a column that says less than the line does.
	if e != nil && e.Line != 0 {
		rt = bind(rt, "line-number", xdm.Sequence{xdm.NewInteger(int64(e.Line))})
	} else {
		rt = bind(rt, "line-number", nil)
	}
	rt = bind(rt, "column-number", nil)
	return rt
}

// errorValue is the third argument of fn:error, or the empty sequence when the
// error did not come from fn:error or supplied no value.
func errorValue(err error) xdm.Sequence {
	var e *xdm.Error
	if errors.As(err, &e) {
		return e.Value
	}
	return nil
}
