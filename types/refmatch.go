package types

// RefMatch, as a domain type rather than an internal one.
//
// magus.IdentifyRef returns []RefMatch, so a caller (the CLI's ref-lookup
// suggestion, the magus_output MCP tool's not-found fallback) reads the
// candidate target(s) that would mint a given ref as values, without importing
// the root magus package just for this one return type.

// RefMatch names a workspace target whose live cache key predicts a ref.
type RefMatch struct {
	Project string
	Target  string
	Charms  []string
}
