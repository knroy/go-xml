package relaxng

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Section 7 of the spec: the restrictions.
//
// A schema can be well-formed against the XML syntax and still be nonsense —
// an attribute inside an attribute, a list inside a list, a <start> that
// matches text. The structural rules in syntax.go cannot catch these, because
// legality depends on what *encloses* a pattern, sometimes several levels up.
//
// The spec states these as "prohibited paths": §7.1.2 forbids the path
// oneOrMore//group//attribute, meaning an attribute with a group ancestor
// which itself has a oneOrMore ancestor. That is a precise formulation and
// this pass implements it directly, by matching path patterns against the
// stack of enclosing elements, rather than re-deriving the intent.
//
// Two subtleties decide whether the checks are right:
//
// The paths apply to the *simplified* grammar (§7 opens by saying so), and
// simplification has already propagated notAllowed outward (§4.20). So
// <group><notAllowed/><attribute/></group> is simply notAllowed by the time
// §7 looks, and the attribute it appears to contain is not there at all.
// prune below performs that erasure.
//
// A step written // matches any number of intervening elements, but only
// within one element's content: <element> starts a fresh subtree, so an
// attribute inside a nested element is not inside the outer oneOrMore for
// these purposes. barrier below marks where a path stops climbing.

// path is one prohibited path, outermost step first.
type path []string

// prohibited is §7.1, transcribed.
var prohibited = []struct {
	steps path
	why   string
}{
	{path{"attribute", "attribute"}, "7.1.1"},

	{path{"oneOrMore", "group", "attribute"}, "7.1.2"},
	{path{"oneOrMore", "interleave", "attribute"}, "7.1.2"},

	{path{"list", "list"}, "7.1.3"},
	{path{"list", "attribute"}, "7.1.3"},
	{path{"list", "text"}, "7.1.3"},
	{path{"list", "interleave"}, "7.1.3"},

	{path{"data", "except", "attribute"}, "7.1.4"},
	{path{"data", "except", "text"}, "7.1.4"},
	{path{"data", "except", "list"}, "7.1.4"},
	{path{"data", "except", "group"}, "7.1.4"},
	{path{"data", "except", "interleave"}, "7.1.4"},
	{path{"data", "except", "oneOrMore"}, "7.1.4"},
	{path{"data", "except", "empty"}, "7.1.4"},

	{path{"start", "attribute"}, "7.1.5"},
	{path{"start", "data"}, "7.1.5"},
	{path{"start", "value"}, "7.1.5"},
	{path{"start", "text"}, "7.1.5"},
	{path{"start", "list"}, "7.1.5"},
	{path{"start", "group"}, "7.1.5"},
	{path{"start", "interleave"}, "7.1.5"},
	{path{"start", "oneOrMore"}, "7.1.5"},
	{path{"start", "empty"}, "7.1.5"},
}

// data/except is the one path whose first two steps must be parent and child
// rather than ancestor and descendant: "data/except//attribute". Recording it
// keeps the matcher from accepting an <except> that belongs to a name class
// several levels below a <data>.
func directStep(steps path, i int) bool {
	return len(steps) == 3 && steps[0] == "data" && i == 1
}

// checkRestrictions applies §7 to a schema document.
func checkRestrictions(root *xdm.Node) error {
	r := &restrictor{defines: map[string]*xdm.Node{}}
	r.collect(root)
	return r.walk(root, nil)
}

type restrictor struct {
	defines map[string]*xdm.Node
	// active guards a definition that reaches itself while being expanded.
	active map[string]bool
}

func (r *restrictor) collect(n *xdm.Node) {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "define":
			name := normalizeToken(kid.AttrValue("name"))
			if _, dup := r.defines[name]; !dup {
				r.defines[name] = kid
			}
		case "div", "grammar", "include", "start":
			r.collect(kid)
		}
	}
}

// walk visits n with stack holding the enclosing RELAX NG element names,
// outermost first.
func (r *restrictor) walk(n *xdm.Node, stack []string) error {
	if n.Name.URI != NS {
		return nil // a foreign element is an annotation
	}
	local := n.Name.Local

	// A pattern that simplification erases cannot violate anything.
	if prune(n) {
		return nil
	}

	if local == "attribute" {
		if err := checkInfiniteAttributeName(n, stack); err != nil {
			return err
		}
	}

	if !transparent(n, local) {
		if err := matchProhibited(append(append([]string{}, stack...), local)); err != nil {
			return err
		}
	}

	// The stack the children see. <element> is a barrier: its content is a
	// fresh context, so an attribute inside it is not inside any enclosing
	// oneOrMore or list. <define> and <grammar> likewise start fresh, since a
	// definition is checked in the context of each <ref> that reaches it.
	var kids []string
	switch local {
	case "element", "grammar", "define", "div":
		kids = nil
	case "start":
		// §7.1.5 constrains the schema's start, which is the start of the
		// outermost grammar. A <grammar> written inside an <element> is
		// inlined there by simplification, so its <start> is ordinary content
		// and the start// paths do not apply to it.
		if !isSchemaStart(n) {
			kids = nil
			break
		}
		kids = append(append([]string{}, stack...), local)
	case "group", "interleave", "choice":
		// Simplification removes a combinator with a single pattern child
		// (§4.12), so it contributes no step to the path. Keeping it would
		// make oneOrMore//group//attribute match a group that is not there.
		if effectiveChildren(n) < 2 {
			kids = append([]string{}, stack...)
			break
		}
		kids = append(append([]string{}, stack...), local)
	default:
		kids = append(append([]string{}, stack...), local)
	}

	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		// Name classes are not patterns; §7.1's paths do not run through
		// them, and an <except> inside one is a name-class except.
		if isNameClass(kid.Name.Local) {
			continue
		}
		if err := r.walk(kid, kids); err != nil {
			return err
		}
	}

	// Simplification expands a <ref> in place (section 4.19), so what the
	// definition holds is what stands at this point in the path. The
	// definition is therefore walked in the caller's context rather than
	// checked once on its own: the same <define> may be legal from a pattern
	// and illegal from inside a <list>.
	if local == "ref" {
		return r.followRef(n, stack)
	}
	return nil
}

// followRef checks a definition as if written where the <ref> stands.
func (r *restrictor) followRef(n *xdm.Node, stack []string) error {
	// Outside the restricted contexts a ref imposes nothing, and following it
	// everywhere would walk a large grammar once per reference.
	if !restrictedStack(stack) {
		return nil
	}
	name := normalizeToken(n.AttrValue("name"))
	def, ok := r.defines[name]
	if !ok {
		return nil // an unresolved ref is compilation's error to report
	}
	// A definition that reaches itself through a list is genuinely infinite
	// and the expansion would not terminate, so recursion is the error here
	// rather than something to guard past.
	if r.active[name] {
		return fmt.Errorf(
			"relaxng: definition %q is recursive within <list> or <attribute> "+
				"(section 7.1)", name)
	}
	if r.active == nil {
		r.active = map[string]bool{}
	}
	r.active[name] = true
	defer delete(r.active, name)
	for _, kid := range def.ChildElements() {
		if kid.Name.URI != NS || isNameClass(kid.Name.Local) {
			continue
		}
		if err := r.walk(kid, stack); err != nil {
			return fmt.Errorf("%w, through <ref name=%q>", err, name)
		}
	}
	return nil
}

// restrictedStack reports whether the path so far is one whose contents §7.1
// constrains, and so one a <ref> must be expanded into.
func restrictedStack(stack []string) bool {
	for _, s := range stack {
		switch s {
		case "list", "attribute", "except", "start":
			return true
		}
	}
	return false
}

// matchProhibited reports whether the stack of enclosing names, ending at the
// element just entered, matches any prohibited path.
func matchProhibited(stack []string) error {
	for _, p := range prohibited {
		// The last step must be the element we are at; the earlier steps must
		// appear in order among its ancestors.
		if stack[len(stack)-1] != p.steps[len(p.steps)-1] {
			continue
		}
		if matchSteps(stack[:len(stack)-1], p.steps[:len(p.steps)-1]) {
			return fmt.Errorf("relaxng: prohibited path %s (section %s)",
				strings.Join(p.steps, "//"), p.why)
		}
	}
	return nil
}

// matchSteps reports whether steps appear in order within stack, with the
// final step of a data/except path required to be the immediate parent.
func matchSteps(stack []string, steps path) bool {
	if len(steps) == 0 {
		return true
	}
	i := 0
	for j := 0; j < len(stack) && i < len(steps); j++ {
		if stack[j] == steps[i] {
			// data/except: the except must be the data's own child.
			if i == 0 && len(steps) == 2 && steps[0] == "data" {
				if j+1 >= len(stack) || stack[j+1] != "except" {
					continue
				}
			}
			i++
		}
	}
	return i == len(steps)
}

// prune reports whether simplification erases this pattern.
//
// §4.20 propagates notAllowed outward: a group, interleave or oneOrMore with a
// notAllowed child becomes notAllowed itself, and so does an element or
// attribute whose content is notAllowed. A choice keeps only its other branch.
// The consequence for §7 is that a construct which looks illegal may not be
// present in the simplified grammar at all, and the suite tests exactly that —
// an <attribute> inside an <attribute> is legal when a sibling <notAllowed/>
// removes the whole group first.
func prune(n *xdm.Node) bool {
	if n.Name.URI != NS {
		return false
	}
	switch n.Name.Local {
	case "notAllowed":
		return true

	case "group", "interleave", "oneOrMore", "list", "mixed", "optional",
		"zeroOrMore", "element", "attribute", "define", "start":
		// notAllowed anywhere among the pattern children makes the whole
		// pattern notAllowed. (For optional and zeroOrMore the result is
		// empty rather than notAllowed, but either way the subtree is gone.)
		kids := patternChildren(n)
		if n.Name.Local == "attribute" && len(kids) == 0 {
			return false
		}
		for _, kid := range kids {
			if prune(kid) {
				return true
			}
		}
		return false

	case "choice":
		// A choice survives if any branch does.
		kids := patternChildren(n)
		if len(kids) == 0 {
			return false
		}
		for _, kid := range kids {
			if !prune(kid) {
				return false
			}
		}
		return true

	case "ref":
		return false
	}
	return false
}

// effectiveChildren counts the pattern children that survive simplification.
//
// group(empty, p) is p and interleave(empty, p) is p (section 4.20), so an
// <empty/> written alongside a real pattern leaves the combinator with one
// child, and a combinator with one child is that child. Counting the raw
// children instead would make a group appear in a path that simplification
// has removed.
func effectiveChildren(n *xdm.Node) int {
	kids := patternChildren(n)
	if n.Name.Local == "choice" {
		return len(kids)
	}
	count := 0
	for _, kid := range kids {
		if kid.Name.URI == NS && kid.Name.Local == "empty" {
			continue
		}
		count++
	}
	return count
}

// transparent reports whether simplification removes this element, so that it
// contributes no step to a path.
func transparent(n *xdm.Node, local string) bool {
	switch local {
	case "grammar", "define", "div":
		return true
	case "group", "interleave", "choice":
		// A combinator over a single pattern is that pattern (section 4.12).
		return effectiveChildren(n) < 2
	case "start":
		return !isSchemaStart(n)
	}
	return false
}

// isSchemaStart reports whether a <start> belongs to the outermost grammar,
// which is the one whose content §7.1.5 constrains. A <grammar> nested inside
// an <element> is inlined where it stands, so its start is ordinary content.
func isSchemaStart(n *xdm.Node) bool {
	for cur := n.Parent; cur != nil && cur.Kind == xdm.KindElement; cur = cur.Parent {
		if cur.Name.URI == NS && cur.Name.Local == "element" {
			return false
		}
	}
	return true
}

func isNameClass(local string) bool {
	switch local {
	case "name", "anyName", "nsName":
		return true
	}
	return false
}

var _ = xdm.QName{}

// Sections 7.3 and 7.4: the competition rules.
//
// §7.1 asks where a pattern sits. These two ask something harder: whether two
// patterns that sit side by side can match the same thing. An element may not
// declare the attribute bar twice, because a document has only one bar to give
// it; two branches of an interleave may not both accept a <bar>, because
// nothing would decide which branch consumed it.
//
// Deciding that needs the *name classes* compared, not the names — an
// attribute with anyName competes with every other attribute. So this works
// over compiled name classes and asks whether two of them overlap.

// nameSet is what one branch of a group or interleave can match, as far as
// these rules care: the attribute names it may consume, the element names it
// may consume, and whether it can consume text.
type nameSet struct {
	attrs []NameClass
	elems []NameClass
	text  bool
}

func (s *nameSet) merge(o nameSet) {
	s.attrs = append(s.attrs, o.attrs...)
	s.elems = append(s.elems, o.elems...)
	s.text = s.text || o.text
}

// checkCompetition applies §7.3 and §7.4 to a compiled pattern.
func checkCompetition(p Pattern) error {
	_, err := competing(p)
	return err
}

// competing returns what p can match, checking as it goes.
//
// The recursion is bottom-up because that is the order the rules need: to know
// whether two branches of a group conflict, both branches must first be
// summarised, and summarising a branch means recursing into it.
func competing(p Pattern) (nameSet, error) {
	switch t := p.(type) {
	case Attribute:
		// The attribute's own content is a separate world — §7.1 has already
		// ruled out anything in it that could compete out here.
		return nameSet{attrs: []NameClass{t.Name}}, nil

	case Element:
		// An element's content is checked, but its names do not escape: two
		// sibling elements named bar are fine, it is two *branches* offering
		// bar to the same interleave that is not.
		if _, err := competing(t.Pattern); err != nil {
			return nameSet{}, err
		}
		return nameSet{elems: []NameClass{t.Name}}, nil

	case Text:
		return nameSet{text: true}, nil

	case Group:
		l, err := competing(t.Left)
		if err != nil {
			return nameSet{}, err
		}
		r, err := competing(t.Right)
		if err != nil {
			return nameSet{}, err
		}
		// §7.3: the two halves of a group are both required, so an attribute
		// name offered by both would have to appear twice.
		if n := overlapAcross(l.attrs, r.attrs); n != "" {
			return nameSet{}, fmt.Errorf(
				"relaxng: attribute %s is required twice in the same element "+
					"(section 7.3)", n)
		}
		l.merge(r)
		return l, nil

	case Interleave:
		l, err := competing(t.Left)
		if err != nil {
			return nameSet{}, err
		}
		r, err := competing(t.Right)
		if err != nil {
			return nameSet{}, err
		}
		if n := overlapAcross(l.attrs, r.attrs); n != "" {
			return nameSet{}, fmt.Errorf(
				"relaxng: attribute %s is required twice in the same element "+
					"(section 7.3)", n)
		}
		// §7.4: two branches of an interleave may not both accept the same
		// element name, because nothing would decide which one took it.
		if n := overlapAcross(l.elems, r.elems); n != "" {
			return nameSet{}, fmt.Errorf(
				"relaxng: both branches of <interleave> accept element %s "+
					"(section 7.4)", n)
		}
		// §7.4: nor may both accept text, for the same reason.
		if l.text && r.text {
			return nameSet{}, fmt.Errorf(
				"relaxng: both branches of <interleave> accept text (section 7.4)")
		}
		l.merge(r)
		return l, nil

	case Choice:
		// Alternatives do not compete: only one of them runs.
		l, err := competing(t.Left)
		if err != nil {
			return nameSet{}, err
		}
		r, err := competing(t.Right)
		if err != nil {
			return nameSet{}, err
		}
		l.merge(r)
		return l, nil

	case OneOrMore:
		s, err := competing(t.Pattern)
		if err != nil {
			return nameSet{}, err
		}
		// A repeated attribute is not a §7.3 conflict: <oneOrMore> over a
		// lone attribute matches one attribute of that name, since a document
		// cannot carry two. It is only a repetition wrapped around a *group*
		// of attributes that could consume two, and §7.1.2 refuses that path
		// before this check ever runs.
		return s, nil

	case List:
		return competing(t.Pattern)

	case After:
		l, err := competing(t.Left)
		if err != nil {
			return nameSet{}, err
		}
		r, err := competing(t.Right)
		if err != nil {
			return nameSet{}, err
		}
		l.merge(r)
		return l, nil
	}
	return nameSet{}, nil
}

// overlapAcross returns a name that some class in a and some class in b both
// admit, or "". The two slices come from different branches, so every pair is
// compared.
func overlapAcross(a, b []NameClass) string {
	for _, x := range a {
		for _, y := range b {
			if s := describeOverlap(x, y); s != "" {
				return s
			}
		}
	}
	return ""
}

// describeOverlap names a name both classes admit, or returns "".
//
// The classes are small and the interesting cases few, so this decides
// overlap structurally rather than building the intersection: two QNames
// overlap when equal, anyName overlaps everything it does not except, and an
// nsName overlaps another class in its namespace.
func describeOverlap(a, b NameClass) string {
	switch x := a.(type) {
	case NameChoice:
		if s := describeOverlap(x.Left, b); s != "" {
			return s
		}
		return describeOverlap(x.Right, b)
	}
	switch y := b.(type) {
	case NameChoice:
		if s := describeOverlap(a, y.Left); s != "" {
			return s
		}
		return describeOverlap(a, y.Right)
	}

	switch x := a.(type) {
	case QName:
		switch y := b.(type) {
		case QName:
			if x.Name == y.Name {
				return showQName(x.Name)
			}
		case AnyName:
			if y.contains(x.Name) {
				return showQName(x.Name)
			}
		case NsName:
			if y.contains(x.Name) {
				return showQName(x.Name)
			}
		}
	case AnyName:
		switch b.(type) {
		case QName:
			return describeOverlap(b, a)
		case AnyName, NsName:
			// Two open classes always share a name unless one excepts the
			// whole of the other, which no schema in practice writes.
			return "any name"
		}
	case NsName:
		switch y := b.(type) {
		case QName:
			return describeOverlap(b, a)
		case AnyName:
			return "any name in " + showNs(x.Ns)
		case NsName:
			if x.Ns == y.Ns {
				return "any name in " + showNs(x.Ns)
			}
		}
	}
	return ""
}

func showQName(n xdm.QName) string {
	if n.URI == "" {
		return n.Local
	}
	return "{" + n.URI + "}" + n.Local
}

func showNs(ns string) string {
	if ns == "" {
		return "no namespace"
	}
	return "namespace " + ns
}

// Section 7.2: string sequences.
//
// A pattern that matches a *child* — an element, or a string standing on its
// own as the whole content — cannot be sequenced with a pattern that matches a
// single string. The spec's example is <data type="int"/> followed by
// <element name="bar"/>: there is no way to say where the integer ends and
// the element begins, because the document does not mark that boundary.
//
// The two may still be offered as *alternatives*, since then only one of them
// runs and no boundary is needed. That is the whole of the rule, and it is why
// this is checked on group and interleave but not on choice.
//
// Inside a <list> the rule is suspended: a list is split on whitespace before
// its pattern sees it, so a sequence of strings does have a boundary.

// contentKind is what a pattern contributes to its enclosing content.
type contentKind uint8

const (
	kindNothing contentKind = 0
	// kindString is a pattern that matches one whitespace-delimited string:
	// data, value, list.
	kindString contentKind = 1 << iota
	// kindChild is a pattern that matches a child of the element: another
	// element, or text.
	kindChild
)

// checkStringSequences applies §7.2 to a compiled pattern.
func checkStringSequences(p Pattern) error {
	_, err := contentOf(p, false)
	return err
}

// contentOf returns what p contributes, checking sequences as it goes.
//
// inList suspends the rule, and is set when descending into a List.
func contentOf(p Pattern, inList bool) (contentKind, error) {
	switch t := p.(type) {
	case Data:
		return kindString, nil
	case Value:
		return kindString, nil
	case List:
		// The list itself is a string as far as its parent is concerned; its
		// contents are checked with the rule suspended.
		if _, err := contentOf(t.Pattern, true); err != nil {
			return 0, err
		}
		return kindString, nil
	case Text:
		return kindChild, nil

	case Element:
		// The element's own content is a fresh scope: what it holds does not
		// sequence with what stands beside it.
		if _, err := contentOf(t.Pattern, false); err != nil {
			return 0, err
		}
		if inList {
			// A list matches a whitespace-separated string, which has no
			// children to give. The suspension inside a list is of the
			// sequencing rule, not of the fact that there is nothing here
			// for an element to match.
			return 0, fmt.Errorf(
				"relaxng: <element> inside <list>; a list matches strings, " +
					"not elements (section 7.2)")
		}
		return kindChild, nil

	case Attribute:
		// An attribute's value is a scope of its own too, and the attribute
		// contributes nothing to the element's content.
		if _, err := contentOf(t.Pattern, false); err != nil {
			return 0, err
		}
		return kindNothing, nil

	case Group:
		return sequenced(t.Left, t.Right, inList)
	case Interleave:
		return sequenced(t.Left, t.Right, inList)

	case Choice:
		// Alternatives: the rule does not apply, and the choice contributes
		// whatever either branch might.
		l, err := contentOf(t.Left, inList)
		if err != nil {
			return 0, err
		}
		r, err := contentOf(t.Right, inList)
		if err != nil {
			return 0, err
		}
		return l | r, nil

	case OneOrMore:
		k, err := contentOf(t.Pattern, inList)
		if err != nil {
			return 0, err
		}
		// A repetition sequences the pattern with itself, so a string that
		// may repeat is a string sequence — unless a list is splitting it.
		if !inList && k&kindString != 0 && k&kindChild != 0 {
			return 0, errStringSequence()
		}
		return k, nil

	case After:
		return sequenced(t.Left, t.Right, inList)
	}
	return kindNothing, nil
}

// sequenced checks the two halves of a group or interleave against §7.2.
func sequenced(a, b Pattern, inList bool) (contentKind, error) {
	l, err := contentOf(a, inList)
	if err != nil {
		return 0, err
	}
	r, err := contentOf(b, inList)
	if err != nil {
		return 0, err
	}
	if inList {
		return l | r, nil
	}
	// A string on one side and anything that occupies content on the other is
	// the prohibited combination. Two strings in sequence are prohibited for
	// the same reason: nothing marks where the first ends.
	if l&kindString != 0 && r != kindNothing ||
		r&kindString != 0 && l != kindNothing {
		return 0, errStringSequence()
	}
	return l | r, nil
}

func errStringSequence() error {
	return fmt.Errorf(
		"relaxng: a pattern matching a single string is sequenced with a " +
			"pattern matching content; they must be alternatives (section 7.2)")
}

// checkInfiniteAttributeName applies the second clause of §7.3.
//
// An <attribute> whose name class is infinite — anyName or nsName, which admit
// unboundedly many names — must sit under a <oneOrMore>. Without one the
// pattern says "exactly one attribute, of any name", and an element carrying
// two would match it twice over with nothing to say which. Under a oneOrMore
// the intent is unambiguous: as many as the document has.
func checkInfiniteAttributeName(n *xdm.Node, stack []string) error {
	if !hasInfiniteNameClass(n) {
		return nil
	}
	for _, s := range stack {
		if s == "oneOrMore" {
			return nil
		}
	}
	return fmt.Errorf(
		"relaxng: <attribute> with an open name class needs a <oneOrMore> " +
			"ancestor to say how many it matches (section 7.3)")
}

// hasInfiniteNameClass reports whether an attribute's name class admits
// unboundedly many names.
func hasInfiniteNameClass(n *xdm.Node) bool {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "anyName", "nsName":
			return true
		case "choice":
			if findDescendant(kid, []string{"anyName", "nsName"}) != "" {
				return true
			}
		}
	}
	return false
}
