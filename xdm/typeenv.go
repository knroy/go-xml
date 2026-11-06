package xdm

import (
	"strings"
	"sync"
)

// TypeEnvironment owns the type derivation facts a single schema assembly
// established: which built-in each of its named types erases to, which of them
// are lists and what their items are, and which are unions and what their
// members are.
//
// It exists because those three tables were process-global and keyed by type
// NAME alone. Two schemas may each legitimately define {urn:x}T -- one
// deriving from xs:decimal, one from xs:string -- and under one global table
// the second load silently rewrote the first's answer for every node already
// validated against it. Atomisation was fixed by recording the resolved
// metadata on the node (see Node.DerivedPrimitive), but every by-NAME
// consumer -- the subtype relation, "instance of", "castable as",
// xsl:validate -- has no node to read and so still asked the shared table.
//
// The lifetime rule, which is not negotiable: an environment lives exactly as
// long as something can still reach it. A compiled schema holds one, so an
// environment stays reachable for as long as the schema is alive and becomes
// collectable only when it is. There is deliberately NO eviction -- no LRU, no
// TTL, no size cap. Evicting an entry a live consumer depends on would turn a
// correct answer into a wrong one at an arbitrary later moment, which is
// strictly worse than the memory it would save.
//
// Environments are safe for concurrent use. Schemas load in parallel and
// atomisation reads on every typed value, so each table is guarded by its own
// RWMutex: a reader on the derivation table is never blocked by a writer on
// the union table.
//
// Names are held split, as a typeName, with the "{namespace}" part interned
// per environment. The global environment is never evicted, so storing each
// "{namespace}local" whole would retain the namespace once per type: 49 MB
// for 10,000 types under one 4 KB namespace. Split, it is retained once.
type TypeEnvironment struct {
	derivedMu sync.RWMutex
	derived   map[typeName]typeName

	listMu sync.RWMutex
	list   map[typeName]typeName

	unionMu sync.RWMutex
	union   map[typeName][]typeName
	// memberLists interns the stored member lists, keyed by the member
	// names joined with NUL, so every union with the same members shares
	// one slice. A restriction of a union inherits its base's whole list,
	// and the xsd package registers that list for each restriction; stored
	// once per type, N restrictions of a K-member union kept N*K names in a
	// table that is never evicted. Interned, they keep K plus N.
	// Guarded by unionMu. A stored list is never modified.
	memberLists map[string][]typeName

	nsMu sync.Mutex
	ns   map[string]string
}

// typeName is an annotation name cut after its closing brace: ns is
// "{uri}" (braces kept, so ns+local is the original name byte for byte) and
// is "" for a bare name.
type typeName struct{ ns, local string }

// splitTypeName cuts name without copying it, which is what a lookup wants.
func splitTypeName(name string) typeName {
	if strings.HasPrefix(name, "{") {
		if i := strings.IndexByte(name, '}'); i >= 0 {
			return typeName{name[:i+1], name[i+1:]}
		}
	}
	return typeName{local: name}
}

// String rebuilds the annotation name. A bare name comes back as stored; a
// qualified one is concatenated on demand, which is the price of not keeping
// the namespace once per type.
func (t typeName) String() string {
	if t.ns == "" {
		return t.local
	}
	return t.ns + t.local
}

// stored returns name in the form an entry keeps: the namespace this
// environment's canonical copy, the local part a copy of its own. Both are
// copies because a substring would pin the caller's whole string, namespace
// and all, which is the retention this layout exists to avoid.
func (e *TypeEnvironment) stored(name string) typeName {
	t := splitTypeName(name)
	t.local = strings.Clone(t.local)
	if t.ns != "" {
		e.nsMu.Lock()
		c, ok := e.ns[t.ns]
		if !ok {
			c = strings.Clone(t.ns)
			e.ns[c] = c
		}
		e.nsMu.Unlock()
		t.ns = c
	}
	return t
}

// restored re-homes a name another environment stored, so this one keeps
// its own single copy of each namespace.
func (e *TypeEnvironment) restored(t typeName) typeName {
	if t.ns == "" {
		return t
	}
	e.nsMu.Lock()
	c, ok := e.ns[t.ns]
	if !ok {
		c = t.ns
		e.ns[c] = c
	}
	e.nsMu.Unlock()
	t.ns = c
	return t
}

// NewTypeEnvironment returns an empty environment.
//
// The xsd package creates one per assembled schema and populates it as it
// walks the type definitions, which is the only place that knows a
// user-defined type's base, item type or member list.
func NewTypeEnvironment() *TypeEnvironment {
	return &TypeEnvironment{
		derived: map[typeName]typeName{},
		list:    map[typeName]typeName{},
		union:   map[typeName][]typeName{},
		ns:      map[string]string{},

		memberLists: map[string][]typeName{},
	}
}

// globalTypeEnv is the environment every registration lands in when no
// schema-owned one is named, and the one every lookup falls back to when the
// caller holds no schema.
//
// It is the compatibility path, kept deliberately rather than deleted: the
// package-level RegisterDerivedType, RegisterListType and RegisterUnionType
// are public API under the 1.2 additive-only guarantee, and callers outside
// this repository use them.
var globalTypeEnv = NewTypeEnvironment()

// GlobalTypeEnvironment returns the process-global environment the name-only
// registration functions conceptually correspond to.
//
// It is exported so that a caller holding no schema can still state a
// derivation, and so that the fallback a by-name consumer uses is nameable in
// a test. Prefer a schema-owned environment: entries here are keyed by type
// name across every schema in the process and are subject to exactly the
// collision this type exists to prevent.
func GlobalTypeEnvironment() *TypeEnvironment { return globalTypeEnv }

// RegisterDerived records that a schema type erases to a built-in one.
//
// Both arguments are annotation names, which AnnotationName builds; passing a
// bare local name for a type that has a namespace re-creates the conflation
// the qualified keying exists to prevent.
//
// A nil environment is a no-op rather than a panic: a caller that has not been
// given one is saying it has no schema facts to record, which is the untyped
// case and not an error.
func (e *TypeEnvironment) RegisterDerived(name, primitive string) {
	if e == nil || name == "" || primitive == "" || name == primitive {
		return
	}
	k, v := e.stored(name), e.stored(primitive)
	e.derivedMu.Lock()
	e.derived[k] = v
	e.derivedMu.Unlock()
}

// DerivedBase returns the type the named schema type derives from, or "" when
// the name is not one this environment knows. The name is an annotation name
// and so is the result, so a chain is walked by feeding the result back in.
//
// A nil environment answers "" for everything, which is the untyped answer and
// the one a caller with no schema should get.
func (e *TypeEnvironment) DerivedBase(name string) string {
	if e == nil {
		return ""
	}
	e.derivedMu.RLock()
	base, ok := e.derived[splitTypeName(name)]
	e.derivedMu.RUnlock()
	if !ok {
		return ""
	}
	return base.String()
}

// RegisterList records that a schema type is a list, and what its items are.
//
// Both arguments are annotation names. itemType is the item type's own name,
// which may itself be a type this environment knows; the derivation walk
// resolves it.
func (e *TypeEnvironment) RegisterList(name, itemType string) {
	if e == nil || name == "" || itemType == "" || name == itemType {
		return
	}
	k, v := e.stored(name), e.stored(itemType)
	e.listMu.Lock()
	e.list[k] = v
	e.listMu.Unlock()
}

// ListItemOf returns the item type recorded for a list type, or "" when the
// name does not denote one in this environment.
func (e *TypeEnvironment) ListItemOf(name string) string {
	if e == nil {
		return ""
	}
	e.listMu.RLock()
	item, ok := e.list[splitTypeName(name)]
	e.listMu.RUnlock()
	if !ok {
		return ""
	}
	return item.String()
}

// RegisterUnion records that a schema type is a union, and what its declared
// member types are, in declaration order.
//
// Which member a given value belongs to is a per-VALUE fact recorded on the
// node (Node.UnionMember), because XSD 1.0 §3.14.4 chooses the member by
// trying each one's lexical space against the value in turn. This records only
// the declaration.
func (e *TypeEnvironment) RegisterUnion(name string, members []string) {
	if e == nil || name == "" || len(members) == 0 {
		return
	}
	// A member listed twice is tried twice to no effect, so only its first
	// occurrence is kept: memberTypes="t:a t:a ..." would otherwise retain
	// one name per reference in a table that is never evicted.
	kept := make([]string, 0, len(members))
	seen := make(map[string]bool, len(members))
	for _, m := range members {
		if m != "" && m != name && !seen[m] {
			seen[m] = true
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return
	}
	cp := e.internMembers(kept)
	k := e.stored(name)
	e.unionMu.Lock()
	e.union[k] = cp
	e.unionMu.Unlock()
}

// internMembers returns the stored form of a member list, shared with every
// union this environment already holds with the same members in the same
// order. The caller must not hold unionMu.
func (e *TypeEnvironment) internMembers(members []string) []typeName {
	key := strings.Join(members, "\x00")
	e.unionMu.RLock()
	cp, ok := e.memberLists[key]
	e.unionMu.RUnlock()
	if ok {
		return cp
	}
	cp = make([]typeName, len(members))
	for i, m := range members {
		cp[i] = e.stored(m)
	}
	e.unionMu.Lock()
	if prev, ok := e.memberLists[key]; ok {
		cp = prev
	} else {
		e.memberLists[key] = cp
	}
	e.unionMu.Unlock()
	return cp
}

// UnionMembersOf returns the declared member types of a union type, or nil
// when the name does not denote one in this environment.
//
// The result is built per call from the stored names, so the caller owns it.
func (e *TypeEnvironment) UnionMembersOf(name string) []string {
	if e == nil {
		return nil
	}
	e.unionMu.RLock()
	m := e.union[splitTypeName(name)]
	e.unionMu.RUnlock()
	if m == nil {
		return nil
	}
	out := make([]string, len(m))
	for i, t := range m {
		out[i] = t.String()
	}
	return out
}

// Len reports how many derivation, list and union entries the environment
// holds. It is here so a heap/lifetime test can state what it is measuring
// rather than reaching into unexported maps.
func (e *TypeEnvironment) Len() (derived, lists, unions int) {
	if e == nil {
		return 0, 0, 0
	}
	e.derivedMu.RLock()
	derived = len(e.derived)
	e.derivedMu.RUnlock()
	e.listMu.RLock()
	lists = len(e.list)
	e.listMu.RUnlock()
	e.unionMu.RLock()
	unions = len(e.union)
	e.unionMu.RUnlock()
	return derived, lists, unions
}

// Merge folds every fact src holds into e, without overwriting one e already
// has.
//
// It exists for the schema assemblies that are themselves a merge of several
// loaded schemas: XSLT's xsl:import-schema and XQuery's "import schema" each
// fold every imported schema into one aggregate *xsd.Schema, and a merge that
// copied the type DEFINITIONS while leaving the derivation facts behind would
// produce a schema whose types no longer know what they derive from -- a
// NOTATION restriction that stopped being a NOTATION.
//
// First writer wins, matching the component merge beside it: a name the
// destination already defines is the destination's, and an import does not
// redefine it.
func (e *TypeEnvironment) Merge(src *TypeEnvironment) {
	if e == nil || src == nil || e == src {
		return
	}
	src.derivedMu.RLock()
	derived := make(map[typeName]typeName, len(src.derived))
	for k, v := range src.derived {
		derived[k] = v
	}
	src.derivedMu.RUnlock()
	e.derivedMu.Lock()
	for k, v := range derived {
		if _, ok := e.derived[k]; !ok {
			e.derived[e.restored(k)] = e.restored(v)
		}
	}
	e.derivedMu.Unlock()

	src.listMu.RLock()
	lists := make(map[typeName]typeName, len(src.list))
	for k, v := range src.list {
		lists[k] = v
	}
	src.listMu.RUnlock()
	e.listMu.Lock()
	for k, v := range lists {
		if _, ok := e.list[k]; !ok {
			e.list[e.restored(k)] = e.restored(v)
		}
	}
	e.listMu.Unlock()

	src.unionMu.RLock()
	unions := make(map[typeName][]typeName, len(src.union))
	for k, v := range src.union {
		unions[k] = v
	}
	src.unionMu.RUnlock()
	for k, v := range unions {
		e.unionMu.RLock()
		_, ok := e.union[k]
		e.unionMu.RUnlock()
		if ok {
			continue
		}
		// Interned here too, so the aggregate schema an import merges into
		// keeps one list per distinct member list rather than one per type.
		names := make([]string, len(v))
		for i, m := range v {
			names[i] = m.String()
		}
		members := e.internMembers(names)
		e.unionMu.Lock()
		if _, ok := e.union[k]; !ok {
			e.union[e.restored(k)] = members
		}
		e.unionMu.Unlock()
	}
}

// typeEnvOf returns the environment to consult for a node: the one the schema
// that validated it owns, or the process-global fallback when the node carries
// none.
//
// A node carries none when it was annotated by something other than schema
// assessment -- a DTD attribute type, an XSLT validation instruction, a plain
// struct literal -- and for those the global table is exactly the behaviour
// that was there before, which is why the fallback is not an error.
func typeEnvOf(n *Node) *TypeEnvironment {
	if e := n.TypeEnv(); e != nil {
		return e
	}
	return globalTypeEnv
}

// TypeEnvOf returns the environment to consult for questions about a node's
// type: the one the schema that validated it owns, or the process-global
// fallback when the node carries none.
//
// Every by-NAME consumer of a schema's type facts that HOLDS a node should go
// through this rather than through the package-level DerivedBase, ListItemOf
// and UnionMembersOf. Those answer from the global table, which is keyed by
// type name across every schema in the process and so answers for whichever
// schema loaded last; this answers from the schema that actually produced the
// node's annotation, which is the only definition that can be correct for it.
func TypeEnvOf(n *Node) *TypeEnvironment { return typeEnvOf(n) }
