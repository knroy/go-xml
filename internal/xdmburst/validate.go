package xdmburst

// AppendAttr appends to el, a *xdm.Node the stream driver has just opened
// (its start tag is being told to an observer, it has no children yet), an
// attribute named {uri}local with prefix and value, and returns it (a
// *xdm.Node), or nil when el is past its start tag. It is how a validator
// running on the stream (package xsd) adds the attributes a schema supplies
// by default, where a typed copy of the document has them: after the
// element's own. They are not nodes the parse counts (MaxNodes): the typed
// copy's are not either. Package xdm sets it.
var AppendAttr func(el any, prefix, uri, local, value string) any

// RemoveAttr takes a, an attribute of el, off el's start tag while the start
// tag is being told to an observer, as AppendAttr adds one, and reports
// whether it did. It is how input-type-annotations="strip" drops xsi:nil from
// a validated stream, as the tree's stripped copy drops it. Package xdm sets
// it.
var RemoveAttr func(el, a any) bool
