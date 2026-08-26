package agent

import "embed"

// skillFS holds the provider-neutral skill bodies, embedded at build time.
//
// They live HERE rather than beside the CLI that installs them, next to the catalog that renders,
// stamps and verifies them. go:embed cannot reach across package directories, so wherever the
// assets sit is the only package that can embed them - and while they sat under cmd/magus, the
// docs generator could not import them at all. It read them off disk through a relative
// `-src ../cmd/magus` instead, which is a path that silently means the wrong thing the moment
// either binary moves.
//
//go:embed skills
var skillFS embed.FS

// agentsSectionMD is the distilled always-on block installed into AGENTS.md for hosts that read
// that contract instead of skill directories. Same rules as the skills, compressed.
//
// It is part of the catalog's content digest, so a caller that supplies its own would produce a
// digest no install ever writes. That is why Default exists rather than every caller assembling
// one: there is one correct pairing of these two assets, and it should not be re-derived.
//
//go:embed agents-section.md
var agentsSectionMD string

// Default returns the catalog over magus's own embedded skill sources at the current knowledge
// schema version.
//
// It is the ONE construction every shipping caller wants - the CLI's `agent install`, the docs
// generator, and anything that needs to know which skills magus ships. NewCatalog stays exported
// for a caller supplying different sources, which in practice means tests.
func Default(schemaVersion int) *Catalog {
	return NewCatalog(skillFS, agentsSectionMD, schemaVersion)
}
