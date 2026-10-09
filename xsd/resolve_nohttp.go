//go:build goxml_nohttp

package xsd

// The wording of resolve_http.go's constants for a build without
// HTTPResolver, so a refusal does not point at a type that is not there.
const (
	resolverChoices = "a FileResolver or a MapResolver"
	remoteHint      = "this build (-tags goxml_nohttp) has no HTTPResolver; use a local copy through a FileResolver, MapResolver or CatalogResolver"
)
