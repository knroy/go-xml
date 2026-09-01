module github.com/knroy/go-xml/w3cschemas

go 1.26

require github.com/knroy/go-xml v0.0.0

require golang.org/x/text v0.37.0 // indirect

// The parent is developed in the same checkout, so the local copy is what
// this module builds and tests against. Drop this line when tagging a release:
// a published module must resolve its dependency from the module proxy like
// anything else, and a replace directive in a released go.mod is ignored by
// consumers anyway, which makes it a trap rather than a shortcut.
replace github.com/knroy/go-xml => ..
