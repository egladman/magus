//go:build !selfmanage

package manpage

// selfSegment (non-selfmanage build) — update/install are compiled in only
// with -tags selfmanage and are omitted here.
var selfSegment = Segment{
	Name:  "self",
	Short: "Manage the magus binary (update/install need -tags selfmanage)",
	Long: `Subcommands for managing the magus binary.

This build was compiled without -tags selfmanage, so self update and
self install — which download and replace the binary — are not available.
Rebuild with -tags selfmanage to enable them.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self <update|install> [flags]",
	Examples: []Example{},
}
