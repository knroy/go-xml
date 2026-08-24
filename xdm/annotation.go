package xdm

import "strings"

// AnnotationName builds the key a type annotation is recorded and compared
// under.
//
// The data model keys annotations by a single string, and that string used to
// be the type's bare local name. That conflated every type sharing a local
// part across namespaces, and the conflation was not theoretical: the W3C's
// own schema-for-xslt20.xsd deliberately declares an xsl:QName of its own, as
// a restriction of xs:Name, and says in its text why ("This schema does not
// use the built-in type xs:QName... a schema processor would expand
// unprefixed QNames incorrectly"). Keyed by "QName", loading that schema
// overwrote the built-in's entry in a package-level, process-global map, so
// every later schema in the same process saw xs:QName deriving from xs:Name.
//
// Both meanings have to coexist rather than one displacing the other:
// import-schema-029 asserts that the SHADOWING xsl:QName does erase to a
// string, while type-functions-0501 asserts that the built-in xs:QName still
// atomises to a QName value. A registration that refuses to shadow breaks the
// first; a global "something shadowed a built-in" flag breaks the second,
// because one flag cannot hold two answers at once. Only qualifying the key
// separates them.
//
// The encoding is Clark notation, {uri}local, which the codebase already
// spells through QName.Clark, with one deliberate exception: a type in the
// XML Schema namespace keys under its BARE local name. That exception is what
// keeps the change tractable. Built-in annotations are compared against bare
// literals — "QName", "NOTATION", "ID", "string" — at roughly a hundred sites
// across four packages, in switch statements, map lookups and equality tests.
// Qualifying them would have required rewriting every one of those, whereas
// leaving them bare means only names that were previously AMBIGUOUS change
// spelling, and a built-in's key is the same string it has always been.
//
// The empty URI also keys bare, which is the no-namespace case and is already
// unambiguous.
func AnnotationName(uri, local string) string {
	if local == "" {
		return ""
	}
	if uri == "" || uri == NSXS {
		return local
	}
	return "{" + uri + "}" + local
}

// SplitAnnotationName is the inverse of AnnotationName: it returns the
// namespace URI and local part of an annotation key.
//
// A bare key is a built-in or a no-namespace type, and the two are told apart
// by nothing here — the URI comes back empty for both, because the callers
// that care (the built-in switches) match on the local part they already
// expect. What this function exists for is the comparison path, which needs
// the local part of a qualified key without mistaking "{uri}local" for a
// prefixed lexical QName.
//
// It must be used in place of SplitQName wherever the input is an annotation.
// SplitQName cuts at the first colon, so handed "{http://x}foo" it returns
// the prefix "{http" and the local part "//x}foo" — nonsense rather than an
// error, and silently wrong.
func SplitAnnotationName(annotation string) (uri, local string) {
	if !strings.HasPrefix(annotation, "{") {
		return "", annotation
	}
	i := strings.IndexByte(annotation, '}')
	if i < 0 {
		return "", annotation
	}
	return annotation[1:i], annotation[i+1:]
}

// AnnotationLocal returns just the local part of an annotation key.
func AnnotationLocal(annotation string) string {
	_, local := SplitAnnotationName(annotation)
	return local
}

// IsQualifiedAnnotation reports whether an annotation key carries a namespace,
// which is to say it names a type that is neither a built-in nor in no
// namespace.
func IsQualifiedAnnotation(annotation string) bool {
	return strings.HasPrefix(annotation, "{")
}
