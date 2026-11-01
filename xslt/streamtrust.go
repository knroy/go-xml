package xslt

// Whether a §19.8 verdict may be trusted by the execution side.
//
// The analysis gives every construct a posture and sweep, and clears known
// where it meets something it does not model. That is enough for XTSE3430,
// which is raised only on a known verdict. It is not enough to drive
// execution, because several rules take a DECLARATION at its word: a call on a
// declared-streamable stylesheet function is assessed by its category
// (§19.8.5), a use-attribute-sets reference by the set's streamable attribute
// (§19.8.6), a call on fn:accumulator-before or -after as grounded (§19.8.9.1,
// §19.8.9.2), and an xsl:apply-templates into a streamable mode by the mode's
// declaration alone (§19.8.4.5). Each declaration has its own check, and each
// of those checks stays silent when it meets something unmodelled -- so a
// container can be known and grounded while resting on a declaration nobody
// verified. The m10 soundness lane found such stylesheets compile with a
// grounded verdict and read data a streaming evaluation would already have
// passed.
//
// So the analysis carries a second bit beside known: unverified, set where a
// verdict rests on a declaration whose own check did not positively pass, or
// on one another module could override. Refusals never read it, so XTSE3430
// results cannot change; the burst gate (streamburst.go) requires it clear.
//
// The check of each declaration is made only on demand, through a declTrust
// the trusting caller creates. Without one -- every refusal path -- each trust
// point simply sets the bit, which nobody reads there, and costs nothing.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// declTrust memoises, for one module, whether each declaration a verdict can
// rest on passed its own check.
type declTrust struct {
	// shared says the module is not the whole stylesheet: it includes,
	// imports or uses other modules, or is itself one of them. Another
	// module can then add template rules to a mode, or override a function,
	// named template, attribute set or accumulator by import precedence, so
	// no declaration seen here can be trusted.
	shared bool
	sets   map[xdm.QName][]*xdm.Node
	funcs  map[funcKey]*streamFunc

	state map[any]trustState
	// depth, chain, cyclic and failed implement the circularity rule below.
	depth          int
	chain          []any
	cyclic, failed bool
}

type trustState int8

const (
	trustUnseen trustState = iota
	trustInProgress
	trustVerified
	trustUnverified
)

func newDeclTrust(root *xdm.Node, shared bool) *declTrust {
	return &declTrust{
		shared: shared || reachesOtherModules(root),
		sets:   attributeSetDeclarations(root),
		funcs:  collectStreamFuncs(root),
		state:  map[any]trustState{},
	}
}

// reachesOtherModules reports whether a module pulls in another.
func reachesOtherModules(root *xdm.Node) bool {
	found := false
	walkElements(root, func(el *xdm.Node) bool {
		if isXSL(el, "import") || isXSL(el, "include") || isXSL(el, "use-package") {
			found = true
		}
		return !found
	})
	return found
}

// local reports whether declarations seen in this module are the ones in
// force: false without a declTrust, or in a module another can override.
func (t *declTrust) local() bool { return t != nil && !t.shared }

// verified runs check for the declaration key once, and reports whether it
// passed.
//
// A declaration whose check is still in progress counts as verified: that is
// §19.8.5's own circularity break, where a recursive call is assessed by the
// callee's declared category without consulting its body. A result reached
// under such an assumption is provisional, so when a cycle was met and any
// check in the same outermost run failed, every result of that run is
// demoted -- an over-approximation, in the safe direction, of the greatest
// fixed point.
func (t *declTrust) verified(key any, check func() bool) bool {
	if t == nil || t.shared {
		return false
	}
	switch t.state[key] {
	case trustInProgress:
		t.cyclic = true
		return true
	case trustVerified:
		return true
	case trustUnverified:
		return false
	}
	t.state[key] = trustInProgress
	t.depth++
	ok := check()
	t.depth--
	if ok {
		t.state[key] = trustVerified
		t.chain = append(t.chain, key)
	} else {
		t.state[key] = trustUnverified
		t.failed = true
	}
	if t.depth == 0 {
		if t.cyclic && t.failed {
			for _, k := range t.chain {
				t.state[k] = trustUnverified
			}
		}
		t.chain, t.cyclic, t.failed = nil, false, false
	}
	return t.state[key] == trustVerified
}

// funcVerified: a declared-streamable function's body meets its category
// (§19.8.5), fully modelled and resting on nothing unverified.
func (t *declTrust) funcVerified(f *streamFunc) bool {
	return t.verified(f.body, func() bool {
		if f.category != catAbsorbing && f.arity() > 0 && typePermitsSeveralNodes(f.params[0]) {
			return false
		}
		p, known, unverified := functionBodyVerdict(f, t.funcs, t)
		return known && !unverified && satisfiesCategory(f, p)
	})
}

// attrSetKey keys an attribute set in declTrust.state.
type attrSetKey struct{ name xdm.QName }

// attrSetVerified: every declaration of a streamable attribute set is
// motionless (§19.8.6), fully modelled and resting on nothing unverified.
func (t *declTrust) attrSetVerified(name xdm.QName, decls []*xdm.Node) bool {
	return t.verified(attrSetKey{name}, func() bool {
		a := &instrAnalyzer{ctxPosture: postureStriding, ctxAllowsChildren: true,
			known: true, attrSets: t.sets, funcs: t.funcs, trust: t}
		p := a.attributeSetProps(decls)
		return a.known && !a.unverified && p.sweep == sweepMotionless
	})
}

// accumulatorVerified: the accumulator a call on fn:accumulator-before or
// -after names is declared streamable in this module and meets every
// condition of §18.2.9, each fully modelled.
func (t *declTrust) accumulatorVerified(at *xdm.Node, nameArg xpath.Expr) bool {
	if t == nil || at == nil {
		return false
	}
	lit, ok := nameArg.(*xpath.Literal)
	if !ok || lit.Val == nil {
		return false
	}
	want, err := resolveQNameAttr(at, strings.TrimSpace(lit.Val.String()))
	if err != nil {
		return false
	}
	var acc *xdm.Node
	walkElements(documentElementOf(at), func(d *xdm.Node) bool {
		if acc == nil && isXSL(d, "accumulator") {
			if n, e := resolveQNameAttr(d, d.AttrValue("name")); e == nil && n == want {
				acc = d
			}
			return false
		}
		return acc == nil
	})
	if acc == nil || !isYes(acc.AttrValue("streamable")) {
		return false
	}
	return t.verified(acc, func() bool { return t.accumulatorOK(acc) })
}

func (t *declTrust) accumulatorOK(acc *xdm.Node) bool {
	// Conditions 4 and 5 of §18.2.9 on an expression: grounded and
	// motionless, the posture forgiven where the declared type atomizes
	// (checkOneAccumulator's reading of §18.2.1).
	exprOK := func(src string, at *xdm.Node, kids, atomic bool) bool {
		ns := newNSResolver(at, xpathDefaultNamespace(at))
		e, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
		if err != nil {
			return false
		}
		a := &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: kids,
			known: true, decl: at, funcs: t.funcs, trust: t}
		p := a.expr(e)
		return a.known && !a.unverified && p.sweep == sweepMotionless &&
			(p.posture == postureGrounded || atomic)
	}
	atomic := atomicSequenceType(acc.AttrValue("as"))
	if a := acc.Attr("", "initial-value"); a != nil && !exprOK(a.Value(), acc, true, false) {
		return false
	}
	for _, rule := range acc.ChildElements() {
		if !isXSL(rule, "accumulator-rule") {
			continue
		}
		if !patternVerified(rule.AttrValue("match"), rule) {
			return false
		}
		kids := matchedNodeAllowsChildren(rule.AttrValue("match"), rule)
		if a := rule.Attr("", "select"); a != nil {
			if !exprOK(a.Value(), rule, kids, atomic) {
				return false
			}
			continue
		}
		// The contained sequence constructor, which the refusal check
		// leaves alone; here it is simply assessed.
		p, known, unverified := sequenceConstructorVerdict(rule, postureStriding, t.sets, t.funcs, t)
		if !known || unverified || p.sweep != sweepMotionless ||
			(p.posture != postureGrounded && !atomic) {
			return false
		}
	}
	return true
}

// patternVerified: a pattern is motionless (§19.8.10), fully modelled, and
// calls no accumulator function -- the pattern classifier assesses those
// without a declTrust, so a call there is never taken on trust.
func patternVerified(src string, at *xdm.Node) bool {
	if src == "" || strings.Contains(src, "accumulator-") {
		return false
	}
	free, known := patternIsFreeRanging(src, at)
	return known && !free
}

// modeKey keys a mode in declTrust.state.
type modeKey struct{ clark string }

// modeVerified: every template rule of a streamable mode, mode="#all" ones
// included, meets §6.6.4 -- a motionless pattern, motionless parameter
// defaults, a grounded body -- each fully modelled and resting on nothing
// unverified. A rule withheld from refusal (current-group(), say) has a body
// that is not known and grounded, so it fails here.
func (t *declTrust) modeVerified(root *xdm.Node, clark string) bool {
	return t.verified(modeKey{clark}, func() bool {
		ok := true
		walkElements(root, func(el *xdm.Node) bool {
			if !ok || !isXSL(el, "template") || el.Attr("", "match") == nil {
				return ok
			}
			if !strings.Contains(" "+el.AttrValue("mode")+" ", " #all ") &&
				!containsString(templateModeNames(el), clark) {
				return true
			}
			ok = t.ruleOK(el)
			return ok
		})
		return ok
	})
}

func (t *declTrust) ruleOK(rule *xdm.Node) bool {
	if !patternVerified(rule.AttrValue("match"), rule) {
		return false
	}
	for _, c := range rule.ChildElements() {
		if !isXSL(c, "param") {
			continue
		}
		if p, known, unverified := ruleParamDefault(c, t.sets, t.funcs, t); !known || unverified || p.sweep != sweepMotionless {
			return false
		}
	}
	p, known, unverified := sequenceConstructorVerdict(rule, postureStriding, t.sets, t.funcs, t)
	return known && !unverified && p.streamable() && p.posture == postureGrounded
}

// enclosingRuleModesVerified is the trust an xsl:next-match, xsl:apply-imports
// or mode="#current" places in the rules of the mode the enclosing template
// rule was selected in. Every mode the rule belongs to must be streamable and
// verified: the current mode is whichever one selected it.
//
// §19.8.4.5 treats mode="#current" as streamable because a rule reached with
// a streamed context item must have been selected in a streamable mode. That
// does not hold inside an xsl:source-document within a rule of a
// non-streamable mode, which is why the modes are checked here rather than
// assumed.
func (t *declTrust) enclosingRuleModesVerified(el *xdm.Node) bool {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if !isXSL(n, "template") {
			continue
		}
		if n.Attr("", "match") == nil || strings.Contains(" "+n.AttrValue("mode")+" ", " #all ") {
			return false
		}
		root := documentElementOf(n)
		streamable := streamableModeNames(root)
		modes := templateModeNames(n)
		for _, m := range modes {
			if !streamable[m] || !t.modeVerified(root, m) {
				return false
			}
		}
		return len(modes) > 0
	}
	return false
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
