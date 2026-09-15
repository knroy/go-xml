package xdm

import "sync"

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
type TypeEnvironment struct {
	derivedMu sync.RWMutex
	derived   map[string]string

	listMu sync.RWMutex
	list   map[string]string

	unionMu sync.RWMutex
	union   map[string][]string
}

// NewTypeEnvironment returns an empty environment.
//
// The xsd package creates one per assembled schema and populates it as it
// walks the type definitions, which is the only place that knows a
// user-defined type's base, item type or member list.
func NewTypeEnvironment() *TypeEnvironment {
	return &TypeEnvironment{
		derived: map[string]string{},
		list:    map[string]string{},
		union:   map[string][]string{},
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
	e.derivedMu.Lock()
	e.derived[name] = primitive
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
	base := e.derived[name]
	e.derivedMu.RUnlock()
	return base
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
	e.listMu.Lock()
	e.list[name] = itemType
	e.listMu.Unlock()
}

// ListItemOf returns the item type recorded for a list type, or "" when the
// name does not denote one in this environment.
func (e *TypeEnvironment) ListItemOf(name string) string {
	if e == nil {
		return ""
	}
	e.listMu.RLock()
	item := e.list[name]
	e.listMu.RUnlock()
	return item
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
	cp := make([]string, 0, len(members))
	for _, m := range members {
		if m != "" && m != name {
			cp = append(cp, m)
		}
	}
	if len(cp) == 0 {
		return
	}
	e.unionMu.Lock()
	e.union[name] = cp
	e.unionMu.Unlock()
}

// UnionMembersOf returns the declared member types of a union type, or nil
// when the name does not denote one in this environment.
//
// The result must not be modified: it is the stored slice, shared with every
// other caller.
func (e *TypeEnvironment) UnionMembersOf(name string) []string {
	if e == nil {
		return nil
	}
	e.unionMu.RLock()
	m := e.union[name]
	e.unionMu.RUnlock()
	return m
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
	derived := make(map[string]string, len(src.derived))
	for k, v := range src.derived {
		derived[k] = v
	}
	src.derivedMu.RUnlock()
	e.derivedMu.Lock()
	for k, v := range derived {
		if _, ok := e.derived[k]; !ok {
			e.derived[k] = v
		}
	}
	e.derivedMu.Unlock()

	src.listMu.RLock()
	lists := make(map[string]string, len(src.list))
	for k, v := range src.list {
		lists[k] = v
	}
	src.listMu.RUnlock()
	e.listMu.Lock()
	for k, v := range lists {
		if _, ok := e.list[k]; !ok {
			e.list[k] = v
		}
	}
	e.listMu.Unlock()

	src.unionMu.RLock()
	unions := make(map[string][]string, len(src.union))
	for k, v := range src.union {
		unions[k] = v
	}
	src.unionMu.RUnlock()
	e.unionMu.Lock()
	for k, v := range unions {
		if _, ok := e.union[k]; !ok {
			e.union[k] = v
		}
	}
	e.unionMu.Unlock()
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
	if n != nil && n.typeEnv != nil {
		return n.typeEnv
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

// TypeEnv returns the type environment of the schema that validated this node,
// or nil when no schema did.
//
// Unlike TypeEnvOf this does NOT fall back to the global table: it reports
// what the node actually carries, which is what a test asserting that the
// stamping happened needs to see.
func (n *Node) TypeEnv() *TypeEnvironment {
	if n == nil {
		return nil
	}
	return n.typeEnv
}

// SetTypeEnv records the type environment of the schema whose assessment
// produced this node's annotation.
//
// The xsd package calls it as it annotates, so that later by-name questions
// about the node's type reach the definitions that schema actually made rather
// than whatever a later, unrelated schema registered under the same name.
func (n *Node) SetTypeEnv(e *TypeEnvironment) {
	if n == nil {
		return
	}
	n.typeEnv = e
}
