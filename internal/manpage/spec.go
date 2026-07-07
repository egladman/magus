// Package manpage defines the Command and Target types used by both the
// magus CLI and the man-page generator (cmd/magus-manpage).
package manpage

import "flag"

// Command is one node in the recursive magus CLI command tree (e.g. "run", "config", "ci github").
type Command struct {
	Name  string // command word
	Short string // one-line summary for SYNOPSIS and cross-references
	Long  string // multi-paragraph DESCRIPTION body; plain text, no roff markup
	Usage string // SYNOPSIS line, e.g. "magus run <target> [flags] [project...]"

	// Description is the ~120-155 char meta-description emitted into the
	// generated docs page's YAML frontmatter (used for search index + <meta
	// name="description">). Falls back to Short if empty.
	Description string
	// Tags list keywords for the docs page's YAML frontmatter (search index).
	// Auto-augmented with the canonical "cli, magus <name>, <name>" if empty.
	Tags []string

	// BuildFlags is called with a fresh FlagSet to register command-specific flags.
	// Do NOT register global flags here (--output, -v, --concurrency, --root, --config).
	BuildFlags func(fs *flag.FlagSet)

	Examples []Example // EXAMPLES section entries
	Children []Command // navigational subcommands (e.g. "github" under "ci")
	Targets  []Target  // project-scoped targets dispatched by this command
}

// Target is a named unit of work a project spell implements (e.g. "build", "test", "lint").
type Target struct {
	Name       string
	Short      string
	BuildFlags func(fs *flag.FlagSet)
}

// Example is a single usage example in the man-page EXAMPLES section.
type Example struct {
	Comment string // e.g. "Build all Go projects"
	Command string // e.g. "magus run build"
}
