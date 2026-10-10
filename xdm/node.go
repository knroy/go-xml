package xdm

import (
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/v2/internal/genid"
)

// NodeKind enumerates the seven node kinds of the XDM.
type NodeKind int

const (
	KindDocument NodeKind = iota
	KindElement
	KindAttribute
	KindText
	KindComment
	KindPI
	KindNamespace
)

func (k NodeKind) String() string {
	switch k {
	case KindDocument:
		return "document-node()"
	case KindElement:
		return "element()"
	case KindAttribute:
		return "attribute()"
	case KindText:
		return "text()"
	case KindComment:
		return "comment()"
	case KindPI:
		return "processing-instruction()"
	case KindNamespace:
		return "namespace-node()"
	}
	return "node()"
}

func (n *Node) isItem() {}

// TypeName implements Item.
func (n *Node) TypeName() string {
	if n.TypeAnnotation() != "" {
		return n.TypeAnnotation()
	}
	return n.Kind().String()
}

func init() { genid.Of = func(n any) string { return n.(*Node).generateID() } }

// StringValue returns the node's string value per XDM: the concatenation of
// all descendant text for document and element nodes, and the value itself for
// the leaf kinds.
func (n *Node) StringValue() string {
	switch n.Kind() {
	case KindDocument, KindElement:
		// An element holding one text node, or nothing, is the usual
		// shape of a simple value, and has nothing to concatenate.
		first := n.FirstChild()
		if first == nil {
			return ""
		}
		if first.kind == uint8(KindText) && first.NextSibling() == nil {
			return first.Value()
		}
		// The text descendants, in document order, are the string value;
		// comments and PIs contribute nothing to an ancestor's.
		var sb strings.Builder
		for d := range n.Descendants() {
			if d.kind == uint8(KindText) {
				sb.WriteString(d.Value())
			}
		}
		return sb.String()
	default:
		return n.Value()
	}
}

// Atomize returns the typed value of a node. Without schema validation every
// node atomises to xs:untypedAtomic, which is what makes untyped comparison
// rules apply throughout a schemaless transform.
func (n *Node) Atomize() *Atomic {
	// A comment, processing instruction or namespace node has xs:string for
	// its typed value, not xs:untypedAtomic. XPath 2.0 appendix I.2 says so
	// outright, and gives the consequence: because the value is a string
	// rather than untyped, no implicit conversion applies, so using a PI as
	// an operand of an arithmetic operator is a type error where XPath 1.0
	// would have coerced it. These kinds are never schema-validated, so
	// there is no annotation to consult and the answer does not depend on
	// one.
	switch n.Kind() {
	case KindComment, KindPI, KindNamespace:
		return NewString(n.StringValue())
	}

	// A node validated against a schema atomises as its annotated type;
	// one that was not is xs:untypedAtomic, the schemaless default.
	//
	// This is what makes "@length eq count(entry)" work in a schema-aware
	// context: without it the attribute is a string, and comparing a string
	// with an integer is XPTY0004 rather than a comparison. The conversion
	// is deliberately narrow — the numeric, boolean and date types, whose
	// lexical forms this package can already parse — because a type it
	// cannot construct is better left untyped than guessed at.
	if n.TypeAnnotation() != "" {
		// xs:QName and xs:NOTATION are handled here rather than in
		// atomicForAnnotation because resolving the prefix needs the
		// node's in-scope namespaces, which a lexical form alone does
		// not carry. That is the whole difference between a QName and
		// the string that spells it.
		//
		// These are the BUILT-INS, which key under their bare local names
		// (see AnnotationName). A schema type that merely shares one of
		// those local names is qualified, does not match here, and takes
		// the derivation walk below instead. That distinction is the point:
		// schema-for-xslt20.xsd declares an xsl:QName that restricts
		// xs:Name and holds no QName value at all, and matching it here
		// atomised it as a QName it is not.
		switch n.TypeAnnotation() {
		case "QName", "NOTATION":
			if q, ok := n.resolveQNameValue(); ok {
				return NewQNameValue(q)
			}
			return NewUntypedAtomic(n.StringValue())
		}
		// A union-typed node is atomised from the member that accepted its
		// value, which is the only thing that knows what the value *is*: the
		// union's own derivation chain runs to xs:anySimpleType and stops, so
		// the walk below would return nil and the node would atomise to
		// xs:untypedAtomic. The value keeps the annotation as its derived name
		// so the union's identity survives, and carries the member alongside.
		if a := atomicForUnionAnnotation(n); a != nil {
			return a
		}
		if a := atomicForAnnotation(n.TypeAnnotation(), n.StringValue()); a != nil {
			// The annotation is kept on the value as its derived type, so
			// that "instance of" can answer for a user-defined type. Without
			// it the value knows only the primitive it erased to, and every
			// question about the schema type it was validated against
			// answered false.
			return a.WithDerived(n.TypeAnnotation()).WithTypeEnv(n.TypeEnv())
		}
		// A user-defined type this package cannot construct still atomises:
		// it is the primitive its schema type derives from, and the schema
		// name is what "instance of" needs. Returning a bare untypedAtomic
		// discarded the annotation entirely.
		if a := atomicForDerivedAnnotation(n); a != nil {
			return a
		}
	}
	return NewUntypedAtomic(n.StringValue())
}

// AtomizeList returns the typed value of a node whose annotation is a list
// type, as one atomic value per whitespace-separated token.
//
// The second result reports whether the annotation is in fact a list type; a
// caller that gets false must fall back to Atomize, which yields the single
// value that every non-list node has.
//
// Only the three built-in list types are recognised here. A user-defined list
// type is registered by the schema layer with its item type, and that
// derivation chain is what DerivedBase walks; a list type derived by
// restriction from one of these three therefore resolves to it and is
// expanded with its item type.
//
// The empty string atomizes to the empty sequence rather than to one
// zero-length token, which is what "a list of no items" means and what
// strings.Fields already produces.
func (n *Node) AtomizeList() (Sequence, bool) {
	// The node's own record wins over the registry, which is keyed by QName
	// alone and holds whatever schema loaded last -- see Node.ListItem. A
	// node that carries no record falls back to the walk, so a node annotated
	// by something other than schema assessment behaves as it always did.
	//
	// It is read ONLY when the node still carries an annotation. The field
	// describes what the annotation MEANS, so without one there is nothing
	// for it to be the meaning of, and a node whose annotation was cleared
	// must atomise as untyped -- one item holding the whole string, not a
	// sequence of tokens. Reading it unconditionally made a stripped result
	// tree still atomise as a list: <my:list-builtin> came out of
	// copy-of/validation with its annotation gone but its item type intact,
	// so "elem = 'one two three'" compared three NMTOKENs against the whole
	// string and answered false (as-3002, as-1811).
	var item string
	if n.TypeAnnotation() != "" {
		item = n.ListItem()
	}
	if item == "" {
		item = listItemType(typeEnvOf(n), n.TypeAnnotation())
	}
	if item == "" {
		// A union whose selected member is a LIST has a sequence for its
		// typed value, and the union's own name says nothing about it: a
		// union derives from xs:anySimpleType, so listItemType walking the
		// annotation stops immediately. Which member accepted the value is
		// exactly the fact validation recorded on the node, so the list-ness
		// is looked for there too.
		//
		// The XSLT 3.0 schema's exclude-result-prefixes is this shape --
		// union(list of prefix-or-default, "#all") -- and its own
		// XTSE0808 assertion iterates the attribute expecting one item per
		// prefix. Given the whole literal as a single item, "xs ul" is not a
		// prefix in scope and every stylesheet excluding two prefixes was
		// reported invalid.
		if item = listItemType(typeEnvOf(n), n.UnionMember()); item == "" {
			return nil, false
		}
	}
	fields := SplitXMLSpace(n.StringValue())
	out := make(Sequence, 0, len(fields))
	for _, f := range fields {
		if a := atomicForLexical(typeEnvOf(n), item, f); a != nil {
			// The item carries the LIST's item type as its derived name, so
			// that "data(@nmtokens) instance of xs:NMTOKEN*" is true. Without
			// it each token is only the xs:string that NMTOKEN erases to and
			// the instance-of test answers false.
			out = append(out, a.WithDerived(item).WithTypeEnv(n.TypeEnv()))
			continue
		}
		out = append(out, NewUntypedAtomic(f))
	}
	return out, true
}

// atomicForLexical builds a typed value for one lexical form annotated with
// the named type, walking the derivation chain the schema registered when the
// name is not itself a built-in this package constructs.
//
// AtomizeList needs this and atomicForDerivedAnnotation cannot serve: that
// function reads the whole node's string value, while a list item is one
// token out of many. The walk is the same, guarded the same way, and the
// value keeps the ITEM type's own name so that
// "data(@list) instance of my:itemType*" answers true.
func atomicForLexical(env *TypeEnvironment, typeName, value string) *Atomic {
	if a := atomicForAnnotation(typeName, value); a != nil {
		return a
	}
	name := typeName
	// The registry is a name->name map, so the only way this walk fails to
	// terminate is a cycle a schema registered. A visited set keyed on the
	// name identifies that exactly, where a step count also cut off the legal
	// deep chains it could not tell apart from a cycle.
	seen := map[string]bool{name: true}
	for {
		prim := env.DerivedBase(name)
		ok := prim != ""
		if !ok {
			return nil
		}
		if a := atomicForAnnotation(prim, value); a != nil {
			return a
		}
		if seen[prim] {
			return nil
		}
		seen[prim] = true
		name = prim
	}
}

// listItemType maps a list type annotation to the type of its items, or ""
// when the annotation does not name a list type.
func listItemType(env *TypeEnvironment, annotation string) string {
	// Guarded by a visited set rather than a step count: only a cycle a schema
	// registered can keep this walk going, and the set names that condition
	// instead of guessing at a depth no legal chain exceeds.
	seen := map[string]bool{}
	for annotation != "" && !seen[annotation] {
		seen[annotation] = true
		switch annotation {
		case "NMTOKENS":
			return "NMTOKEN"
		case "IDREFS":
			return "IDREF"
		case "ENTITIES":
			return "ENTITY"
		}
		// A user-defined list type is not reachable through DerivedBase: the
		// schema layer registers it here with the item type it was declared
		// with, because that is information the data model has no other way to
		// obtain. It is consulted before the derivation walk so that a list
		// whose base happens to be registered as something atomic does not
		// lose its list-ness one step in.
		if item := env.ListItemOf(annotation); item != "" {
			return item
		}
		next := env.DerivedBase(annotation)
		if next == annotation {
			return ""
		}
		annotation = next
	}
	return ""
}

// RegisterListType records that a schema type is a list, and what its items
// are.
//
// The xsd package calls this as it loads a schema, for the same reason it
// calls RegisterDerivedType: the typed value of a list-typed node is a
// SEQUENCE of one atomic per token, and nothing in the data model can work out
// from a bare type name that "numbers" is a list of xs:decimal. Without it a
// list-typed node atomises to one untypedAtomic holding the whole literal, so
// count(data(@list)) answers 1 and "data(@list) instance of xs:untypedAtomic"
// answers true for a node the schema plainly gave a typed value.
//
// Both arguments are annotation names, as RegisterDerivedType's are.
//
// itemType is the item type's own name, which may itself be a registered
// schema type; atomicForAnnotation and the derivation walk resolve it.
func RegisterListType(name, itemType string) {
	globalTypeEnv.RegisterList(name, itemType)
}

// ListItemOf returns the item type registered for a list type, or "" when the
// name is not a registered list.
func ListItemOf(name string) string {
	return globalTypeEnv.ListItemOf(name)
}

// atomicForAnnotation builds a typed value from a schema type annotation, or
// returns nil when the annotation names a type this package does not construct.
func atomicForAnnotation(typeName, value string) *Atomic {
	switch typeName {
	case "string", "normalizedString", "token", "language", "Name", "NCName",
		"ID", "IDREF", "ENTITY", "NMTOKEN":
		return NewString(value)

	case "boolean":
		// Trimmed, as every other branch here trims: xs:boolean carries
		// whiteSpace="collapse", so the value a validated node atomises to is
		// the collapsed form and not the characters as they were written.
		// Matching the raw text made "<e>   true   </e>" atomise to nothing
		// at all, so a value the schema had validated as a restriction of
		// xs:boolean fell back to untypedAtomic and compared as a string.
		switch strings.TrimSpace(value) {
		case "true", "1":
			return NewBoolean(true)
		case "false", "0":
			return NewBoolean(false)
		}
		return nil

	case "decimal", "integer", "long", "int", "short", "byte",
		"nonNegativeInteger", "positiveInteger", "nonPositiveInteger",
		"negativeInteger", "unsignedLong", "unsignedInt", "unsignedShort",
		"unsignedByte":
		// The integer family atomises as xs:integer and xs:decimal as
		// itself. Both parse exactly, through big.Rat rather than a
		// float, so that a value too large for a machine word keeps
		// every digit.
		r, ok := new(big.Rat).SetString(strings.TrimSpace(value))
		if !ok {
			return nil
		}
		if typeName == "decimal" {
			return NewDecimal(r)
		}
		return NewIntegerFromRat(r)

	case "float", "double":
		// ParseFloat reports a magnitude no double can hold as
		// strconv.ErrRange, and returns ±Inf along with it. That is a
		// correct value carried by an error, and treating the error as
		// "not a lexical form of this type" was wrong: "1e400" IS a
		// valid xs:double literal, the schema validated it as one, and
		// this branch only runs on text a schema already accepted.
		//
		// The value returned is the one the rest of the processor has
		// settled on: F&O 3.0 §4.2 permits an overflow to yield ±INF,
		// which is what casting and JSON parsing do (xpath/cast.go,
		// xpath/fn_json.go). Rejecting it here returned nil, the caller
		// fell back to xs:untypedAtomic, and a validated xs:double
		// silently stopped being a double.
		//
		// Underflow needs no handling: Go returns 0 with a nil error
		// for it, so only the overflow case ever reached the switch.
		trimmed := strings.TrimSpace(value)
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			var numErr *strconv.NumError
			switch {
			case errors.As(err, &numErr) && numErr.Err == strconv.ErrRange:
				// Keep f: it is already ±Inf (or ±0).
			case trimmed == "INF":
				f = math.Inf(1)
			case trimmed == "-INF":
				f = math.Inf(-1)
			case trimmed == "NaN":
				f = math.NaN()
			default:
				return nil
			}
		}
		if typeName == "float" {
			return NewFloat(f)
		}
		return NewDouble(f)

	case "anyURI":
		return NewAnyURI(value)

	case "date", "time", "dateTime", "dateTimeStamp":
		// xs:dateTimeStamp is xs:dateTime with a required timezone; the
		// lexical form is a dateTime's, and the requirement was already
		// enforced when the value was validated.
		code := TypeDate
		switch typeName {
		case "time":
			code = TypeTime
		case "dateTime", "dateTimeStamp":
			code = TypeDateTime
		}
		dt, err := ParseDateTime(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewDateTime(dt, code)

	case "gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay":
		code := TypeGYear
		switch typeName {
		case "gYearMonth":
			code = TypeGYearMonth
		case "gMonth":
			code = TypeGMonth
		case "gMonthDay":
			code = TypeGMonthDay
		case "gDay":
			code = TypeGDay
		}
		dt, err := ParseGregorian(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewGregorian(dt, code)

	case "duration", "yearMonthDuration", "dayTimeDuration":
		code := TypeDuration
		switch typeName {
		case "yearMonthDuration":
			code = TypeYearMonthDuration
		case "dayTimeDuration":
			code = TypeDayTimeDuration
		}
		d, err := ParseDuration(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewDuration(d, code)

	case "hexBinary":
		return NewBinary(value, TypeHexBinary)
	case "base64Binary":
		return NewBinary(value, TypeBase64Binary)
	}
	return nil
}

// resolveQNameValue expands the node's string value as a QName against the
// namespaces in scope at the node.
//
// An unprefixed name takes the default namespace, whichever kind of node
// carries it. The rule that leaves an unprefixed name in no namespace applies
// to an attribute's own *name*, not to a QName written as its *value*: XML
// Schema Part 2 §3.2.18 gives xs:QName and xs:NOTATION the value space of
// expanded names and resolves an unprefixed one against the default namespace.
//
// The suite pins the distinction. notation-03.xml writes NOTATION-attribute="mp3"
// under xmlns="http://notation.example.com" and expects it to be the same
// notation as one written "one:mp3"; treating the bare form as absent-namespace
// made distinct-values, xsl:for-each-group and key() all see two values.
func (n *Node) resolveQNameValue() (QName, bool) {
	value := strings.TrimSpace(n.StringValue())
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return QName{}, false
	}
	scope := n
	if scope.Kind() == KindAttribute && scope.Parent() != nil {
		scope = scope.Parent()
	}
	if prefix == "" {
		uri, _ := scope.LookupPrefix("")
		return QName{URI: uri, Local: local}, true
	}
	uri, ok := scope.LookupPrefix(prefix)
	if !ok {
		return QName{}, false
	}
	return QName{URI: uri, Local: local, Prefix: prefix}, true
}

// Attr returns the attribute node with the given expanded name, or nil.
func (n *Node) Attr(uri, local string) *Node {
	if n.isLeaf() || n.flags&fSide != 0 {
		return nil
	}
	t := n.tree
	for i, k := n.self+1, n.attrCount(); k > 0; i, k = i+1, k-1 {
		a := t.rec(i)
		if q := &t.names[a.name]; q.Local == local && q.URI == uri {
			return a
		}
	}
	return nil
}

// AttrValue returns the value of a no-namespace attribute, or "". Most
// attributes the stylesheet compiler reads (match, select, name, test) are
// unprefixed, so this is the common case worth a helper.
func (n *Node) AttrValue(local string) string {
	if a := n.Attr("", local); a != nil {
		return a.Value()
	}
	return ""
}

// Root returns the root of the containing tree, walking parent links. For a
// well-formed parsed document this is the document node.
func (n *Node) Root() *Node {
	cur := n
	for p := cur.Parent(); p != nil; p = cur.Parent() {
		cur = p
	}
	return cur
}

// IsElement reports whether n is an element with the given expanded name.
func (n *Node) IsElement(uri, local string) bool {
	if n.Kind() != KindElement {
		return false
	}
	q := n.Name()
	return q.URI == uri && q.Local == local
}

// ChildElements returns the element children, which is what almost every
// stylesheet-compilation walk wants.
func (n *Node) ChildElements() []*Node {
	var out []*Node
	for c := range n.Children() {
		if c.Kind() == KindElement {
			out = append(out, c)
		}
	}
	return out
}

// Walk calls fn for n and every element in document order beneath it. fn
// returning false stops the walk. n itself is visited whatever its kind, so a
// document node can be walked directly; below n only elements are visited,
// never text, comments, processing instructions, attributes or namespaces.
// Recursion depth is the element depth, which the parser bounds with
// ParseOptions.MaxDepth.
func (n *Node) Walk(fn func(*Node) bool) {
	if !fn(n) {
		return
	}
	for d := range n.Descendants() {
		if d.Kind() == KindElement && !fn(d) {
			return
		}
	}
}

// FirstElement returns the first element in document order at or beneath n
// with the given namespace URI and local name, or nil. The name matches as
// in IsElement.
func (n *Node) FirstElement(uri, local string) *Node {
	var found *Node
	n.Walk(func(e *Node) bool {
		if e.IsElement(uri, local) {
			found = e
		}
		return found == nil
	})
	return found
}

// --- Tree construction ------------------------------------------------------

// HasPositions reports whether the tree was parsed with TrackPositions, and so
// can answer Node.Position for the elements it holds.
//
// A caller that caches trees needs it: one parsed without positions cannot
// serve a request that needs them, and the only way to tell is to ask.
func (t *Tree) HasPositions() bool { return t != nil && t.srcText() != "" }

// positionAt converts a byte offset into a 1-based line and column.
//
// Column is counted in bytes, not runes: the offsets come from the XML
// decoder, and a caller pointing an editor at the failure wants the same
// units the decoder used.
func (t *Tree) positionAt(off int) (line, col int, ok bool) {
	s := t.source
	if s == nil || s.src == "" || off < 0 || off > len(s.src) {
		return 0, 0, false
	}
	s.lineOnce.Do(func() {
		// Line 1 starts at offset 0; every byte after a newline starts another.
		s.lineStarts = append(s.lineStarts, 0)
		for i := 0; i < len(s.src); i++ {
			if s.src[i] == '\n' {
				s.lineStarts = append(s.lineStarts, i+1)
			}
		}
	})
	// The line is the last one starting at or before off.
	i := sort.SearchInts(s.lineStarts, off+1) - 1
	if i < 0 {
		return 0, 0, false
	}
	return i + 1, off - s.lineStarts[i] + 1, true
}

// The six functions below are the name-only face of the type derivation
// facts. They read and write globalTypeEnv, the process-global
// TypeEnvironment; the tables themselves, their keying and their locking now
// live in typeenv.go, and a schema that owns an environment records the same
// facts there as well (see xsd.Schema.TypeEnv).
//
// Every name is an ANNOTATION NAME (see AnnotationName): a built-in under its
// bare local name, a schema type under {uri}local. Keying by the bare local
// name conflated the two, and in a table shared by the whole process the
// conflation was permanent — one schema declaring its own type named "QName"
// rewrote the built-in's entry for every later schema. See
// TestShadowedBuiltinCoexistsWithItsShadow.
//
// They are populated by the xsd package as a schema loads, which is the only
// place that knows a user-defined type's base. Keeping them here rather than
// in xsd is what lets xdm.Node.Atomize consult them without importing xsd,
// which it cannot: xsd already imports xdm.

// RegisterDerivedType records that a schema type erases to a built-in one.
//
// Both arguments are annotation names, which AnnotationName builds; passing a
// bare local name for a type that has a namespace re-creates the conflation
// this keying exists to prevent.
//
// The xsd package calls this as it loads a schema, so that a node annotated
// with a user-defined type still atomises to a typed value rather than to
// untypedAtomic. Without it, "instance of my:partNumberType" could never be
// true for a value read out of a validated document, because the value would
// have discarded the annotation on the way out of the tree.
func RegisterDerivedType(name, primitive string) {
	globalTypeEnv.RegisterDerived(name, primitive)
}

// RegisterUnionType records that a schema type is a union, and what its member
// types are.
//
// The xsd package calls this as it loads a schema, for the reason it calls
// RegisterListType: a union's base is always xs:anySimpleType, so the
// derivation chain RegisterDerivedType records dead-ends immediately and
// carries no information about what the value actually is. Without the member
// list a union-typed node atomises to xs:untypedAtomic — the walk finds
// anySimpleType, cannot build a value for it, and gives up — which makes
// "data(u) instance of xs:untypedAtomic" true for a node the schema plainly
// gave a typed value, and makes every question about the member it validated
// as answer false.
//
// The name and every member are annotation names, as RegisterDerivedType's
// arguments are. This registry is keyed by the same strings as
// derivedPrimitives and listItems, so qualifying one of the three and not the
// others would leave unions silently unresolvable.
//
// The members are the *declared* members, in declaration order; which of them
// a given value belongs to is a per-value fact recorded on the node, because
// XSD 1.0 §3.14.4 chooses the member by trying each one's lexical space
// against the value in turn.
func RegisterUnionType(name string, members []string) {
	if name == "" || len(members) == 0 {
		return
	}
	globalTypeEnv.RegisterUnion(name, members)
}

// UnionMembersOf returns the member types of a registered union type, or nil
// when the name does not denote one.
//
// The result must not be modified: it is the stored slice, shared with every
// other caller.
func UnionMembersOf(name string) []string {
	return globalTypeEnv.UnionMembersOf(name)
}

// DerivedBase returns the type a schema type derives from, or "" if the name
// is not a registered schema type. The name is an annotation name, and so is
// the result, so a chain can be walked by feeding one back in.
//
// It is what makes the subtype relation work for schema types: a value
// annotated as a restriction of xs:NOTATION is an instance of xs:NOTATION as
// well as of its own type, and answering that means walking the chain the
// schema recorded.
func DerivedBase(name string) string {
	return globalTypeEnv.DerivedBase(name)
}

// atomicForUnionAnnotation builds a typed value for a node whose type is a
// union, using the member that validation recorded as having accepted it.
//
// It returns nil unless the node carries a member — a union with no recorded
// member is one this package cannot say anything about, and guessing a member
// here would be wrong: which member accepts "100" depends on the member list
// and on facets this package does not hold, and XSD 1.0 §3.14.4 makes the
// choice a property of the value rather than of the type. The schema layer
// already performs that selection while validating, so the answer is carried
// rather than recomputed.
//
// The member's own name may itself be a user-defined schema type, so the value
// is built through the same lexical walk a list item uses.
func atomicForUnionAnnotation(n *Node) *Atomic {
	member := n.UnionMember()
	if member == "" {
		return nil
	}
	// xs:QName and xs:NOTATION members need the node's in-scope namespaces to
	// resolve the prefix, which a lexical form alone does not carry — the same
	// reason Atomize handles them before consulting the annotation table.
	switch member {
	case "QName", "NOTATION":
		if q, ok := n.resolveQNameValue(); ok {
			return NewQNameValue(q).WithDerivedUnion(n.TypeAnnotation(), member).WithTypeEnv(n.TypeEnv())
		}
		return nil
	}
	a := atomicForLexical(typeEnvOf(n), member, n.StringValue())
	if a == nil {
		return nil
	}
	return a.WithDerivedUnion(n.TypeAnnotation(), member).WithTypeEnv(n.TypeEnv())
}

// atomicForDerivedAnnotation builds a typed value for a user-defined schema
// type, using the built-in it derives from.
func atomicForDerivedAnnotation(n *Node) *Atomic {
	// The chain is walked rather than followed one step. A schema type is
	// often a restriction of another *user-defined* type — specialPartNumber
	// restricts partNumberType which restricts xs:string — and the registered
	// base of the annotation is then itself a schema name that
	// atomicForAnnotation cannot build. Stopping after one step returned nil
	// for exactly those types and the node atomised to xs:untypedAtomic,
	// which lost the annotation and made every "instance of" on the value
	// answer false.
	//
	// The walk is guarded by a visited set so that a schema whose derivations
	// somehow formed a cycle cannot spin here. That is the only thing that can
	// stop it terminating — the registry is a name->name map — and a count
	// cannot tell such a cycle from a legally deep chain of restrictions.
	name := n.TypeAnnotation()
	seen := map[string]bool{name: true}
	// The node's own record of what its type erases to is preferred over the
	// registry for the FIRST step, which is the step that names the type this
	// node was actually validated against. Later steps walk names that came
	// out of the registry already, so there is nothing node-local to prefer.
	// See the commentary on Node.DerivedPrimitive: the registry is keyed by
	// QName alone and answers for whatever schema loaded last.
	// Guarded on the annotation for the reason AtomizeList is: the field
	// describes what the annotation means, and an annotation-less node has
	// no meaning to prefer. (Atomize only reaches here with one set, but the
	// walk is exported through other paths and must not depend on that.)
	first := ""
	if n.TypeAnnotation() != "" {
		first = n.DerivedPrimitive()
	}
	for {
		var prim string
		var ok bool
		if first != "" {
			prim, ok, first = first, true, ""
		} else {
			prim = typeEnvOf(n).DerivedBase(name)
			ok = prim != ""
		}
		if !ok {
			return nil
		}
		// The bare names are the built-ins, for the same reason as in
		// Atomize: a qualified key naming a schema type never lands here,
		// and the walk continues past it to whatever it really derives from.
		switch prim {
		case "QName", "NOTATION":
			if q, ok := n.resolveQNameValue(); ok {
				return NewQNameValue(q).WithDerived(n.TypeAnnotation()).WithTypeEnv(n.TypeEnv())
			}
			return nil
		}
		if a := atomicForAnnotation(prim, n.StringValue()); a != nil {
			// The value keeps the annotation it was *validated* as, not the
			// intermediate name the walk stopped at: that is what makes
			// "instance of my:specialPartNumber" true as well as
			// "instance of my:partNumberType".
			return a.WithDerived(n.TypeAnnotation()).WithTypeEnv(n.TypeEnv())
		}
		if seen[prim] {
			return nil
		}
		seen[prim] = true
		name = prim
	}
}

// annotationIDKind reports whether an annotation name is derived from xs:ID or
// from xs:IDREF/xs:IDREFS, walking the derivation chain a schema registered.
//
// The walk is guarded for the same reason the other derivation walks are: a
// schema whose derivations somehow formed a cycle must not spin here. A
// visited set says that and only that, so a long but acyclic chain of
// restrictions over xs:ID still reports the ID kind it inherits.
func annotationIDKind(annotation string) (isID, isIDREFS bool) {
	seen := map[string]bool{}
	for annotation != "" && !seen[annotation] {
		seen[annotation] = true
		switch annotation {
		case "ID":
			return true, false
		case "IDREF", "IDREFS":
			return false, true
		}
		annotation = DerivedBase(annotation)
	}
	return false, false
}

// SetTypeAnnotation records a type annotation and the is-id / is-idrefs
// properties that go with it.
//
// It exists so that every producer of annotations — schema assessment, DTD
// attribute types, the XSLT validation instructions — sets the two properties
// the same way. Assigning TypeAnnotation directly is still legal but leaves
// is-id and is-idrefs at whatever they were, which is what a caller
// deliberately preserving them across a strip wants and what a caller
// annotating a fresh node does not.
//
// The properties are only ever turned *on* here. A node that was already
// marked keeps its marking when re-annotated with a non-ID type, because the
// data model's properties describe how the node was validated originally and
// XSLT's stripping rules are the only thing entitled to change them — and
// those rules say the properties do not change at all.
func (n *Node) SetTypeAnnotation(annotation string) {
	if annotation == "" && n.flags&fTyped == 0 {
		return
	}
	t := n.ownTyping()
	t.annotation = annotation
	if annotation == "" {
		// Clearing the annotation clears what it meant. DerivedPrimitive and
		// ListItem describe a type this node no longer claims, and leaving
		// them would let it keep atomising as that type -- an annotation-less
		// node that still splits into list items is exactly the bug
		// input-type-annotations="strip" and xsl:copy-of would have hit.
		t.derivedPrimitive, t.listItem = "", ""
	}
	if isID, isRefs := annotationIDKind(annotation); isID || isRefs {
		t.isID = t.isID || isID
		t.isIDREFS = t.isIDREFS || isRefs
	}
}

// SetTypeAnnotationResolved records an annotation together with what it means
// according to the schema doing the validating: the built-in the type erases
// to, and the item type when it is a list. Either may be empty when the
// caller has no answer, which leaves the corresponding field unset and lets
// atomisation fall back to the process-global registries.
//
// It exists because SetTypeAnnotation cannot answer those questions. Resolving
// a name to its base means consulting the registries, and those are keyed by
// QName alone across the whole process, so the answer they give depends on
// which schema loaded most recently rather than on which schema validated
// this node. The validator holds the right schema at the right moment; this
// is how it hands the answer to the node instead of leaving it to be looked
// up again later against possibly different definitions. See the commentary
// on Node.DerivedPrimitive.
//
// The fields are ASSIGNED rather than or-ed, unlike is-id and is-idrefs: they
// describe the assessment happening now, so re-annotating a node replaces
// them. Clearing them when the caller has no answer is deliberate — a stale
// value from a previous assessment would be a wrong answer rather than a
// missing one, and a missing one falls back correctly.
func (n *Node) SetTypeAnnotationResolved(annotation, derivedPrimitive, listItem string) {
	n.SetTypeAnnotation(annotation)
	if n.flags&fTyped == 0 && derivedPrimitive == "" && listItem == "" {
		return
	}
	t := n.ownTyping()
	t.derivedPrimitive = derivedPrimitive
	t.listItem = listItem
}

// SetAssessedTyping is SetTypeAnnotationResolved followed by SetTypeEnv, and
// turns NoTypedValue and MixedContent on when asked, in one write: the shape
// in which schema assessment annotates a node. annotation must not be "".
func (n *Node) SetAssessedTyping(annotation, derivedPrimitive, listItem string,
	env *TypeEnvironment, noTypedValue, mixedContent bool) {
	t := n.ownTyping()
	t.annotation = annotation
	if isID, isRefs := annotationIDKind(annotation); isID || isRefs {
		t.isID = t.isID || isID
		t.isIDREFS = t.isIDREFS || isRefs
	}
	t.derivedPrimitive, t.listItem, t.env = derivedPrimitive, listItem, env
	t.noTypedValue = t.noTypedValue || noTypedValue
	t.mixedContent = t.mixedContent || mixedContent
}

// CopyTypingFrom copies every PSVI property of src onto n, so that the copy
// answers each of them exactly as the original does.
//
// It exists because there is no such thing as "the important half" of a node's
// typing. Nine properties record what an assessment concluded --
// TypeAnnotation, UnionMember, DerivedPrimitive, ListItem, IsID, IsIDREFS,
// IsNilled, NoTypedValue, MixedContent -- and each one of them has, at some point in this repository, been
// dropped by a copy site that hand-picked the fields it thought mattered. Each
// omission was silent and each produced a confidently wrong answer rather than
// a missing one: a union-typed value atomising to xs:untypedAtomic, fn:id
// finding nothing, nilled() going false on a preserved copy, a list type
// erased to the wrong primitive by the process-global registries. The failure
// mode is always the same shape, so the fix is one operation rather than seven
// more careful field lists.
//
// The fields are ASSIGNED rather than or-ed. The destination is a copy of the
// source and holds no assessment of its own; anything already on it is either
// identical or wrong.
//
// It deliberately does NOT go through SetTypeAnnotation. That setter is for a
// PRODUCER of annotations -- schema assessment, DTD attribute types, the XSLT
// validation instructions -- which knows a name and must derive the rest from
// it. Here every property is already known, so deriving would be at best
// redundant and at worst wrong: SetTypeAnnotation only ever turns is-id and
// is-idrefs ON, which would make a copy of a non-ID node inherit a marking the
// original does not have. The invariant SetTypeAnnotation protects is upheld
// here by construction: the resolved fields cannot outlive their annotation,
// because src is a coherent node and all nine fields travel together.
func (n *Node) CopyTypingFrom(src *Node) { n.ApplyTyping(TypingOf(src)) }

// Typing is the complete set of PSVI properties an assessment concludes about
// one node, detached from any node.
//
// It exists so that a caller who ALREADY KNOWS these facts can hand them over
// as a unit instead of passing a type name and letting the receiver look the
// rest up. The lookup is the problem: resolving a name to its base, its item
// type or its ID kind means consulting derivedPrimitives, listItems and
// unionMembers, which are process-global and keyed by QName alone, so they
// answer for whichever schema loaded LAST rather than for the schema that
// validated this node. The validator holds the right schema at the right
// moment; Typing is the shape that lets it say so.
//
// The field list is CopyTypingFrom's, and deliberately the same one: nine
// properties travel together or the copy is wrong, and every historical bug in
// this area was a hand-picked subset of them. A new PSVI property must be
// added here, to CopyTypingFrom and to CopyTypingStrippedFrom together.
//
// The zero Typing means "nothing assessed this node", which is the correct
// state for an unvalidated node and is what the name-only convenience wrappers
// produce when given an empty annotation.
type Typing struct {
	TypeAnnotation   string
	UnionMember      string
	DerivedPrimitive string
	ListItem         string
	IsID             bool
	IsIDREFS         bool
	IsNilled         bool
	NoTypedValue     bool
	MixedContent     bool
}

// TypingOf reads a node's PSVI properties out as a Typing. A nil node has none.
func TypingOf(n *Node) Typing {
	if n == nil {
		return Typing{}
	}
	t := n.typ()
	return Typing{
		TypeAnnotation:   t.annotation,
		UnionMember:      t.unionMember,
		DerivedPrimitive: t.derivedPrimitive,
		ListItem:         t.listItem,
		IsID:             t.isID,
		IsIDREFS:         t.isIDREFS,
		IsNilled:         t.isNilled,
		NoTypedValue:     t.noTypedValue,
		MixedContent:     t.mixedContent,
	}
}

// ApplyTyping writes a Typing's properties onto n, replacing whatever was
// there.
//
// The fields are ASSIGNED, not or-ed, and nothing is derived from the
// annotation name -- for the reason CopyTypingFrom assigns rather than going
// through SetTypeAnnotation. The caller already holds every answer, so
// re-deriving would be redundant where it agreed and wrong where it did not:
// SetTypeAnnotation only ever turns is-id ON, which would let a node inherit a
// marking its assessment did not give it.
func (n *Node) ApplyTyping(t Typing) {
	if t == (Typing{}) && n.flags&fTyped == 0 {
		return
	}
	d := n.ownTyping()
	d.annotation = t.TypeAnnotation
	d.unionMember = t.UnionMember
	d.derivedPrimitive = t.DerivedPrimitive
	d.listItem = t.ListItem
	d.isID = t.IsID
	d.isIDREFS = t.IsIDREFS
	d.isNilled = t.IsNilled
	d.noTypedValue = t.NoTypedValue
	d.mixedContent = t.MixedContent
}

// CopyTypingStrippedFrom copies onto n the PSVI properties of src that survive
// input-type-annotations="strip" and validation="strip", and clears the rest.
//
// The split between the two groups is not a judgement call; XSLT 2.0 sections
// 3.5 and 19.2 draw it explicitly, and it lands differently on each field:
//
//   - TypeAnnotation, UnionMember, DerivedPrimitive and ListItem are CLEARED.
//     They are one fact in four parts -- the name of the type, which member of
//     a union accepted the value, the built-in the type erases to, and the item
//     type of a list -- and stripping removes that fact. Clearing the name
//     alone is precisely the bug SetTypeAnnotation guards against: atomisation
//     gates on TypeAnnotation != "", so a surviving ListItem would go unread
//     until something else re-annotated the node, and then describe a type the
//     node no longer claims. The four go together in both directions.
//
//   - IsID and IsIDREFS are KEPT. Section 3.5 says so in as many words: the
//     setting "does not change the is-id and is-idrefs properties". They are
//     separate state for exactly this reason (see Node.IsID), and fn:id and
//     fn:idref are defined over them rather than over the annotation, so a
//     stripped document whose ID attributes are not spelled "id" would
//     otherwise become invisible to both.
//
//   - IsNilled is CLEARED. XDM 5.10 makes dm:nilled a property of an element
//     that a schema assessment found nil, and a stripped tree is one nothing
//     assessed. Section 3.5 states the consequence directly: after stripping,
//     the nilled property of every element is false. It parts company with
//     is-id here because is-id survives by explicit exemption and this does
//     not; the xsi:nil attribute, being an ordinary attribute once the type is
//     gone, is a separate question that belongs to whoever is doing the copy.
//
// Fields outside the PSVI set -- name, value, base URI, children -- are the
// caller's business, exactly as in CopyTypingFrom.
//
// NoTypedValue and MixedContent are cleared for the same reason IsNilled is:
// they are conclusions of an assessment, and a stripped tree is one nothing
// assessed. Every element of it is xs:untypedAtomic and atomizes.
func (n *Node) CopyTypingStrippedFrom(src *Node) {
	s := src.typ()
	n.ApplyTyping(Typing{IsID: s.isID, IsIDREFS: s.isIDREFS})
}

// StripTyping clears in place every PSVI property that stripping removes,
// keeping the two it preserves.
//
// It is CopyTypingStrippedFrom applied to a node that is its own source, for
// the callers that strip a tree they already own rather than building a copy.
// Spelling it separately keeps those callers from writing n.CopyTypingStripped
// From(n), which reads as though it might do something else.
func (n *Node) StripTyping() {
	n.CopyTypingStrippedFrom(n)
}

// HasSimpleTypeAnnotation reports whether an annotation names a simple type,
// or a complex type with simple content.
//
// XSLT 2.0 section 4.4 preserves whitespace-only text in such an element
// *regardless* of xsl:strip-space: that text is the element's entire typed
// value, which the schema validated, and stripping it would leave a node whose
// annotation describes a value it no longer holds. An element with
// element-only or mixed content has no such value and is stripped normally.
//
// The registration table is the oracle rather than a list of names, because
// the annotation on such an element is the built-in its content type erases
// to — "string" for both an element of type xs:string and one whose anonymous
// complex type extends xs:string, which is exactly the pair section 4.4 groups
// together. A complex type with element-only content registers no derivation
// to a built-in, so it answers false, which is the distinction being drawn.
func HasSimpleTypeAnnotation(annotation string) bool {
	if annotation == "" {
		return false
	}
	// A list type is a simple type: its typed value is a sequence of atomics,
	// so the same reasoning applies even though it is the item type that the
	// registry records.
	if ListItemOf(annotation) != "" {
		return true
	}
	// Guarded like every other derivation walk here, so that a schema whose
	// derivations somehow formed a cycle cannot spin. The next == annotation
	// test below catches a self-loop; the visited set catches the longer
	// cycles it cannot see.
	seen := map[string]bool{}
	for annotation != "" && !seen[annotation] {
		seen[annotation] = true
		if isBuiltinSimpleTypeName(annotation) {
			return true
		}
		next := DerivedBase(annotation)
		if next == annotation {
			return false
		}
		annotation = next
	}
	return false
}

// isBuiltinSimpleTypeName reports whether a name is one of the built-in simple
// types this package can build a typed value for.
//
// It asks atomicForAnnotation wherever a lexical form the type accepts exists,
// because that function is the single place the set is defined and a second
// list would drift from it. The types named directly are those whose lexical
// space excludes the empty string: their absence from that answer would be a
// property of the probe value rather than of the type.
func isBuiltinSimpleTypeName(name string) bool {
	switch name {
	case "QName", "NOTATION", "anySimpleType", "anyAtomicType", "untypedAtomic":
		return true
	}
	if atomicForAnnotation(name, "") != nil {
		return true
	}
	switch name {
	case "boolean", "decimal", "float", "double", "integer",
		"nonPositiveInteger", "negativeInteger", "long", "int", "short", "byte",
		"nonNegativeInteger", "unsignedLong", "unsignedInt", "unsignedShort",
		"unsignedByte", "positiveInteger":
		return atomicForAnnotation(name, "1") != nil
	case "date", "dateTime", "time", "duration", "dayTimeDuration",
		"yearMonthDuration", "gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay",
		"hexBinary", "base64Binary", "anyURI":
		return true
	}
	return false
}
