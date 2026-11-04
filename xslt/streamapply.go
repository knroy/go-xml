package xslt

// Streamable modes and template rules (full streaming, component C6; DESIGN
// sections 1.6 sApply, 1.7).
//
// An xsl:apply-templates in a trusted container (or in a rule body below one)
// dispatches the nodes it selects as the cursor reaches them: an element at
// its start tag, with its view, to the rule findTemplateFrom picks -- the
// same scan, priorities and precedence as the tree's, the patterns matched on
// the view -- whose body then runs as the core plans it, the cursor where it
// is: by §19.8 one instruction of the body consumes the element's content,
// and when none did the dispatcher skips it. Rules and the built-in rules
// recurse the same way, so a document of any depth and shape is read once,
// on one cursor; the Go stack is the template stack, as it is the tree's.
//
//   - apply ops (streamOp.apply): no select or select="node()" reads the
//     children (children); select="." hands the context itself on (self);
//     another select is a downward path the split runs, each match handed on
//     at its start tag (split, matchBody); a select of the context's
//     attributes is motionless and runs off the cursor, on the view (attrs);
//     xsl:next-match and xsl:apply-imports hand the context on to the next
//     rule (next). Their with-params are motionless values.
//   - rule plans (rulePlan): each template rule's body is planned at
//     compile time for each kind of node its pattern can match: an
//     element's view or the document node by the core's builder, a text,
//     comment or processing instruction (handed over as a copy, which has no
//     identity) or an attribute of a view by a check that the body reads
//     only what such a node has, off the cursor. A pattern must be decided
//     by what a view has: no positional predicate, nothing below or beside
//     the candidate.
//   - built-in rules (builtIn): text-only-copy, shallow-copy, deep-copy,
//     shallow-skip, deep-skip and fail, each as applyBuiltInRule does it,
//     the descent read off the cursor.
//
// The container's plan is linked once the stylesheet has compiled
// (linkApply): every mode an apply op can reach, transitively, with every
// kind of node it can hand it, must be streamable and verified, and every
// rule of it that can match such a node planned for it. Otherwise the whole
// container runs on the tree, as anything the core does not take does. At
// run time a failure re-runs the container on the tree (streamexec.go).

import (
	"fmt"
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// applyKind is how an apply op reads its context.
type applyKind uint8

const (
	applyNone  applyKind = iota
	applyKids            // the children, on the cursor
	applySelf            // the context itself, the cursor where it is
	applySplit           // a downward path's matches, on the cursor
	applyAttrs           // the context's attributes, off the cursor
	applyNext            // xsl:next-match, xsl:apply-imports
)

// nodeClass is a kind of node a rule can be handed.
type nodeClass uint8

const (
	classElem nodeClass = iota // an element's view, at its start tag
	classDoc                   // the document node (its stand-in)
	classLeaf                  // a copy of a text, comment or PI node
	classAttr                  // an attribute of a view
	numClasses
)

var classNames = [numClasses]string{"element", "document node", "text, comment or PI node", "attribute"}

// --- Planning -------------------------------------------------------------

// strict is b with no records: every expression is checked.
func (b *streamBuilder) strict() *streamBuilder {
	return &streamBuilder{pl: &streamPlan{recs: map[any]*planRec{}}, x: b.x, ops: b.ops,
		kinds: b.kinds, noIdent: b.noIdent, up: b.up, key: b.key}
}

// applyOp plans an xsl:apply-templates that reads its context, focus f.
func (b *streamBuilder) applyOp(el *xdm.Node, f vfocus) bool {
	if f == vLeaf {
		return b.refuse("xsl:apply-templates on a node with no content")
	}
	kind := applyKids
	if a := el.Attr("", "select"); a != nil {
		switch e := parseAt(el, a.Value()); {
		case e == nil:
			return b.refuse("xsl:apply-templates select does not parse")
		case isContextItem(e):
			kind = applySelf
		case childNodes(e):
		default:
			kind = applySplit
		}
	}
	return b.applyAttrs(el, f) && b.withParams(el, f) && b.addApply(el, f, kind)
}

// attrApply plans an xsl:apply-templates the analysis calls motionless: one
// that selects attributes of the context, which its view has. It runs off
// the cursor.
func (b *streamBuilder) attrApply(el *xdm.Node, f vfocus) bool {
	if f == vLeaf && leafEmptySelect(el) {
		return b.applyAttrs(el, f) && b.withParams(el, f) // runs on the copy, over nothing
	}
	src := el.AttrValue("select")
	e := parseAt(el, src)
	if f != vElem || e == nil || !endsInAttribute(e) || !(viewCheck{noIdent: b.noIdent}).safe(e, f, true) {
		return b.refuse("motionless xsl:apply-templates select " + src)
	}
	return b.applyAttrs(el, f) && b.withParams(el, f) && b.addApply(el, f, applyAttrs)
}

// nextOp plans an xsl:next-match or xsl:apply-imports.
func (b *streamBuilder) nextOp(el *xdm.Node, f vfocus) bool {
	if f == vLeaf {
		return b.refuse(elName(el) + " on a node with no content")
	}
	return b.applyAttrs(el, f) && b.withParams(el, f) && b.addApply(el, f, applyNext)
}

// applyAttrs checks the attributes of an apply op.
func (b *streamBuilder) applyAttrs(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		switch n := a.Name(); {
		case n.URI != "", n.Local == "select", n.Local == "mode":
		default:
			if !streamAttrsOK[n.Local] {
				return b.refuse("attribute " + n.Local + " of " + elName(el))
			}
		}
	}
	return true
}

// withParams checks the xsl:with-param children of an apply op: values made
// on the focus, motionless (a node of it would be handed on as a node).
func (b *streamBuilder) withParams(el *xdm.Node, f vfocus) bool {
	sb := b.strict()
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		case isXSL(ch, "with-param"):
			if !sb.attrs(ch, f) || !sb.content(ch, f) {
				return b.refuse(sb.why)
			}
		default:
			return b.refuse(elName(ch) + " in " + elName(el))
		}
	}
	return true
}

func (b *streamBuilder) addApply(el *xdm.Node, f vfocus, kind applyKind) bool {
	// In a variable's sequence (@as) the built-in rules' nodes are kept as
	// they are, and the tree keeps the source's own: see executeStream.
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if n.Name().URI != xdm.NSXSL || isXSL(n, "element") || isXSL(n, "copy") ||
			isXSL(n, "template") || isXSL(n, "source-document") {
			break
		}
		if (isXSL(n, "variable") || isXSL(n, "param") || isXSL(n, "with-param")) && n.Attr("", "as") != nil {
			return b.refuse(elName(el) + " in a sequence-typed " + elName(n))
		}
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, apply: kind, inGroup: b.grp, key: b.key}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	b.kinds[streamtrace.KindApply]++
	return true
}

// copyDoc plans an xsl:copy of the document node, which takes the
// document's DOCTYPE and base URI once its body has run: from the stand-in,
// which has them once a dispatch has met the first view (element). So its one
// consumer must be an apply op reading the document node's children, which
// dispatches the document element first.
func (b *streamBuilder) copyDoc(el *xdm.Node) bool {
	n := len(*b.ops)
	if !b.attrs(el, vNone) || !b.seq(el, vNone) {
		return false
	}
	if ops := (*b.ops)[n:]; len(ops) != 1 || ops[0].apply != applyKids || ops[0].el.Parent() != el {
		return b.refuse("xsl:copy of the document node")
	}
	b.kinds[streamtrace.KindElement]++
	return true
}

// childNodes reports whether e is node() or child::node(): the children.
func childNodes(e xpath.Expr) bool {
	e = oneStep(e)
	st, ok := e.(*xpath.Step)
	if !ok || st.Axis != xpath.AxisChild || len(st.Predicates) > 0 {
		return false
	}
	kt, ok := st.Test.(*xpath.KindTest)
	return ok && kt.Any
}

// noteStreamApply records the compiled form of n, an apply op of the plans
// being compiled; sel is its compiled select (nil when it has none).
func (c *compiler) noteStreamApply(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	for _, op := range c.streamOps(n) {
		if op.apply == applyNone {
			continue
		}
		op.instr, op.sel = instr, sel
		if op.apply == applySplit {
			if op.split, op.dead = c.splitFor(sel, ns, op.doc, false, op.inGroup, op.key); op.dead != "" {
				continue
			}
			if !op.split.bare() {
				// A window over a bare path (streampop.go): head(), subsequence() ...
				pop, s, _ := c.popFor(op, ns)
				if pop == nil || !pop.windowsOnly() {
					op.dead = "apply-templates select is not a bare path"
					continue
				}
				op.pop, op.split = pop, s
			}
		}
		op.x.byInstr[instr] = op
	}
}

// readyApply readies an apply op once it has compiled.
func (op *streamOp) readyApply() string {
	if s := op.split; op.apply == applySplit {
		if s.nests && (!s.outermost || s.predFail) {
			// A rule may read a match whole, and with it the matches inside.
			return "apply-templates select: matches may lie inside each other"
		}
		if why := leafApplyRefusal(s); why != "" {
			return why
		}
		s.mode = matchBody
	}
	return ""
}

// enclosingRuleModes returns the modes of the template rule el is in, which
// #current and xsl:next-match name there; nil when none, or it is in #all.
func enclosingRuleModes(el *xdm.Node) []string {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if isXSL(n, "template") {
			return templateModeNames(n)
		}
	}
	return nil
}

// --- Rule plans -------------------------------------------------------------

// rulePlan is how a template rule's body runs when an apply op hands it a
// node of each class (see nodeClass).
type rulePlan struct {
	// can says the pattern may match a node of the class; pattern, when not
	// empty, that a view cannot decide it.
	can     [numClasses]bool
	pattern string
	// x is the body's plan for an element or the document node; why says
	// why a class the pattern may match has none (or, for a leaf or an
	// attribute, why the body reads more than it has).
	x   [numClasses]*streamExec
	why [numClasses]string
	// keeps is resultKeeps of the body (streammain.go).
	keeps string
}

// rulesPlan is the record of the trusting walk over the rules of every
// streamable mode of one module, which their bodies are planned from.
type rulesPlan struct {
	pl    *streamPlan
	trust *declTrust
	modes map[string]bool // the streamable ones
}

// rulesPlanFor returns the walk of the module of el, made on first use; nil
// when the module declares no streamable mode, or is not the whole
// stylesheet (streamtrust.go).
func (c *compiler) rulesPlanFor(el *xdm.Node) *rulesPlan {
	root := documentElementOf(el)
	if rp, ok := c.rulePlans[root]; ok {
		return rp
	}
	var rp *rulesPlan
	if modes := streamableModeNames(root); len(modes) > 0 && el.Root() == c.sheet.source {
		t := newDeclTrust(root, false)
		if !t.shared {
			t.plan = &streamPlan{recs: map[any]*planRec{}, sites: map[planSite][]planRoot{}}
			names := make([]string, 0, len(modes))
			for m := range modes {
				names = append(names, m)
			}
			slices.Sort(names)
			for _, m := range names {
				t.modeVerified(root, m)
			}
			rp = &rulesPlan{pl: t.plan, trust: t, modes: modes}
		}
	}
	if c.rulePlans == nil {
		c.rulePlans = map[*xdm.Node]*rulesPlan{}
	}
	c.rulePlans[root] = rp
	return rp
}

// verified reports whether the walk verified mode m.
func (rp *rulesPlan) verified(m string) bool {
	return rp.modes[m] && rp.trust.state[modeKey{m}] == trustVerified
}

// planRule plans the body of the template rule el, whose pattern is p and
// whose sequence constructor starts at its element child from, before the
// body compiles: the plans go into c.ruleExecs, so that the body's
// instructions are noted into them as they compile. readyRule finishes it.
func (c *compiler) planRule(el *xdm.Node, p *Pattern, from int) *rulePlan {
	rs := c.rulesPlanFor(el)
	if rs == nil {
		return nil
	}
	r := &rulePlan{}
	r.can, r.pattern = patternClasses(p)
	r.keeps = resultKeeps(el)
	kids := el.ChildElements()
	for cl := range numClasses {
		if !r.can[cl] {
			continue
		}
		x := newStreamExec()
		x.class, x.modes, x.rule = cl, templateModeNames(el), true
		b := &streamBuilder{pl: rs.pl, x: x, ops: &x.top, kinds: &x.kinds, noIdent: cl == classLeaf, up: cl == classLeaf}
		f := vElem
		switch cl {
		case classDoc:
			f = vNone
		case classLeaf, classAttr:
			f = vLeaf
			// Off the cursor: every expression is checked, none consumes.
			b = b.strict()
		}
		if !b.ruleBody(el, kids, from, f) {
			r.why[cl] = b.why
			continue
		}
		if cl == classElem || cl == classDoc {
			r.x[cl] = x
			c.ruleExecs = append(c.ruleExecs, x)
		}
	}
	return r
}

// ruleBody plans or checks the body of the rule el at focus f: its
// parameters' defaults motionless values, its sequence constructor the
// members a loop body may have.
func (b *streamBuilder) ruleBody(el *xdm.Node, kids []*xdm.Node, from int, f vfocus) bool {
	for ch := range el.Children() {
		if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
			return b.refuse("text with a brace")
		}
	}
	sb := b.strict()
	for _, p := range kids[:from] {
		if isXSL(p, "param") && (!sb.attrs(p, f) || !sb.content(p, f)) {
			return b.refuse(sb.why)
		}
	}
	if f == vLeaf {
		for _, ch := range kids[from:] {
			if !b.motionless(ch, f) {
				return false
			}
		}
		return true
	}
	return b.members(el, kids[from:], f)
}

// readyRule finishes the plans of r once the rule's body has compiled.
func (c *compiler) readyRule(r *rulePlan) {
	for cl, x := range r.x {
		if x == nil {
			continue
		}
		if why := x.ready(); why != "" {
			r.x[cl], r.why[cl] = nil, why
		}
	}
	c.ruleExecs = c.ruleExecs[:0]
}

// patternClasses reports which classes of node p may match, and why a
// view cannot decide it ("" when it can): it must name its candidate and
// the candidate's ancestors by what their views have -- names, kinds,
// attributes -- with no position among siblings; a text, comment or PI
// node is matched live, its ancestors open, and so may be named through
// them as well.
func patternClasses(p *Pattern) (can [numClasses]bool, why string) {
	if matchesNoNode(p) { // streampattern_g5.go
		return can, ""
	}
	if dotPattern(p) {
		return [numClasses]bool{true, true, true, true}, "" // streamgroup.go
	}
	if len(p.general) > 0 {
		return can, "pattern " + p.src + ": an XSLT 3.0 form"
	}
	for _, a := range p.alts {
		if a.call != nil {
			return can, "pattern " + p.src + ": id() or key()"
		}
		if len(a.steps) == 0 {
			return can, "pattern " + p.src
		}
		var c [numClasses]bool
		last := a.steps[len(a.steps)-1]
		switch t := last.nodeTest.(type) {
		case *xpath.NameTest:
			if last.namespace {
				return can, "pattern " + p.src + ": namespace nodes"
			}
			c[classElem] = !last.attribute
			c[classAttr] = last.attribute
		case *xpath.KindTest:
			// A type or a schema declaration is matched on the node's own
			// annotation, which a validated stream has written by the time
			// the node is matched (streamvalidin.go); an untyped one has none.
			if t.Content != nil || last.namespace {
				return can, "pattern " + p.src + ": kind test " + t.String()
			}
			switch {
			case last.attribute:
				c[classAttr] = t.Any || t.Kind == xdm.KindAttribute
			case t.Any:
				c[classElem], c[classLeaf] = true, true
				c[classDoc] = last.explicitAxis || last.self
			case t.Kind == xdm.KindElement:
				c[classElem] = true
			case t.Kind == xdm.KindDocument:
				c[classDoc] = true
			case t.Kind == xdm.KindText || t.Kind == xdm.KindComment || t.Kind == xdm.KindPI:
				c[classLeaf] = true
			default:
				return can, "pattern " + p.src + ": kind test " + t.String()
			}
		default:
			return can, "pattern " + p.src
		}
		// A leaf is matched live on the cursor, its ancestors open, and its
		// rule runs on its copy (Cursor.Leaf), which has their copies.
		if c[classDoc] && (len(a.steps) > 1 || last.descendant) {
			return can, "pattern " + p.src + ": a step above a node with no view of its parent"
		}
		for i, st := range a.steps {
			for _, pr := range st.preds {
				if needsPosition(pr) {
					return can, "pattern " + p.src + ": positional predicate"
				}
				ok := true
				if i < len(a.steps)-1 || c[classElem] {
					ok = (viewCheck{}).safe(pr, vElem, true)
				}
				if i == len(a.steps)-1 && (c[classLeaf] || c[classAttr]) {
					ok = ok && (viewCheck{noIdent: c[classLeaf], up: c[classLeaf]}).safe(pr, vLeaf, true)
				}
				if !ok || i == len(a.steps)-1 && c[classDoc] {
					return can, "pattern " + p.src + ": predicate " + pr.String() + " not motionless on the view"
				}
			}
		}
		for i := range c {
			can[i] = can[i] || c[i]
		}
	}
	return can, ""
}

// --- Linking ----------------------------------------------------------------

// linkStreams links the containers whose plans apply templates, once every
// rule has compiled: one that cannot be linked runs on the tree.
func (c *compiler) linkStreams() {
	for _, sd := range c.streamLinks {
		why := c.sheet.linkApply(sd.instr.stream, c.rulesPlanFor(sd.el))
		if why != "" {
			sd.instr.stream = nil
		}
		if traceCore != nil {
			traceCore(sd.el, why)
		}
	}
	c.streamLinks = nil
}

// streamLink is a container waiting for linkStreams.
type streamLink struct {
	el    *xdm.Node
	instr *sourceDocumentInstr
}

// hasApply reports whether the plan, or a loop in it, has an apply op.
func (x *streamExec) hasApply() bool {
	found := false
	x.eachApply(func(*streamOp) { found = true })
	return found
}

// eachApply calls fn with every apply op of the plan, in loop bodies too.
func (x *streamExec) eachApply(fn func(*streamOp)) {
	var walk func(ops []*streamOp)
	walk = func(ops []*streamOp) {
		for _, op := range ops {
			if op.apply != applyNone {
				fn(op)
			}
			walk(op.inner)
		}
	}
	walk(x.top)
}

// modeClass is a mode handed a class of node; top says at the top of the
// principal result, or of an xsl:result-document's body (linkMain).
type modeClass struct {
	mode  string
	class nodeClass
	top   topKind
}

// topKind is where what a rule returns goes as it is, for linkMain.
type topKind uint8

const (
	notTop    topKind = iota
	mainTop           // the principal result (Result.Nodes)
	resultTop         // a secondary result (SecondaryResult.Nodes)
)

// linkApply checks what the apply ops of x reach, transitively: each mode
// streamable and verified by rs's walk, its built-in rule one the stream
// can run for the class of node it is handed, and each of its rules that
// may match such a node planned for it. It returns why not.
func (s *Stylesheet) linkApply(x *streamExec, rs *rulesPlan) string {
	return s.link(rs, x, modeClass{})
}

// linkMain is linkApply for a principal input streamed in mode m
// (streammain.go): what dispatching the document node in m reaches, with,
// besides, nothing that would put a node of the stream at the top of the
// principal result, where the tree's result holds the source's own node
// (Result.Nodes), and nothing at the document node that would fall back to
// a tree there is none of.
func (s *Stylesheet) linkMain(m string, rs *rulesPlan) string {
	return s.link(rs, nil, modeClass{m, classDoc, mainTop})
}

// link is linkApply from the apply ops of x, or, x nil, linkMain from main.
func (s *Stylesheet) link(rs *rulesPlan, x *streamExec, main modeClass) string {
	if rs == nil {
		return "apply-templates: no streamable mode"
	}
	seen := map[modeClass]bool{}
	var work []modeClass
	add := func(m string, cl nodeClass, top topKind) {
		if k := (modeClass{m, cl, top}); !seen[k] {
			seen[k] = true
			work = append(work, k)
		}
	}
	type visitKey struct {
		x   *streamExec
		top topKind
	}
	visited := map[visitKey]bool{}
	var why string
	streamed := x == nil // a principal input: no tree to fall back to
	visit := func(x *streamExec, top topKind) {
		if visited[visitKey{x, top}] {
			return
		}
		visited[visitKey{x, top}] = true
		if streamed && x.class == classDoc && x.mayGiveUp() && why == "" {
			why = "a select at the document node may fall back to its tree"
		}
		x.eachApply(func(op *streamOp) {
			var modes []string
			switch i := op.instr.(type) {
			case *applyTemplatesInstr:
				switch i.mode {
				case "#current":
					modes = x.modes
				case "#default":
					modes = []string{""}
				default:
					modes = []string{i.mode}
				}
			case *nextMatchInstr:
				modes = x.modes
			}
			if len(modes) == 0 && why == "" {
				why = elName(op.el) + ": no template rule's mode to name"
			}
			// A secondary result holds its body's items as Result.Nodes
			// does (SecondaryResult.Nodes).
			t := notTop
			switch {
			case streamed && atResultTop(op.el):
				t = resultTop
			case atTop(op.el):
				t = top
			}
			for _, m := range modes {
				switch op.apply {
				case applyKids:
					add(m, classElem, t)
					add(m, classLeaf, t)
				case applySelf:
					add(m, x.class, t)
				case applySplit:
					add(m, classElem, t)
				case applyAttrs:
					add(m, classAttr, t)
				case applyNext:
					add(m, x.class, t)
				}
			}
		})
	}
	if x != nil {
		visit(x, notTop)
	} else {
		add(main.mode, main.class, main.top)
	}
	for len(work) > 0 && why == "" {
		k := work[0]
		work = work[1:]
		label := func() string { return fmt.Sprintf("mode %q, %s", k.mode, classNames[k.class]) }
		if !rs.verified(k.mode) {
			return label() + ": not a verified streamable mode"
		}
		nm := s.modeNoMatch[k.mode]
		copies := nm == "shallow-copy" || nm == "copy" || nm == "deep-copy"
		// A built-in's copy of a leaf in a secondary result is a new node,
		// as a container's is there (keptStreamNode): its bytes are the
		// tree's; SecondaryResult.Nodes holds the copy, not the source's.
		if k.top != notTop && (copies && k.class != classDoc && (k.class != classElem || nm == "deep-copy")) &&
			(k.top == mainTop || k.class != classLeaf) {
			return label() + ": the built-in " + nm + " would put a node of the stream at the top of the result"
		}
		if k.class == classElem || k.class == classDoc {
			// Inside the copy a shallow-copy makes, its descent is not at
			// the top of the result.
			in := k.top
			if nm == "shallow-copy" || nm == "copy" {
				in = notTop
			}
			switch nm {
			case "", "text-only-copy", "deep-skip":
			case "shallow-copy", "copy", "shallow-skip":
				if k.class == classElem {
					add(k.mode, classAttr, in)
				}
			case "deep-copy":
				if k.class == classDoc {
					return label() + ": the built-in deep-copy would ground the document"
				}
			case "fail":
			default:
				return label() + ": on-no-match " + nm
			}
			if nm != "deep-copy" && nm != "fail" && (nm != "deep-skip" || k.class == classDoc) {
				add(k.mode, classElem, in)
				add(k.mode, classLeaf, in)
			}
		}
		for _, t := range s.templates {
			if t.Match == nil || !t.matchesMode(k.mode) {
				continue
			}
			r := t.stream
			switch {
			case r == nil:
				return label() + ": rule " + t.Match.String() + " not planned"
			case r.pattern != "":
				return label() + ": " + r.pattern
			case !r.can[k.class]:
				continue
			case r.why[k.class] != "":
				return label() + ": rule " + t.Match.String() + ": " + r.why[k.class]
			case streamed && r.keeps != "":
				return label() + ": rule " + t.Match.String() + ": " + r.keeps
			}
			if x := r.x[k.class]; x != nil {
				visit(x, k.top)
			}
		}
	}
	return why
}

// --- Running ----------------------------------------------------------------

// executeStream runs i, an apply op of the plan running, on the cursor.
func (i *applyTemplatesInstr) executeStream(rt *runtime, out *outputBuilder, run *streamRun, op *streamOp) error {
	gate, done := func() (bool, error) { return true, nil }, func() error { return nil }
	if op.pop != nil {
		// The windows' arguments are the select's: before the parameters.
		var err error
		if gate, done, err = op.pop.gates(rt.ctx); err != nil {
			return err
		}
	}
	var attrs xdm.Sequence
	if op.apply == applyAttrs {
		// Attributes of the view, which has them all: evaluated as the tree
		// does, before the parameters.
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		attrs = seq
	}
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	disp := rt
	if i.streamed {
		disp = rt.withoutGroupingScope()
	}
	mode := i.effectiveMode(rt)
	// The size of the selection is not known as it is read; last() is not
	// admitted in a plan.
	focus := focuser{disp, mode, false}
	switch op.apply {
	case applyAttrs:
		for idx, it := range attrs {
			if err := rt.ctx.Err(); err != nil {
				return err
			}
			a, ok := it.(*xdm.Node)
			if !ok || a.Kind() != xdm.KindAttribute {
				return errStreamPlan // attrApply admits attribute paths only
			}
			if err := run.dispatch(disp.withCurrent(a, idx+1, len(attrs)), a, mode, params, tunnels, out); err != nil {
				return err
			}
		}
		return nil
	case applySelf:
		if run.used {
			return errStreamPlan
		}
		node, err := run.context()
		if err != nil {
			return err
		}
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		return run.dispatch(disp.withCurrent(node, 1, 1), node, mode, params, tunnels, out)
	case applyKids:
		return run.children(rt, out, mode, params, tunnels, focus)
	}
	if run.used {
		return errStreamPlan
	}
	run.used = true
	pos := 0
	if err := op.split.loopAt(rt.ctx, run.c, run.d, run.groupAt(op), nil, func(v *xdm.Node, md int) (bool, error) {
		if err := rt.ctx.Err(); err != nil {
			return false, err
		}
		if ok, err := gate(); err != nil || !ok {
			return false, err
		}
		pos++
		return run.element(focus.at(v, pos), v, md, mode, params, tunnels, out)
	}); err != nil {
		return err
	}
	return done()
}

// context is the node the context of the instruction running is: the view
// of the element at depth run.d, at its start tag, or the document node's
// stand-in.
func (run *streamRun) context() (*xdm.Node, error) {
	if run.d == 0 {
		return run.root, nil
	}
	return run.c.View()
}

// offCursor runs fn, which reads nothing of the stream, with no
// instruction intercepted.
func (run *streamRun) offCursor(fn func() error) error {
	off := run.off
	run.off = true
	err := fn()
	run.off = off
	return err
}

// children dispatches mode to each child of the context, at depth run.d:
// an element at its start tag, a text, comment or PI node as a copy. focus
// makes a child's runtime; rt, when set, is the instruction's (its focus
// checked for cancellation per child, as xsl:apply-templates does). It reads
// to the context's end.
func (run *streamRun) children(rt *runtime, out *outputBuilder, mode string, params, tunnels paramValues,
	focus focuser) error {

	if run.used {
		return errStreamPlan
	}
	run.used = true
	d, c := run.d, run.c
	f := run.kidFilter(d)
	pos, skip := 0, false
	for {
		var (
			ev  xdmstream.Event
			err error
		)
		if skip {
			ev, err = c.SkipTag(f)
		} else {
			ev, err = c.NextTag(f)
		}
		skip = false
		switch {
		case err != nil:
			return err
		case ev.Kind == xdmstream.EndDocument || ev.Kind == xdmstream.EndElement && ev.Depth == d:
			return nil
		case ev.Depth != d+1 || ev.Closed != 0 && ev.Closed <= d+1:
			// Every child is reported and read or skipped to its end: the
			// filter passes over nothing at this depth.
			return errStreamPlan
		}
		if rt != nil {
			if err := rt.ctx.Err(); err != nil {
				return err
			}
		}
		pos++
		if ev.Kind != xdmstream.StartElement {
			if err := run.leaf(focus, pos, ev.Node, mode, params, tunnels, out); err != nil {
				return err
			}
			continue
		}
		if run.top != nil {
			read, ok, err := run.liveBuiltIn(focus, pos, ev.Node, d+1, mode, params, tunnels, out)
			if err != nil {
				return err
			}
			if ok {
				skip = !read
				continue
			}
		}
		v, err := c.View()
		if err != nil {
			return err
		}
		read, err := run.element(focus.at(v, pos), v, d+1, mode, params, tunnels, out)
		if err != nil {
			return err
		}
		skip = !read
	}
}

// element dispatches mode to the element at depth md whose view is v, the
// cursor at its start tag, and reports whether what ran read it to its end.
func (run *streamRun) element(rt *runtime, v *xdm.Node, md int, mode string, params, tunnels paramValues,
	out *outputBuilder) (bool, error) {

	if run.top == nil {
		// The document node of the views: the stand-in takes its properties,
		// which xsl:copy and the shallow-copy rule read of it.
		top := v
		for top.Parent() != nil {
			top = top.Parent()
		}
		run.top = top
		run.root.Tree().DocType = top.Tree().DocType
		run.root.SetBaseURI(top.BaseURI())
		run.root.SetDocumentURI(top.DocumentURI())
	}
	// The rule's focus is v, not a group's first item: nothing in a rule
	// reads a group (its ops are not inGroup), and an accumulator-after of v
	// may read v to its end (streamAccums.consume) as anywhere else, which
	// it refuses while a group is the context (fuzz seed 92048).
	d, grp := run.d, run.grp
	run.d, run.used, run.grp = md, false, nil
	err := run.dispatch(rt, v, mode, params, tunnels, out)
	read := run.used
	run.d, run.used, run.grp = d, true, grp
	return read, err
}

// leaf dispatches a text, comment or PI node n, live on the cursor until
// its next call, at position pos: matched as it is (a pattern that may match
// one reads the node and the names and attributes of its open ancestors:
// patternClasses), and copied (Cursor.Leaf: the original to identity, under
// its ancestors' copies) for a rule's body to run on, which reads only what
// a copy has (rulePlan). A built-in rule
// copies what it keeps (detached).
func (run *streamRun) leaf(focus focuser, pos int, n *xdm.Node, mode string,
	params, tunnels paramValues, out *outputBuilder) error {

	s := focus.disp.sheet
	if err := s.checkModeTyped(n, mode); err != nil {
		return err
	}
	// A pattern focuses on its candidate and binds current() itself, so it
	// is matched in disp's context: the node is given one runtime, its
	// rule's (on its copy) or the built-in's.
	t, next, err := s.findTemplateFrom(n, mode, focus.disp.ctx, 0)
	if err != nil {
		return err
	}
	if t == nil {
		rt := focus.at(n, pos)
		rt.sheet.warnNoMatch(rt, n, mode)
		return run.builtIn(rt, n, mode, params, tunnels, out)
	}
	c, err := run.c.Leaf()
	if err != nil {
		return err
	}
	rt := focus.at(c, pos)
	if err := rt.sheet.warnMultipleMatch(rt, c, mode, t, next, rt.ctx); err != nil {
		return err
	}
	if err := rt.sheet.failMultipleMatch(c, mode, t, next, rt.ctx); err != nil {
		return err
	}
	sub := rt.withSelection(t, next, mode, params, tunnels)
	sub.ctx = withCurrentItem(sub.ctx, c)
	return run.offCursor(func() error { return runTemplate(sub, t, params, tunnels, out) })
}

// focuser makes the runtime a child is dispatched in, at position pos:
// with disp's focus moved to it, or, for a built-in rule, as builtInFocus
// does. The size is not known as it is read; last() is not admitted.
type focuser struct {
	disp    *runtime
	mode    string
	builtIn bool
}

func (f focuser) at(n *xdm.Node, pos int) *runtime {
	if f.builtIn {
		return f.disp.builtInFocus(f.mode, n, pos, 0)
	}
	return f.disp.withCurrent(n, pos, 0)
}

// onCursor reports whether node is read off the cursor (an element's view,
// or the document node), not complete (a copy of a leaf, an attribute).
func onCursor(node *xdm.Node) bool {
	k := node.Kind()
	return k == xdm.KindElement || k == xdm.KindDocument
}

// dispatch is applyToNode for node, as an apply op hands it over: the
// context on the cursor (an element's view, the document node's stand-in),
// or off it (a copy of a text, comment or PI node, an attribute of a view).
// The selection is the tree's; the rule's body, or the built-in rule, runs
// on the stream.
func (run *streamRun) dispatch(rt *runtime, node *xdm.Node, mode string, params, tunnels paramValues,
	out *outputBuilder) error {

	if err := rt.sheet.checkModeTyped(node, mode); err != nil {
		return err
	}
	t, next, err := rt.sheet.findTemplateFrom(node, mode, rt.ctx, 0)
	if err != nil {
		return err
	}
	if t == nil {
		rt.sheet.warnNoMatch(rt, node, mode)
		return run.builtIn(rt, node, mode, params, tunnels, out)
	}
	if err := rt.sheet.warnMultipleMatch(rt, node, mode, t, next, rt.ctx); err != nil {
		return err
	}
	if err := rt.sheet.failMultipleMatch(node, mode, t, next, rt.ctx); err != nil {
		return err
	}
	sub := rt.withSelection(t, next, mode, params, tunnels)
	sub.ctx = withCurrentItem(sub.ctx, node)
	if !onCursor(node) {
		// Its body was checked to read only what the node has.
		return run.offCursor(func() error { return runTemplate(sub, t, params, tunnels, out) })
	}
	return run.rule(sub, t, node, params, tunnels, out)
}

// rule is runTemplate for node on the cursor: the body runs on its plan for
// node's class.
func (run *streamRun) rule(rt *runtime, t *Template, node *xdm.Node, params, tunnels paramValues,
	out *outputBuilder) error {

	cl := classElem
	if node.Kind() == xdm.KindDocument {
		cl = classDoc
	}
	var x *streamExec
	if t.stream != nil {
		x = t.stream.x[cl]
	}
	if x == nil {
		return errStreamPlan // linkApply has seen to it that there is one
	}
	defer rt.leaveFrame(rt.enterFrame())
	prev := run.x
	run.x = x
	err := runTemplateBody(rt, t, params, tunnels, out)
	run.x = prev
	return err
}

// keep appends n, a node of the stream, as the built-in rules append the
// source's. At the top of a sequence a builder keeps the node itself: a leaf
// live on the cursor is copied first (its record is reused once the cursor
// moves), and at the top of the container's own result the tree's node is
// the source's, so the result is the tree's to make (kept, executeStream).
func (run *streamRun) keep(out *outputBuilder, n *xdm.Node) {
	if out.Open() == nil {
		if out == run.held {
			run.kept = true
		}
		if k := n.Kind(); k != xdm.KindElement && k != xdm.KindAttribute && k != xdm.KindDocument {
			n = xdm.Copy(n)
		}
	}
	out.AppendNode(n)
}

// builtIn is applyBuiltInRule for node, as dispatch has it.
func (run *streamRun) builtIn(rt *runtime, node *xdm.Node, mode string, params, tunnels paramValues,
	out *outputBuilder) error {

	nm := rt.sheet.modeNoMatch[mode]
	if !onCursor(node) {
		switch nm {
		case "deep-skip", "shallow-skip":
		case "fail":
			return builtInFail(node, mode)
		case "deep-copy", "shallow-copy", "copy":
			run.keep(out, node)
		default:
			if k := node.Kind(); k == xdm.KindText || k == xdm.KindAttribute {
				out.AppendText(node.StringValue())
			}
		}
		return nil
	}
	doc := node.Kind() == xdm.KindDocument
	b := builtInRun{run, rt, node, mode, params, tunnels}
	switch nm {
	case "deep-skip":
		if !doc {
			return nil // the caller skips it
		}
		return b.descend(out)
	case "shallow-skip":
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		if !doc {
			if err := b.attrs(out, false); err != nil {
				return err
			}
		}
		return b.kids(out)
	case "fail":
		return builtInFail(node, mode)
	case "deep-copy":
		if doc || run.used {
			return errStreamPlan // linkApply refuses the document node
		}
		run.used = true
		g, err := run.c.Ground()
		if err != nil {
			return err
		}
		run.keep(out, g)
		return nil
	case "shallow-copy", "copy":
		if doc {
			return b.shallowCopyDoc(out)
		}
		sub := out.StartElement(node.Name())
		if out.Open() == nil && sub.Open().BaseURI() == "" {
			sub.Open().SetBaseURI(node.BaseURI())
		}
		copyNamespacesTo(rt, sub, node)
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		// builtInShallowCopy gives each attribute position 1.
		if err := b.attrs(sub, true); err != nil {
			return err
		}
		return b.kids(sub)
	}
	return b.descend(out)
}

// builtInRun is a built-in rule running for node on the cursor.
type builtInRun struct {
	run            *streamRun
	rt             *runtime
	node           *xdm.Node
	mode           string
	params, tunnel paramValues
}

// kids applies the mode to the node's children.
func (b builtInRun) kids(out *outputBuilder) error {
	return b.run.children(nil, out, b.mode, b.params, b.tunnel, focuser{b.rt, b.mode, true})
}

// descend is builtInDescend's: rt.descend around the children.
func (b builtInRun) descend(out *outputBuilder) error {
	if err := b.rt.descend(); err != nil {
		return err
	}
	defer b.rt.ascend()
	return b.kids(out)
}

// attrs applies the mode to the node's attributes, each at position 1
// (one), as builtInShallowCopy has them, or at its own.
func (b builtInRun) attrs(out *outputBuilder, one bool) error {
	n := b.node.NumAttrs()
	for idx := range n {
		a, pos := b.node.AttrAt(idx), idx+1
		if one {
			pos = 1
		}
		if err := b.run.dispatch(b.rt.builtInFocus(b.mode, a, pos, n), a, b.mode, b.params, b.tunnel, out); err != nil {
			return err
		}
	}
	return nil
}

// builtInFail is the on-no-match="fail" rule's error.
func builtInFail(node *xdm.Node, mode string) error {
	return fmt.Errorf(
		"XTDE0555: no template rule matches %s in mode %q, and the mode "+
			"declares on-no-match=\"fail\"", builtInNodeLabel(node), mode)
}

// shallowCopyDoc is builtInShallowCopy for the document node: its copy built
// aside, then appended; the document's DOCTYPE and base URI come from the
// views (element), which the descent has met by then.
func (b builtInRun) shallowCopyDoc(out *outputBuilder) error {
	run, rt, node, mode, descend := b.run, b.rt, b.node, b.mode, b.descend
	var doc *xdm.Node
	if mr := rt.sheet.rules[mode]; mr != nil && mr.empty {
		sub, root := inPlaceDocument(rt, out)
		if err := descend(sub); err != nil {
			return err
		}
		if err := sub.Refused(); err != nil {
			return err
		}
		root.Tree().Finalize()
		doc = root
	} else {
		sub := documentBuilder(rt, out)
		if err := descend(sub); err != nil {
			return err
		}
		var err error
		if doc, err = sub.ToDocument(); err != nil {
			return err
		}
	}
	if run.top == nil {
		return errStreamPlan // no element was read: nothing has been written
	}
	if src := node.Tree(); src != nil {
		if dst := doc.Tree(); dst != nil {
			dst.DocType = src.DocType
		}
	}
	if doc.BaseURI() == "" {
		doc.SetBaseURI(node.BaseURI())
	}
	out.AppendNode(doc)
	return nil
}
