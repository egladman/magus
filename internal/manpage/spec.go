// Package manpage defines the Segment and Subcommand types used by both the
// magus CLI and the man-page generator (cmd/magus-manpage).
package manpage

import "flag"

// Segment is one node in the recursive magus CLI command tree (e.g. "run", "config", "ci github").
type Segment struct {
	Name  string // command word
	Short string // one-line summary for SYNOPSIS and cross-references
	Long  string // multi-paragraph DESCRIPTION body; plain text, no roff markup
	Usage string // SYNOPSIS line, e.g. "magus run <target> [flags] [project...]"

	// BuildFlags is called with a fresh FlagSet to register segment-specific flags.
	// Do NOT register global flags here (--output, -v, --concurrency, --root, --config).
	BuildFlags func(fs *flag.FlagSet)

	Examples []Example    // EXAMPLES section entries
	Children []Segment    // navigational sub-segments (e.g. "github" under "ci")
	Targets  []Subcommand // project-scoped targets dispatched by this segment
}

// Subcommand is a named unit of work a project spell implements (e.g. "build", "test", "lint").
type Subcommand struct {
	Name       string
	Short      string
	BuildFlags func(fs *flag.FlagSet)
}

// Example is a single usage example in the man-page EXAMPLES section.
type Example struct {
	Comment string // e.g. "Build all Go projects"
	Command string // e.g. "magus run build"
}
