package xslt

// Lane L2: call frames for recursive inlined calls (su-absorbing-202/203,
// si-fork-808: mf:group(tail(current-group()), $level + 1) inside the
// streamed for-each-group of mf:group's own body).
//
// An inlined body (streamfn_inline.go) is planned once in its caller's
// plan, its first parameter S read as the caller's argument P. A call on the
// same function from inside that body has another argument (P2: a part of
// the current group), so its body needs its own copy: a frame, a plan of
// its own (streamExec, with its own op table keyed by the body's nodes), one
// per call site, made the first time the site is met while its function is
// being planned (root.planning) and found by the site after that. A frame's
// body holds the same sites, so a frame refers to itself or to the other
// sites' frames: a cycle of plans, never of planning (a site's frame is
// registered before its body is planned, and met again it is only looked
// up). The frames hang off the root plan (the container's or the rule's,
// which the run has as run.x); run.feed looks an instruction up there when
// the root's own table has not got it (frameOp).
//
// A frame's body is compiled once per site (frameCodeFor), with the frames
// of the plans compiling as c.ruleExecs (so its consumers' ops are the
// frames'), its substitution S -> P2 alone (P2 reads no variable, so no
// enclosing substitution applies); its own call to the site finds the code
// being compiled and reads its body when it runs.
//
// Gating: a frame exists only where inlineCallOp met a call on a function
// it is already planning, which only a call item 4's inlineCheck accepted
// leads to; a plan with no such call has no frames, no inlines, frameOp
// returns nil at once, readyInlines returns "", and selectOf is parseAt.
// Every other plan runs exactly as before.
//
// Fallbacks: a site at its body's own focus (its focus would be the
// caller's, which differs between copies) is refused; so is S anywhere but
// a select the plan substitutes. At run time an inlined call in a streamed
// run of a plan it was not compiled for, or a frame whose body did not
// finish compiling, returns errStreamPlan: the container runs on the tree.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// frameCode is a frame's compiled body, shared by its plans; done once it
// has compiled.
type frameCode struct {
	body []Instruction
	done bool
}

// rootPlan is x's root plan: itself, or a frame's.
func (x *streamExec) rootPlan() *streamExec {
	if x.frameRoot != nil {
		return x.frameRoot
	}
	return x
}

// frameOp is the op of instr in one of x's frames, or nil.
func (x *streamExec) frameOp(instr Instruction) *streamOp {
	if len(x.frames) == 0 {
		return nil
	}
	var found *streamOp
	for _, fx := range x.frames {
		if op := fx.byInstr[instr]; op != nil {
			if found != nil && found != op {
				return nil // cannot happen: an instruction is one frame's
			}
			found = op
		}
	}
	return found
}

// frameCall plans el, a call ic on the function being planned, as its call
// frame's: the site's frame made and planned the first time, looked up
// after.
func (b *streamBuilder) frameCall(el *xdm.Node, ic *inlinedCall, body []*xdm.Node, f vfocus) bool {
	fx, why := b.frameFor(el, ic, body, f, b.grp)
	if fx == nil {
		return b.refuse(why)
	}
	ic.frame = fx
	b.x.inlines[el] = ic
	return true
}

// frameFor is the call frame of the site el: made, its body planned at
// focus f (grp: in a streamed group's body), the first time; looked up
// after. nil and why when its body does not plan.
func (b *streamBuilder) frameFor(el *xdm.Node, ic *inlinedCall, body []*xdm.Node, f vfocus, grp bool) (*streamExec, string) {
	top := b.x.rootPlan()
	if fx := top.frames[el]; fx != nil {
		return fx, ""
	}
	fx := newStreamExec()
	fx.class, fx.modes, fx.rule, fx.frameRoot = top.class, top.modes, top.rule, top
	if top.frames == nil {
		top.frames = map[*xdm.Node]*streamExec{}
	}
	top.frames[el] = fx
	fb := &streamBuilder{pl: b.pl, x: fx, ops: &fx.top, kinds: &fx.kinds,
		noIdent: b.noIdent, grp: grp, key: grp && b.key, sub: ic}
	if !fb.members(ic.fn, body, f) {
		delete(top.frames, el)
		return nil, "call frame of " + elName(el) + ": " + fb.why
	}
	return fx, ""
}

// readyFrames readies the frames of x, a root plan, each once.
func (x *streamExec) readyFrames() string {
	if x.frameRoot != nil {
		return ""
	}
	seen := map[*streamExec]bool{}
	for _, fx := range x.frames {
		if seen[fx] {
			continue
		}
		seen[fx] = true
		for _, ic := range fx.inlines {
			if ic.failed != "" {
				return ic.failed
			}
		}
		if fx.code == nil || !fx.code.done {
			return "call frame not compiled"
		}
		if why := fx.readyOps(fx.top, 1, &fx.kinds); why != "" {
			return "call frame: " + why
		}
		if why := fx.readyTries(); why != "" {
			return "call frame: " + why
		}
	}
	return ""
}

// frameCodeFor compiles the frame body of ic's site once, its ops those of
// frames (the site's frame in each plan compiling).
func (c *compiler) frameCodeFor(ic *inlinedCall, frames map[*streamExec]bool, nParams int) (*frameCode, string) {
	var code *frameCode
	for fx := range frames {
		if fx.code != nil {
			if code != nil && code != fx.code {
				return nil, "a call frame compiled twice"
			}
			code = fx.code
		}
	}
	if code != nil {
		for fx := range frames {
			fx.code = code
		}
		return code, ""
	}
	code = &frameCode{}
	execs := make([]*streamExec, 0, len(frames))
	for fx := range frames {
		fx.code = code
		execs = append(execs, fx)
	}
	stream, rules, inline := c.stream, c.ruleExecs, c.inline
	c.stream, c.ruleExecs, c.inline = nil, execs, &inlineSubst{ic: ic}
	body, err := c.compileSequenceFrom(ic.fn, ic.fn, nParams)
	c.stream, c.ruleExecs, c.inline = stream, rules, inline
	if err != nil {
		for fx := range frames {
			fx.code = nil
		}
		return nil, err.Error()
	}
	code.body, code.done = body, true
	return code, ""
}

// plannedIn reports whether i was compiled for root, the plan a streamed
// run runs: in its table, or in one of its frames'.
func (i *inlineCallInstr) plannedIn(root *streamExec) bool {
	if i.plans[root] {
		return true
	}
	for p := range i.plans {
		if p.frameRoot == root {
			return true
		}
	}
	return false
}

// selectOf is el's select as the plan reads it: with the first parameter of
// the body being planned read as its call's argument.
func (b *streamBuilder) selectOf(el *xdm.Node) xpath.Expr {
	e := parseAt(el, el.AttrValue("select"))
	if b.sub == nil || e == nil {
		return e
	}
	ic := b.sub
	bad := false
	e2 := substS(e, ic.param, func() xpath.Expr { return inlineArg(ic) }, &bad)
	if bad {
		return e
	}
	if ic.loop {
		e2 = dropContextMaps(e2) // streamfn_loop.go
	}
	return e2
}

// within reports whether el is inside fn.
func within(el, fn *xdm.Node) bool {
	for n := el.Parent(); n != nil; n = n.Parent() {
		if n == fn {
			return true
		}
	}
	return false
}

// insideFocusChange reports whether an instruction of fn's body between el
// and fn gives its content a focus of its own.
func insideFocusChange(el, fn *xdm.Node) bool {
	for n := el.Parent(); n != nil && n != fn; n = n.Parent() {
		if n.Kind() == xdm.KindElement && n.Name().URI == xdm.NSXSL && focusChanging[n.Name().Local] &&
			n.Name().Local != "sort" && n.Name().Local != "key" {
			return true
		}
	}
	return false
}

// groupOfS reports whether the xsl:for-each-group nearest above el in fn's
// body groups exactly $s: its groups' members are then items S was given,
// of S's type already.
func groupOfS(el, fn *xdm.Node, s xdm.QName) bool {
	for n := el.Parent(); n != nil && n != fn; n = n.Parent() {
		if isXSL(n, "for-each-group") {
			v, ok := parseAt(n, n.AttrValue("select")).(*xpath.VarRef)
			return ok && v.Name.URI == s.URI && v.Name.Local == s.Local
		}
	}
	return false
}

// ofCurrentGroup reports whether e is a part of the current group:
// current-group(), tail() of it, or it filtered.
func ofCurrentGroup(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.FuncCall:
		if isCurrentGroup(x) {
			return true
		}
		return x.Name.URI == fnNS && x.Name.Local == "tail" && len(x.Args) == 1 && ofCurrentGroup(x.Args[0])
	case *xpath.FilterExpr:
		return ofCurrentGroup(x.Base)
	}
	return false
}

// substAllowed is inlineAttr's rule for a body whose first parameter is
// read as the call's argument: in a select whose split the compile
// substitutes.
func substAllowed(el *xdm.Node, name string, _ xpath.Expr) bool {
	return name == "select" && substSite(el)
}

// substSite reports whether el's select is one whose split the compile
// substitutes and whose plan-time reading selectOf or the analysis records
// give: the consumers an inlined body may read S in.
func substSite(el *xdm.Node) bool {
	if el.Name().URI != xdm.NSXSL {
		return false
	}
	switch el.Name().Local {
	case "for-each-group", "apply-templates", "sequence", "copy-of", "value-of":
		return true
	}
	return false
}

// namedElements is t when it is element(N)* (written on p), so that a case
// of a switch on the type matches it; "element()*" otherwise.
func namedElements(p *xdm.Node, t string) string {
	if strings.HasPrefix(t, "element(") && strings.HasSuffix(t, ")*") && !strings.Contains(t, ",") &&
		t != "element()*" && t != "element(*)*" {
		if _, ok := elementTypeName(p, t); ok {
			return t
		}
	}
	return "element()*"
}

// elementTypeName is N of element(N)*, written on p.
func elementTypeName(p *xdm.Node, t string) (xdm.QName, bool) {
	st, err := xpath.ParseSequenceType(t, newNSResolver(p, xpathDefaultNamespace(p)))
	if err != nil {
		return xdm.QName{}, false
	}
	kt, ok := st.ItemType.(*xpath.KindTest)
	if !ok || kt.Kind != xdm.KindElement || kt.Name == nil || kt.TypeName != "" || kt.SchemaDeclared {
		return xdm.QName{}, false
	}
	return *kt.Name, true
}

// sameElementName reports whether nt, a path's last name test, selects
// only elements named N of t = element(N)*.
func sameElementName(p *xdm.Node, t string, nt *xpath.NameTest) bool {
	n, ok := elementTypeName(p, t)
	return ok && nt != nil && !nt.AnyURI && !nt.AnyLocal && nt.Name.URI == n.URI && nt.Name.Local == n.Local
}
